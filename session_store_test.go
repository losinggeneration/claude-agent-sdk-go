package claudeagentsdk

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestControlLoopHookCallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ft := &fakeTransport{in: make(chan map[string]any, 10), writes: make(chan string, 10)}
	called := false
	q, err := newControlLoop(ft, ft.in, make(chan error), Options{Hooks: map[string][]HookMatcher{
		"PreToolUse": {{Matcher: "Bash", Hooks: []HookCallback{func(ctx context.Context, input map[string]any, toolUseID string) (map[string]any, error) {
			called = true
			return map[string]any{"ok": true}, nil
		}}}},
	}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	q.start(ctx)
	go func() {
		w := <-ft.writes
		var m map[string]any
		_ = json.Unmarshal([]byte(strings.TrimSpace(w)), &m)
		ft.in <- map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m["request_id"], "response": map[string]any{}}}
	}()
	if err := q.initialize(ctx); err != nil {
		t.Fatal(err)
	}
	ft.in <- map[string]any{"type": "control_request", "request_id": "r1", "request": map[string]any{"subtype": "hook_callback", "callback_id": "hook_0", "input": map[string]any{"x": 1}, "tool_use_id": "t1"}}
	select {
	case w := <-ft.writes:
		if !strings.Contains(w, "control_response") || !called {
			t.Fatalf("bad response %s called=%v", w, called)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

type memStore struct {
	data map[SessionKey][]map[string]any
	list []SessionStoreListEntry
}

func (m memStore) Append(_ context.Context, k SessionKey, entries []map[string]any) error {
	m.data[k] = append(m.data[k], entries...)
	return nil
}

func (m memStore) Load(ctx context.Context, k SessionKey) ([]map[string]any, error) {
	return m.data[k], nil
}

func (m memStore) ListSessions(ctx context.Context, p string) ([]SessionStoreListEntry, error) {
	return m.list, nil
}

func TestMaterializeResumeSession(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	pk := ProjectKeyForDirectory(".")
	s := memStore{
		data: map[SessionKey][]map[string]any{
			{ProjectKey: pk, SessionID: sid}: {{"type": "user", "uuid": "u"}},
		},
		list: []SessionStoreListEntry{{SessionID: sid, MTime: 1}},
	}
	m, err := MaterializeResumeSession(context.Background(), Options{SessionStore: s, ContinueConversation: true})
	if err != nil {
		t.Fatal(err)
	}
	defer m.Cleanup()
	if m.ResumeSessionID != sid {
		t.Fatalf("bad sid %q", m.ResumeSessionID)
	}
}
