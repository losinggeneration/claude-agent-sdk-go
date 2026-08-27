// Persisted resume example.
//
// Run with: go run ./examples/resume_session
//
// Demonstrates the persisted multi-turn shape: save ResultMessage.SessionID,
// start a new client/query with Options.Resume, and send only the new follow-up
// prompt. The prior turns are loaded by Claude Code from the resumed transcript;
// the SDK does not resend full history as prompt text.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

func run(ctx context.Context, prompt string, opts claude.Options) (string, error) {
	out, errs := claude.Query(ctx, prompt, opts)
	var sessionID string
	for msg := range out {
		switch m := msg.(type) {
		case claude.AssistantMessage:
			for _, b := range m.Content {
				if tb, ok := b.(claude.TextBlock); ok {
					fmt.Println(tb.Text)
				}
			}
		case claude.ResultMessage:
			sessionID = m.SessionID
		}
	}
	if err := <-errs; err != nil {
		return "", err
	}
	return sessionID, nil
}

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cwd, err := os.Getwd()
	if err != nil {
		log.Fatal(err)
	}
	opts := claude.Options{CWD: cwd}

	fmt.Println("=== First process ===")
	sessionID, err := run(ctx, "Remember this word: cerulean. Reply with only OK.", opts)
	if err != nil {
		log.Fatal(err)
	}
	if sessionID == "" {
		log.Fatal("Claude Code did not return a session ID")
	}

	fmt.Println("\n=== New process, resumed session ===")
	opts.Resume = sessionID
	if _, err := run(ctx, "What word did I ask you to remember?", opts); err != nil {
		log.Fatal(err)
	}
}
