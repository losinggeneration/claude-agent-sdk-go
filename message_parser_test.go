package claudeagentsdk

import "testing"

func TestParseAssistantMessage(t *testing.T) {
	msg, err := ParseMessage(map[string]any{
		"type": "assistant",
		"message": map[string]any{
			"model": "claude",
			"content": []any{
				map[string]any{"type": "text", "text": "hi"},
				map[string]any{"type": "tool_use", "id": "1", "name": "Read", "input": map[string]any{"file_path": "x"}},
			},
		},
		"session_id": "s",
	})
	if err != nil {
		t.Fatal(err)
	}
	a, ok := msg.(AssistantMessage)
	if !ok || a.Model != "claude" || len(a.Content) != 2 {
		t.Fatalf("bad message: %#v", msg)
	}
	if a.Content[0].(TextBlock).Text != "hi" {
		t.Fatalf("bad text block: %#v", a.Content[0])
	}
}

func TestParseResultMessage(t *testing.T) {
	msg, err := ParseMessage(map[string]any{
		"type":            "result",
		"subtype":         "success",
		"duration_ms":     float64(1),
		"duration_api_ms": float64(2),
		"is_error":        true,
		"num_turns":       float64(3),
		"session_id":      "s",
		"errors":          []any{"boom"},
	})
	if err != nil {
		t.Fatal(err)
	}
	r := msg.(ResultMessage)
	if !r.IsError || r.NumTurns != 3 || len(r.Errors) != 1 || r.Errors[0] != "boom" {
		t.Fatalf("bad result: %#v", r)
	}
}
