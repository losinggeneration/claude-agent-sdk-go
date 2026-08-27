package claudeagentsdk

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"1.9.9", "2.0.0", true},
		{"2.0.0", "2.0.0", false},
		{"2.1.0", "2.0.0", false},
		{"2.0.0", "2.0.1", true},
		{"2.0", "2.0.0", false},
		{"2.0", "2.0.1", true},
	}
	for _, c := range cases {
		if got := versionLess(c.a, c.b); got != c.want {
			t.Errorf("versionLess(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestWindowsBatchCLIPath(t *testing.T) {
	batch := []string{
		`C:\npm\claude.cmd`,
		`C:\npm\claude.bat`,
		`C:\npm\claude.cmd\..\claude.cmd`,
		`claude.cmd:stream`,
		`C:claude.cmd`,
		`claude.CMD.`,
		`claude.cmd `,
		`/c/npm/claude.cmd`,
		`C:\npm\dir.cmd\claude.exe`, // any component counts
	}
	for _, p := range batch {
		if !windowsBatchCLIPath(p) {
			t.Errorf("windowsBatchCLIPath(%q) = false, want true", p)
		}
	}
	notBatch := []string{
		`C:\npm\claude.exe`,
		`/usr/local/bin/claude`,
		`C:\claude.cmdx`,
		``,
		`claude`,
		`C:\npm\claude`,
	}
	for _, p := range notBatch {
		if windowsBatchCLIPath(p) {
			t.Errorf("windowsBatchCLIPath(%q) = true, want false", p)
		}
	}
}

func TestChildEnvParity(t *testing.T) {
	t.Setenv("CLAUDECODE", "1")
	t.Setenv("CLAUDE_CODE_ENTRYPOINT", "inherited")
	env := childEnv(map[string]string{"FOO": "bar"}, "")
	var sawClaudecode, sawInheritedEntrypoint bool
	var entrypoint, sdkVersion, foo string
	for _, kv := range env {
		switch {
		case kv == "CLAUDECODE=1":
			sawClaudecode = true
		case kv == "CLAUDE_CODE_ENTRYPOINT=inherited":
			sawInheritedEntrypoint = true
		case strings.HasPrefix(kv, "CLAUDE_CODE_ENTRYPOINT="):
			entrypoint = strings.TrimPrefix(kv, "CLAUDE_CODE_ENTRYPOINT=")
		case strings.HasPrefix(kv, "CLAUDE_AGENT_SDK_VERSION="):
			sdkVersion = strings.TrimPrefix(kv, "CLAUDE_AGENT_SDK_VERSION=")
		case kv == "FOO=bar":
			foo = "bar"
		}
	}
	if sawClaudecode {
		t.Error("CLAUDECODE must be filtered from the child env")
	}
	if sawInheritedEntrypoint {
		t.Error("inherited CLAUDE_CODE_ENTRYPOINT must not shadow the sdk-go default")
	}
	if entrypoint != "sdk-go" {
		t.Errorf("CLAUDE_CODE_ENTRYPOINT = %q, want sdk-go", entrypoint)
	}
	if sdkVersion != Version {
		t.Errorf("CLAUDE_AGENT_SDK_VERSION = %q, want %q", sdkVersion, Version)
	}
	if foo != "bar" {
		t.Error("Options.Env entry missing from child env")
	}

	// Options.Env may override the entrypoint but never the SDK version.
	env = childEnv(map[string]string{"CLAUDE_CODE_ENTRYPOINT": "custom", "CLAUDE_AGENT_SDK_VERSION": "spoofed"}, "")
	for _, kv := range env {
		if kv == "CLAUDE_CODE_ENTRYPOINT=custom" && entrypoint != "custom" {
			entrypoint = "custom"
		}
		if kv == "CLAUDE_AGENT_SDK_VERSION=spoofed" {
			t.Error("CLAUDE_AGENT_SDK_VERSION must not be overridable")
		}
	}
	if entrypoint != "custom" {
		t.Error("Options.Env override of CLAUDE_CODE_ENTRYPOINT lost")
	}
}

func TestChildEnvPWDRewritten(t *testing.T) {
	t.Setenv("PWD", "/old/cwd")
	env := childEnv(nil, "/new/cwd")
	for _, kv := range env {
		if strings.HasPrefix(kv, "PWD=") && kv != "PWD=/new/cwd" {
			t.Fatalf("inherited PWD shadowed the working dir: %q", kv)
		}
	}
	found := false
	for _, kv := range env {
		if kv == "PWD=/new/cwd" {
			found = true
		}
	}
	if !found {
		t.Fatal("PWD=/new/cwd missing from child env")
	}
}

// The close path must give the child a grace period after stdin EOF before
// escalating to SIGTERM/Kill — the CLI flushes its session file after EOF
// (Python #625). A child that exits on its own must never be signaled.
func TestCloseWaitsForGracefulExit(t *testing.T) {
	tr := NewSubprocessTransport("hi", Options{CLIPath: "/bin/cat"})
	if err := tr.Connect(context.Background()); err != nil {
		t.Skipf("needs a real child process: %v", err)
	}
	msgs, errs := tr.ReadMessages(context.Background())
	// /bin/cat echoes nothing parseable and exits on stdin EOF.
	_ = msgs
	_ = errs
	start := time.Now()
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Fatalf("graceful child took %v to close, escalation likely fired", d)
	}
}

// A child that ignores stdin EOF must be SIGTERM'd after the grace period,
// and one that blocks SIGTERM too must be SIGKILL'd — Close must always reap.
func TestCloseEscalatesOnStuckChild(t *testing.T) {
	for _, tc := range []struct {
		name   string
		script string
	}{
		{"term", "#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo \"2.0.0 (Claude Code)\"; exit 0; fi\ntrap 'exit 0' TERM\nsleep 10 &\nwait\n"},
		{"kill", "#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo \"2.0.0 (Claude Code)\"; exit 0; fi\ntrap '' TERM INT\nsleep 30 &\nwait\n"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			cli := dir + "/fake-cli"
			if err := os.WriteFile(cli, []byte(tc.script), 0o755); err != nil {
				t.Fatal(err)
			}
			tr := NewSubprocessTransport("hi", Options{CLIPath: cli})
			tr.closeGrace = 200 * time.Millisecond
			if err := tr.Connect(context.Background()); err != nil {
				t.Fatal(err)
			}
			_, _ = tr.ReadMessages(context.Background())
			start := time.Now()
			if err := tr.Close(); err != nil {
				t.Fatal(err)
			}
			if d := time.Since(start); d > 5*time.Second {
				t.Fatalf("escalation took %v; child never reaped", d)
			}
		})
	}
}

// The version probe must never block the spawn: a fake CLI whose -v hangs
// still connects (2s probe timeout), and the warn fires for old versions.
func TestCheckCLIVersionWarnsOnOldVersion(t *testing.T) {
	dir := t.TempDir()
	cli := dir + "/fake-cli"
	script := "#!/bin/sh\nif [ \"$1\" = \"-v\" ]; then echo \"1.0.3 (Claude Code)\"; exit 0; fi\nexec cat\n"
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	tr := NewSubprocessTransport("hi", Options{CLIPath: cli})
	// Capture stderr.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	tr.checkCLIVersion(cli)
	os.Stderr = old
	_ = w.Close()
	buf := make([]byte, 4096)
	n, _ := r.Read(buf)
	msg := string(buf[:n])
	_ = r.Close()
	if !strings.Contains(msg, "1.0.3") || !strings.Contains(msg, "2.0.0") {
		t.Fatalf("expected unsupported-version warning, got %q", msg)
	}
}

func TestScanMessagesDeliversLineWithoutTrailingNewline(t *testing.T) {
	tt := &SubprocessTransport{stdout: io.NopCloser(strings.NewReader("{\"a\":1}\n{\"b\":2}"))}
	out := make(chan map[string]any, 2)
	err := tt.scanMessages(context.Background(), out)
	if err != nil {
		t.Fatalf("scanMessages: %v", err)
	}
	close(out)
	var got []string
	for m := range out {
		keys := make([]string, 0, len(m))
		for k := range m {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		got = append(got, strings.Join(keys, ","))
	}
	if strings.Join(got, ";") != "a;b" {
		t.Fatalf("got %v, want both messages incl. the no-newline tail", got)
	}
}

func TestScanMessagesDropsTruncatedTail(t *testing.T) {
	tt := &SubprocessTransport{stdout: io.NopCloser(strings.NewReader("{\"a\":1}\n{\"trunc"))}
	out := make(chan map[string]any, 2)
	err := tt.scanMessages(context.Background(), out)
	if err != nil {
		t.Fatalf("scanMessages: %v", err)
	}
	close(out)
	n := 0
	for range out {
		n++
	}
	if n != 1 {
		t.Fatalf("got %d messages, want 1 (truncated JSON tail dropped)", n)
	}
}

func TestScanMessagesRejectsOversizedLine(t *testing.T) {
	tt := &SubprocessTransport{Options: Options{MaxBufferSize: 64}, stdout: io.NopCloser(strings.NewReader("{\"pad\":\"" + strings.Repeat("x", 100) + "\"}\n"))}
	out := make(chan map[string]any, 1)
	err := tt.scanMessages(context.Background(), out)
	var de *JSONDecodeError
	if !errors.As(err, &de) {
		t.Fatalf("got %v, want JSONDecodeError", err)
	}
	if !strings.Contains(de.Error(), "maximum buffer size") {
		t.Fatalf("error should mention buffer size: %v", de)
	}
}

func TestScanMessagesRejectsOversizedPending(t *testing.T) {
	// No newline ever arrives: the pending buffer must still be bounded.
	tt := &SubprocessTransport{Options: Options{MaxBufferSize: 64}, stdout: io.NopCloser(strings.NewReader("{\"pad\":\"" + strings.Repeat("x", 100)))}
	out := make(chan map[string]any, 1)
	err := tt.scanMessages(context.Background(), out)
	var de *JSONDecodeError
	if !errors.As(err, &de) {
		t.Fatalf("got %v, want JSONDecodeError for unbounded pending line", err)
	}
}

func TestScanLinesFlushesPartialAndIsolatesCallbackPanics(t *testing.T) {
	var mu sync.Mutex
	var got []string
	cb := func(line string) {
		if strings.Contains(line, "boom") {
			panic("callback exploded")
		}
		mu.Lock()
		defer mu.Unlock()
		got = append(got, line)
	}
	// Two chunk-sized writes exercise chunk-boundary framing; the final
	// line has no trailing newline.
	scanLines(io.NopCloser(strings.NewReader("first line\nsecond\r\nboom\nno-newline-tail")), 1024, cb)
	mu.Lock()
	defer mu.Unlock()
	want := []string{"first line", "second", "no-newline-tail"} // "boom" line dropped by its own panic, later lines survive
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestScanLinesFlushesOverlongPartial(t *testing.T) {
	var mu sync.Mutex
	var got []string
	scanLines(io.NopCloser(strings.NewReader(strings.Repeat("a", 50))), 10, func(line string) {
		mu.Lock()
		defer mu.Unlock()
		got = append(got, line)
	})
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 1 || len(got[0]) != 50 {
		t.Fatalf("got %q, want one flushed 50-char partial line", got)
	}
}

func TestApplyUserUnknownUserErrors(t *testing.T) {
	err := applyUser(&exec.Cmd{}, "no-such-user-xyz-please")
	if err == nil {
		t.Fatal("want lookup error for unknown user")
	}
}

func TestChildEnvExplicitOverridesInherited(t *testing.T) {
	t.Setenv("SDK_TEST_PARENT_VAR", "inherited")
	env := childEnv(map[string]string{"SDK_TEST_PARENT_VAR": "override"}, "")
	var seen []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "SDK_TEST_PARENT_VAR=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 || seen[0] != "SDK_TEST_PARENT_VAR=override" {
		t.Fatalf("got %q, want single override entry", seen)
	}
}

func TestChildEnvInheritedSDKVersionReplaced(t *testing.T) {
	t.Setenv("CLAUDE_AGENT_SDK_VERSION", "0.0.0-parent")
	env := childEnv(nil, "")
	var seen []string
	for _, kv := range env {
		if strings.HasPrefix(kv, "CLAUDE_AGENT_SDK_VERSION=") {
			seen = append(seen, kv)
		}
	}
	if len(seen) != 1 || seen[0] != "CLAUDE_AGENT_SDK_VERSION="+Version {
		t.Fatalf("got %q, want the SDK's own version only", seen)
	}
}

func TestMergeTraceContext(t *testing.T) {
	env := []string{"PATH=/bin", "TRACEPARENT=stale", "TRACESTATE=oldstate"}
	carrier := map[string]string{"traceparent": "fresh", "tracestate": "newstate"}
	got := mergeTraceContext(env, carrier, nil)
	want := []string{"PATH=/bin", "TRACEPARENT=fresh", "TRACESTATE=newstate"}
	if !slices.Equal(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}

	// No traceparent in carrier → env untouched.
	same := mergeTraceContext(env, map[string]string{"baggage": "x"}, nil)
	if !slices.Equal(same, env) {
		t.Fatalf("baggage-only carrier must not touch env: %q", same)
	}

	// Explicit Options.Env always wins: childEnv has already replaced the
	// inherited value with the explicit one, and the carrier must not
	// overwrite it.
	opts := map[string]string{"TRACEPARENT": "explicit"}
	got = mergeTraceContext([]string{"PATH=/bin", "TRACEPARENT=explicit"}, carrier, opts)
	if !contains(got, "TRACEPARENT=explicit") || contains(got, "TRACEPARENT=fresh") {
		t.Fatalf("explicit env must win: %q", got)
	}
}

// TestFakeCLITransportEndToEnd runs the full transport against a local
// shell script standing in for the CLI — no network, no real Claude, no
// subscription context. It validates, through a real exec + pipes:
//
//   - stdout framing across chunk boundaries (a line split by a sleep) and
//     a trailing line with no final newline (framer tail path)
//   - stderr framing incl. its no-newline tail reaching Options.Stderr
//   - env propagation: OTEL TRACEPARENT scrubbed+injected, Options.Env
//     overriding an inherited var, PWD=CWD, SDK version + entrypoint vars
func TestFakeCLITransportEndToEnd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell script")
	}
	script := `#!/bin/sh
if [ "$1" = "-v" ]; then echo "2.0.0 (Claude Code)"; exit 0; fi
printf '{"traceparent":"%s"}\n' "$TRACEPARENT"
printf '{"pwd":"%s"}\n' "$PWD"
printf '{"parent":"%s"}\n' "$SDK_TEST_PARENT_VAR"
printf '{"sdkver":"%s"}\n' "$CLAUDE_AGENT_SDK_VERSION"
echo err-one >&2
printf 'err-no-newline' >&2
printf '{"part'
sleep 0.3
printf 'ial":1}\n'
printf '{"tail":"end"}'
`
	dir := t.TempDir()
	cli := filepath.Join(dir, "fake-cli")
	if err := os.WriteFile(cli, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("SDK_TEST_PARENT_VAR", "inherited")
	t.Setenv("TRACEPARENT", "stale")
	cwd := t.TempDir()
	prevInject := InjectTraceContext
	InjectTraceContext = func() map[string]string {
		return map[string]string{"traceparent": "fresh", "tracestate": "ts"}
	}
	defer func() { InjectTraceContext = prevInject }()

	stderrLines := make(chan string, 4)
	tr := NewSubprocessTransport("hi", Options{
		CLIPath: cli,
		CWD:     cwd,
		Env:     map[string]string{"SDK_TEST_PARENT_VAR": "override"},
		Stderr:  func(line string) { stderrLines <- line },
	})
	ctx := context.Background()
	if err := tr.Connect(ctx); err != nil {
		t.Skipf("needs a real child process: %v", err)
	}
	msgs, _ := tr.ReadMessages(ctx)
	got := map[string]string{}
	for m := range msgs {
		for k, v := range m {
			got[k] = fmt.Sprint(v)
		}
	}

	for k, want := range map[string]string{
		"traceparent": "fresh",    // scrubbed inherited "stale", carrier won
		"pwd":         cwd,        // PWD follows CWD into the child
		"parent":      "override", // Options.Env beats the inherited value
		"sdkver":      Version,
		"partial":     "1",   // line reassembled across the sleep seam
		"tail":        "end", // final line without a newline
	} {
		if got[k] != want {
			t.Errorf("%s = %q, want %q", k, got[k], want)
		}
	}

	recv := func() string {
		select {
		case l := <-stderrLines:
			return l
		case <-time.After(3 * time.Second):
			return ""
		}
	}
	if l := recv(); l != "err-one" {
		t.Errorf("stderr line 1 = %q, want %q", l, "err-one")
	}
	if l := recv(); l != "err-no-newline" {
		t.Errorf("stderr tail = %q, want %q", l, "err-no-newline")
	}
	if err := tr.Close(); err != nil {
		t.Fatal(err)
	}
}
