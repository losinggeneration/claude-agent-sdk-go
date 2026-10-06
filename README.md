# Claude Agent SDK for Go

Go port of the [Claude Agent SDK](https://github.com/anthropics/claude-agent-sdk-python),
mirroring the non-Python-specific surface of the Python original. Use Claude
Code from Go with the same control protocol, in-process MCP servers, hooks,
session stores, and message shapes.

See the [Claude Agent SDK documentation](https://platform.claude.com/docs/en/agent-sdk/overview) for more information.

## Installation

```sh
go get github.com/losinggeneration/claude-agent-sdk-go
```

**Prerequisites:**

- Go 1.27.0 or later
- Claude Code CLI on `$PATH` (or under `~/.local/bin`, `~/.claude/local`,
  or `/usr/local/bin`)

Unlike the Python package, the CLI is not bundled: install it separately
(`curl -fsSL https://claude.ai/install.sh | bash`) or point at a specific
binary with `Options.CLIPath`.

## Quick Start

```go
package main

import (
    "context"
    "fmt"

    claude "github.com/losinggeneration/claude-agent-sdk-go"
)

func main() {
    out, errs := claude.Query(context.Background(), "What is 2 + 2?", claude.Options{})
    for msg := range out {
        if m, ok := msg.(claude.AssistantMessage); ok {
            for _, b := range m.Content {
                if tb, ok := b.(claude.TextBlock); ok {
                    fmt.Println(tb.Text)
                }
            }
        }
    }
    if err := <-errs; err != nil {
        panic(err)
    }
}
```

## Basic Usage: Query()

`Query` sends one prompt to Claude Code and returns a channel of typed
response messages. See [client.go](client.go).

```go
// Simple query
out, errs := claude.Query(ctx, "Hello Claude", claude.Options{})
for msg := range out {
    if m, ok := msg.(claude.AssistantMessage); ok {
        for _, b := range m.Content {
            if tb, ok := b.(claude.TextBlock); ok {
                fmt.Println(tb.Text)
            }
        }
    }
}

// With options
options := claude.Options{
    SystemPrompt: "You are a helpful assistant",
    MaxTurns:     1,
}
out, errs = claude.Query(ctx, "Tell me a joke", options)
```

### Send images

`Query` and `Client.Send` accept either a `string` or `[]InputBlock`.
Use input blocks to combine text with base64-encoded images or HTTP(S) image URLs:

```go
blocks := []claude.InputBlock{
    {Type: "text", Text: "Describe this image."},
    {Type: "image", Source: &claude.ImageSource{
        Type: "url",
        URL:  "https://example.com/image.png",
    }},
}
out, errs := claude.Query(ctx, blocks, options)
```

For base64 images, set the source's `Type` to `"base64"`, `Data` to the
base64-encoded image, and `MediaType` to `image/jpeg`, `image/png`, `image/gif`,
or `image/webp`. Omit `URL`.

Invalid blocks return an error before a transport write. `Query` reports
validation errors on its error channel before connecting to the CLI.
Validation checks the source structure and base64 encoding, not image contents
or provider size limits.

Use `InputBlock` for prompts, not the response-only `ContentBlock` types.
Raw `[]map[string]any` prompts are not accepted. `Client.SendMessage` remains
available for raw protocol messages without prompt validation.

### Using Tools

By default, Claude has access to the full [Claude Code toolset](https://code.claude.com/docs/en/settings#tools-available-to-claude) (Read, Write, Edit, Bash, and others). `AllowedTools` is a permission allowlist: listed tools are auto-approved, and unlisted tools fall through to `PermissionMode` and `CanUseTool` for a decision. It does not remove tools from Claude's toolset. To block specific tools, use `DisallowedTools`. See the [permissions guide](https://platform.claude.com/docs/en/agent-sdk/permissions) for the full evaluation order.

```go
options := claude.Options{
    AllowedTools:   []string{"Read", "Write", "Bash"}, // auto-approve these tools
    PermissionMode: claude.PermissionAcceptEdits,      // auto-accept file edits
}
out, errs := claude.Query(ctx, "Create a hello.go file", options)
```

### Working Directory

```go
options := claude.Options{
    CWD: "/path/to/project",
}
```

## Client

`Client` (the `ClaudeSDKClient` equivalent) supports bidirectional,
interactive conversations with Claude Code. See [client.go](client.go).

Unlike `Query`, `Client` additionally enables **custom tools** and **hooks**,
both defined as Go functions.

Use the same streaming `session_id` for each turn in one logical
conversation; omitting it uses Python's default of `"default"`. The
`SessionID` returned on `ResultMessage` is the persisted Claude session ID;
use it with `Options.Resume` when starting a new client/process, not as the
normal follow-up key inside an already-connected client.

```go
c := claude.NewClient(options)
if err := c.Connect(ctx); err != nil {
    log.Fatal(err)
}
defer c.Close()

if err := c.Send(ctx, "Hello Claude"); err != nil {
    log.Fatal(err)
}
for {
    msg, err := c.Receive(ctx)
    if err != nil || msg == nil {
        log.Fatal(err)
    }
    fmt.Println(msg)
    if _, ok := msg.(claude.ResultMessage); ok {
        break
    }
}
```

`Client` also exposes the typed control protocol: `Interrupt`,
`SetPermissionMode`, `SetModel`, `RewindFiles`, `ReconnectMCPServer`,
`ToggleMCPServer`, `StopTask`, `MCPStatus`, `ContextUsage`, `MCPMessage`.

### Custom Tools (as In-Process SDK MCP Servers)

A **custom tool** is a Go function that you can offer to Claude, for Claude
to invoke as needed.

Custom tools are implemented as in-process MCP servers that run directly
within your application, eliminating the need for separate processes that
regular MCP servers require. The JSON schema is derived from the handler's
generic argument type.

For an end-to-end example, see [MCP Calculator](examples/mcp_calculator/main.go).

#### Creating a Simple Tool

```go
type greetArgs struct {
    Name string `json:"name"`
}

func greetUser(ctx context.Context, args greetArgs) (claude.ToolResult, error) {
    return claude.TextResult(fmt.Sprintf("Hello, %s!", args.Name)), nil
}

// Create an SDK MCP server and register the tool
server := claude.NewServer("my-tools", "1.0.0")
tool, err := claude.NewTool("greet", "Greet a user", greetUser)
if err != nil {
    log.Fatal(err)
}
server.MustRegister(tool)

// Use it with Claude. AllowedTools pre-approves the tool so it runs
// without a permission prompt; it does not control tool availability.
options := claude.Options{
    MCPServers: map[string]any{
        "tools": claude.CreateSdkMCPServer("tools", server),
    },
    AllowedTools: []string{"mcp__tools__greet"},
}
```

#### Benefits Over External MCP Servers

- **No subprocess management** - Runs in the same process as your application
- **Better performance** - No IPC overhead for tool calls
- **Simpler deployment** - Single Go binary instead of multiple
- **Easier debugging** - All code runs in the same process
- **Type safety** - Handler argument structs derive the tool's JSON schema and decode

#### Migration from External Servers

```go
// BEFORE: External MCP server (separate process)
options := claude.Options{
    MCPServers: map[string]any{
        "calculator": claude.McpStdioServerConfig{
            Command: "python",
            Args:    []string{"-m", "calculator_server"},
        },
    },
}

// AFTER: SDK MCP server (in-process)
server := claude.NewServer("calculator", "1.0.0")
server.MustRegister(addTool)
server.MustRegister(subtractTool)

options := claude.Options{
    MCPServers: map[string]any{
        "calculator": claude.CreateSdkMCPServer("calculator", server),
    },
}
```

#### Mixed Server Support

You can use both SDK and external MCP servers together:

```go
options := claude.Options{
    MCPServers: map[string]any{
        "internal": claude.CreateSdkMCPServer("internal", sdkServer), // In-process SDK server
        "external": claude.McpStdioServerConfig{                      // External subprocess server
            Type:    "stdio",
            Command: "external-server",
        },
    },
}
```

### Hooks

A **hook** is a Go function that the Claude Code _application_ (_not_ Claude)
invokes at specific points of the Claude agent loop. Hooks can provide
deterministic processing and automated feedback for Claude. Read more in
[Intercept and control agent behavior with hooks](https://platform.claude.com/docs/en/agent-sdk/hooks).

For more examples, see [examples/hooks/main.go](examples/hooks/main.go).

#### Example

```go
func checkBashCommand(ctx context.Context, in claude.PreToolUseHookInput) (claude.PreToolUseHookOutput, error) {
    if in.ToolName != "Bash" {
        return claude.PreToolUseHookOutput{}, nil
    }
    cmd, _ := in.ToolInput["command"].(string)
    for _, pattern := range []string{"foo.sh"} {
        if strings.Contains(cmd, pattern) {
            return claude.PreToolUseHookOutput{
                PermissionDecision:       "deny",
                PermissionDecisionReason: fmt.Sprintf("Command contains invalid pattern: %s", pattern),
            }, nil
        }
    }
    return claude.PreToolUseHookOutput{}, nil
}

options := claude.Options{
    AllowedTools: []string{"Bash"},
    Hooks: map[string][]claude.HookMatcher{
        "PreToolUse": {
            {Matcher: "Bash", Hooks: []claude.HookCallback{claude.PreToolUseHook(checkBashCommand)}},
        },
    },
}
```

## Types

See [types.go](types.go) for complete type definitions:

- `Options` - Configuration options (the `ClaudeAgentOptions` equivalent)
- `AssistantMessage`, `UserMessage`, `SystemMessage`, `ResultMessage` - Message types
- `TextBlock`, `ToolUseBlock`, `ToolResultBlock` - Content blocks

## Error Handling

```go
import (
    "errors"

    claude "github.com/losinggeneration/claude-agent-sdk-go"
)

out, errs := claude.Query(ctx, "Hello", claude.Options{})
for msg := range out {
    _ = msg
}
err := <-errs

switch {
case errors.Is(err, claude.ErrCLINotFound):
    fmt.Println("Please install Claude Code")
default:
    var re *claude.ResultError
    if errors.As(err, &re) {
        // The CLI reported a terminal error result (also yielded as the
        // final ResultMessage) and exited. Structured fields are on the error.
        fmt.Println(re.Subtype, re.TerminalReason, re.APIErrorStatus)
    }
    var pe *claude.ProcessError
    if errors.As(err, &pe) {
        fmt.Printf("Process failed with exit code: %d\n", pe.ExitCode)
    }
    var de *claude.JSONDecodeError
    if errors.As(err, &de) {
        fmt.Printf("Failed to parse response: %v\n", de)
    }
}
```

See [errors.go](errors.go) for all error types.

## Available Tools

See the [Claude Code documentation](https://code.claude.com/docs/en/settings#tools-available-to-claude) for a complete list of available tools.

## Examples

See [examples/quick_start/main.go](examples/quick_start/main.go) for a complete working example.

See [examples/streaming_mode/main.go](examples/streaming_mode/main.go) for comprehensive examples involving `Client`, including in-process multi-turn conversation and interrupt.

See [examples/resume_session/main.go](examples/resume_session/main.go) for persisted multi-turn continuation across processes using `ResultMessage.SessionID` + `Options.Resume`.

```sh
go run ./examples/quick_start
go run ./examples/streaming_mode
go run ./examples/mcp_calculator
go run ./examples/hooks
go run ./examples/session_stores
go run ./examples/resume_session
```

## Sessions

Beyond the Python surface above, the port also covers session management:

- **Session stores**: `SessionStore`, `SessionLister`, `SessionSubkeyLister`,
  `SessionDeleter`, `SessionSummarizer`; in-memory reference implementation
  in [memory_store.go](memory_store.go).
- **Session listing**: `ListSessions`, `GetSessionInfo`, `GetSessionMessages`,
  `ListSubagents`, `GetSubagentMessages` ([sessions.go](sessions.go)).
- **Session mutation**: `RenameSession`, `TagSession`, `DeleteSession`,
  `ForkSession` plus store-backed variants ([session_mutations.go](session_mutations.go)).
- **Import / mirror**: `ImportSessionToStore`, `TranscriptMirrorBatcher`.
- **Resume materialization**: copies Claude config into a temp `~/.claude`
  tree with refresh-token redaction.

## Status

This SDK targets feature parity with the Python SDK on the
non-Python-specific surface (control protocol, in-process MCP, hooks, session
stores, session listing / mutation, mirror, resume materialization).
Python-specific conveniences (decorators, `anyio` shims, wheel bundling, MCP
class wrappers around `jsonschema` / `pydantic` models) are intentionally out
of scope.

## Development

```sh
gofmt -l .
go vet ./...
go test ./...
go test ./... -race
```

## License and terms

Use of this SDK is governed by Anthropic's [Commercial Terms of Service](https://www.anthropic.com/legal/commercial-terms), except to the extent a specific component or dependency is covered by a different license as indicated in that component's LICENSE file.
