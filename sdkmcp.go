package claudeagentsdk

// In-process MCP server: tools registered with NewTool / NewToolFromSchema,
// dispatched over an in-memory pipe by mcp_bridge.go. The wire format
// matches MCP JSON-RPC: every line on the read side is one
// {"jsonrpc":"2.0","id":N,"method":"...","params":{...}} frame; responses
// are {"jsonrpc":"2.0","id":N,"result":{...}} or {"jsonrpc":"2.0","id":N,"error":{...}}.

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"sync"
)

// ToolHandler runs one tool call. Args is the JSON-decoded params object
// (always a map[string]any at this layer; typed wrappers do their own
// decoding in Validate). Implementations must be safe to call from many
// goroutines — a bridge session may invoke one handler while another is
// still running.
type ToolHandler func(ctx context.Context, args map[string]any) (ToolResult, error)

// Tool is one server-side tool. Build with NewTool (derive schema from a
// struct via reflection) or NewToolFromSchema (caller supplies a raw JSON
// Schema dict). Schema is sent on the wire as MCP requires.
type Tool struct {
	Name        string
	Description string
	InputSchema map[string]any
	Annotations *ToolAnnotations

	handler ToolHandler
	// validate, when set, is called before handler with the JSON-decoded
	// args. It returns either (args, nil) on success or
	// (nil, errMessage) on validation failure. NewTool wires this from
	// reflect-derived per-field rules; NewToolFromSchema leaves it nil and
	// relies on the caller for validation.
	validate func(args map[string]any) (map[string]any, string)
}

// ToolAnnotations mirrors MCP ToolAnnotations plus the Claude Code
// `maxResultSizeChars` extension, which travels in tool._meta under the
// namespaced key "anthropic/maxResultSizeChars".
type ToolAnnotations struct {
	ReadOnly           bool   `json:"readOnlyHint,omitempty"`
	Destructive        bool   `json:"destructiveHint,omitempty"`
	Idempotent         bool   `json:"idempotentHint,omitempty"`
	OpenWorld          bool   `json:"openWorldHint,omitempty"`
	MaxResultSizeChars *int   `json:"-"`
	Title              string `json:"title,omitempty"`
}

// ToolResult is the structured result a handler returns. Content blocks
// follow MCP's shape: text/image/resource/etc. Callers should construct
// with TextResult / ImageResult / ErrorResult rather than raw maps.
type ToolResult struct {
	Content []map[string]any `json:"content"`
	IsError bool             `json:"isError,omitempty"`
}

// TextResult is the common success shape: a single text block.
func TextResult(text string) ToolResult {
	return ToolResult{Content: []map[string]any{{"type": "text", "text": text}}}
}

// ErrorResult wraps a message in a single text block with isError=true so
// the model sees the failure as data rather than a transport error.
func ErrorResult(msg string) ToolResult {
	return ToolResult{Content: []map[string]any{{"type": "text", "text": msg}}, IsError: true}
}

// ImageResult is a base64-encoded image content block.
func ImageResult(data, mimeType string) ToolResult {
	return ToolResult{Content: []map[string]any{{"type": "image", "data": data, "mimeType": mimeType}}}
}

// NewTool registers a handler with a name, description, and input schema
// derived from the reflect.Type of sample. sample is a zero value (or
// pointer to zero) of the argument struct; its exported fields with `json`
// tags become required properties. Unsupported field types fall back to
// {"type": "string"}.
func NewTool[T any](name, description string, handler func(ctx context.Context, args T) (ToolResult, error)) (Tool, error) {
	if name == "" {
		return Tool{}, fmt.Errorf("tool name is required")
	}
	var zero T
	t := reflect.TypeOf(zero)
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return Tool{}, fmt.Errorf("tool %q: schema sample must be a struct, got %s", name, t.Kind())
	}
	schema, validate, err := schemaFromStruct(t)
	if err != nil {
		return Tool{}, fmt.Errorf("tool %q: derive schema: %w", name, err)
	}
	wrapped := func(ctx context.Context, args map[string]any) (ToolResult, error) {
		cleaned, errMsg := validate(args)
		if errMsg != "" {
			return ErrorResult("Input validation error: " + errMsg), nil
		}
		decoded, err := decodeArgs[T](cleaned)
		if err != nil {
			return ErrorResult("Input validation error: " + err.Error()), nil
		}
		return handler(ctx, decoded)
	}
	return Tool{Name: name, Description: description, InputSchema: schema, handler: wrapped, validate: validate}, nil
}

// NewToolFromSchema registers a handler with a caller-supplied JSON Schema
// dict. Use when the input is not a struct (a free-form map or a fully
// dynamic schema).
func NewToolFromSchema(name, description string, schema map[string]any, handler ToolHandler) (Tool, error) {
	if name == "" {
		return Tool{}, fmt.Errorf("tool name is required")
	}
	if schema == nil {
		schema = map[string]any{"type": "object", "properties": map[string]any{}}
	}
	return Tool{Name: name, Description: description, InputSchema: schema, handler: handler}, nil
}

// WithAnnotations returns a copy of t with the given annotations.
func (t Tool) WithAnnotations(a *ToolAnnotations) Tool {
	t.Annotations = a
	return t
}

// Server is a JSON-RPC MCP server. It owns its tools and runs one
// `Server.Run` over a pair of io.ReadWriteClosers — for in-process use
// those are the two ends of an io.Pipe. The bridge writes client requests
// into one end and reads server responses from the other.
type Server struct {
	Name    string
	Version string
	tools   map[string]Tool
	mu      sync.RWMutex
}

// NewServer returns an empty server. Version defaults to "0.0.0" when
// empty; the CLI uses it for status reporting.
func NewServer(name, version string) *Server {
	if version == "" {
		version = "0.0.0"
	}
	return &Server{Name: name, Version: version, tools: map[string]Tool{}}
}

// Register adds t to the server. A duplicate name is rejected so the wire
// shape and the server's advertised tool list never disagree.
func (s *Server) Register(t Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.tools[t.Name]; exists {
		return fmt.Errorf("tool %q already registered", t.Name)
	}
	if t.handler == nil {
		return fmt.Errorf("tool %q has no handler", t.Name)
	}
	s.tools[t.Name] = t
	return nil
}

// MustRegister is Register that panics on error.
func (s *Server) MustRegister(t Tool) { _ = s.Register(t) }

// Tools returns a snapshot of the registered tools in insertion order
// (Go map iteration is randomized; we sort by name to make the advertised
// list deterministic).
func (s *Server) Tools() []Tool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Tool, 0, len(s.tools))
	for _, t := range s.tools {
		out = append(out, t)
	}
	sortTools(out)
	return out
}

// ListToolsWire returns the tools/list response payload.
func (s *Server) ListToolsWire() map[string]any {
	tools := s.Tools()
	items := make([]map[string]any, 0, len(tools))
	for _, t := range tools {
		entry := map[string]any{
			"name":        t.Name,
			"description": t.Description,
			"inputSchema": t.InputSchema,
		}
		if t.Annotations != nil {
			if meta := annotationsMeta(t.Annotations); meta != nil {
				entry["_meta"] = meta
			}
			if !annotationsZeroExceptSize(t.Annotations) {
				entry["annotations"] = t.Annotations
			} else if t.Annotations.MaxResultSizeChars != nil {
				// Even when every hint is zero, MaxResultSizeChars on its
				// own needs to ride along via _meta; advertise a null
				// annotations object so the schema is unambiguous.
				entry["annotations"] = map[string]any{}
			}
		}
		items = append(items, entry)
	}
	return map[string]any{"tools": items}
}

// CallTool runs one tools/call. The bridge invokes this on a goroutine
// per request; the handler is responsible for its own concurrency.
func (s *Server) CallTool(ctx context.Context, name string, args map[string]any) (ToolResult, error) {
	s.mu.RLock()
	t, ok := s.tools[name]
	s.mu.RUnlock()
	if !ok {
		return ErrorResult(fmt.Sprintf("Tool '%s' not found", name)), nil
	}
	if args == nil {
		args = map[string]any{}
	}
	if t.validate != nil {
		if cleaned, errMsg := t.validate(args); errMsg != "" {
			return ErrorResult("Input validation error: " + errMsg), nil
		} else {
			args = cleaned
		}
	}
	res, err := t.handler(ctx, args)
	if err != nil {
		return ErrorResult(err.Error()), nil
	}
	return convertToolContent(res), nil
}

func annotationsMeta(a *ToolAnnotations) map[string]any {
	if a == nil || a.MaxResultSizeChars == nil {
		return nil
	}
	return map[string]any{"anthropic/maxResultSizeChars": *a.MaxResultSizeChars}
}

func annotationsZeroExceptSize(a *ToolAnnotations) bool {
	return !a.ReadOnly && !a.Destructive && !a.Idempotent && !a.OpenWorld && a.Title == ""
}

// convertToolContent mirrors the Python _convert_tool_content: text and
// image blocks pass through; resource_link and text-resource blocks
// flatten to text; binary resources are skipped with a warning; unknown
// block types are skipped. Returns a new ToolResult whose Content has
// only the surviving blocks.
func convertToolContent(r ToolResult) ToolResult {
	out := make([]map[string]any, 0, len(r.Content))
	for _, item := range r.Content {
		switch item["type"] {
		case "text":
			out = append(out, map[string]any{"type": "text", "text": item["text"]})
		case "image":
			out = append(out, map[string]any{"type": "image", "data": item["data"], "mimeType": item["mimeType"]})
		case "resource_link":
			parts := make([]string, 0, 3)
			for _, k := range []string{"name", "uri", "description"} {
				if v, ok := item[k].(string); ok && v != "" {
					parts = append(parts, v)
				}
			}
			if len(parts) == 0 {
				out = append(out, map[string]any{"type": "text", "text": "Resource link"})
			} else {
				out = append(out, map[string]any{"type": "text", "text": strings.Join(parts, "\n")})
			}
		case "resource":
			res, _ := item["resource"].(map[string]any)
			if res != nil {
				if text, ok := res["text"].(string); ok {
					out = append(out, map[string]any{"type": "text", "text": text})
				}
			}
		}
	}
	return ToolResult{Content: out, IsError: r.IsError}
}

// schemaFromStruct derives a JSON Schema object from a struct type's
// exported fields. It honours `json:"name"` tags (with omitempty marking
// the field optional), `jsonschema:"required"` to force-required a field
// the omitempty tag would have marked optional, and `desc:"..."` for a
// per-field description. Unsupported types map to {"type":"string"}.
//
// Returns the schema plus a validator closure. The validator only checks
// that required keys are present and have the right JSON type — enough to
// give a useful error before the typed wrapper decodes.
func schemaFromStruct(t reflect.Type) (map[string]any, func(map[string]any) (map[string]any, string), error) {
	if t.Kind() != reflect.Struct {
		return nil, nil, fmt.Errorf("schema sample must be a struct, got %s", t.Kind())
	}
	props := map[string]any{}
	required := []string{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name, opts := splitJSONTag(f.Tag.Get("json"))
		if name == "-" {
			continue
		}
		if name == "" {
			name = lowerFirst(f.Name)
		}
		schema := schemaForType(f.Type)
		if d := f.Tag.Get("desc"); d != "" {
			schema["description"] = d
		}
		props[name] = schema
		_, omitempty := opts["omitempty"]
		_, forceRequired := opts["required"]
		if !omitempty || forceRequired {
			required = append(required, name)
		}
	}
	schema := map[string]any{"type": "object", "properties": props}
	if len(required) > 0 {
		schema["required"] = required
	}
	validate := func(in map[string]any) (map[string]any, string) {
		if in == nil {
			in = map[string]any{}
		}
		for _, k := range required {
			if _, ok := in[k]; !ok {
				return nil, "missing required field " + k
			}
		}
		for k, v := range in {
			p, ok := props[k].(map[string]any)
			if !ok {
				// Unknown property: pass through; the typed decode will
				// reject with a clearer error.
				continue
			}
			if msg := checkType(k, v, p); msg != "" {
				return nil, msg
			}
		}
		return in, ""
	}
	return schema, validate, nil
}

func splitJSONTag(tag string) (string, map[string]bool) {
	if tag == "" {
		return "", nil
	}
	parts := strings.Split(tag, ",")
	name := parts[0]
	opts := map[string]bool{}
	for _, p := range parts[1:] {
		opts[p] = true
	}
	return name, opts
}

func lowerFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToLower(s[:1]) + s[1:]
}

func schemaForType(t reflect.Type) map[string]any {
	for t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	switch t.Kind() {
	case reflect.String:
		return map[string]any{"type": "string"}
	case reflect.Bool:
		return map[string]any{"type": "boolean"}
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
		reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return map[string]any{"type": "integer"}
	case reflect.Float32, reflect.Float64:
		return map[string]any{"type": "number"}
	case reflect.Slice, reflect.Array:
		items := schemaForType(t.Elem())
		return map[string]any{"type": "array", "items": items}
	case reflect.Map:
		return map[string]any{"type": "object", "additionalProperties": true}
	case reflect.Struct:
		// Nested struct: recurse so the schema reflects the actual shape.
		// Best-effort: errors here are not fatal; fall back to "object".
		s, _, err := schemaFromStruct(t)
		if err != nil {
			return map[string]any{"type": "object"}
		}
		return s
	default:
		return map[string]any{"type": "string"}
	}
}

func checkType(name string, v any, prop map[string]any) string {
	if v == nil {
		return ""
	}
	switch prop["type"] {
	case "string":
		if _, ok := v.(string); !ok {
			return fmt.Sprintf("field %q: expected string", name)
		}
	case "integer":
		switch v.(type) {
		case float64, int, int64, json.Number:
			// JSON numbers decode to float64 by default; accept those.
		default:
			return fmt.Sprintf("field %q: expected integer", name)
		}
	case "number":
		switch v.(type) {
		case float64, int, json.Number:
		default:
			return fmt.Sprintf("field %q: expected number", name)
		}
	case "boolean":
		if _, ok := v.(bool); !ok {
			return fmt.Sprintf("field %q: expected boolean", name)
		}
	case "array":
		if _, ok := v.([]any); !ok {
			return fmt.Sprintf("field %q: expected array", name)
		}
	case "object":
		if _, ok := v.(map[string]any); !ok {
			return fmt.Sprintf("field %q: expected object", name)
		}
	}
	return ""
}

// decodeArgs converts a map[string]any (as produced by json.Unmarshal into
// interface{}) into T via a round-trip through JSON. This is cheaper than
// reflecting each field and gives us precise error messages on type
// mismatches.
func decodeArgs[T any](in map[string]any) (T, error) {
	var zero T
	b, err := json.Marshal(in)
	if err != nil {
		return zero, fmt.Errorf("encode args: %w", err)
	}
	var out T
	if err := json.Unmarshal(b, &out); err != nil {
		return zero, err
	}
	return out, nil
}

func sortTools(tools []Tool) {
	// Tiny insertion sort; tool lists are short and this runs once per
	// tools/list call.
	for i := 1; i < len(tools); i++ {
		for j := i; j > 0 && tools[j-1].Name > tools[j].Name; j-- {
			tools[j-1], tools[j] = tools[j], tools[j-1]
		}
	}
}
