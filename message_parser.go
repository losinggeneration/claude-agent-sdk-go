package claudeagentsdk

import "fmt"

func ParseMessage(data map[string]any) (Message, error) {
	if data == nil {
		return nil, &MessageParseError{Msg: "invalid message data type"}
	}
	if data["type"] == "system" && (data["subtype"] == "hook_started" || data["subtype"] == "hook_response") {
		return HookEventMessage{
			SystemMessage: SystemMessage{Subtype: str(data["subtype"]), Data: data},
			HookEventName: firstString(data["hook_event"], data["hook_name"], data["hook_event_name"]),
			SessionID:     str(data["session_id"]), UUID: str(data["uuid"]),
		}, nil
	}

	switch typ := str(data["type"]); typ {
	case "user":
		msg := rawMap(data["message"])
		content := msg["content"]
		if blocks, ok := content.([]any); ok {
			parsed, err := parseContentBlocks(blocks)
			if err != nil {
				return nil, err
			}
			content = parsed
		}
		return UserMessage{Content: content, UUID: str(data["uuid"]), ParentToolUseID: str(data["parent_tool_use_id"]), ToolUseResult: rawMap(data["tool_use_result"]), Origin: parseOrigin(data)}, nil
	case "assistant":
		msg := rawMap(data["message"])
		blocks, ok := msg["content"].([]any)
		if !ok {
			return nil, &MessageParseError{Msg: "invalid assistant content", Data: data}
		}
		content, err := parseContentBlocks(blocks)
		if err != nil {
			return nil, err
		}
		return AssistantMessage{Content: content, Model: str(msg["model"]), ParentToolUseID: str(data["parent_tool_use_id"]), Error: str(data["error"]), Usage: rawMap(msg["usage"]), MessageID: str(msg["id"]), StopReason: str(msg["stop_reason"]), SessionID: str(data["session_id"]), UUID: str(data["uuid"])}, nil
	case "system":
		subtype := str(data["subtype"])
		sys := SystemMessage{Subtype: subtype, Data: data}
		switch subtype {
		case "task_started":
			return TaskStartedMessage{SystemMessage: sys, TaskID: str(data["task_id"]), Description: str(data["description"]), UUID: str(data["uuid"]), SessionID: str(data["session_id"]), ToolUseID: str(data["tool_use_id"]), TaskType: str(data["task_type"])}, nil
		case "task_progress":
			return TaskProgressMessage{SystemMessage: sys, TaskID: str(data["task_id"]), Description: str(data["description"]), Usage: rawMap(data["usage"]), UUID: str(data["uuid"]), SessionID: str(data["session_id"]), ToolUseID: str(data["tool_use_id"]), LastToolName: str(data["last_tool_name"])}, nil
		case "task_notification":
			return TaskNotificationMessage{SystemMessage: sys, TaskID: str(data["task_id"]), Status: str(data["status"]), OutputFile: str(data["output_file"]), Summary: str(data["summary"]), UUID: str(data["uuid"]), SessionID: str(data["session_id"]), ToolUseID: str(data["tool_use_id"]), Usage: rawMap(data["usage"])}, nil
		case "task_updated":
			patch := rawMap(data["patch"])
			return TaskUpdatedMessage{SystemMessage: sys, TaskID: str(data["task_id"]), Patch: patch, Status: str(patch["status"]), SessionID: str(data["session_id"]), UUID: str(data["uuid"])}, nil
		case "mirror_error":
			return MirrorErrorMessage{SystemMessage: sys, Key: rawMap(data["key"]), Error: str(data["error"])}, nil
		default:
			return sys, nil
		}
	case "result":
		var cost *float64
		if v, ok := asFloat(data["total_cost_usd"]); ok {
			cost = &v
		}
		var status *int
		if v, ok := asInt(data["api_error_status"]); ok {
			status = &v
		}
		var deferred *DeferredToolUse
		if d := rawMap(data["deferred_tool_use"]); d != nil {
			deferred = &DeferredToolUse{ID: str(d["id"]), Name: str(d["name"]), Input: rawMap(d["input"])}
		}
		return ResultMessage{Subtype: str(data["subtype"]), DurationMS: intVal(data["duration_ms"]), DurationAPIMS: intVal(data["duration_api_ms"]), IsError: boolVal(data["is_error"]), NumTurns: intVal(data["num_turns"]), SessionID: str(data["session_id"]), StopReason: str(data["stop_reason"]), TotalCostUSD: cost, Usage: rawMap(data["usage"]), Result: str(data["result"]), StructuredOutput: data["structured_output"], ModelUsage: rawMap(data["modelUsage"]), PermissionDenials: rawSlice(data["permission_denials"]), DeferredToolUse: deferred, Errors: normalizeResultErrors(data["errors"]), APIErrorStatus: status, UUID: str(data["uuid"]), TerminalReason: str(data["terminal_reason"]), Origin: parseOrigin(data)}, nil
	case "stream_event":
		return StreamEvent{UUID: str(data["uuid"]), SessionID: str(data["session_id"]), Event: rawMap(data["event"]), ParentToolUseID: str(data["parent_tool_use_id"])}, nil
	case "rate_limit_event":
		info := rawMap(data["rate_limit_info"])
		return RateLimitEvent{RateLimitInfo: RateLimitInfo{Status: str(info["status"]), ResetsAt: int64Ptr(info["resetsAt"]), RateLimitType: str(info["rateLimitType"]), Utilization: floatPtr(info["utilization"]), OverageStatus: str(info["overageStatus"]), OverageResetsAt: int64Ptr(info["overageResetsAt"]), OverageDisabledReason: str(info["overageDisabledReason"]), Raw: info}, UUID: str(data["uuid"]), SessionID: str(data["session_id"])}, nil
	case "conversation_reset":
		return ConversationResetMessage{NewConversationID: str(data["new_conversation_id"]), UUID: str(data["uuid"]), SessionID: str(data["session_id"])}, nil
	case "":
		return nil, &MessageParseError{Msg: "message missing 'type' field", Data: data}
	default:
		return nil, nil
	}
}

func parseContentBlocks(blocks []any) ([]ContentBlock, error) {
	out := make([]ContentBlock, 0, len(blocks))
	for _, b := range blocks {
		m, ok := b.(map[string]any)
		if !ok {
			return nil, &MessageParseError{Msg: "invalid content block"}
		}
		switch str(m["type"]) {
		case "text":
			out = append(out, TextBlock{Text: str(m["text"])})
		case "thinking":
			out = append(out, ThinkingBlock{Thinking: str(m["thinking"]), Signature: str(m["signature"])})
		case "tool_use":
			out = append(out, ToolUseBlock{ID: str(m["id"]), Name: str(m["name"]), Input: rawMap(m["input"])})
		case "tool_result":
			var isErr *bool
			if v, ok := m["is_error"].(bool); ok {
				isErr = &v
			}
			out = append(out, ToolResultBlock{ToolUseID: str(m["tool_use_id"]), Content: m["content"], IsError: isErr})
		case "server_tool_use":
			out = append(out, ServerToolUseBlock{ID: str(m["id"]), Name: str(m["name"]), Input: rawMap(m["input"])})
		case "advisor_tool_result":
			out = append(out, ServerToolResultBlock{ToolUseID: str(m["tool_use_id"]), Content: rawMap(m["content"])})
		}
	}
	return out, nil
}

func parseOrigin(data map[string]any) map[string]any {
	origin := rawMap(data["origin"])
	if origin != nil && str(origin["kind"]) != "" {
		return origin
	}
	return nil
}

func firstString(v ...any) string {
	for _, x := range v {
		if s := str(x); s != "" {
			return s
		}
	}
	return ""
}
func str(v any) string   { s, _ := v.(string); return s }
func boolVal(v any) bool { b, _ := v.(bool); return b }
func intVal(v any) int   { i, _ := asInt(v); return i }
func int64Ptr(v any) *int64 {
	if i, ok := asInt(v); ok {
		p := int64(i)
		return &p
	}
	return nil
}

func floatPtr(v any) *float64 {
	if f, ok := asFloat(v); ok {
		return &f
	}
	return nil
}

func asInt(v any) (int, bool) {
	switch x := v.(type) {
	case int:
		return x, true
	case float64:
		return int(x), true
	case jsonNumber:
		i, err := x.Int64()
		return int(i), err == nil
	default:
		return 0, false
	}
}

func asFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int:
		return float64(x), true
	case jsonNumber:
		f, err := x.Float64()
		return f, err == nil
	default:
		return 0, false
	}
}
func floatVal(v any) float64 { f, _ := asFloat(v); return f }

type jsonNumber interface {
	Int64() (int64, error)
	Float64() (float64, error)
	String() string
}

var _ = fmt.Sprintf
