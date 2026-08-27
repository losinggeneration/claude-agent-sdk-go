package claudeagentsdk

import (
	"context"
	"io"
)

type fakeTransport struct {
	in     chan map[string]any
	writes chan string
}

func (f *fakeTransport) Connect(context.Context) error {
	if f.in == nil {
		f.in = make(chan map[string]any, 10)
	}
	if f.writes == nil {
		f.writes = make(chan string, 10)
	}
	return nil
}
func (f *fakeTransport) Write(ctx context.Context, s string) error { f.writes <- s; return nil }
func (f *fakeTransport) EndInput() error                           { return nil }
func (f *fakeTransport) ReadMessages(context.Context) (<-chan map[string]any, <-chan error) {
	e := make(chan error)
	return f.in, e
}
func (f *fakeTransport) Close() error { return nil }

var _ io.Closer = (*fakeTransport)(nil)
