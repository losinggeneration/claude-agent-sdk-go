package claudeagentsdk

import "fmt"

// serverConfigsToMap converts an agent's []any of MCP server configs into a
// name->config map so the wire conversion can run. Names are taken from
// each config's Name field; SDK configs without a name fall back to the
// empty string (callers should set Name).
func serverConfigsToMap(xs []any) map[string]any {
	if len(xs) == 0 {
		return nil
	}
	out := make(map[string]any, len(xs))
	for i, x := range xs {
		name := fmt.Sprint(i)
		switch c := x.(type) {
		case McpStdioServerConfig:
			name = c.Command
		case McpSSEServerConfig:
			name = c.URL
		case McpHttpServerConfig:
			name = c.URL
		case McpSdkServerConfig:
			if c.Name != "" {
				name = c.Name
			}
		case *McpSdkServerConfig:
			if c != nil && c.Name != "" {
				name = c.Name
			}
		}
		out[name] = x
	}
	return out
}

func agentsWire(agents map[string]AgentDefinition) map[string]any {
	out := make(map[string]any, len(agents))
	for name, a := range agents {
		m := map[string]any{"description": a.Description, "prompt": a.Prompt}
		if len(a.Tools) > 0 {
			m["tools"] = a.Tools
		}
		if len(a.DisallowedTools) > 0 {
			m["disallowedTools"] = a.DisallowedTools
		}
		if a.Model != "" {
			m["model"] = a.Model
		}
		if len(a.Skills) > 0 {
			m["skills"] = a.Skills
		}
		if a.Memory != "" {
			m["memory"] = a.Memory
		}
		if len(a.MCPServers) > 0 {
			wire, _, err := mcpServersWire(serverConfigsToMap(a.MCPServers))
			if err != nil {
				// Bubble up via a panic: agentsWire has no error return
				// and the caller in transport.go/control.go should be
				// validating these upfront. Falling back to the raw
				// value keeps a misconfigured agent from crashing the
				// whole initialize.
				wire = map[string]any{}
				for k, v := range serverConfigsToMap(a.MCPServers) {
					wire[k] = v
				}
			}
			m["mcpServers"] = wire
		}
		if a.InitialPrompt != "" {
			m["initialPrompt"] = a.InitialPrompt
		}
		if a.MaxTurns != nil {
			m["maxTurns"] = *a.MaxTurns
		}
		if a.Background != nil {
			m["background"] = *a.Background
		}
		if a.Effort != nil {
			m["effort"] = a.Effort
		}
		if a.PermissionMode != "" {
			m["permissionMode"] = a.PermissionMode
		}
		out[name] = m
	}
	return out
}
