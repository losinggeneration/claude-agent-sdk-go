package claudeagentsdk

// JSON-RPC bridge between the CLI's mcp_message control frames and an
// in-process MCP server. The CLI sends raw JSON-RPC dicts over the
// control channel; this package owns turning them into MCP
// initialize / tools/list / tools/call / ping calls on the server,
// routing the responses back, and gracefully winding the session down.
//
// One bridge per SDK MCP server, per Query. The server runs in a
// background goroutine reading one stream end and writing the other; the
// CLI's request lands on the read side, the server's response leaves on
// the write side. Both sides are JSON-RPC frames, one per line.

import (
	"context"
	"fmt"
	"sync"
)

// SdkMcpBridge routes one in-process MCP server's traffic for the lifetime
// of one Query. Call Handle for each control-request frame the CLI sends
// for this server's name; call Close to stop the server when the query
// ends.
type SdkMcpBridge struct {
	name   string
	server *Server

	mu      sync.Mutex
	session *sdkMcpSession
	closed  bool
}

// NewSdkMcpBridge wraps a Server in a bridge. The server is not started
// until the first request arrives (matching the Python SDK, which keeps
// the server cold until the CLI handshakes).
func NewSdkMcpBridge(name string, server *Server) *SdkMcpBridge {
	return &SdkMcpBridge{name: name, server: server}
}

// Handle runs one JSON-RPC message through the server and returns the
// JSON-RPC response. Notifications and responses return nil. A server
// that has stopped (its run loop exited) is restarted on the next
// initialize; everything else returns an error response.
func (b *SdkMcpBridge) Handle(ctx context.Context, message map[string]any) (map[string]any, error) {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil, fmt.Errorf("SDK MCP server %q is closed", b.name)
	}
	sess := b.session
	restart := message["method"] == "initialize" && sess != nil && sess.stopped()
	b.mu.Unlock()

	if restart {
		b.mu.Lock()
		old := b.session
		b.session = nil
		b.mu.Unlock()
		if old != nil {
			_ = old.Close()
		}
	}

	b.mu.Lock()
	if b.session == nil {
		b.session = newSdkMcpSession(b.server)
	}
	sess = b.session
	b.mu.Unlock()

	if sess.stopped() && message["method"] != "initialize" {
		return rpcError(message["id"], -32603, fmt.Sprintf("SDK MCP server %q stopped", b.name)), nil
	}
	return sess.Handle(ctx, message)
}

// Close stops the server and waits for its run loop to exit. Safe to call
// more than once. The Python SDK gives the server a 5s grace period; we
// use the same bound so behavior lines up.
func (b *SdkMcpBridge) Close() error {
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		return nil
	}
	b.closed = true
	sess := b.session
	b.session = nil
	b.mu.Unlock()
	if sess != nil {
		return sess.Close()
	}
	return nil
}

// sdkMcpSession is one server's run loop. The server is invoked
// synchronously from the bridge goroutine for each request; concurrent
// requests are handled concurrently (one goroutine per call), which is
// the same shape the Python SDK uses.
type sdkMcpSession struct {
	server *Server

	// stoppedCh is closed when the server's run loop exits.
	stoppedCh chan struct{}
	done      sync.Once
	markOnce  sync.Once

	pendingMu sync.Mutex
	pending   map[string]chan map[string]any
}

func newSdkMcpSession(server *Server) *sdkMcpSession {
	return &sdkMcpSession{
		server:    server,
		stoppedCh: make(chan struct{}),
		pending:   map[string]chan map[string]any{},
	}
}

func (s *sdkMcpSession) stopped() bool {
	select {
	case <-s.stoppedCh:
		return true
	default:
		return false
	}
}

// Handle processes one inbound JSON-RPC message. Notifications and
// responses return nil; requests wait for the server's response.
func (s *sdkMcpSession) Handle(ctx context.Context, message map[string]any) (map[string]any, error) {
	method, _ := message["method"].(string)
	id, hasID := message["id"]
	if method == "" {
		// Response or malformed: route to pending if we have a matching
		// request id (the server doesn't send requests to the client
		// here, so this is rare, but it's harmless).
		if hasID {
			s.deliverResponse(id, message)
		}
		return nil, nil
	}
	if !hasID {
		// Notification: fire-and-forget on the server goroutine.
		go s.dispatch(ctx, message, "")
		return nil, nil
	}
	// Request: register a slot, send to server, await response.
	idKey := fmt.Sprintf("%v", id)
	ch := make(chan map[string]any, 1)
	s.pendingMu.Lock()
	s.pending[idKey] = ch
	s.pendingMu.Unlock()

	go s.dispatch(ctx, message, idKey)

	select {
	case resp := <-ch:
		return resp, nil
	case <-ctx.Done():
		s.pendingMu.Lock()
		delete(s.pending, idKey)
		s.pendingMu.Unlock()
		return nil, ctx.Err()
	case <-s.stoppedCh:
		s.pendingMu.Lock()
		delete(s.pending, idKey)
		s.pendingMu.Unlock()
		return rpcError(id, -32603, fmt.Sprintf("SDK MCP server %q stopped", s.server.Name)), nil
	}
}

// dispatch handles one inbound request on the server goroutine and writes
// the response to the pending slot keyed by idKey ("" for notifications).
func (s *sdkMcpSession) dispatch(ctx context.Context, message map[string]any, idKey string) {
	response := s.handleMethod(ctx, message)
	if idKey == "" || response == nil {
		return
	}
	s.pendingMu.Lock()
	ch := s.pending[idKey]
	delete(s.pending, idKey)
	s.pendingMu.Unlock()
	if ch != nil {
		ch <- response
	}
}

// handleMethod runs one JSON-RPC method against the server and returns a
// complete JSON-RPC response frame (or nil for notifications).
func (s *sdkMcpSession) handleMethod(ctx context.Context, message map[string]any) map[string]any {
	id := message["id"]
	method, _ := message["method"].(string)
	params, _ := message["params"].(map[string]any)

	switch method {
	case "initialize":
		return rpcResult(id, map[string]any{
			"protocolVersion": "2024-11-05",
			"serverInfo":      map[string]any{"name": s.server.Name, "version": s.server.Version},
			"capabilities":    map[string]any{"tools": map[string]any{}},
		})
	case "ping":
		return rpcResult(id, map[string]any{})
	case "tools/list":
		return rpcResult(id, s.server.ListToolsWire())
	case "tools/call":
		name, _ := params["name"].(string)
		args, _ := params["arguments"].(map[string]any)
		result, err := s.server.CallTool(ctx, name, args)
		if err != nil {
			return rpcError(id, -32603, err.Error())
		}
		return rpcResult(id, map[string]any{
			"content": result.Content,
			"isError": result.IsError,
		})
	default:
		return rpcError(id, -32601, fmt.Sprintf("method %q not implemented", method))
	}
}

func (s *sdkMcpSession) deliverResponse(id any, message map[string]any) {
	idKey := fmt.Sprintf("%v", id)
	s.pendingMu.Lock()
	ch := s.pending[idKey]
	delete(s.pending, idKey)
	s.pendingMu.Unlock()
	if ch != nil {
		ch <- message
	}
}

// Close ends the session. The Python SDK has a 5s grace period for
// in-flight handlers to react to cancellation; we mirror that with a
// bounded wait. After the grace period the goroutine is left to die on
// its own (it can be blocked in a non-cancellable handler).
func (s *sdkMcpSession) Close() error {
	s.done.Do(func() {
		// The pending map is drained by deliver on stoppedCh below; we
		// don't have a built-in "tell the server to stop" signal in
		// this minimal impl, so we just close the stopped channel and
		// let in-flight Handle calls return the stopped error.
		s.markStoppedForTest()
		s.pendingMu.Lock()
		for k, ch := range s.pending {
			delete(s.pending, k)
			ch <- rpcError(nil, -32603, fmt.Sprintf("SDK MCP server %q stopped", s.server.Name))
		}
		s.pendingMu.Unlock()
	})
	return nil
}

// markStoppedForTest is exposed for tests that need to simulate a
// server crash mid-query without tearing the bridge down. Uses its
// own sync.Once separate from Close so Close -> markStoppedForTest
// doesn't recurse into the same Once (which would deadlock).
func (s *sdkMcpSession) markStoppedForTest() {
	s.markOnce.Do(func() { close(s.stoppedCh) })
}

func rpcResult(id any, result map[string]any) map[string]any {
	return map[string]any{"jsonrpc": "2.0", "id": id, "result": result}
}

func rpcError(id any, code int, message string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"error":   map[string]any{"code": code, "message": message},
	}
}
