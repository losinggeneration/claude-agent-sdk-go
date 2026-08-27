package claudeagentsdk

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode"
)

const defaultMaxBufferSize = 1024 * 1024

// Version tracks the Python SDK release this port targets; sent to the CLI
// as CLAUDE_AGENT_SDK_VERSION for telemetry.
const Version = "0.2.143"

// minimumCLICodeVersion is the lowest Claude Code version the SDK supports.
// Below it the SDK warns (not errors) — see checkCLIVersion.
const minimumCLICodeVersion = "2.0.0"

// Transport is the low-level NDJSON transport used by Client.
type Transport interface {
	Connect(context.Context) error
	Write(context.Context, string) error
	EndInput() error
	ReadMessages(context.Context) (<-chan map[string]any, <-chan error)
	Close() error
}

type SubprocessTransport struct {
	Prompt  any
	Options Options

	cmd        *exec.Cmd
	cliPath    string
	stdin      io.WriteCloser
	stdout     io.ReadCloser
	exited     chan struct{} // closed by the read loop after cmd.Wait reaps the child
	reading    bool
	closed     bool
	closeGrace time.Duration // per-stage Close timeout; 0 → 5s (Python parity)
	mu         sync.Mutex
	ready      bool
}

func NewSubprocessTransport(prompt any, opts Options) *SubprocessTransport {
	return &SubprocessTransport{Prompt: prompt, Options: opts}
}

func (t *SubprocessTransport) Connect(ctx context.Context) error {
	if t.cmd != nil {
		return nil
	}
	cli := t.Options.CLIPath
	if cli == "" {
		var err error
		cli, err = findCLI()
		if err != nil {
			return err
		}
	}
	// Validate the resolved CLI before anything is spawned with it —
	// this guards the version probe below as well as the main spawn.
	if err := rejectWindowsBatchCLI(cli); err != nil {
		return err
	}
	if os.Getenv("CLAUDE_AGENT_SDK_SKIP_VERSION_CHECK") == "" {
		t.checkCLIVersion(cli)
	}
	t.cliPath = cli
	args, err := t.buildArgs()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, cli, args...)
	if t.Options.CWD != "" {
		cmd.Dir = t.Options.CWD
	}
	cmd.Env = childEnv(t.Options.Env, t.Options.CWD)
	if t.Options.EnableFileCheckpointing {
		cmd.Env = append(cmd.Env, "CLAUDE_CODE_ENABLE_SDK_FILE_CHECKPOINTING=true")
	}
	if InjectTraceContext != nil {
		if carrier := InjectTraceContext(); carrier != nil {
			cmd.Env = mergeTraceContext(cmd.Env, carrier, t.Options.Env)
		}
	}
	if t.Options.User != "" {
		if err := applyUser(cmd, t.Options.User); err != nil {
			return &CLIConnectionError{Msg: "failed to set Options.User: " + err.Error()}
		}
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("open stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("open stdout: %w", err)
	}
	if t.Options.Stderr != nil {
		stderr, err := cmd.StderrPipe()
		if err != nil {
			return fmt.Errorf("open stderr: %w", err)
		}
		go scanLines(stderr, t.maxBufferSize(), t.Options.Stderr)
	}
	if err := cmd.Start(); err != nil {
		return &CLIConnectionError{Msg: "failed to start Claude Code: " + err.Error()}
	}
	t.cmd, t.stdin, t.stdout, t.exited, t.ready = cmd, stdin, stdout, make(chan struct{}), true
	return nil
}

func (t *SubprocessTransport) buildArgs() ([]string, error) {
	o := t.Options
	args := []string{"--output-format", "stream-json", "--verbose"}
	if o.SystemPromptFile != "" {
		args = append(args, "--system-prompt-file", o.SystemPromptFile)
	} else if o.AppendSystemPrompt != "" {
		args = append(args, "--append-system-prompt", o.AppendSystemPrompt)
	} else {
		args = append(args, "--system-prompt", o.SystemPrompt)
	}
	if o.Tools != nil {
		if len(o.Tools) == 0 {
			args = append(args, "--tools", "")
		} else {
			args = append(args, "--tools", strings.Join(o.Tools, ","))
		}
	}
	allowedTools, settingSources, err := o.skillsDefaults()
	if err != nil {
		return nil, err
	}
	if len(allowedTools) > 0 {
		args = append(args, "--allowedTools", strings.Join(allowedTools, ","))
	}
	if o.MaxTurns > 0 {
		args = append(args, "--max-turns", fmt.Sprint(o.MaxTurns))
	}
	if o.MaxBudgetUSD != 0 {
		args = append(args, "--max-budget-usd", fmt.Sprint(o.MaxBudgetUSD))
	}
	if len(o.DisallowedTools) > 0 {
		args = append(args, "--disallowedTools", strings.Join(o.DisallowedTools, ","))
	}
	if o.TaskBudgetTotal > 0 {
		args = append(args, "--task-budget", fmt.Sprint(o.TaskBudgetTotal))
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.FallbackModel != "" {
		args = append(args, "--fallback-model", o.FallbackModel)
	}
	if len(o.Betas) > 0 {
		args = append(args, "--betas", strings.Join(o.Betas, ","))
	}
	if o.PermissionPromptTool != "" {
		args = append(args, "--permission-prompt-tool", o.PermissionPromptTool)
	}
	if o.PermissionMode != "" {
		args = append(args, "--permission-mode", string(o.PermissionMode))
	}
	if o.ContinueConversation {
		args = append(args, "--continue")
	}
	if o.Resume != "" {
		if err := rejectWindowsCmdMetacharacters("resume", o.Resume); err != nil {
			return nil, err
		}
		args = append(args, "--resume="+o.Resume)
	}
	if o.SessionID != "" {
		if err := rejectWindowsCmdMetacharacters("session_id", o.SessionID); err != nil {
			return nil, err
		}
		args = append(args, "--session-id="+o.SessionID)
	}
	settings, err := o.settingsValue()
	if err != nil {
		return nil, err
	}
	if settings != "" {
		args = append(args, "--settings", settings)
	}
	for _, d := range o.AddDirs {
		args = append(args, "--add-dir", d)
	}
	if len(o.MCPServers) > 0 {
		wire, _, err := mcpServersWire(o.MCPServers)
		if err != nil {
			return nil, err
		}
		b, _ := json.Marshal(map[string]any{"mcpServers": wire})
		args = append(args, "--mcp-config", string(b))
	}
	if o.IncludePartialMessages {
		args = append(args, "--include-partial-messages")
	}
	if o.IncludeHookEvents {
		args = append(args, "--include-hook-events")
	}
	if o.StrictMCPConfig {
		args = append(args, "--strict-mcp-config")
	}
	if o.ForkSession {
		args = append(args, "--fork-session")
	}
	if o.ResumeSessionAt != "" {
		if err := rejectWindowsCmdMetacharacters("resume_session_at", o.ResumeSessionAt); err != nil {
			return nil, err
		}
		args = append(args, "--resume-session-at="+o.ResumeSessionAt)
	}
	if o.ResumeDropsTurn != nil {
		if err := rejectWindowsCmdMetacharacters("resume_drops_turn", *o.ResumeDropsTurn); err != nil {
			return nil, err
		}
		args = append(args, "--resume-drops-turn="+*o.ResumeDropsTurn)
	}
	if settingSources != nil {
		args = append(args, "--setting-sources="+strings.Join(settingSources, ","))
	}
	for _, p := range o.Plugins {
		if p.Type == "local" {
			args = append(args, "--plugin-dir", p.Path)
		} else {
			return nil, fmt.Errorf("unsupported plugin type: %s", p.Type)
		}
	}
	for flag, value := range o.ExtraArgs {
		if value == nil {
			args = append(args, "--"+flag)
		} else if strings.HasPrefix(*value, "-") {
			args = append(args, "--"+flag+"="+*value)
		} else {
			args = append(args, "--"+flag, *value)
		}
	}
	if o.Thinking != nil {
		switch o.Thinking.Type {
		case "adaptive":
			args = append(args, "--thinking", "adaptive")
		case "enabled":
			args = append(args, "--max-thinking-tokens", fmt.Sprint(o.Thinking.BudgetTokens))
		case "disabled":
			args = append(args, "--thinking", "disabled")
		}
		if o.Thinking.Type != "disabled" && o.Thinking.Display != "" {
			args = append(args, "--thinking-display", o.Thinking.Display)
		}
	} else if o.MaxThinkingTokens != nil {
		args = append(args, "--max-thinking-tokens", fmt.Sprint(*o.MaxThinkingTokens))
	}
	if o.Effort != "" {
		args = append(args, "--effort", o.Effort)
	}
	if o.OutputFormat != nil && o.OutputFormat["type"] == "json_schema" {
		if schema := o.OutputFormat["schema"]; schema != nil {
			b, _ := json.Marshal(schema)
			args = append(args, "--json-schema", string(b))
		}
	}
	if o.SessionStore != nil {
		args = append(args, "--session-mirror")
	}
	args = append(args, "--input-format", "stream-json")
	return args, nil
}

func (t *SubprocessTransport) Write(ctx context.Context, data string) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.ready || t.stdin == nil {
		return &CLIConnectionError{Msg: "transport is not ready for writing"}
	}
	_, err := io.WriteString(t.stdin, data)
	if err != nil {
		t.ready = false
		return &CLIConnectionError{Msg: "failed to write to process stdin: " + err.Error()}
	}
	return nil
}

func (t *SubprocessTransport) EndInput() error {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.stdin != nil {
		err := t.stdin.Close()
		t.stdin = nil
		return err
	}
	return nil
}

func (t *SubprocessTransport) ReadMessages(ctx context.Context) (<-chan map[string]any, <-chan error) {
	out, errs := make(chan map[string]any), make(chan error, 1)
	t.mu.Lock()
	t.reading = true
	t.mu.Unlock()
	go func() {
		defer close(out)
		defer close(errs)
		// At most one error ever reaches errs (cap 1, typically nobody is
		// receiving yet): the first failure wins, whether it came from the
		// stream decode or from the process exit status.
		scanErr := t.scanMessages(ctx, out)
		// This goroutine is the sole reaper while reading: always Wait,
		// even on early exit, so the child never leaks as a zombie. Close's
		// escalation signals the child to make this Wait return.
		var waitErr error
		if t.cmd != nil {
			if err := t.cmd.Wait(); err != nil {
				if ee, ok := err.(*exec.ExitError); ok {
					waitErr = &ProcessError{Msg: "command failed", ExitCode: ee.ExitCode(), Stderr: string(ee.Stderr)}
				} else {
					waitErr = err
				}
			}
		}
		if scanErr == nil {
			scanErr = waitErr
		}
		if scanErr != nil {
			errs <- scanErr
		}
		if exited := t.exited; exited != nil {
			close(exited)
		}
	}()
	return out, errs
}

// scanMessages parses NDJSON lines from the CLI stdout into out. It
// returns the terminal stream error (decode failure, ctx cancel, or read
// error) without sending it — the caller decides what reaches errs. It
// never waits on the process; reaping is the caller's job.
func (t *SubprocessTransport) maxBufferSize() int {
	if t.Options.MaxBufferSize > 0 {
		return t.Options.MaxBufferSize
	}
	return defaultMaxBufferSize
}

// lineFramer reassembles newline-delimited lines from arbitrary read
// chunks. A chunk boundary can fall anywhere — including inside a JSON
// string value — so lines are only ever split on "\n".
type lineFramer struct {
	pending []byte
}

// push adds a chunk and returns the lines it completed.
func (f *lineFramer) push(chunk []byte) []string {
	f.pending = append(f.pending, chunk...)
	if !bytes.ContainsRune(chunk, '\n') {
		return nil
	}
	parts := strings.Split(string(f.pending), "\n")
	f.pending = []byte(parts[len(parts)-1])
	return parts[:len(parts)-1]
}

// flush takes the trailing partial line, if any.
func (f *lineFramer) flush() []byte {
	line := f.pending
	f.pending = nil
	return line
}

func (t *SubprocessTransport) scanMessages(ctx context.Context, out chan<- map[string]any) error {
	max := t.maxBufferSize()
	var framer lineFramer
	chunk := make([]byte, 32*1024)
	for {
		n, err := t.stdout.Read(chunk)
		if n > 0 {
			for _, raw := range framer.push(chunk[:n]) {
				if len(raw) > max {
					return &JSONDecodeError{Line: raw, Err: fmt.Errorf("JSON message exceeded maximum buffer size of %d bytes", max)}
				}
				line := strings.TrimSpace(raw)
				if line == "" || !strings.HasPrefix(line, "{") {
					continue
				}
				var m map[string]any
				if err := json.Unmarshal([]byte(line), &m); err != nil {
					return &JSONDecodeError{Line: line, Err: err}
				}
				select {
				case out <- m:
				case <-ctx.Done():
					return ctx.Err()
				}
			}
			// Bound a line still being received, so a producer that never
			// emits a newline can't grow memory without limit.
			if len(framer.pending) > max {
				return &JSONDecodeError{Line: string(framer.pending), Err: fmt.Errorf("JSON message exceeded maximum buffer size of %d bytes", max)}
			}
		}
		if err == io.EOF {
			// The CLI terminates every message with "\n", so a residual tail
			// means either a producer that omits the final newline (yield it)
			// or one cut off mid-write (unrecoverable — drop it).
			tail := strings.TrimSpace(string(framer.flush()))
			if strings.HasPrefix(tail, "{") {
				var m map[string]any
				if err := json.Unmarshal([]byte(tail), &m); err == nil {
					select {
					case out <- m:
					case <-ctx.Done():
						return ctx.Err()
					}
				}
			}
			return nil
		}
		if err != nil {
			return err
		}
	}
}

func (t *SubprocessTransport) Close() error {
	t.mu.Lock()
	t.ready = false
	if t.closed {
		t.mu.Unlock()
		return nil
	}
	t.closed = true
	cmd := t.cmd
	grace := t.closeGrace
	if grace <= 0 {
		grace = 5 * time.Second
	}
	reading := t.reading
	exited := t.exited
	t.mu.Unlock()
	// stdin EOF first: the CLI flushes its session file after EOF, and a
	// premature SIGTERM can interrupt that write and lose the last
	// assistant message (Python #625).
	_ = t.EndInput()
	if t.stdout != nil {
		_ = t.stdout.Close()
	}
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	waitExited := func(d time.Duration) bool {
		if reading {
			select {
			case <-exited:
				return true
			case <-time.After(d):
				return false
			}
		}
		// No reader goroutine was ever started, so Close reaps directly.
		done := make(chan struct{})
		go func() {
			_, _ = cmd.Process.Wait()
			close(done)
		}()
		select {
		case <-done:
			return true
		case <-time.After(d):
			return false
		}
	}
	if waitExited(grace) {
		return nil
	}
	// ponytail: Signal(SIGTERM) is a no-op error on Windows — there the
	// escalation degrades to Kill-only after the grace periods.
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if waitExited(grace) {
		return nil
	}
	_ = cmd.Process.Kill()
	waitExited(grace)
	return nil
}

func findCLI() (string, error) {
	if p, err := exec.LookPath("claude"); err == nil {
		return p, nil
	}
	name := "claude"
	if runtime.GOOS == "windows" {
		name = "claude.exe"
	}
	for _, p := range []string{filepath.Join(os.Getenv("HOME"), ".local/bin", name), filepath.Join(os.Getenv("HOME"), ".claude/local", name), "/usr/local/bin/claude"} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w. install Claude Code or set Options.CLIPath", ErrCLINotFound)
}

func childEnv(extra map[string]string, cwd string) []string {
	// CLAUDECODE is filtered so SDK-spawned subprocesses don't think
	// they're running inside a Claude Code parent (Python #573). The
	// inherited CLAUDE_CODE_ENTRYPOINT is dropped too so the sdk-go default
	// wins (POSIX getenv returns the first match, so a plain append could
	// leave the parent's value shadowing ours).
	env := make([]string, 0, len(os.Environ())+len(extra)+2)
	for _, kv := range os.Environ() {
		k, _, _ := strings.Cut(kv, "=")
		if k == "CLAUDECODE" || k == "CLAUDE_CODE_ENTRYPOINT" || k == "CLAUDE_AGENT_SDK_VERSION" || (k == "PWD" && cwd != "") {
			continue
		}
		if _, ok := extra[k]; ok {
			continue // Options.Env overrides the inherited value
		}
		env = append(env, kv)
	}
	// Options.Env may override the entrypoint; CLAUDE_AGENT_SDK_VERSION is
	// always set by the SDK and cannot be overridden (matches Python).
	env = append(env, "CLAUDE_CODE_ENTRYPOINT=sdk-go")
	for k, v := range extra {
		if k == "CLAUDE_AGENT_SDK_VERSION" {
			continue
		}
		env = append(env, k+"="+v)
	}
	env = append(env, "CLAUDE_AGENT_SDK_VERSION="+Version)
	if cwd != "" {
		// Last, like Python, so the inherited PWD never shadows the
		// directory the child was chdir'd to.
		env = append(env, "PWD="+cwd)
	}
	return env
}

// InjectTraceContext, when non-nil, is called once per Connect to
// propagate a distributed trace to the CLI: return the W3C carrier
// (containing "traceparent") to inject into the child environment, or nil /
// an empty carrier to skip. The Go port keeps OTEL an optional concern the
// same way Python treats the opentelemetry dependency — set this from
// otel's propagator:
//
//	InjectTraceContext = func() map[string]string {
//		c := map[string]string{}
//		otel.GetTextMapPropagator().Inject(ctx, propagation.MapCarrier(c))
//		return c
//	}
var InjectTraceContext func() map[string]string

// mergeTraceContext merges an OTEL carrier into env: inherited
// TRACEPARENT/TRACESTATE are scrubbed so a stale ambient trace is never
// paired with a fresh one, carrier keys are uppercased, and explicit
// Options.Env entries always win. No-op without a traceparent, so a
// baggage-only or non-W3C carrier leaves inherited values alone.
func mergeTraceContext(env []string, carrier map[string]string, optsEnv map[string]string) []string {
	if _, ok := carrier["traceparent"]; !ok {
		return env
	}
	out := make([]string, 0, len(env)+len(carrier))
	for _, kv := range env {
		k, _, _ := strings.Cut(kv, "=")
		if (k == "TRACEPARENT" || k == "TRACESTATE") && !containsKey(optsEnv, k) {
			continue
		}
		out = append(out, kv)
	}
	for _, k := range slices.Sorted(maps.Keys(carrier)) {
		v := carrier[k]
		k = strings.ToUpper(k)
		if !containsKey(optsEnv, k) {
			out = append(out, k+"="+v)
		}
	}
	return out
}

func containsKey(m map[string]string, k string) bool {
	_, ok := m[k]
	return ok
}

var cliVersionRe = regexp.MustCompile(`[0-9]+\.[0-9]+\.[0-9]+`)

// checkCLIVersion probes `<cli> -v` and warns on stderr when the version is
// below minimumCLICodeVersion. Warn-only, like Python: an old CLI is
// unsupported, not fatal, and every probe failure is swallowed — the probe
// must never block the spawn.
func (t *SubprocessTransport) checkCLIVersion(cli string) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, cli, "-v")
	// WaitDelay bounds Output() even when a killed child's grandchildren
	// inherit the pipe FDs and hold them open.
	cmd.WaitDelay = 500 * time.Millisecond
	out, err := cmd.Output()
	if err != nil {
		return
	}
	v := cliVersionRe.FindString(string(out))
	if v == "" || !versionLess(v, minimumCLICodeVersion) {
		return
	}
	fmt.Fprintf(os.Stderr, "Claude Code version %s at %s is unsupported in the Agent SDK. Minimum required version is %s. Some features may not work correctly.\n", v, cli, minimumCLICodeVersion)
}

// versionLess compares dotted numeric versions: "1.9.0" < "2.0.0".
// Non-numeric parts count as 0; a shorter list pads with zeros.
func versionLess(a, b string) bool {
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y int
		if i < len(as) {
			x, _ = strconv.Atoi(as[i])
		}
		if i < len(bs) {
			y, _ = strconv.Atoi(bs[i])
		}
		if x != y {
			return x < y
		}
	}
	return false
}

// windowsBatchCLIPath reports whether any path component names a .bat/.cmd
// batch script. Windows runs such scripts via cmd.exe, which re-parses the
// command line at execution time and can execute commands injected through
// argument values — with no reliable cmd.exe escaping (the "BatBadBut"
// class, CVE-2024-27980), so spawning them with runtime arguments cannot be
// made safe. Every component is classified, not just the last, because
// Win32 lexical normalization ("."/".." collapsing, trailing dot/space
// trimming) can resolve a path through any of them; colon-splitting covers
// drive prefixes and NTFS stream specs ("claude.cmd:stream" opens
// claude.cmd). Pure classification, so it is testable on POSIX.
func windowsBatchCLIPath(cliPath string) bool {
	for _, component := range strings.Split(strings.ReplaceAll(cliPath, "\\", "/"), "/") {
		for _, segment := range strings.Split(component, ":") {
			seg := strings.ToLower(strings.TrimRight(segment, ". "))
			if strings.HasSuffix(seg, ".bat") || strings.HasSuffix(seg, ".cmd") {
				return true
			}
		}
	}
	return false
}

// rejectWindowsBatchCLI refuses to execute a .bat/.cmd script as the CLI on
// Windows; a no-op everywhere else.
func rejectWindowsBatchCLI(cliPath string) error {
	if runtime.GOOS != "windows" || !windowsBatchCLIPath(cliPath) {
		return nil
	}
	return &CLIConnectionError{Msg: fmt.Sprintf("Refusing to execute batch script %q: Windows runs .bat/.cmd files via cmd.exe, which can execute commands injected through CLI arguments, and no reliable escaping for cmd.exe exists. Use a native claude executable instead: install Claude Code natively (irm https://claude.ai/install.ps1 | iex), point Options.CLIPath at a claude.exe, or install a build that bundles claude.exe.", cliPath)}
}

// cmdExeMetacharacters that cmd.exe reinterprets inside quoted arguments
// (%VAR% expands even inside double quotes).
const cmdExeMetacharacters = `&|<>^%!"` + "\r\n"

// rejectWindowsCmdMetacharacters rejects cmd.exe metacharacters in values
// handed to CLI flags on Windows. Defense in depth behind the batch-script
// refusal: harmless for native executables, but keeps externally-sourced
// resume values inert if a cmd.exe hop is ever reintroduced between the SDK
// and the CLI. No format is imposed (resume values may be arbitrary session
// titles). No-op off Windows.
func rejectWindowsCmdMetacharacters(optionName, value string) error {
	if runtime.GOOS != "windows" {
		return nil
	}
	var bad []string
	for _, r := range cmdExeMetacharacters {
		if strings.ContainsRune(value, r) {
			bad = append(bad, strconv.QuoteRune(r))
		}
	}
	if len(bad) == 0 {
		return nil
	}
	slices.Sort(bad)
	return fmt.Errorf("%s value %q contains characters that are unsafe to pass on a Windows command line: %v", optionName, value, bad)
}

// scanLines frames lines out of the chunks r yields and calls cb once per
// line, isolating cb panics per line so one bad line can't silence the
// rest. A line is delivered even when it lacks a trailing newline, and an
// overlong partial line is flushed rather than buffered without bound.
func scanLines(r io.Reader, max int, cb func(string)) {
	var framer lineFramer
	chunk := make([]byte, 32*1024)
	emit := func(line string) {
		defer func() { _ = recover() }() // a panicking callback drops this line, not the rest
		line = strings.TrimRightFunc(line, unicode.IsSpace)
		if line != "" {
			cb(line)
		}
	}
	for {
		n, err := r.Read(chunk)
		if n > 0 {
			for _, line := range framer.push(chunk[:n]) {
				emit(line)
			}
			if len(framer.pending) > max {
				emit(string(framer.flush()))
			}
		}
		if err != nil {
			break
		}
	}
	// The last partial line still reaches the callback when the stream
	// closes mid-line.
	emit(string(framer.flush()))
}
