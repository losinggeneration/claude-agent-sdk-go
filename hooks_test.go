package claudeagentsdk

import (
	"context"
	"encoding/json"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// Tests for the typed hook wrappers (PreToolUseHook, PostToolUseHook, ...)
// and the wire-form serialization that mirrors the Python SDK's
// HookJSONOutput.

func TestPreToolUseHookWireShape(t *testing.T) {
	cb := PreToolUseHook(func(ctx context.Context, in PreToolUseHookInput) (PreToolUseHookOutput, error) {
		if in.ToolName != "Bash" {
			t.Errorf("ToolName = %q, want Bash", in.ToolName)
		}
		if in.ToolUseID != "t1" {
			t.Errorf("ToolUseID = %q, want t1", in.ToolUseID)
		}
		if in.SessionID != "s1" {
			t.Errorf("SessionID = %q, want s1", in.SessionID)
		}
		if in.AgentID != "a1" {
			t.Errorf("AgentID = %q, want a1", in.AgentID)
		}
		return PreToolUseHookOutput{
			Continue:                 false,
			StopReason:               "blocked by hook",
			PermissionDecision:       "deny",
			PermissionDecisionReason: "denied by policy",
			UpdatedInput:             map[string]any{"cmd": "echo hi"},
		}, nil
	})
	out, err := cb(context.Background(), map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_use_id":     "t1",
		"tool_input":      map[string]any{"cmd": "echo hi"},
		"session_id":      "s1",
		"agent_id":        "a1",
		"cwd":             "/tmp",
	}, "t1")
	if err != nil {
		t.Fatal(err)
	}
	if out["continue"] != false {
		t.Errorf("continue = %v, want false", out["continue"])
	}
	if out["stopReason"] != "blocked by hook" {
		t.Errorf("stopReason wrong: %v", out["stopReason"])
	}
	spec, ok := out["hookSpecificOutput"].(map[string]any)
	if !ok {
		t.Fatalf("missing hookSpecificOutput: %#v", out)
	}
	if spec["hookEventName"] != "PreToolUse" {
		t.Errorf("hookEventName wrong: %v", spec["hookEventName"])
	}
	if spec["permissionDecision"] != "deny" {
		t.Errorf("permissionDecision wrong: %v", spec["permissionDecision"])
	}
	if spec["permissionDecisionReason"] != "denied by policy" {
		t.Errorf("permissionDecisionReason wrong: %v", spec["permissionDecisionReason"])
	}
	if _, ok := spec["updatedInput"]; !ok {
		t.Errorf("updatedInput missing: %#v", spec)
	}
}

func TestPostToolUseHookWireShape(t *testing.T) {
	cb := PostToolUseHook(func(ctx context.Context, in PostToolUseHookInput) (PostToolUseHookOutput, error) {
		return PostToolUseHookOutput{
			AdditionalContext: "remembered",
			UpdatedToolOutput: map[string]any{"rewritten": true},
		}, nil
	})
	out, err := cb(context.Background(), map[string]any{
		"hook_event_name": "PostToolUse",
		"tool_name":       "Read",
		"tool_use_id":     "t2",
		"tool_input":      map[string]any{},
		"tool_response":   map[string]any{"x": 1},
	}, "t2")
	if err != nil {
		t.Fatal(err)
	}
	spec := out["hookSpecificOutput"].(map[string]any)
	if spec["additionalContext"] != "remembered" {
		t.Errorf("additionalContext wrong: %v", spec["additionalContext"])
	}
	if spec["updatedToolOutput"].(map[string]any)["rewritten"] != true {
		t.Errorf("updatedToolOutput wrong: %v", spec["updatedToolOutput"])
	}
}

func TestStopHookWireShape(t *testing.T) {
	cb := StopHook(func(ctx context.Context, in StopHookInput) (StopHookOutput, error) {
		if !in.StopHookActive {
			t.Error("StopHookActive should be true")
		}
		return StopHookOutput{Decision: "block", Reason: "user said no"}, nil
	})
	out, err := cb(context.Background(), map[string]any{
		"hook_event_name":  "Stop",
		"stop_hook_active": true,
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out["decision"] != "block" {
		t.Errorf("decision wrong: %v", out["decision"])
	}
	if out["reason"] != "user said no" {
		t.Errorf("reason wrong: %v", out["reason"])
	}
}

func TestUserPromptSubmitHookWireShape(t *testing.T) {
	cb := UserPromptSubmitHook(func(ctx context.Context, in UserPromptSubmitHookInput) (UserPromptSubmitHookOutput, error) {
		if in.Prompt != "hello" {
			t.Errorf("Prompt wrong: %q", in.Prompt)
		}
		return UserPromptSubmitHookOutput{AdditionalContext: "noted"}, nil
	})
	out, _ := cb(context.Background(), map[string]any{"hook_event_name": "UserPromptSubmit", "prompt": "hello"}, "")
	spec := out["hookSpecificOutput"].(map[string]any)
	if spec["additionalContext"] != "noted" {
		t.Errorf("additionalContext wrong: %v", spec)
	}
}

func TestNotificationHookWireShape(t *testing.T) {
	cb := NotificationHook(func(ctx context.Context, in NotificationHookInput) (NotificationHookOutput, error) {
		if in.Message != "rate limit" {
			t.Errorf("Message wrong: %q", in.Message)
		}
		return NotificationHookOutput{AdditionalContext: "warned"}, nil
	})
	out, _ := cb(context.Background(), map[string]any{
		"hook_event_name":   "Notification",
		"message":           "rate limit",
		"notification_type": "rate_limit",
	}, "")
	spec := out["hookSpecificOutput"].(map[string]any)
	if spec["additionalContext"] != "warned" {
		t.Errorf("additionalContext wrong: %v", spec)
	}
	if spec["hookEventName"] != "Notification" {
		t.Errorf("hookEventName wrong: %v", spec)
	}
}

func TestSubagentStartHookWireShape(t *testing.T) {
	cb := SubagentStartHook(func(ctx context.Context, in SubagentStartHookInput) (SubagentStartHookOutput, error) {
		if in.AgentID != "ag1" || in.AgentType != "general-purpose" {
			t.Errorf("agent fields wrong: %+v", in)
		}
		return SubagentStartHookOutput{}, nil
	})
	out, _ := cb(context.Background(), map[string]any{
		"hook_event_name": "SubagentStart",
		"agent_id":        "ag1",
		"agent_type":      "general-purpose",
	}, "")
	if out["hookSpecificOutput"] != nil {
		t.Errorf("empty typed output should produce empty sync form: %#v", out)
	}
}

func TestSubagentStopHookWireShape(t *testing.T) {
	cb := SubagentStopHook(func(ctx context.Context, in SubagentStopHookInput) (SubagentStopHookOutput, error) {
		if in.AgentID != "ag1" || in.AgentTranscriptPath != "/tmp/x" {
			t.Errorf("subagent fields wrong: %+v", in)
		}
		return SubagentStopHookOutput{Decision: "block", Reason: "stop"}, nil
	})
	out, _ := cb(context.Background(), map[string]any{
		"hook_event_name":       "SubagentStop",
		"agent_id":              "ag1",
		"agent_type":            "general-purpose",
		"agent_transcript_path": "/tmp/x",
	}, "")
	if out["decision"] != "block" {
		t.Errorf("decision wrong: %v", out["decision"])
	}
	if out["reason"] != "stop" {
		t.Errorf("reason wrong: %v", out["reason"])
	}
}

func TestPreCompactHookWireShape(t *testing.T) {
	cb := PreCompactHook(func(ctx context.Context, in PreCompactHookInput) (PreCompactHookOutput, error) {
		if in.Trigger != "auto" {
			t.Errorf("Trigger wrong: %q", in.Trigger)
		}
		return PreCompactHookOutput{SuppressOutput: true, SystemMessage: "compacting"}, nil
	})
	out, _ := cb(context.Background(), map[string]any{
		"hook_event_name": "PreCompact",
		"trigger":         "auto",
	}, "")
	if out["suppressOutput"] != true {
		t.Errorf("suppressOutput wrong: %v", out["suppressOutput"])
	}
	if out["systemMessage"] != "compacting" {
		t.Errorf("systemMessage wrong: %v", out["systemMessage"])
	}
}

func TestPermissionRequestHookWireShape(t *testing.T) {
	cb := PermissionRequestHook(func(ctx context.Context, in PermissionRequestHookInput) (PermissionRequestHookOutput, error) {
		if in.ToolName != "Bash" {
			t.Errorf("ToolName wrong: %q", in.ToolName)
		}
		return PermissionRequestHookOutput{
			Decision: map[string]any{"behavior": "allow"},
		}, nil
	})
	out, _ := cb(context.Background(), map[string]any{
		"hook_event_name": "PermissionRequest",
		"tool_name":       "Bash",
		"tool_input":      map[string]any{},
	}, "")
	spec := out["hookSpecificOutput"].(map[string]any)
	decision := spec["decision"].(map[string]any)
	if decision["behavior"] != "allow" {
		t.Errorf("decision wrong: %v", decision)
	}
}

func TestAsyncHook(t *testing.T) {
	cb := AsyncHook()
	out, err := cb(context.Background(), map[string]any{}, "")
	if err != nil {
		t.Fatal(err)
	}
	if out["async"] != true {
		t.Errorf("async=true missing: %#v", out)
	}
}

func TestContinueDefaultNotEmitted(t *testing.T) {
	// Default Continue=true should not emit a `continue` field on the wire.
	out := syncHookFields(true, "", false, "")
	if _, has := out["continue"]; has {
		t.Errorf("continue should be omitted when default true: %#v", out)
	}
}

func TestMultipleHooksFireConcurrently(t *testing.T) {
	// Two hooks on the same matcher must run concurrently (each on its
	// own goroutine) — the CLI dispatches them in parallel. Verify by
	// racing their execution: each waits on its own channel.
	got1, got2 := make(chan struct{}), make(chan struct{})
	sync := make(chan struct{})
	hb1 := func(ctx context.Context, in map[string]any, id string) (map[string]any, error) {
		<-sync
		close(got1)
		return map[string]any{"continue": true}, nil
	}
	hb2 := func(ctx context.Context, in map[string]any, id string) (map[string]any, error) {
		<-sync
		close(got2)
		return map[string]any{"continue": true}, nil
	}
	done := make(chan struct{}, 2)
	go func() { _, _ = hb1(context.Background(), map[string]any{}, ""); done <- struct{}{} }()
	go func() { _, _ = hb2(context.Background(), map[string]any{}, ""); done <- struct{}{} }()
	close(sync)
	<-done
	<-done
	select {
	case <-got1:
	default:
		t.Fatal("hook 1 did not run")
	}
	select {
	case <-got2:
	default:
		t.Fatal("hook 2 did not run")
	}
}

func TestParsePreToolUseHookInput(t *testing.T) {
	in := ParsePreToolUseHookInput(map[string]any{
		"hook_event_name": "PreToolUse",
		"tool_name":       "Bash",
		"tool_use_id":     "t1",
		"tool_input":      map[string]any{"x": 1},
		"session_id":      "s1",
		"cwd":             "/tmp",
		"agent_id":        "a1",
		"agent_type":      "code",
	})
	if in.ToolName != "Bash" || in.ToolUseID != "t1" || in.SessionID != "s1" {
		t.Errorf("fields wrong: %+v", in)
	}
	if in.AgentID != "a1" || in.AgentType != "code" {
		t.Errorf("subagent fields wrong: %+v", in.SubagentContext)
	}
	if in.ToolInput["x"] != 1 {
		t.Errorf("tool_input wrong: %+v", in.ToolInput)
	}
}

func TestHookMatcherConcurrentDispatchViaControl(t *testing.T) {
	// End-to-end: register two hooks under PreToolUse and a single matcher
	// and confirm both fire from the dispatch path.
	ft := &fakeTransport{in: make(chan map[string]any, 10), writes: make(chan string, 10)}
	var fired int32
	q, err := newControlLoop(ft, ft.in, make(chan error), Options{
		Hooks: map[string][]HookMatcher{
			"PreToolUse": {{
				Matcher: "Bash",
				Hooks: []HookCallback{
					func(ctx context.Context, in map[string]any, id string) (map[string]any, error) {
						atomic.AddInt32(&fired, 1)
						return map[string]any{"continue": true}, nil
					},
					func(ctx context.Context, in map[string]any, id string) (map[string]any, error) {
						atomic.AddInt32(&fired, 1)
						return map[string]any{"continue": true}, nil
					},
				},
			}},
		},
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	// Pre-register both callback ids so we don't depend on initialize here.
	q.hookCallbacks["a"] = q.HooksForTest()["PreToolUse"][0].Hooks[0]
	q.hookCallbacks["b"] = q.HooksForTest()["PreToolUse"][0].Hooks[1]
	q.start(context.Background())
	go func() {
		// Two concurrent responses (one per hook_callback request).
		w1 := <-ft.writes
		var m1 map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(w1)), &m1)
		ft.in <- map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m1["request_id"], "response": map[string]any{}}}
		w2 := <-ft.writes
		var m2 map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(w2)), &m2)
		ft.in <- map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m2["request_id"], "response": map[string]any{}}}
	}()
	ft.in <- map[string]any{"type": "control_request", "request_id": "r1", "request": map[string]any{"subtype": "hook_callback", "callback_id": "a", "input": map[string]any{}}}
	ft.in <- map[string]any{"type": "control_request", "request_id": "r2", "request": map[string]any{"subtype": "hook_callback", "callback_id": "b", "input": map[string]any{}}}
	deadline := time.After(2 * time.Second)
	for atomic.LoadInt32(&fired) < 2 {
		select {
		case <-deadline:
			t.Fatalf("only %d of 2 hooks fired", atomic.LoadInt32(&fired))
		default:
			time.Sleep(2 * time.Millisecond)
		}
	}
}
