package claudeagentsdk

// Typed hook inputs and outputs. The control protocol gives the SDK a
// `hook_callback` request per hook event with a free-form input dict;
// users can either consume that dict through the original HookCallback
// signature or wrap their logic in one of the typed *Hook constructors
// below for a strongly-typed experience.
//
// The wire form of a hook output mirrors the Python SDK's
// HookJSONOutput: an object that may be the async variant ({"async_":
// true}) or the sync variant ({"continue_": ..., "decision": ..., ...}).
// The Go convention is the same: callbacks return map[string]any via the
// raw HookCallback, but the typed constructors below rename async_ ->
// async and continue_ -> continue when serializing, and route event-
// specific fields (permissionDecision, additionalContext, ...) under
// hookSpecificOutput so the CLI sees the shape it expects.

import "context"

// HookContext is the second positional argument a hook callback receives
// after the input. Signal is reserved for future abort-signal support.
type HookContext struct {
	Signal any
}

// HookEventName is a typed alias for the hook_event_name field of a hook
// input. The CLI may emit names that aren't in this set on a newer
// version; treat anything unrecognized as "not a known hook event".
type HookEventName = string

const (
	HookEventPreToolUse         HookEventName = "PreToolUse"
	HookEventPostToolUse        HookEventName = "PostToolUse"
	HookEventPostToolUseFailure HookEventName = "PostToolUseFailure"
	HookEventUserPromptSubmit   HookEventName = "UserPromptSubmit"
	HookEventStop               HookEventName = "Stop"
	HookEventSubagentStop       HookEventName = "SubagentStop"
	HookEventPreCompact         HookEventName = "PreCompact"
	HookEventNotification       HookEventName = "Notification"
	HookEventSubagentStart      HookEventName = "SubagentStart"
	HookEventPermissionRequest  HookEventName = "PermissionRequest"
	HookEventSessionStart       HookEventName = "SessionStart"
)

// BaseHookInput is the field set shared by every hook input. The CLI may
// not populate every field on every event.
type BaseHookInput struct {
	SessionID      string
	TranscriptPath string
	CWD            string
	PermissionMode string
}

// SubagentContext fields are populated for hook events fired from inside
// a Task-spawned subagent (agent_id, agent_type). They are optional on
// every event.
type SubagentContext struct {
	AgentID   string
	AgentType string
}

// PreToolUseHookInput is the input for PreToolUse hooks.
type PreToolUseHookInput struct {
	BaseHookInput
	SubagentContext
	HookEventName string
	ToolName      string
	ToolInput     map[string]any
	ToolUseID     string
}

// PreToolUseHookOutput is the typed output for PreToolUse hooks.
type PreToolUseHookOutput struct {
	Continue                 bool
	StopReason               string
	SuppressOutput           bool
	SystemMessage            string
	PermissionDecision       string // "allow" | "deny" | "ask" | "defer"
	PermissionDecisionReason string
	UpdatedInput             map[string]any
	AdditionalContext        string
}

// PostToolUseHookInput is the input for PostToolUse hooks.
type PostToolUseHookInput struct {
	BaseHookInput
	SubagentContext
	HookEventName string
	ToolName      string
	ToolInput     map[string]any
	ToolResponse  any
	ToolUseID     string
}

// PostToolUseHookOutput is the typed output for PostToolUse hooks.
type PostToolUseHookOutput struct {
	Continue             bool
	StopReason           string
	SuppressOutput       bool
	SystemMessage        string
	AdditionalContext    string
	UpdatedToolOutput    any
	UpdatedMCPToolOutput any
}

// PostToolUseFailureHookInput is the input for PostToolUseFailure hooks.
type PostToolUseFailureHookInput struct {
	BaseHookInput
	SubagentContext
	HookEventName string
	ToolName      string
	ToolInput     map[string]any
	ToolUseID     string
	Error         string
	IsInterrupt   bool
}

// PostToolUseFailureHookOutput is the typed output for PostToolUseFailure
// hooks.
type PostToolUseFailureHookOutput struct {
	Continue          bool
	StopReason        string
	SuppressOutput    bool
	SystemMessage     string
	AdditionalContext string
}

// UserPromptSubmitHookInput is the input for UserPromptSubmit hooks.
type UserPromptSubmitHookInput struct {
	BaseHookInput
	HookEventName string
	Prompt        string
}

// UserPromptSubmitHookOutput is the typed output for UserPromptSubmit
// hooks.
type UserPromptSubmitHookOutput struct {
	Continue          bool
	StopReason        string
	SuppressOutput    bool
	SystemMessage     string
	AdditionalContext string
}

// StopHookInput is the input for Stop hooks.
type StopHookInput struct {
	BaseHookInput
	HookEventName  string
	StopHookActive bool
}

// StopHookOutput is the typed output for Stop hooks.
type StopHookOutput struct {
	Continue       bool
	StopReason     string
	SuppressOutput bool
	SystemMessage  string
	// Decision: "block" to indicate blocking behavior.
	Decision string
	Reason   string
}

// SubagentStartHookInput is the input for SubagentStart hooks.
type SubagentStartHookInput struct {
	BaseHookInput
	HookEventName string
	AgentID       string
	AgentType     string
}

// SubagentStartHookOutput is the typed output for SubagentStart hooks.
type SubagentStartHookOutput struct {
	Continue          bool
	StopReason        string
	SuppressOutput    bool
	SystemMessage     string
	AdditionalContext string
}

// SubagentStopHookInput is the input for SubagentStop hooks.
type SubagentStopHookInput struct {
	BaseHookInput
	HookEventName       string
	StopHookActive      bool
	AgentID             string
	AgentTranscriptPath string
	AgentType           string
}

// SubagentStopHookOutput is the typed output for SubagentStop hooks.
type SubagentStopHookOutput struct {
	Continue       bool
	StopReason     string
	SuppressOutput bool
	SystemMessage  string
	Decision       string
	Reason         string
}

// PreCompactHookInput is the input for PreCompact hooks.
type PreCompactHookInput struct {
	BaseHookInput
	HookEventName      string
	Trigger            string // "manual" | "auto"
	CustomInstructions string
}

// PreCompactHookOutput is the typed output for PreCompact hooks.
type PreCompactHookOutput struct {
	Continue       bool
	StopReason     string
	SuppressOutput bool
	SystemMessage  string
}

// NotificationHookInput is the input for Notification hooks.
type NotificationHookInput struct {
	BaseHookInput
	HookEventName    string
	Message          string
	Title            string
	NotificationType string
}

// NotificationHookOutput is the typed output for Notification hooks.
type NotificationHookOutput struct {
	Continue          bool
	StopReason        string
	SuppressOutput    bool
	SystemMessage     string
	AdditionalContext string
}

// PermissionRequestHookInput is the input for PermissionRequest hooks.
type PermissionRequestHookInput struct {
	BaseHookInput
	SubagentContext
	HookEventName         string
	ToolName              string
	ToolInput             map[string]any
	PermissionSuggestions []map[string]any
}

// PermissionRequestHookOutput is the typed output for PermissionRequest
// hooks.
type PermissionRequestHookOutput struct {
	Continue       bool
	StopReason     string
	SuppressOutput bool
	SystemMessage  string
	// Decision is required: a "block" indicates blocking behavior.
	Decision map[string]any
}

// AsyncHookOutput is the wire form for hooks that defer execution.
// {async: true, asyncTimeout?: ms}.
type AsyncHookOutput struct {
	Async        bool
	AsyncTimeout int
}

// AsyncHook is a HookCallback that always returns the async wire form so
// the CLI defers execution until later. Useful for long-running hooks.
func AsyncHook() HookCallback {
	return func(ctx context.Context, input map[string]any, toolUseID string) (map[string]any, error) {
		return map[string]any{"async": true}, nil
	}
}

// --- input parsers: raw map -> typed struct ---

func parseBaseHookInput(raw map[string]any) BaseHookInput {
	return BaseHookInput{
		SessionID:      str(raw["session_id"]),
		TranscriptPath: str(raw["transcript_path"]),
		CWD:            str(raw["cwd"]),
		PermissionMode: str(raw["permission_mode"]),
	}
}

func parseSubagentContext(raw map[string]any) SubagentContext {
	return SubagentContext{AgentID: str(raw["agent_id"]), AgentType: str(raw["agent_type"])}
}

func ParsePreToolUseHookInput(raw map[string]any) PreToolUseHookInput {
	in := PreToolUseHookInput{
		BaseHookInput:   parseBaseHookInput(raw),
		SubagentContext: parseSubagentContext(raw),
		HookEventName:   str(raw["hook_event_name"]),
		ToolName:        str(raw["tool_name"]),
		ToolInput:       rawMap(raw["tool_input"]),
		ToolUseID:       str(raw["tool_use_id"]),
	}
	return in
}

func ParsePostToolUseHookInput(raw map[string]any) PostToolUseHookInput {
	return PostToolUseHookInput{
		BaseHookInput:   parseBaseHookInput(raw),
		SubagentContext: parseSubagentContext(raw),
		HookEventName:   str(raw["hook_event_name"]),
		ToolName:        str(raw["tool_name"]),
		ToolInput:       rawMap(raw["tool_input"]),
		ToolResponse:    raw["tool_response"],
		ToolUseID:       str(raw["tool_use_id"]),
	}
}

func ParsePostToolUseFailureHookInput(raw map[string]any) PostToolUseFailureHookInput {
	return PostToolUseFailureHookInput{
		BaseHookInput:   parseBaseHookInput(raw),
		SubagentContext: parseSubagentContext(raw),
		HookEventName:   str(raw["hook_event_name"]),
		ToolName:        str(raw["tool_name"]),
		ToolInput:       rawMap(raw["tool_input"]),
		ToolUseID:       str(raw["tool_use_id"]),
		Error:           str(raw["error"]),
		IsInterrupt:     boolVal(raw["is_interrupt"]),
	}
}

func ParseUserPromptSubmitHookInput(raw map[string]any) UserPromptSubmitHookInput {
	return UserPromptSubmitHookInput{
		BaseHookInput: parseBaseHookInput(raw),
		HookEventName: str(raw["hook_event_name"]),
		Prompt:        str(raw["prompt"]),
	}
}

func ParseStopHookInput(raw map[string]any) StopHookInput {
	return StopHookInput{
		BaseHookInput:  parseBaseHookInput(raw),
		HookEventName:  str(raw["hook_event_name"]),
		StopHookActive: boolVal(raw["stop_hook_active"]),
	}
}

func ParseSubagentStartHookInput(raw map[string]any) SubagentStartHookInput {
	return SubagentStartHookInput{
		BaseHookInput: parseBaseHookInput(raw),
		HookEventName: str(raw["hook_event_name"]),
		AgentID:       str(raw["agent_id"]),
		AgentType:     str(raw["agent_type"]),
	}
}

func ParseSubagentStopHookInput(raw map[string]any) SubagentStopHookInput {
	return SubagentStopHookInput{
		BaseHookInput:       parseBaseHookInput(raw),
		HookEventName:       str(raw["hook_event_name"]),
		StopHookActive:      boolVal(raw["stop_hook_active"]),
		AgentID:             str(raw["agent_id"]),
		AgentTranscriptPath: str(raw["agent_transcript_path"]),
		AgentType:           str(raw["agent_type"]),
	}
}

func ParsePreCompactHookInput(raw map[string]any) PreCompactHookInput {
	return PreCompactHookInput{
		BaseHookInput:      parseBaseHookInput(raw),
		HookEventName:      str(raw["hook_event_name"]),
		Trigger:            str(raw["trigger"]),
		CustomInstructions: str(raw["custom_instructions"]),
	}
}

func ParseNotificationHookInput(raw map[string]any) NotificationHookInput {
	return NotificationHookInput{
		BaseHookInput:    parseBaseHookInput(raw),
		HookEventName:    str(raw["hook_event_name"]),
		Message:          str(raw["message"]),
		Title:            str(raw["title"]),
		NotificationType: str(raw["notification_type"]),
	}
}

func ParsePermissionRequestHookInput(raw map[string]any) PermissionRequestHookInput {
	return PermissionRequestHookInput{
		BaseHookInput:   parseBaseHookInput(raw),
		SubagentContext: parseSubagentContext(raw),
		HookEventName:   str(raw["hook_event_name"]),
		ToolName:        str(raw["tool_name"]),
		ToolInput:       rawMap(raw["tool_input"]),
	}
}

// --- typed wrappers: typed callback -> HookCallback ---

// PreToolUseHook wraps a typed callback as a HookCallback.
func PreToolUseHook(fn func(ctx context.Context, in PreToolUseHookInput) (PreToolUseHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParsePreToolUseHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return preToolUseOutputToWire(out), nil
	}
}

// PostToolUseHook wraps a typed callback as a HookCallback.
func PostToolUseHook(fn func(ctx context.Context, in PostToolUseHookInput) (PostToolUseHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParsePostToolUseHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return postToolUseOutputToWire(out), nil
	}
}

// PostToolUseFailureHook wraps a typed callback as a HookCallback.
func PostToolUseFailureHook(fn func(ctx context.Context, in PostToolUseFailureHookInput) (PostToolUseFailureHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParsePostToolUseFailureHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return postToolUseFailureOutputToWire(out), nil
	}
}

// UserPromptSubmitHook wraps a typed callback as a HookCallback.
func UserPromptSubmitHook(fn func(ctx context.Context, in UserPromptSubmitHookInput) (UserPromptSubmitHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParseUserPromptSubmitHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return userPromptSubmitOutputToWire(out), nil
	}
}

// StopHook wraps a typed callback as a HookCallback.
func StopHook(fn func(ctx context.Context, in StopHookInput) (StopHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParseStopHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return stopOutputToWire(out), nil
	}
}

// SubagentStartHook wraps a typed callback as a HookCallback.
func SubagentStartHook(fn func(ctx context.Context, in SubagentStartHookInput) (SubagentStartHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParseSubagentStartHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return subagentStartOutputToWire(out), nil
	}
}

// SubagentStopHook wraps a typed callback as a HookCallback.
func SubagentStopHook(fn func(ctx context.Context, in SubagentStopHookInput) (SubagentStopHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParseSubagentStopHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return subagentStopOutputToWire(out), nil
	}
}

// PreCompactHook wraps a typed callback as a HookCallback.
func PreCompactHook(fn func(ctx context.Context, in PreCompactHookInput) (PreCompactHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParsePreCompactHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return preCompactOutputToWire(out), nil
	}
}

// NotificationHook wraps a typed callback as a HookCallback.
func NotificationHook(fn func(ctx context.Context, in NotificationHookInput) (NotificationHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParseNotificationHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return notificationOutputToWire(out), nil
	}
}

// PermissionRequestHook wraps a typed callback as a HookCallback.
func PermissionRequestHook(fn func(ctx context.Context, in PermissionRequestHookInput) (PermissionRequestHookOutput, error)) HookCallback {
	return func(ctx context.Context, raw map[string]any, toolUseID string) (map[string]any, error) {
		in := ParsePermissionRequestHookInput(raw)
		out, err := fn(ctx, in)
		if err != nil {
			return nil, err
		}
		return permissionRequestOutputToWire(out), nil
	}
}

// --- typed output -> wire form ---
//
// The Python SDK's HookJSONOutput uses snake_case names (async_, continue_)
// on the Go side and renames them at serialization time. We mirror that
// here: callers see async_/continue_ on the typed structs; the wire
// helpers below emit "async" and "continue".
//
// Event-specific fields (permissionDecision, additionalContext, ...)
// ride under hookSpecificOutput because that's where the CLI looks for
// them. The Continue/StopReason/SuppressOutput/SystemMessage fields are
// top-level on both variants.

func preToolUseOutputToWire(out PreToolUseHookOutput) map[string]any {
	spec := map[string]any{}
	if out.PermissionDecision != "" {
		spec["permissionDecision"] = out.PermissionDecision
	}
	if out.PermissionDecisionReason != "" {
		spec["permissionDecisionReason"] = out.PermissionDecisionReason
	}
	if out.UpdatedInput != nil {
		spec["updatedInput"] = out.UpdatedInput
	}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventPreToolUse
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func postToolUseOutputToWire(out PostToolUseHookOutput) map[string]any {
	spec := map[string]any{}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	if out.UpdatedToolOutput != nil {
		spec["updatedToolOutput"] = out.UpdatedToolOutput
	}
	if out.UpdatedMCPToolOutput != nil {
		spec["updatedMCPToolOutput"] = out.UpdatedMCPToolOutput
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventPostToolUse
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func postToolUseFailureOutputToWire(out PostToolUseFailureHookOutput) map[string]any {
	spec := map[string]any{}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventPostToolUseFailure
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func userPromptSubmitOutputToWire(out UserPromptSubmitHookOutput) map[string]any {
	spec := map[string]any{}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventUserPromptSubmit
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func stopOutputToWire(out StopHookOutput) map[string]any {
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if out.Decision != "" {
		sync["decision"] = out.Decision
	}
	if out.Reason != "" {
		sync["reason"] = out.Reason
	}
	return sync
}

func subagentStartOutputToWire(out SubagentStartHookOutput) map[string]any {
	spec := map[string]any{}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventSubagentStart
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func subagentStopOutputToWire(out SubagentStopHookOutput) map[string]any {
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if out.Decision != "" {
		sync["decision"] = out.Decision
	}
	if out.Reason != "" {
		sync["reason"] = out.Reason
	}
	return sync
}

func preCompactOutputToWire(out PreCompactHookOutput) map[string]any {
	return syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
}

func notificationOutputToWire(out NotificationHookOutput) map[string]any {
	spec := map[string]any{}
	if out.AdditionalContext != "" {
		spec["additionalContext"] = out.AdditionalContext
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventNotification
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

func permissionRequestOutputToWire(out PermissionRequestHookOutput) map[string]any {
	spec := map[string]any{}
	if out.Decision != nil {
		spec["decision"] = out.Decision
	}
	sync := syncHookFields(out.Continue, out.StopReason, out.SuppressOutput, out.SystemMessage)
	if len(spec) > 0 {
		spec["hookEventName"] = HookEventPermissionRequest
		sync["hookSpecificOutput"] = spec
	}
	return sync
}

// syncHookFields builds the sync-variant top-level fields. Continue is
// emitted only when explicitly false (the CLI defaults to true; writing
// the default is harmless but the Python SDK writes it only when set
// false to keep the wire form terse).
func syncHookFields(continueOk bool, stopReason string, suppressOutput bool, systemMessage string) map[string]any {
	out := map[string]any{}
	if !continueOk {
		out["continue"] = false
	}
	if stopReason != "" {
		out["stopReason"] = stopReason
	}
	if suppressOutput {
		out["suppressOutput"] = true
	}
	if systemMessage != "" {
		out["systemMessage"] = systemMessage
	}
	return out
}
