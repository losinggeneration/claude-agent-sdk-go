// Quick start example for the claude-agent-sdk-go SDK.
//
// Run with: go run ./examples/quick_start
//
// Each section connects to the local Claude Code CLI, runs a short
// prompt, and prints the model's reply.
package main

import (
	"context"
	"fmt"
	"log"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

func basicExample(ctx context.Context) error {
	fmt.Println("=== Basic Example ===")
	out, errs := claude.Query(ctx, "What is 2 + 2?", claude.Options{})
	if err := drainMessages(out, errs); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func withOptionsExample(ctx context.Context) error {
	fmt.Println("=== With Options Example ===")
	opts := claude.Options{
		SystemPrompt: "You are a helpful assistant that explains things simply.",
		MaxTurns:     1,
	}
	out, errs := claude.Query(ctx, "Explain what Go is in one sentence.", opts)
	if err := drainMessages(out, errs); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

func withToolsExample(ctx context.Context) error {
	fmt.Println("=== With Tools Example ===")
	opts := claude.Options{
		AllowedTools:   []string{"Read", "Write"},
		SystemPrompt:   "You are a helpful file assistant.",
		PermissionMode: claude.PermissionAcceptEdits,
	}
	out, errs := claude.Query(ctx, "Create a file called hello.txt with 'Hello, World!' in it", opts)
	if err := drainMessages(out, errs); err != nil {
		return err
	}
	fmt.Println()
	return nil
}

// drainMessages consumes a Query channel pair, prints assistant text
// blocks, and surfaces any terminal error.
func drainMessages(out <-chan claude.Message, errs <-chan error) error {
	for {
		select {
		case msg, ok := <-out:
			if !ok {
				return nil
			}
			if m, ok := msg.(claude.AssistantMessage); ok {
				for _, b := range m.Content {
					if tb, ok := b.(claude.TextBlock); ok {
						fmt.Printf("Claude: %s\n", tb.Text)
					}
				}
			}
			if m, ok := msg.(claude.ResultMessage); ok && m.TotalCostUSD != nil {
				fmt.Printf("\nCost: $%.4f\n", *m.TotalCostUSD)
			}
		case err := <-errs:
			if err != nil {
				return err
			}
			return nil
		}
	}
}

func main() {
	ctx := context.Background()
	for _, fn := range []func(context.Context) error{basicExample, withOptionsExample, withToolsExample} {
		if err := fn(ctx); err != nil {
			log.Fatal(err)
		}
	}
}
