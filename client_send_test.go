package claudeagentsdk

import (
	"context"
	"encoding/json"
	"reflect"
	"testing"
)

func TestSendContentBlocks(t *testing.T) {
	ft := &fakeTransport{writes: make(chan string, 1)}
	c := NewClientWithTransport(Options{}, ft)
	ctx := context.Background()
	if err := c.Send(ctx, "hi"); err != nil {
		t.Fatal(err)
	}
	assertPromptWire(t, <-ft.writes, `"hi"`)
	blocks := []InputBlock{
		{Type: "text", Text: "What is this?"},
		{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: testPNG}},
		{Type: "image", Source: &ImageSource{Type: "url", URL: "https://example.com/image.png"}},
	}
	if err := c.Send(ctx, blocks); err != nil {
		t.Fatal(err)
	}
	assertPromptWire(t, <-ft.writes, `[{"type":"text","text":"What is this?"},{"type":"image","source":{"type":"base64","media_type":"image/png","data":"`+testPNG+`"}},{"type":"image","source":{"type":"url","url":"https://example.com/image.png"}}]`)
}

func assertPromptWire(t *testing.T, line, content string) {
	t.Helper()
	var got, want any
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(`{"type":"user","message":{"role":"user","content":`+content+`},"parent_tool_use_id":null,"session_id":"default"}`), &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("wire message = %s, want %#v", line, want)
	}
}
