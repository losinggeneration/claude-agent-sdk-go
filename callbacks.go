package claudeagentsdk

import "context"

type HookCallback func(ctx context.Context, input map[string]any, toolUseID string) (map[string]any, error)

type HookMatcher struct {
	Matcher string
	Hooks   []HookCallback
	Timeout float64
}

type PermissionUpdate struct {
	Type        string
	Rules       []PermissionRuleValue
	Behavior    string
	Mode        PermissionMode
	Directories []string
	Destination string
}

type PermissionRuleValue struct {
	ToolName    string
	RuleContent string
}

type ToolPermissionContext struct {
	Suggestions    []PermissionUpdate
	ToolUseID      string
	AgentID        string
	BlockedPath    string
	DecisionReason string
	Title          string
	DisplayName    string
	Description    string
}

type PermissionResult struct {
	Behavior           string // allow or deny
	UpdatedInput       map[string]any
	UpdatedPermissions []PermissionUpdate
	Message            string
	Interrupt          bool
}

type CanUseTool func(ctx context.Context, toolName string, input map[string]any, c ToolPermissionContext) (PermissionResult, error)

func (p PermissionUpdate) toWire() map[string]any {
	m := map[string]any{"type": p.Type}
	if p.Destination != "" {
		m["destination"] = p.Destination
	}
	if p.Behavior != "" {
		m["behavior"] = p.Behavior
	}
	if p.Mode != "" {
		m["mode"] = p.Mode
	}
	if len(p.Directories) > 0 {
		m["directories"] = p.Directories
	}
	if len(p.Rules) > 0 {
		rules := make([]map[string]any, 0, len(p.Rules))
		for _, r := range p.Rules {
			rules = append(rules, map[string]any{"toolName": r.ToolName, "ruleContent": r.RuleContent})
		}
		m["rules"] = rules
	}
	return m
}

func permissionUpdateFromWire(m map[string]any) PermissionUpdate {
	p := PermissionUpdate{Type: str(m["type"]), Behavior: str(m["behavior"]), Mode: PermissionMode(str(m["mode"])), Destination: str(m["destination"])}
	for _, d := range rawSlice(m["directories"]) {
		if s := str(d); s != "" {
			p.Directories = append(p.Directories, s)
		}
	}
	for _, r := range rawSlice(m["rules"]) {
		rm := rawMap(r)
		p.Rules = append(p.Rules, PermissionRuleValue{ToolName: str(rm["toolName"]), RuleContent: str(rm["ruleContent"])})
	}
	return p
}
