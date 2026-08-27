// Streaming mode example.
//
// Run with: go run ./examples/streaming_mode
//
// Demonstrates ClaudeSDKClient.Connect / Query / Receive / Interrupt /
// Close, including a multi-turn conversation and an interrupt.
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

func displayMessage(m claude.Message) {
	switch msg := m.(type) {
	case claude.UserMessage:
		fmt.Printf("User: %v\n", msg.Content)
	case claude.AssistantMessage:
		for _, b := range msg.Content {
			if tb, ok := b.(claude.TextBlock); ok {
				fmt.Printf("Claude: %s\n", tb.Text)
			}
			if tu, ok := b.(claude.ToolUseBlock); ok {
				fmt.Printf("Claude: [tool call %s(%v)]\n", tu.Name, tu.Input)
			}
			if tr, ok := b.(claude.ToolResultBlock); ok {
				fmt.Printf("Result: %v\n", tr.Content)
			}
		}
	case claude.ResultMessage:
		fmt.Println("Result ended")
	}
}

func basicStreaming(ctx context.Context) {
	fmt.Println("=== Basic Streaming ===")
	c := claude.NewClient(claude.Options{})
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	if err := c.Send(ctx, "What is 2 + 2?"); err != nil {
		log.Fatal(err)
	}
	for {
		msg, err := c.Receive(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if msg == nil {
			return
		}
		displayMessage(msg)
		if _, ok := msg.(claude.ResultMessage); ok {
			return
		}
	}
}

func multiTurn(ctx context.Context) {
	fmt.Println("=== Multi-Turn Conversation ===")
	c := claude.NewClient(claude.Options{})
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	turns := []string{
		"What's the capital of France?",
		"And what's the population of that city? Just an estimate.",
		"Compare it to the population of Berlin.",
	}
	for _, t := range turns {
		fmt.Printf("\nUser: %s\n", t)
		if err := c.Send(ctx, t); err != nil {
			log.Fatal(err)
		}
		for {
			msg, err := c.Receive(ctx)
			if err != nil {
				log.Fatal(err)
			}
			if msg == nil {
				return
			}
			displayMessage(msg)
			if _, ok := msg.(claude.ResultMessage); ok {
				break
			}
		}
	}
}

func interruptDemo(ctx context.Context) {
	fmt.Println("=== Interrupt Demo ===")
	c := claude.NewClient(claude.Options{})
	if err := c.Connect(ctx); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	if err := c.Send(ctx, "Count slowly from 1 to 100, one number per line."); err != nil {
		log.Fatal(err)
	}

	// Interrupt after a short delay to demonstrate mid-turn cancellation.
	go func() {
		time.Sleep(500 * time.Millisecond)
		if err := c.Interrupt(ctx); err != nil {
			log.Printf("interrupt: %v", err)
		}
	}()
	for {
		msg, err := c.Receive(ctx)
		if err != nil {
			log.Fatal(err)
		}
		if msg == nil {
			return
		}
		displayMessage(msg)
		if _, ok := msg.(claude.ResultMessage); ok {
			return
		}
	}
}

func main() {
	// Honor SIGINT so the example doesn't hang on Ctrl-C mid-stream.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	basicStreaming(ctx)
	multiTurn(ctx)
	interruptDemo(ctx)
}
