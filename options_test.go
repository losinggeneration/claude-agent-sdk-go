package claudeagentsdk

import (
	"context"
	"encoding/json"
	"log"
	"strings"
	"testing"
)

// Tests for the can-use-tool shadowing warning parity, plus the typed
// Client.MCPStatus / ContextUsage / MCPMessage methods. The shadowing
// warning is advisory; it goes through the standard log package, so the
// tests intercept log output.

func TestWholeToolAllowed(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", ""},
		{"Read", "Read"},
		{"Read()", "Read"},
		{"Read(*)", "Read"},
		{"Bash(ls:*)", ""}, // specific specifier, not whole-tool
		{"Bash( )", ""},    // not just empty/whitespace
		{"(Read)", ""},     // no name
		{"Read", "Read"},
		{"Bash", "Bash"},
	}
	for _, c := range cases {
		if got := wholeToolAllowed(c.in); got != c.want {
			t.Errorf("wholeToolAllowed(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestCanUseToolShadowedMessageBypassMode(t *testing.T) {
	msg := canUseToolShadowedMessage(PermissionBypassPermissions, []string{"Read"})
	if !strings.Contains(msg, "bypassPermissions") {
		t.Fatalf("expected bypassPermissions in message, got %q", msg)
	}
}

func TestCanUseToolShadowedMessageWholeTool(t *testing.T) {
	msg := canUseToolShadowedMessage(PermissionDefault, []string{"Read", "Edit()", "Bash(ls:*)", "Read"})
	if !strings.Contains(msg, "Read, Edit") {
		t.Fatalf("expected deduped Read+Edit, got %q", msg)
	}
	if strings.Contains(msg, "Bash") {
		t.Fatalf("Bash(ls:*) should not be shadowed, got %q", msg)
	}
}

func TestCanUseToolShadowedMessageClean(t *testing.T) {
	if msg := canUseToolShadowedMessage(PermissionDefault, []string{"Bash(ls:*)"}); msg != "" {
		t.Fatalf("expected empty message, got %q", msg)
	}
	if msg := canUseToolShadowedMessage("", nil); msg != "" {
		t.Fatalf("expected empty message, got %q", msg)
	}
}

func TestWarnCanUseToolShadowedSkipsWhenNoCallback(t *testing.T) {
	// Should not panic or log when can_use_tool is nil.
	warnCanUseToolShadowed(Options{AllowedTools: []string{"Read"}})
}

func TestWarnCanUseToolShadowedLogsWithCallback(t *testing.T) {
	called := captureLog(t)
	defer called.restore()
	warnCanUseToolShadowed(Options{
		CanUseTool: func(ctx context.Context, name string, in map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			return PermissionResult{Behavior: "allow"}, nil
		},
		AllowedTools: []string{"Read"},
	})
	out := called.text()
	if !strings.Contains(out, "Read") {
		t.Fatalf("expected shadowing log mentioning Read, got %q", out)
	}
}

func TestWarnCanUseToolShadowedSkillsAllAddsSkill(t *testing.T) {
	// skills="all" injects a bare "Skill" entry, which is a whole-tool
	// allow and should be reported as shadowed.
	called := captureLog(t)
	defer called.restore()
	warnCanUseToolShadowed(Options{
		CanUseTool: func(ctx context.Context, name string, in map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			return PermissionResult{Behavior: "allow"}, nil
		},
		Skills: SkillsAll,
	})
	out := called.text()
	if !strings.Contains(out, "Skill") {
		t.Fatalf("expected Skill in shadowing list, got %q", out)
	}
}

func TestPrepareOptionsSetsStdioPromptTool(t *testing.T) {
	got, err := prepareOptions(Options{
		CanUseTool: func(ctx context.Context, name string, in map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			return PermissionResult{Behavior: "allow"}, nil
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.PermissionPromptTool != "stdio" {
		t.Fatalf("expected stdio, got %q", got.PermissionPromptTool)
	}
}

func TestPrepareOptionsRejectsBothSet(t *testing.T) {
	_, err := prepareOptions(Options{
		CanUseTool: func(ctx context.Context, name string, in map[string]any, c ToolPermissionContext) (PermissionResult, error) {
			return PermissionResult{Behavior: "allow"}, nil
		},
		PermissionPromptTool: "stdio",
	})
	if err == nil || !strings.Contains(err.Error(), "cannot be used") {
		t.Fatalf("expected mutual-exclusion error, got %v", err)
	}
}

// --- typed parseMcpStatus / parseContextUsage ---

func TestParseMcpStatus(t *testing.T) {
	raw := map[string]any{
		"mcpServers": []any{
			map[string]any{
				"name":   "calc",
				"status": "connected",
				"serverInfo": map[string]any{
					"name":    "calc",
					"version": "1.0.0",
				},
				"tools": []any{
					map[string]any{
						"name":        "add",
						"description": "add two numbers",
						"annotations": map[string]any{"readOnlyHint": true},
					},
				},
			},
			map[string]any{
				"name":   "broken",
				"status": "failed",
				"error":  "boom",
			},
		},
	}
	out := parseMcpStatus(raw)
	if len(out.MCPServers) != 2 {
		t.Fatalf("want 2 servers, got %d", len(out.MCPServers))
	}
	calc := out.MCPServers[0]
	if calc.Name != "calc" || calc.Status != McpStatusConnected {
		t.Fatalf("calc wrong: %+v", calc)
	}
	if calc.ServerInfo == nil || calc.ServerInfo.Version != "1.0.0" {
		t.Fatalf("serverInfo wrong: %+v", calc.ServerInfo)
	}
	if len(calc.Tools) != 1 || calc.Tools[0].Name != "add" {
		t.Fatalf("tools wrong: %+v", calc.Tools)
	}
	if calc.Tools[0].Annotations == nil || !calc.Tools[0].Annotations.ReadOnly {
		t.Fatalf("annotations not promoted: %+v", calc.Tools[0].Annotations)
	}
	broken := out.MCPServers[1]
	if broken.Status != McpStatusFailed || broken.Error != "boom" {
		t.Fatalf("broken wrong: %+v", broken)
	}
}

func TestParseMcpStatusEmpty(t *testing.T) {
	out := parseMcpStatus(map[string]any{})
	if out.MCPServers == nil {
		t.Fatalf("MCPServers should be non-nil empty slice, got %+v", out)
	}
	if len(out.MCPServers) != 0 {
		t.Fatalf("want 0, got %d", len(out.MCPServers))
	}
}

func TestParseContextUsage(t *testing.T) {
	threshold := 150000
	raw := map[string]any{
		"categories": []any{
			map[string]any{"name": "system", "tokens": float64(1000), "color": "red"},
			map[string]any{"name": "tools", "tokens": float64(2000), "color": "blue", "isDeferred": true},
		},
		"totalTokens":          float64(5000),
		"maxTokens":            float64(200000),
		"rawMaxTokens":         float64(200000),
		"percentage":           float64(2.5),
		"model":                "claude-sonnet-4-5",
		"isAutoCompactEnabled": true,
		"autoCompactThreshold": float64(150000),
		"mcpTools":             []any{map[string]any{"name": "add", "serverName": "calc"}},
		"skills":               map[string]any{"loaded": []any{"x"}},
	}
	out := parseContextUsage(raw)
	if len(out.Categories) != 2 {
		t.Fatalf("categories wrong: %+v", out.Categories)
	}
	if out.Categories[1].IsDeferred != true {
		t.Fatalf("isDeferred not promoted: %+v", out.Categories[1])
	}
	if out.TotalTokens != 5000 || out.MaxTokens != 200000 || out.Model != "claude-sonnet-4-5" {
		t.Fatalf("scalar fields wrong: %+v", out)
	}
	if out.AutoCompactThreshold == nil || *out.AutoCompactThreshold != threshold {
		t.Fatalf("autoCompactThreshold not promoted: %+v", out.AutoCompactThreshold)
	}
	if len(out.MCPTools) != 1 || out.MCPTools[0]["name"] != "add" {
		t.Fatalf("mcpTools not passed through: %+v", out.MCPTools)
	}
	if out.Skills["loaded"] == nil {
		t.Fatalf("skills not passed through: %+v", out.Skills)
	}
}

func TestParseContextUsageThresholdMissing(t *testing.T) {
	out := parseContextUsage(map[string]any{"totalTokens": float64(1)})
	if out.AutoCompactThreshold != nil {
		t.Fatalf("threshold should be nil when absent: %+v", out.AutoCompactThreshold)
	}
	if out.TotalTokens != 1 {
		t.Fatalf("totalTokens wrong: %d", out.TotalTokens)
	}
}

// Sanity: Client.MCPMessage sends the right wire frame over the control
// loop. Uses a fakeTransport that captures writes.
func TestClientMCPMessageWire(t *testing.T) {
	ft := &fakeTransport{in: make(chan map[string]any, 10), writes: make(chan string, 10)}
	q, err := newControlLoop(ft, ft.in, make(chan error), Options{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	c := &Client{q: q, Options: Options{}}
	// Start the loop so control_response frames are dispatched to
	// pending requests; without this sendControl blocks forever.
	q.start(context.Background())
	go func() {
		w := <-ft.writes
		var m map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(w)), &m)
		// Echo a successful control response so sendControl returns.
		ft.in <- map[string]any{
			"type": "control_response",
			"response": map[string]any{
				"subtype":    "success",
				"request_id": m["request_id"],
				"response":   map[string]any{"mcp_response": map[string]any{"jsonrpc": "2.0", "result": map[string]any{}}},
			},
		}
	}()
	resp, err := c.MCPMessage(context.Background(), "calc", map[string]any{"jsonrpc": "2.0", "id": 1, "method": "tools/list"})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := resp["mcp_response"]; !ok {
		t.Fatalf("expected mcp_response in reply, got %v", resp)
	}
}

// --- log capture helpers ---

type logCapture struct {
	t      *testing.T
	prev   *log.Logger
	out    strings.Builder
	logger *log.Logger
}

func captureLog(t *testing.T) *logCapture {
	t.Helper()
	c := &logCapture{t: t}
	c.logger = log.New(&c.out, "", 0)
	c.prev = log.Default()
	log.SetOutput(&c.out)
	log.SetFlags(0)
	t.Cleanup(func() {
		log.SetOutput(c.prev.Writer())
		log.SetFlags(c.prev.Flags())
	})
	return c
}

func (c *logCapture) text() string { return c.out.String() }
func (c *logCapture) restore()     { /* cleanup handled by t.Cleanup */ }
