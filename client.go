package claudeagentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

type Client struct {
	Options      Options
	transport    Transport
	cancel       context.CancelFunc
	q            *controlLoop
	materialized *MaterializedResume
}

func NewClient(opts Options) *Client { return &Client{Options: opts} }
func NewClientWithTransport(opts Options, t Transport) *Client {
	return &Client{Options: opts, transport: t}
}

// Query sends one prompt and streams the resulting messages. The prompt is
// either a plain string or Anthropic content blocks (e.g. []map[string]any
// holding {"type": "text"} and {"type": "image", "source": {...}} entries),
// passed through as the user message content.
func Query(ctx context.Context, prompt any, opts Options) (<-chan Message, <-chan error) {
	c := NewClient(opts)
	out, errs := make(chan Message), make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		if err := c.Connect(ctx); err != nil {
			errs <- err
			return
		}
		defer c.Close()
		if err := c.Send(ctx, prompt); err != nil {
			errs <- err
			return
		}
		if !c.hasBidirectionalNeeds() {
			_ = c.transport.EndInput()
		}
		closedInput := !c.hasBidirectionalNeeds()
		for {
			msg, err := c.Receive(ctx)
			if err != nil {
				errs <- err
				return
			}
			if msg == nil {
				return
			}
			select {
			case out <- msg:
			case <-ctx.Done():
				errs <- ctx.Err()
				return
			}
			if _, ok := msg.(ResultMessage); ok && !closedInput {
				_ = c.transport.EndInput()
				closedInput = true
			}
		}
	}()
	return out, errs
}

func (c *Client) Connect(ctx context.Context) error {
	if err := ValidateSessionStoreOptions(c.Options); err != nil {
		return err
	}
	opts, err := prepareOptions(c.Options)
	if err != nil {
		return err
	}
	warnCanUseToolShadowed(c.Options)
	c.Options = opts
	if c.transport == nil {
		c.materialized, err = MaterializeResumeSession(ctx, opts)
		if err != nil {
			return err
		}
		if c.materialized != nil {
			if opts.Env == nil {
				opts.Env = map[string]string{}
			}
			opts.Env["CLAUDE_CONFIG_DIR"] = c.materialized.ConfigDir
			opts.Resume = c.materialized.ResumeSessionID
			opts.ContinueConversation = false
		}
		c.transport = NewSubprocessTransport(nil, opts)
	}
	ctx, cancel := context.WithCancel(ctx)
	c.cancel = cancel
	if err := c.transport.Connect(ctx); err != nil {
		cancel()
		return err
	}
	msgs, rawErrs := c.transport.ReadMessages(ctx)
	q, err := newControlLoop(c.transport, msgs, rawErrs, opts, nil)
	if err != nil {
		cancel()
		return err
	}
	c.q = q
	if opts.SessionStore != nil {
		projectsDir := ""
		if c.materialized != nil {
			projectsDir = c.materialized.ConfigDir + "/projects"
		} else {
			projectsDir = configProjectsDir(opts.Env)
		}
		c.q.mirror = NewTranscriptMirrorBatcher(opts.SessionStore, projectsDir, func(key SessionKey, msg string) {
			select {
			case c.q.out <- map[string]any{"type": "system", "subtype": "mirror_error", "key": map[string]any{"project_key": key.ProjectKey, "session_id": key.SessionID, "subpath": key.Subpath}, "error": msg, "session_id": key.SessionID}:
			default:
			}
		}, opts.SessionStoreFlush)
	}
	c.q.start(ctx)
	if err := c.q.initialize(ctx); err != nil {
		_ = c.Close()
		return err
	}
	return nil
}

// Send writes a user prompt to the streaming client. The prompt is a plain
// string or content blocks (see Query). sessionID is the
// streaming logical conversation key, not the persisted ResultMessage.SessionID;
// omit it to reuse "default" across normal multi-turn calls.
func (c *Client) Send(ctx context.Context, prompt any, sessionID ...string) error {
	sid := "default"
	if len(sessionID) > 0 && sessionID[0] != "" {
		sid = sessionID[0]
	}
	m := map[string]any{"type": "user", "message": map[string]any{"role": "user", "content": prompt}, "parent_tool_use_id": nil, "session_id": sid}
	return c.SendMessage(ctx, m)
}

// SendMessage writes a raw streaming message, filling session_id="default" if
// missing to match Python's async-iterable query behavior.
func (c *Client) SendMessage(ctx context.Context, msg map[string]any) error {
	if _, ok := msg["session_id"]; !ok {
		msg["session_id"] = "default"
	}
	b, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	return c.transport.Write(ctx, string(b)+"\n")
}

func (c *Client) Receive(ctx context.Context) (Message, error) {
	if c.q == nil {
		return nil, &CLIConnectionError{Msg: "not connected"}
	}
	for {
		select {
		case m, ok := <-c.q.out:
			if !ok {
				return nil, nil
			}
			if m["type"] == "error" {
				if e, ok := m["exception"].(error); ok {
					return nil, e
				}
				return nil, fmt.Errorf("%s", m["error"])
			}
			return ParseMessage(m)
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (c *Client) ReceiveResponse(ctx context.Context) (<-chan Message, <-chan error) {
	out, errs := make(chan Message), make(chan error, 1)
	go func() {
		defer close(out)
		defer close(errs)
		for {
			msg, err := c.Receive(ctx)
			if err != nil {
				errs <- err
				return
			}
			if msg == nil {
				return
			}
			out <- msg
			if _, ok := msg.(ResultMessage); ok {
				return
			}
		}
	}()
	return out, errs
}

func (c *Client) Interrupt(ctx context.Context) error {
	_, err := c.control(ctx, map[string]any{"subtype": "interrupt"})
	return err
}

func (c *Client) SetPermissionMode(ctx context.Context, mode PermissionMode) error {
	_, err := c.control(ctx, map[string]any{"subtype": "set_permission_mode", "mode": mode})
	return err
}

func (c *Client) SetModel(ctx context.Context, model string) error {
	_, err := c.control(ctx, map[string]any{"subtype": "set_model", "model": model})
	return err
}

func (c *Client) RewindFiles(ctx context.Context, userMessageID string) error {
	_, err := c.control(ctx, map[string]any{"subtype": "rewind_files", "user_message_id": userMessageID})
	return err
}

func (c *Client) ReconnectMCPServer(ctx context.Context, name string) error {
	_, err := c.control(ctx, map[string]any{"subtype": "mcp_reconnect", "serverName": name})
	return err
}

func (c *Client) ToggleMCPServer(ctx context.Context, name string, enabled bool) error {
	_, err := c.control(ctx, map[string]any{"subtype": "mcp_toggle", "serverName": name, "enabled": enabled})
	return err
}

func (c *Client) StopTask(ctx context.Context, taskID string) error {
	_, err := c.control(ctx, map[string]any{"subtype": "stop_task", "task_id": taskID})
	return err
}

func (c *Client) MCPStatus(ctx context.Context) (McpStatusResponse, error) {
	resp, err := c.control(ctx, map[string]any{"subtype": "mcp_status"})
	if err != nil {
		return McpStatusResponse{}, err
	}
	return parseMcpStatus(resp), nil
}

func (c *Client) ContextUsage(ctx context.Context) (ContextUsageResponse, error) {
	resp, err := c.control(ctx, map[string]any{"subtype": "get_context_usage"})
	if err != nil {
		return ContextUsageResponse{}, err
	}
	return parseContextUsage(resp), nil
}

func (c *Client) MCPMessage(ctx context.Context, serverName string, message map[string]any) (map[string]any, error) {
	return c.control(ctx, map[string]any{
		"subtype":     "mcp_message",
		"server_name": serverName,
		"message":     message,
	})
}

// parseMcpStatus decodes the wire response for get_mcp_status. The CLI
// returns {"mcpServers": [...]}; missing or malformed entries fall through
// to zero values so a partial status still parses.
func parseMcpStatus(raw map[string]any) McpStatusResponse {
	out := McpStatusResponse{MCPServers: []McpServerStatus{}}
	servers, _ := raw["mcpServers"].([]any)
	for _, s := range servers {
		m, _ := s.(map[string]any)
		if m == nil {
			continue
		}
		status := McpServerStatus{
			Name:   str(m["name"]),
			Status: McpServerConnectionStatus(str(m["status"])),
			Error:  str(m["error"]),
			Scope:  str(m["scope"]),
		}
		if si, ok := m["serverInfo"].(map[string]any); ok {
			status.ServerInfo = &McpServerInfo{Name: str(si["name"]), Version: str(si["version"])}
		}
		if cfg, ok := m["config"].(map[string]any); ok {
			status.Config = cfg
		}
		if ts, ok := m["tools"].([]any); ok {
			for _, t := range ts {
				if tm, ok := t.(map[string]any); ok {
					tool := McpToolInfo{Name: str(tm["name"]), Description: str(tm["description"])}
					if ann, ok := tm["annotations"].(map[string]any); ok {
						tool.Annotations = &McpToolAnnotations{
							ReadOnly:    boolVal(ann["readOnlyHint"]),
							Destructive: boolVal(ann["destructiveHint"]),
							OpenWorld:   boolVal(ann["openWorldHint"]),
						}
					}
					status.Tools = append(status.Tools, tool)
				}
			}
		}
		out.MCPServers = append(out.MCPServers, status)
	}
	return out
}

// parseContextUsage decodes the wire response for get_context_usage. Most
// fields are pass-through maps; only the strongly-typed fields promote to
// their real types. The CLI may add fields; unknown ones round-trip in
// the raw map.
func parseContextUsage(raw map[string]any) ContextUsageResponse {
	out := ContextUsageResponse{
		TotalTokens:          intVal(raw["totalTokens"]),
		MaxTokens:            intVal(raw["maxTokens"]),
		RawMaxTokens:         intVal(raw["rawMaxTokens"]),
		Percentage:           floatVal(raw["percentage"]),
		Model:                str(raw["model"]),
		IsAutoCompactEnabled: boolVal(raw["isAutoCompactEnabled"]),
	}
	if cats, ok := raw["categories"].([]any); ok {
		for _, c := range cats {
			if cm, ok := c.(map[string]any); ok {
				out.Categories = append(out.Categories, ContextUsageCategory{
					Name:       str(cm["name"]),
					Tokens:     intVal(cm["tokens"]),
					Color:      str(cm["color"]),
					IsDeferred: boolVal(cm["isDeferred"]),
				})
			}
		}
	}
	if v, ok := raw["autoCompactThreshold"]; ok {
		if i, ok := asInt(v); ok {
			out.AutoCompactThreshold = &i
		}
	}
	if mf, ok := raw["memoryFiles"].([]any); ok {
		for _, m := range mf {
			if mm, ok := m.(map[string]any); ok {
				out.MemoryFiles = append(out.MemoryFiles, mm)
			}
		}
	}
	if mt, ok := raw["mcpTools"].([]any); ok {
		for _, m := range mt {
			if mm, ok := m.(map[string]any); ok {
				out.MCPTools = append(out.MCPTools, mm)
			}
		}
	}
	if ag, ok := raw["agents"].([]any); ok {
		for _, m := range ag {
			if mm, ok := m.(map[string]any); ok {
				out.Agents = append(out.Agents, mm)
			}
		}
	}
	if gr, ok := raw["gridRows"].([]any); ok {
		for _, row := range gr {
			if rrow, ok := row.([]any); ok {
				var cells []map[string]any
				for _, cell := range rrow {
					if mm, ok := cell.(map[string]any); ok {
						cells = append(cells, mm)
					}
				}
				out.GridRows = append(out.GridRows, cells)
			}
		}
	}
	if dt, ok := raw["deferredBuiltinTools"].([]any); ok {
		for _, m := range dt {
			if mm, ok := m.(map[string]any); ok {
				out.DeferredBuiltinTools = append(out.DeferredBuiltinTools, mm)
			}
		}
	}
	if st, ok := raw["systemTools"].([]any); ok {
		for _, m := range st {
			if mm, ok := m.(map[string]any); ok {
				out.SystemTools = append(out.SystemTools, mm)
			}
		}
	}
	if sp, ok := raw["systemPromptSections"].([]any); ok {
		for _, m := range sp {
			if mm, ok := m.(map[string]any); ok {
				out.SystemPromptSections = append(out.SystemPromptSections, mm)
			}
		}
	}
	if sc, ok := raw["slashCommands"].(map[string]any); ok {
		out.SlashCommands = sc
	}
	if sk, ok := raw["skills"].(map[string]any); ok {
		out.Skills = sk
	}
	if mb, ok := raw["messageBreakdown"].(map[string]any); ok {
		out.MessageBreakdown = mb
	}
	if au, ok := raw["apiUsage"].(map[string]any); ok {
		out.APIUsage = au
	}
	return out
}

func (c *Client) control(ctx context.Context, req map[string]any) (map[string]any, error) {
	if c.q == nil {
		return nil, &CLIConnectionError{Msg: "not connected"}
	}
	return c.q.sendControl(ctx, req, 60*time.Second)
}

func (c *Client) Close() error {
	if c.q != nil {
		c.q.closeBridges()
		if c.q.mirror != nil {
			_ = c.q.mirror.Flush(context.Background())
		}
	}
	if c.cancel != nil {
		c.cancel()
	}
	if c.transport != nil {
		_ = c.transport.Close()
	}
	if c.materialized != nil {
		_ = c.materialized.Cleanup()
	}
	return nil
}

func (c *Client) hasBidirectionalNeeds() bool {
	if c.Options.CanUseTool != nil || len(c.Options.Hooks) > 0 {
		return true
	}
	for _, cfg := range c.Options.MCPServers {
		switch cfg.(type) {
		case McpSdkServerConfig, *McpSdkServerConfig:
			return true
		}
	}
	return false
}
