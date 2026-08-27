package claudeagentsdk

import (
	"context"
	"testing"
	"time"
)

func TestControlCancelRequest(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ft := &fakeTransport{in: make(chan map[string]any, 10), writes: make(chan string, 10)}
	started := make(chan struct{})
	cancelled := make(chan struct{})
	q, err := newControlLoop(ft, ft.in, make(chan error), Options{CanUseTool: func(ctx context.Context, toolName string, input map[string]any, c ToolPermissionContext) (PermissionResult, error) {
		close(started)
		<-ctx.Done()
		close(cancelled)
		return PermissionResult{Behavior: "deny"}, ctx.Err()
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.start(ctx)
	ft.in <- map[string]any{"type": "control_request", "request_id": "r1", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]any{}, "tool_use_id": "t1"}}
	<-started
	ft.in <- map[string]any{"type": "control_cancel_request", "request_id": "r1"}
	select {
	case <-cancelled:
	case <-time.After(time.Second):
		t.Fatal("callback was not cancelled")
	}
	select {
	case w := <-ft.writes:
		t.Fatalf("cancelled request wrote response: %s", w)
	case <-time.After(50 * time.Millisecond):
	}
}
