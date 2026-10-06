package claudeagentsdk

import (
	"context"
	"testing"
	"time"
)

const testPNG = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+aD1sAAAAASUVORK5CYII="

func TestInvalidPrompts(t *testing.T) {
	tests := map[string][]InputBlock{
		"nil":               nil,
		"empty":             {},
		"unknown block":     {{Type: "tool_use"}},
		"empty text":        {{Type: "text"}},
		"text source":       {{Type: "text", Text: "hi", Source: &ImageSource{}}},
		"missing source":    {{Type: "image"}},
		"image text":        {{Type: "image", Text: "hi", Source: &ImageSource{}}},
		"unknown source":    {{Type: "image", Source: &ImageSource{Type: "file"}}},
		"empty data":        {{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png"}}},
		"invalid base64":    {{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: "???"}}},
		"unsupported media": {{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/svg+xml", Data: testPNG}}},
		"base64 with URL":   {{Type: "image", Source: &ImageSource{Type: "base64", MediaType: "image/png", Data: testPNG, URL: "https://example.com/a"}}},
		"empty URL":         {{Type: "image", Source: &ImageSource{Type: "url"}}},
		"relative URL":      {{Type: "image", Source: &ImageSource{Type: "url", URL: "/a.png"}}},
		"file URL":          {{Type: "image", Source: &ImageSource{Type: "url", URL: "file:///a.png"}}},
		"malformed URL":     {{Type: "image", Source: &ImageSource{Type: "url", URL: "https://%"}}},
		"URL with data":     {{Type: "image", Source: &ImageSource{Type: "url", URL: "https://example.com/a", Data: testPNG}}},
		"URL with media":    {{Type: "image", Source: &ImageSource{Type: "url", URL: "https://example.com/a", MediaType: "image/png"}}},
	}
	for name, blocks := range tests {
		t.Run(name, func(t *testing.T) {
			ft := &fakeTransport{writes: make(chan string, 1)}
			c := NewClientWithTransport(Options{}, ft)
			if err := c.Send(context.Background(), blocks); err == nil {
				t.Fatal("Send accepted invalid blocks")
			}
			if len(ft.writes) != 0 {
				t.Fatal("invalid blocks reached the transport")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			out, errs := Query(ctx, blocks, Options{CLIPath: "/nonexistent/claude"})
			for msg := range out {
				t.Fatalf("unexpected message: %#v", msg)
			}
			err := <-errs
			want := validatePrompt(blocks)
			if err == nil || err.Error() != want.Error() {
				t.Fatalf("Query error = %v, want %v before CLI connection", err, want)
			}
		})
	}
}
