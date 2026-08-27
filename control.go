package claudeagentsdk

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log"
	"sync"
	"time"
)

// SkillToolName is the synthetic tool the CLI exposes for invoking a
// skill. It is injected into allowed_tools when Options.Skills is set,
// matching the Python SDK.
const SkillToolName = "Skill"

// CreateSdkMCPServer wraps a registered Server as an McpSdkServerConfig
// suitable for Options.MCPServers. The instance stays in-process; the
// bridge spawned for it is owned by Client.Connect.
//
// Example:
//
//	server := sdkmcp.NewServer("calc", "1.0.0")
//	add, _ := sdkmcp.NewTool("add", "Add two numbers", func(ctx context.Context, args struct {
//	    A float64 `json:"a"`
//	    B float64 `json:"b"`
//	}) (sdkmcp.ToolResult, error) {
//	    return sdkmcp.TextResult(fmt.Sprintf("%v", args.A+args.B)), nil
//	})
//	server.MustRegister(add)
//	opts := Options{MCPServers: map[string]any{"calc": CreateSdkMCPServer("calc", server)}}
func CreateSdkMCPServer(name string, server *Server) McpSdkServerConfig {
	return McpSdkServerConfig{Type: "sdk", Name: name, Instance: server}
}

// mcpServersWire converts the mixed MCPServers map into the wire form:
// SDK configs advertise type+name and strip the in-process instance;
// everything else passes through unchanged. Names of SDK servers are
// returned alongside for the initialize payload.
func mcpServersWire(in map[string]any) (map[string]any, []string, error) {
	if in == nil {
		return nil, nil, nil
	}
	out := make(map[string]any, len(in))
	var sdkNames []string
	for name, cfg := range in {
		switch c := cfg.(type) {
		case McpSdkServerConfig:
			if c.Type == "" {
				c.Type = "sdk"
			}
			if c.Name == "" {
				c.Name = name
			}
			out[name] = map[string]any{"type": c.Type, "name": c.Name}
			sdkNames = append(sdkNames, c.Name)
		case *McpSdkServerConfig:
			if c == nil {
				return nil, nil, fmt.Errorf("MCPServers[%q]: nil SDK config", name)
			}
			t, n := c.Type, c.Name
			if t == "" {
				t = "sdk"
			}
			if n == "" {
				n = name
			}
			out[name] = map[string]any{"type": t, "name": n}
			sdkNames = append(sdkNames, n)
		default:
			out[name] = cfg
		}
	}
	return out, sdkNames, nil
}

// sdkMcpBridges extracts the SDK MCP servers from Options.MCPServers,
// keyed by their instance name (not the user-supplied map key, which can
// differ). A nil or duplicated server is a configuration error.
func sdkMcpBridges(in map[string]any) (map[string]*SdkMcpBridge, error) {
	if in == nil {
		return nil, nil
	}
	out := map[string]*SdkMcpBridge{}
	for _, cfg := range in {
		var c *McpSdkServerConfig
		switch v := cfg.(type) {
		case McpSdkServerConfig:
			c = &v
		case *McpSdkServerConfig:
			c = v
		default:
			continue
		}
		if c == nil {
			continue
		}
		if c.Instance == nil {
			return nil, fmt.Errorf("SDK MCP server %q has no instance", c.Name)
		}
		if _, dup := out[c.Name]; dup {
			return nil, fmt.Errorf("duplicate SDK MCP server name %q", c.Name)
		}
		out[c.Name] = NewSdkMcpBridge(c.Name, c.Instance)
	}
	return out, nil
}

type controlResponse struct {
	Response map[string]any
	Err      error
}

type controlLoop struct {
	transport           Transport
	in                  <-chan map[string]any
	errs                <-chan error
	out                 chan map[string]any
	canUseTool          CanUseTool
	hooks               map[string][]HookMatcher
	agents              map[string]AgentDefinition
	skills              any
	forwardSubagentText bool
	hookCallbacks       map[string]HookCallback
	sdkBridges          map[string]*SdkMcpBridge
	pending             map[string]chan controlResponse
	inflight            map[string]context.CancelFunc
	mirror              *TranscriptMirrorBatcher
	mu                  sync.Mutex
	counter             int
	lastError           map[string]any
}

func newControlLoop(t Transport, in <-chan map[string]any, errs <-chan error, opts Options, mirror *TranscriptMirrorBatcher) (*controlLoop, error) {
	bridges, err := sdkMcpBridges(opts.MCPServers)
	if err != nil {
		return nil, err
	}
	return &controlLoop{
		transport:           t,
		in:                  in,
		errs:                errs,
		out:                 make(chan map[string]any, 100),
		canUseTool:          opts.CanUseTool,
		hooks:               opts.Hooks,
		agents:              opts.Agents,
		skills:              opts.Skills,
		forwardSubagentText: opts.ForwardSubagentText,
		hookCallbacks:       map[string]HookCallback{},
		sdkBridges:          bridges,
		pending:             map[string]chan controlResponse{},
		inflight:            map[string]context.CancelFunc{},
		mirror:              mirror,
	}, nil
}

func (q *controlLoop) closeBridges() {
	q.mu.Lock()
	bridges := q.sdkBridges
	q.sdkBridges = nil
	q.mu.Unlock()
	for _, b := range bridges {
		_ = b.Close()
	}
}

// HooksForTest exposes the configured hook matchers for tests that need
// to register pre-built callback ids. Not part of the public API.
func (q *controlLoop) HooksForTest() map[string][]HookMatcher { return q.hooks }

// CanUseToolShadowedWarning is emitted when can_use_tool is set alongside
// options that visibly shadow it: allowed_tools entries that allow a whole
// tool (e.g. "Read", "Read()", "Read(*)") and permission_mode
// "bypassPermissions". Surfaced through a logger rather than raised, because
// shadowing can be intentional (e.g. a callback scoped to tools outside
// allowed_tools). Matches the Python SDK's CanUseToolShadowedWarning.
type CanUseToolShadowedWarning struct {
	Message string
}

func (w *CanUseToolShadowedWarning) Error() string { return w.Message }

// wholeToolAllowed returns the tool an allowed_tools entry allows outright,
// or empty when the entry only matches invocations with a real specifier
// (e.g. "Bash(ls:*)"). Mirrors the Python _whole_tool_allowed helper so the
// shadowing list matches.
func wholeToolAllowed(entry string) string {
	if entry == "" {
		return ""
	}
	open := -1
	for i, r := range entry {
		if r == '(' {
			open = i
			break
		}
	}
	if open == -1 {
		return entry
	}
	if open == 0 || entry[len(entry)-1] != ')' {
		return ""
	}
	spec := entry[open+1 : len(entry)-1]
	if spec == "" || spec == "*" {
		return entry[:open]
	}
	return ""
}

// warnCanUseToolShadowed emits a CanUseToolShadowedWarning when can_use_tool
// is set but some tool calls will be auto-approved before the callback is
// consulted. Called once per Connect/Query; the Go stdlib log package
// already dedupes identical lines per process (matching the Python
// warnings.registry behavior).
func warnCanUseToolShadowed(o Options) {
	if o.CanUseTool == nil {
		return
	}
	allowed := append([]string(nil), o.AllowedTools...)
	// skills="all" makes the transport append a bare "Skill" entry, which
	// is a whole-tool allow and therefore shadows the callback just like
	// a hand-written entry.
	if s, ok := o.Skills.(string); ok && s == SkillsAll && !contains(allowed, SkillToolName) {
		allowed = append(allowed, SkillToolName)
	}
	if msg := canUseToolShadowedMessage(o.PermissionMode, allowed); msg != "" {
		log.Print("claude-agent-sdk-go: " + msg)
	}
}

// canUseToolShadowedMessage returns the warning body for the given
// permission mode and effective allowed_tools, or "" when no shadowing.
func canUseToolShadowedMessage(mode PermissionMode, allowedTools []string) string {
	if mode == PermissionBypassPermissions {
		return "can_use_tool will not be invoked: permission_mode 'bypassPermissions' auto-approves every tool call (except explicit deny rules) before the callback is consulted. To gate every tool call, use a PreToolUse hook instead."
	}
	// Dedup-preserving: dict.fromkeys in Python.
	seen := map[string]bool{}
	var shadowed []string
	for _, e := range allowedTools {
		if tool := wholeToolAllowed(e); tool != "" && !seen[tool] {
			seen[tool] = true
			shadowed = append(shadowed, tool)
		}
	}
	if len(shadowed) == 0 {
		return ""
	}
	return fmt.Sprintf("can_use_tool will not be invoked for: %s. An allowed_tools entry that allows a whole tool auto-approves it before the callback is consulted. To gate every tool call, use a PreToolUse hook; or narrow the entry so calls fall through to can_use_tool. Allow rules from settings files can also shadow the callback but are not visible here.", joinComma(shadowed))
}

func joinComma(xs []string) string {
	out := ""
	for i, x := range xs {
		if i > 0 {
			out += ", "
		}
		out += x
	}
	return out
}

// prepareOptions validates the user's Options against the cross-option rules
// the SDK enforces (can_use_tool + permission_prompt_tool mutual exclusion,
// skill-name validation, system prompt union, etc.) and returns a copy with
// any implicit additions (e.g. PermissionPromptTool="stdio" when can_use_tool
// is set, Skill injection into allowed_tools).
func prepareOptions(o Options) (Options, error) {
	if o.CanUseTool != nil {
		if o.PermissionPromptTool != "" {
			return o, fmt.Errorf("can_use_tool callback cannot be used with permission_prompt_tool")
		}
		o.PermissionPromptTool = "stdio"
	}
	return o, nil
}

func (q *controlLoop) start(ctx context.Context) { go q.run(ctx) }

func (q *controlLoop) initialize(ctx context.Context) error {
	hooks := map[string]any{}
	for event, matchers := range q.hooks {
		var hs []map[string]any
		for _, m := range matchers {
			ids := make([]string, 0, len(m.Hooks))
			for _, cb := range m.Hooks {
				id := fmt.Sprintf("hook_%d", len(q.hookCallbacks))
				q.hookCallbacks[id] = cb
				ids = append(ids, id)
			}
			h := map[string]any{"matcher": m.Matcher, "hookCallbackIds": ids}
			if m.Timeout != 0 {
				h["timeout"] = m.Timeout
			}
			hs = append(hs, h)
		}
		hooks[event] = hs
	}
	req := map[string]any{"subtype": "initialize"}
	if len(hooks) > 0 {
		req["hooks"] = hooks
	} else {
		req["hooks"] = nil
	}
	if len(q.agents) > 0 {
		req["agents"] = agentsWire(q.agents)
	}
	if names, ok := q.skills.([]string); ok {
		req["skills"] = names
	}
	if q.forwardSubagentText {
		req["forwardSubagentText"] = true
	}
	q.mu.Lock()
	sdkNames := make([]string, 0, len(q.sdkBridges))
	for name := range q.sdkBridges {
		sdkNames = append(sdkNames, name)
	}
	q.mu.Unlock()
	if len(sdkNames) > 0 {
		req["sdkMcpServers"] = sdkNames
	}
	_, err := q.sendControl(ctx, req, 60*time.Second)
	return err
}

func (q *controlLoop) run(ctx context.Context) {
	defer close(q.out)
	for {
		select {
		case m, ok := <-q.in:
			if !ok {
				q.forwardErr()
				return
			}
			switch m["type"] {
			case "control_response":
				q.handleControlResponse(m)
			case "control_request":
				q.spawnControlRequest(ctx, m)
			case "control_cancel_request":
				q.cancelControlRequest(str(m["request_id"]))
			case "transcript_mirror":
				if q.mirror != nil {
					q.mirror.Enqueue(str(m["filePath"]), entriesFromAny(m["entries"]))
				}
			case "result":
				if q.mirror != nil {
					_ = q.mirror.Flush(ctx)
				}
				if boolVal(m["is_error"]) {
					q.lastError = jsonCloneMap(m)
				} else {
					q.lastError = nil
				}
				q.out <- m
			default:
				q.out <- m
			}
		case err, ok := <-q.errs:
			if ok && err != nil {
				q.out <- map[string]any{"type": "error", "exception": q.wrapResultError(err), "error": err.Error()}
			}
			return
		case <-ctx.Done():
			q.out <- map[string]any{"type": "error", "exception": ctx.Err(), "error": ctx.Err().Error()}
			return
		}
	}
}

func (q *controlLoop) spawnControlRequest(ctx context.Context, m map[string]any) {
	id := str(m["request_id"])
	if id == "" {
		go q.handleControlRequest(ctx, m)
		return
	}
	reqCtx, cancel := context.WithCancel(ctx)
	q.mu.Lock()
	q.inflight[id] = cancel
	q.mu.Unlock()
	go func() {
		defer func() {
			q.mu.Lock()
			delete(q.inflight, id)
			q.mu.Unlock()
			cancel()
		}()
		q.handleControlRequest(reqCtx, m)
	}()
}

func (q *controlLoop) cancelControlRequest(id string) {
	q.mu.Lock()
	cancel := q.inflight[id]
	delete(q.inflight, id)
	q.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

func (q *controlLoop) handleControlResponse(m map[string]any) {
	resp := rawMap(m["response"])
	id := str(resp["request_id"])
	q.mu.Lock()
	ch := q.pending[id]
	delete(q.pending, id)
	q.mu.Unlock()
	if ch == nil {
		return
	}
	if resp["subtype"] == "error" {
		ch <- controlResponse{Err: fmt.Errorf("%s", resp["error"])}
	} else {
		ch <- controlResponse{Response: rawMap(resp["response"])}
	}
}

func (q *controlLoop) handleControlRequest(ctx context.Context, m map[string]any) {
	id := str(m["request_id"])
	req := rawMap(m["request"])
	resp, err := q.dispatchControl(ctx, req)
	if ctx.Err() != nil {
		return
	}
	wire := map[string]any{"type": "control_response", "response": map[string]any{"request_id": id}}
	if err != nil {
		wire["response"].(map[string]any)["subtype"] = "error"
		wire["response"].(map[string]any)["error"] = err.Error()
	} else {
		wire["response"].(map[string]any)["subtype"] = "success"
		wire["response"].(map[string]any)["response"] = resp
	}
	b, _ := json.Marshal(wire)
	_ = q.transport.Write(ctx, string(b)+"\n")
}

func (q *controlLoop) dispatchControl(ctx context.Context, req map[string]any) (map[string]any, error) {
	switch req["subtype"] {
	case "can_use_tool":
		if q.canUseTool == nil {
			return nil, fmt.Errorf("can_use_tool callback is not provided")
		}
		var suggestions []PermissionUpdate
		for _, s := range rawSlice(req["permission_suggestions"]) {
			suggestions = append(suggestions, permissionUpdateFromWire(rawMap(s)))
		}
		input := rawMap(req["input"])
		res, err := q.canUseTool(ctx, str(req["tool_name"]), input, ToolPermissionContext{Suggestions: suggestions, ToolUseID: str(req["tool_use_id"]), AgentID: str(req["agent_id"]), BlockedPath: str(req["blocked_path"]), DecisionReason: str(req["decision_reason"]), Title: str(req["title"]), DisplayName: str(req["display_name"]), Description: str(req["description"])})
		if err != nil {
			return nil, err
		}
		if res.Behavior == "allow" {
			out := map[string]any{"behavior": "allow", "updatedInput": input}
			if res.UpdatedInput != nil {
				out["updatedInput"] = res.UpdatedInput
			}
			if len(res.UpdatedPermissions) > 0 {
				ups := []map[string]any{}
				for _, u := range res.UpdatedPermissions {
					ups = append(ups, u.toWire())
				}
				out["updatedPermissions"] = ups
			}
			return out, nil
		}
		if res.Behavior == "deny" {
			out := map[string]any{"behavior": "deny", "message": res.Message}
			if res.Interrupt {
				out["interrupt"] = true
			}
			return out, nil
		}
		return nil, fmt.Errorf("permission callback returned invalid behavior %q", res.Behavior)
	case "hook_callback":
		cb := q.hookCallbacks[str(req["callback_id"])]
		if cb == nil {
			return nil, fmt.Errorf("no hook callback found for ID: %s", req["callback_id"])
		}
		return cb(ctx, rawMap(req["input"]), str(req["tool_use_id"]))
	case "mcp_message":
		return q.dispatchSdkMcp(ctx, req)
	default:
		return nil, fmt.Errorf("unsupported control request subtype: %s", req["subtype"])
	}
}

func (q *controlLoop) dispatchSdkMcp(ctx context.Context, req map[string]any) (map[string]any, error) {
	serverName := str(req["server_name"])
	message := rawMap(req["message"])
	if serverName == "" || message == nil {
		return nil, fmt.Errorf("missing server_name or message for mcp_message")
	}
	q.mu.Lock()
	bridge := q.sdkBridges[serverName]
	q.mu.Unlock()
	if bridge == nil {
		return map[string]any{"mcp_response": map[string]any{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"error":   map[string]any{"code": -32601, "message": fmt.Sprintf("SDK MCP server %q not registered", serverName)},
		}}, nil
	}
	resp, err := bridge.Handle(ctx, message)
	if err != nil {
		return map[string]any{"mcp_response": map[string]any{
			"jsonrpc": "2.0",
			"id":      message["id"],
			"error":   map[string]any{"code": -32603, "message": err.Error()},
		}}, nil
	}
	if resp == nil {
		// Notification: the control protocol still expects an ack.
		return map[string]any{"mcp_response": map[string]any{"jsonrpc": "2.0", "result": map[string]any{}}}, nil
	}
	return map[string]any{"mcp_response": resp}, nil
}

func (q *controlLoop) sendControl(ctx context.Context, req map[string]any, timeout time.Duration) (map[string]any, error) {
	id := q.nextID()
	ch := make(chan controlResponse, 1)
	q.mu.Lock()
	q.pending[id] = ch
	q.mu.Unlock()
	defer func() {
		q.mu.Lock()
		delete(q.pending, id)
		q.mu.Unlock()
	}()
	b, err := json.Marshal(map[string]any{"type": "control_request", "request_id": id, "request": req})
	if err != nil {
		return nil, err
	}
	if err := q.transport.Write(ctx, string(b)+"\n"); err != nil {
		return nil, err
	}
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case r := <-ch:
		return r.Response, r.Err
	case <-t.C:
		return nil, fmt.Errorf("control request timeout: %s", req["subtype"])
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (q *controlLoop) nextID() string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	q.mu.Lock()
	defer q.mu.Unlock()
	q.counter++
	return fmt.Sprintf("req_%d_%s", q.counter, hex.EncodeToString(b[:]))
}

func (q *controlLoop) forwardErr() {
	if err := q.drainErr(); err != nil {
		q.out <- map[string]any{"type": "error", "exception": err, "error": err.Error()}
	}
}

func (q *controlLoop) drainErr() error {
	select {
	case err, ok := <-q.errs:
		if ok && err != nil {
			return q.wrapResultError(err)
		}
	default:
	}
	return nil
}

func (q *controlLoop) wrapResultError(err error) error {
	if q.lastError == nil {
		return err
	}
	pe, ok := err.(*ProcessError)
	if !ok {
		return err
	}
	re := &ResultError{ProcessError: ProcessError{Msg: "Claude Code returned an error result: " + resultErrorText(q.lastError), ExitCode: pe.ExitCode}, Data: q.lastError, Subtype: str(q.lastError["subtype"]), Errors: normalizeResultErrors(q.lastError["errors"]), Result: str(q.lastError["result"]), TerminalReason: str(q.lastError["terminal_reason"]), SessionID: str(q.lastError["session_id"])}
	if v, ok := asInt(q.lastError["api_error_status"]); ok {
		re.APIErrorStatus = &v
	}
	return re
}
