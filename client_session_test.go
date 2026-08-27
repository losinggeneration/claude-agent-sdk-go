package claudeagentsdk

import (
	"context"
	"encoding/json"
	"testing"
)

func TestClientSendDefaultsSessionID(t *testing.T) {
	ft := &fakeTransport{writes: make(chan string, 1)}
	c := &Client{transport: ft}
	if err := c.Send(context.Background(), "hi"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(<-ft.writes), &got); err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "default" {
		t.Fatalf("session_id = %q, want default", got["session_id"])
	}
}

func TestClientSendUsesCustomSessionID(t *testing.T) {
	ft := &fakeTransport{writes: make(chan string, 1)}
	c := &Client{transport: ft}
	if err := c.Send(context.Background(), "hi", "custom-session"); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(<-ft.writes), &got); err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "custom-session" {
		t.Fatalf("session_id = %q, want custom-session", got["session_id"])
	}
}

func TestClientSendMessageDefaultsSessionID(t *testing.T) {
	ft := &fakeTransport{writes: make(chan string, 1)}
	c := &Client{transport: ft}
	if err := c.SendMessage(context.Background(), map[string]any{"type": "user"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(<-ft.writes), &got); err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "default" {
		t.Fatalf("session_id = %q, want default", got["session_id"])
	}
}

func TestClientSendMessagePreservesSessionID(t *testing.T) {
	ft := &fakeTransport{writes: make(chan string, 1)}
	c := &Client{transport: ft}
	if err := c.SendMessage(context.Background(), map[string]any{"type": "user", "session_id": "kept"}); err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(<-ft.writes), &got); err != nil {
		t.Fatal(err)
	}
	if got["session_id"] != "kept" {
		t.Fatalf("session_id = %q, want kept", got["session_id"])
	}
}
