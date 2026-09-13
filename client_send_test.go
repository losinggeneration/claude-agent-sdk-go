package claudeagentsdk

import (
	"context"
	"encoding/json"
	"testing"
	"time"
)

// Send must pass content blocks through as the user message content, so
// hosts can attach images ({"type": "image", "source": {...}}) alongside text.
func TestSendContentBlocks(t *testing.T) {
	read := func(t *testing.T, ft *fakeTransport) map[string]any {
		t.Helper()
		select {
		case line := <-ft.writes:
			var m map[string]any
			if err := json.Unmarshal([]byte(line), &m); err != nil {
				t.Fatalf("wire line is not JSON: %v", err)
			}
			return m
		case <-time.After(time.Second):
			t.Fatal("no message written")
			return nil
		}
	}

	t.Run("string prompt stays a JSON string", func(t *testing.T) {
		ft := &fakeTransport{writes: make(chan string, 4)}
		c := NewClientWithTransport(Options{}, ft)
		if err := c.Send(context.Background(), "hi"); err != nil {
			t.Fatal(err)
		}
		inner := read(t, ft)["message"].(map[string]any)
		if inner["content"] != "hi" {
			t.Fatalf("expected string content, got %v", inner["content"])
		}
	})

	t.Run("content blocks pass through as an array", func(t *testing.T) {
		ft := &fakeTransport{writes: make(chan string, 4)}
		c := NewClientWithTransport(Options{}, ft)
		blocks := []map[string]any{
			{"type": "text", "text": "What is this?"},
			{"type": "image", "source": map[string]any{"type": "base64", "media_type": "image/png", "data": "aGk="}},
		}
		if err := c.Send(context.Background(), blocks); err != nil {
			t.Fatal(err)
		}
		inner := read(t, ft)["message"].(map[string]any)
		content, ok := inner["content"].([]any)
		if !ok || len(content) != 2 {
			t.Fatalf("expected array content of 2 blocks, got %v", inner["content"])
		}
		if image := content[1].(map[string]any); image["type"] != "image" {
			t.Fatalf("expected image block, got %v", image)
		}
	})
}
