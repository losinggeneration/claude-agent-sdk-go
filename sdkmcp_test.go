package claudeagentsdk

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"
)

// sdkmcp tests cover the in-process MCP server and the bridge that
// connects it to the CLI's mcp_message control frames.

type addArgs struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

func TestSdkMcpServerRoundTrip(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	add, err := NewTool("add", "Add two numbers", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult(formatFloat(a.A + a.B)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}

	bridge := NewSdkMcpBridge("calc", srv)

	resp, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "initialize",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := resp["result"].(map[string]any); res == nil {
		t.Fatalf("initialize returned no result: %#v", resp)
	}

	resp, err = bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/list",
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resp["result"].(map[string]any)
	tools, _ := res["tools"].([]map[string]any)
	if len(tools) != 1 || tools[0]["name"] != "add" {
		t.Fatalf("tools/list wrong: %#v", tools)
	}

	resp, err = bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      3,
		"method":  "tools/call",
		"params":  map[string]any{"name": "add", "arguments": map[string]any{"a": 2, "b": 3}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res = resp["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); isErr {
		t.Fatalf("call returned isError=true: %#v", res)
	}
	blocks, _ := res["content"].([]map[string]any)
	if len(blocks) != 1 || blocks[0]["text"] != "5" {
		t.Fatalf("unexpected content: %#v", blocks)
	}
}

func TestSdkMcpServerUnknownToolIsError(t *testing.T) {
	srv := NewServer("empty", "0.0.0")
	bridge := NewSdkMcpBridge("empty", srv)
	resp, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "missing", "arguments": map[string]any{}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res, _ := resp["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true: %#v", res)
	}
}

func TestSdkMcpServerSchemaInvalidArgsIsError(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	add, err := NewTool("add", "Add", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}
	bridge := NewSdkMcpBridge("calc", srv)
	resp, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "add", "arguments": map[string]any{"a": "not a number"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resp["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true: %#v", res)
	}
	if !strings.Contains(contentText(res["content"].([]map[string]any)), "validation") {
		t.Fatalf("expected validation message, got %#v", res["content"])
	}
}

func TestSdkMcpServerPanickingHandlerIsError(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	// Handler that always returns an error: the server should map it to
	// isError=true, never propagate to the caller.
	boom, err := NewTool("boom", "Always fails", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return ToolResult{}, errors.New("kaboom")
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(boom); err != nil {
		t.Fatal(err)
	}
	bridge := NewSdkMcpBridge("calc", srv)
	resp, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params":  map[string]any{"name": "boom", "arguments": map[string]any{"a": 1, "b": 2}},
	})
	if err != nil {
		t.Fatal(err)
	}
	res := resp["result"].(map[string]any)
	if isErr, _ := res["isError"].(bool); !isErr {
		t.Fatalf("expected isError=true: %#v", res)
	}
	if !strings.Contains(contentText(res["content"].([]map[string]any)), "kaboom") {
		t.Fatalf("expected kaboom in error text, got %#v", res["content"])
	}
}

func TestSdkMcpServerAnnotationsMetaSize(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	add, err := NewTool("add", "Add", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult("ok"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	size := 12345
	add = add.WithAnnotations(&ToolAnnotations{MaxResultSizeChars: &size})
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}
	wire := srv.ListToolsWire()
	tools, _ := wire["tools"].([]map[string]any)
	if len(tools) != 1 {
		t.Fatalf("want 1 tool, got %d", len(tools))
	}
	meta, _ := tools[0]["_meta"].(map[string]any)
	if meta == nil || meta["anthropic/maxResultSizeChars"] != size {
		t.Fatalf("_meta wrong: %#v", meta)
	}
}

func TestSdkMcpServerContentConversion(t *testing.T) {
	// Mixed content: text, image, resource_link, binary resource, unknown
	// type. Only text/image/resource_link-with-text pass through; the
	// binary resource and unknown type are dropped.
	in := ToolResult{Content: []map[string]any{
		{"type": "text", "text": "hello"},
		{"type": "image", "data": "AAAA", "mimeType": "image/png"},
		{"type": "resource_link", "name": "doc", "uri": "file:///x", "description": "docs"},
		{"type": "resource", "resource": map[string]any{"blob": "AAA"}},
		{"type": "weird", "data": "?"},
	}}
	out := convertToolContent(in)
	if len(out.Content) != 3 {
		t.Fatalf("want 3 blocks, got %d: %#v", len(out.Content), out.Content)
	}
	if out.Content[0]["text"] != "hello" {
		t.Fatalf("text wrong: %#v", out.Content[0])
	}
	if out.Content[1]["type"] != "image" {
		t.Fatalf("image wrong: %#v", out.Content[1])
	}
	if out.Content[2]["type"] != "text" || !strings.Contains(out.Content[2]["text"].(string), "doc") {
		t.Fatalf("resource_link flatten wrong: %#v", out.Content[2])
	}
}

func TestSdkMcpServerSchemaDerivation(t *testing.T) {
	type req struct {
		Name  string   `json:"name" desc:"the name"`
		Count int      `json:"count,omitempty"`
		Tags  []string `json:"tags"`
	}
	got, err := NewTool("list", "list things", func(ctx context.Context, r req) (ToolResult, error) {
		return TextResult(r.Name), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	schema := got.InputSchema
	if schema["type"] != "object" {
		t.Fatalf("schema not object: %#v", schema)
	}
	props, _ := schema["properties"].(map[string]any)
	if props["name"] == nil || props["count"] == nil || props["tags"] == nil {
		t.Fatalf("missing props: %#v", props)
	}
	if props["name"].(map[string]any)["description"] != "the name" {
		t.Fatalf("desc tag missing: %#v", props["name"])
	}
	required, _ := schema["required"].([]string)
	// name and tags are required; count is omitempty
	wantReq := map[string]bool{"name": true, "tags": true}
	if len(required) != len(wantReq) {
		t.Fatalf("required mismatch: %#v", required)
	}
	for _, r := range required {
		if !wantReq[r] {
			t.Fatalf("unexpected required %q", r)
		}
	}
}

func TestSdkMcpBridgeNotFound(t *testing.T) {
	q, err := newControlLoop(&fakeTransport{}, make(chan map[string]any), make(chan error), Options{
		MCPServers: map[string]any{
			"only": CreateSdkMCPServer("only", NewServer("only", "0.0.0")),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := q.dispatchSdkMcp(context.Background(), map[string]any{
		"subtype":     "mcp_message",
		"server_name": "ghost",
		"message":     map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp := resp["mcp_response"].(map[string]any)
	errObj, _ := mcp["error"].(map[string]any)
	if errObj == nil || !strings.Contains(errObj["message"].(string), "ghost") {
		t.Fatalf("expected not-registered error, got %#v", mcp)
	}
}

func TestSdkMcpBridgeDispatchEndToEnd(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	add, err := NewTool("add", "Add", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult(formatFloat(a.A + a.B)), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}
	q, err := newControlLoop(&fakeTransport{}, make(chan map[string]any), make(chan error), Options{
		MCPServers: map[string]any{
			"calc": CreateSdkMCPServer("calc", srv),
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// initialize first so the session exists
	if _, err := q.dispatchSdkMcp(context.Background(), map[string]any{
		"subtype":     "mcp_message",
		"server_name": "calc",
		"message":     map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize"},
	}); err != nil {
		t.Fatal(err)
	}
	resp, err := q.dispatchSdkMcp(context.Background(), map[string]any{
		"subtype":     "mcp_message",
		"server_name": "calc",
		"message":     map[string]any{"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": map[string]any{"name": "add", "arguments": map[string]any{"a": 4, "b": 6}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	mcp := resp["mcp_response"].(map[string]any)
	if mcp["error"] != nil {
		t.Fatalf("unexpected error: %#v", mcp)
	}
	body := mcp["result"].(map[string]any)
	if contentText(body["content"].([]map[string]any)) != "10" {
		t.Fatalf("want 10, got %#v", body)
	}
}

func TestMcpServersWireStripsInstance(t *testing.T) {
	srv := NewServer("calc", "1.0.0")
	raw, names, err := mcpServersWire(map[string]any{
		"calc":    CreateSdkMCPServer("calc", srv),
		"stdio-x": McpStdioServerConfig{Command: "node", Args: []string{"x.js"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(names) != 1 || names[0] != "calc" {
		t.Fatalf("sdkNames wrong: %#v", names)
	}
	b, _ := json.Marshal(raw)
	s := string(b)
	if strings.Contains(s, `"instance"`) {
		t.Fatalf("wire form leaked instance: %s", s)
	}
	if !strings.Contains(s, `"type":"sdk"`) {
		t.Fatalf("wire form missing sdk type: %s", s)
	}
	if !strings.Contains(s, `"command":"node"`) {
		t.Fatalf("stdio config not preserved: %s", s)
	}
}

func TestSdkMcpBridgeCloseWithInflight(t *testing.T) {
	// A handler that never returns. Closing the bridge must unblock the
	// Handle call instead of leaking it.
	srv := NewServer("slow", "0.0.0")
	hang, err := NewTool("hang", "hangs", func(ctx context.Context, a addArgs) (ToolResult, error) {
		<-ctx.Done()
		return ErrorResult("cancelled"), nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(hang); err != nil {
		t.Fatal(err)
	}
	bridge := NewSdkMcpBridge("slow", srv)
	// initialize first to create the session
	if _, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	}); err != nil {
		t.Fatal(err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		// Background goroutine drives the request; Close unblocks it.
		_, _ = bridge.Handle(context.Background(), map[string]any{
			"jsonrpc": "2.0", "id": 2, "method": "tools/call",
			"params": map[string]any{"name": "hang", "arguments": map[string]any{"a": 1, "b": 2}},
		})
	}()
	// Give the goroutine a moment to register its pending slot.
	time.Sleep(20 * time.Millisecond)
	if err := bridge.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("bridge.Handle did not return after Close")
	}
	// A subsequent Handle returns the closed-bridge error.
	if _, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "tools/list",
	}); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed error, got %v", err)
	}
}

func TestSdkMcpBridgeRestartOnInitialize(t *testing.T) {
	// After Close the bridge is dead; a fresh Handle against a brand-new
	// bridge must work, and after a server error only an initialize
	// should restart it.
	srv := NewServer("calc", "0.0.0")
	add, _ := NewTool("add", "Add", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult("ok"), nil
	})
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}
	bridge := NewSdkMcpBridge("calc", srv)
	if _, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 1, "method": "initialize",
	}); err != nil {
		t.Fatal(err)
	}
	// Manually stop the session: a stopped server returns errors until a
	// new initialize.
	bridge.mu.Lock()
	bridge.session.markStoppedForTest()
	bridge.mu.Unlock()

	resp, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 2, "method": "tools/list",
	})
	if err != nil {
		t.Fatal(err)
	}
	if errObj, _ := resp["error"].(map[string]any); errObj == nil {
		t.Fatalf("expected stopped error, got %#v", resp)
	}
	// initialize restarts the session.
	if _, err := bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 3, "method": "initialize",
	}); err != nil {
		t.Fatal(err)
	}
	resp, err = bridge.Handle(context.Background(), map[string]any{
		"jsonrpc": "2.0", "id": 4, "method": "tools/call",
		"params": map[string]any{"name": "add", "arguments": map[string]any{"a": 1, "b": 1}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res, _ := resp["result"].(map[string]any); res == nil {
		t.Fatalf("expected result after restart, got %#v", resp)
	}
}

func TestSdkMcpServerDuplicateRegister(t *testing.T) {
	srv := NewServer("calc", "0.0.0")
	add, _ := NewTool("add", "Add", func(ctx context.Context, a addArgs) (ToolResult, error) {
		return TextResult("x"), nil
	})
	if err := srv.Register(add); err != nil {
		t.Fatal(err)
	}
	if err := srv.Register(add); err == nil {
		t.Fatal("expected duplicate-register error")
	}
}

func TestSdkMcpServerMissingInstance(t *testing.T) {
	_, err := sdkMcpBridges(map[string]any{
		"x": McpSdkServerConfig{Type: "sdk", Name: "x"},
	})
	if err == nil {
		t.Fatal("expected missing-instance error")
	}
}

// helpers

func formatFloat(f float64) string {
	s := strconv.FormatFloat(f, 'f', -1, 64)
	if strings.Contains(s, ".") {
		s = strings.TrimRight(s, "0")
		s = strings.TrimRight(s, ".")
	}
	return s
}

func contentText(blocks []map[string]any) string {
	if len(blocks) == 0 {
		return ""
	}
	if t, ok := blocks[0]["text"].(string); ok {
		return t
	}
	return ""
}
