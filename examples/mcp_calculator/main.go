// Calculator MCP server example.
//
// Run with: go run ./examples/mcp_calculator
//
// Demonstrates an in-process SDK MCP server: the calculator tools run in
// this binary (no subprocess), and the bridge inside the SDK forwards
// each tools/call to them through the control protocol.
package main

import (
	"context"
	"fmt"
	"log"
	"math"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

type twoArgs struct {
	A float64 `json:"a"`
	B float64 `json:"b"`
}

type oneArg struct {
	N float64 `json:"n"`
}

func add(ctx context.Context, a twoArgs) (claude.ToolResult, error) {
	return claude.TextResult(fmt.Sprintf("%g + %g = %g", a.A, a.B, a.A+a.B)), nil
}

func subtract(ctx context.Context, a twoArgs) (claude.ToolResult, error) {
	return claude.TextResult(fmt.Sprintf("%g - %g = %g", a.A, a.B, a.A-a.B)), nil
}

func multiply(ctx context.Context, a twoArgs) (claude.ToolResult, error) {
	return claude.TextResult(fmt.Sprintf("%g * %g = %g", a.A, a.B, a.A*a.B)), nil
}

func divide(ctx context.Context, a twoArgs) (claude.ToolResult, error) {
	if a.B == 0 {
		return claude.ErrorResult("Error: Division by zero is not allowed"), nil
	}
	return claude.TextResult(fmt.Sprintf("%g / %g = %g", a.A, a.B, a.A/a.B)), nil
}

func sqrt(ctx context.Context, a oneArg) (claude.ToolResult, error) {
	if a.N < 0 {
		return claude.ErrorResult(fmt.Sprintf("Error: Cannot calculate square root of negative number %g", a.N)), nil
	}
	return claude.TextResult(fmt.Sprintf("sqrt(%g) = %g", a.N, math.Sqrt(a.N))), nil
}

func main() {
	srv := claude.NewServer("calculator", "1.0.0")
	addTool, err := claude.NewTool("add", "Add two numbers", add)
	if err != nil {
		log.Fatal(err)
	}
	subTool, err := claude.NewTool("subtract", "Subtract one number from another", subtract)
	if err != nil {
		log.Fatal(err)
	}
	mulTool, err := claude.NewTool("multiply", "Multiply two numbers", multiply)
	if err != nil {
		log.Fatal(err)
	}
	divTool, err := claude.NewTool("divide", "Divide one number by another", divide)
	if err != nil {
		log.Fatal(err)
	}
	sqrtTool, err := claude.NewTool("sqrt", "Calculate square root", sqrt)
	if err != nil {
		log.Fatal(err)
	}
	srv.MustRegister(addTool)
	srv.MustRegister(subTool)
	srv.MustRegister(mulTool)
	srv.MustRegister(divTool)
	srv.MustRegister(sqrtTool)

	opts := claude.Options{
		MCPServers: map[string]any{
			"calc": claude.CreateSdkMCPServer("calc", srv),
		},
		AllowedTools: []string{"add", "subtract", "multiply", "divide", "sqrt"},
	}

	prompt := "Add 5 and 3, then take the square root of the result."
	out, errs := claude.Query(context.Background(), prompt, opts)
	for {
		select {
		case msg, ok := <-out:
			if !ok {
				return
			}
			if m, ok := msg.(claude.AssistantMessage); ok {
				for _, b := range m.Content {
					if tb, ok := b.(claude.TextBlock); ok {
						fmt.Printf("Claude: %s\n", tb.Text)
					}
				}
			}
		case err := <-errs:
			if err != nil {
				log.Fatal(err)
			}
			return
		}
	}
}
