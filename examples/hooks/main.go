// Hooks example.
//
// Run with: go run ./examples/hooks
//
// Demonstrates typed PreToolUse and PostToolUse hooks via the
// PreToolUseHook / PostToolUseHook wrappers that turn typed callbacks
// into the underlying HookCallback shape.
package main

import (
	"context"
	"fmt"
	"log"
	"strings"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

// checkBashCommand blocks bash invocations whose command contains a
// forbidden substring, returning the typed PreToolUseHookOutput with a
// permissionDecision="deny" routing through the wire-form hookSpecificOutput.
func checkBashCommand(ctx context.Context, in claude.PreToolUseHookInput) (claude.PreToolUseHookOutput, error) {
	if in.ToolName != "Bash" {
		return claude.PreToolUseHookOutput{}, nil
	}
	cmd, _ := in.ToolInput["command"].(string)
	for _, blocked := range []string{"foo.sh"} {
		if strings.Contains(cmd, blocked) {
			return claude.PreToolUseHookOutput{
				PermissionDecision:       "deny",
				PermissionDecisionReason: fmt.Sprintf("Command contains invalid pattern: %s", blocked),
			}, nil
		}
	}
	return claude.PreToolUseHookOutput{}, nil
}

// rememberColor adds a system message every PostToolUse so the model
// sees a reminder injected via additionalContext.
func rememberColor(ctx context.Context, in claude.PostToolUseHookInput) (claude.PostToolUseHookOutput, error) {
	if in.ToolName == "Read" {
		return claude.PostToolUseHookOutput{AdditionalContext: "the user prefers terse summaries"}, nil
	}
	return claude.PostToolUseHookOutput{}, nil
}

func displayMessage(m claude.Message) {
	switch msg := m.(type) {
	case claude.AssistantMessage:
		for _, b := range msg.Content {
			if tb, ok := b.(claude.TextBlock); ok {
				fmt.Printf("Claude: %s\n", tb.Text)
			}
		}
	case claude.ResultMessage:
		fmt.Println("Result ended")
	}
}

func main() {
	opts := claude.Options{
		SystemPrompt: "You are a helpful assistant.",
		Hooks: map[string][]claude.HookMatcher{
			"PreToolUse": {{
				Matcher: "Bash",
				Hooks:   []claude.HookCallback{claude.PreToolUseHook(checkBashCommand)},
			}},
			"PostToolUse": {{
				Matcher: "Read|Glob|Grep",
				Hooks:   []claude.HookCallback{claude.PostToolUseHook(rememberColor)},
			}},
		},
	}

	c := claude.NewClient(opts)
	if err := c.Connect(context.Background()); err != nil {
		log.Fatal(err)
	}
	defer c.Close()

	if err := c.Send(context.Background(), "List the files in the current directory with Bash."); err != nil {
		log.Fatal(err)
	}
	for {
		msg, err := c.Receive(context.Background())
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
