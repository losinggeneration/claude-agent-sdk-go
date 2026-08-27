package claudeagentsdk

import (
	"errors"
	"fmt"
)

var ErrCLINotFound = errors.New("claude code not found")

type CLIConnectionError struct{ Msg string }

func (e *CLIConnectionError) Error() string { return e.Msg }

type ProcessError struct {
	Msg      string
	ExitCode int
	Stderr   string
}

func (e *ProcessError) Error() string {
	msg := e.Msg
	if e.ExitCode != 0 {
		msg = fmt.Sprintf("%s (exit code: %d)", msg, e.ExitCode)
	}
	if e.Stderr != "" {
		msg += "\nError output: " + e.Stderr
	}
	return msg
}

type ResultError struct {
	ProcessError
	Data           map[string]any
	Subtype        string
	Errors         []string
	Result         string
	APIErrorStatus *int
	TerminalReason string
	SessionID      string
}

type JSONDecodeError struct {
	Line string
	Err  error
}

func (e *JSONDecodeError) Error() string {
	line := e.Line
	if len(line) > 100 {
		line = line[:100]
	}
	return "failed to decode JSON: " + line + ": " + e.Err.Error()
}
func (e *JSONDecodeError) Unwrap() error { return e.Err }

type MessageParseError struct {
	Msg  string
	Data map[string]any
}

func (e *MessageParseError) Error() string { return e.Msg }

func resultErrorText(m map[string]any) string {
	if errs := normalizeResultErrors(m["errors"]); len(errs) > 0 {
		return joinStrings(errs, "; ")
	}
	if s, ok := m["result"].(string); ok && s != "" {
		return s
	}
	if s, ok := m["subtype"].(string); ok && s != "" && s != "success" {
		return s
	}
	if v, ok := m["api_error_status"]; ok && v != nil {
		return fmt.Sprintf("API error (HTTP %v)", v)
	}
	return "unknown error"
}

func normalizeResultErrors(v any) []string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	case []any:
		out := make([]string, 0, len(x))
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return x
	default:
		return nil
	}
}

func joinStrings(s []string, sep string) string {
	if len(s) == 0 {
		return ""
	}
	out := s[0]
	for _, v := range s[1:] {
		out += sep + v
	}
	return out
}
