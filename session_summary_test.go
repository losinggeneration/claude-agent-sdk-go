package claudeagentsdk

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"
)

// Tests for the mirror batcher retry + per-session serialization, the
// foldSessionSummary helper, the SessionDeleter cascade, and the
// SessionSummarizer fast path.

func TestFoldSummaryFirstPrompt(t *testing.T) {
	prev := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s1"}, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "hello world"}, "timestamp": "2024-01-01T00:00:00Z"},
	})
	if prev.Data["first_prompt"] != "hello world" {
		t.Fatalf("first_prompt wrong: %v", prev.Data["first_prompt"])
	}
	if locked, _ := prev.Data["first_prompt_locked"].(bool); !locked {
		t.Fatal("first_prompt_locked not set")
	}
	// Once locked, further entries must not change the prompt.
	next := foldSessionSummary(&prev, SessionKey{ProjectKey: "p", SessionID: "s1"}, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "second"}},
	})
	if next.Data["first_prompt"] != "hello world" {
		t.Fatalf("first_prompt overwrote: %v", next.Data["first_prompt"])
	}
}

func TestFoldSummarySkipsMetaAndCompact(t *testing.T) {
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "user", "isMeta": true, "message": map[string]any{"content": "ignore me"}},
		{"type": "user", "isCompactSummary": true, "message": map[string]any{"content": "also ignored"}},
		{"type": "user", "message": map[string]any{"content": "real prompt"}},
	})
	if out.Data["first_prompt"] != "real prompt" {
		t.Fatalf("expected real prompt, got %v", out.Data["first_prompt"])
	}
}

func TestFoldSummarySkipsToolResult(t *testing.T) {
	// A user message carrying tool_result content should be skipped from
	// first_prompt derivation, matching Python.
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "user", "message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "x"}}}},
	})
	if _, has := out.Data["first_prompt"]; has {
		t.Fatalf("tool_result message should be skipped, got %v", out.Data)
	}
}

func TestFoldSummarySlashCommandFallback(t *testing.T) {
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "user", "message": map[string]any{"content": "/help please"}},
	})
	if out.Data["first_prompt"] != nil {
		t.Fatalf("slash command should not produce first_prompt: %v", out.Data["first_prompt"])
	}
	if out.Data["command_fallback"] != "help" {
		t.Fatalf("command_fallback wrong: %v", out.Data["command_fallback"])
	}
}

func TestFoldSummaryLongPromptTruncated(t *testing.T) {
	long := strings.Repeat("a", 250)
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "user", "message": map[string]any{"content": long}},
	})
	got := out.Data["first_prompt"].(string)
	// Python len() counts characters: 200 + 1 ellipsis rune. Go len() is
	// bytes (201 runes / 203 bytes), so count runes.
	if utf8.RuneCountInString(got) != 201 || !strings.HasSuffix(got, "\u2026") {
		t.Fatalf("expected 200 chars + ellipsis, got len=%d suffix=%q", utf8.RuneCountInString(got), got[len(got)-3:])
	}
}

func TestFoldSummaryLastWinsFields(t *testing.T) {
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"customTitle": "first"},
		{"customTitle": "second"},
		{"aiTitle": "ai title"},
		{"gitBranch": "main"},
		{"lastPrompt": "remember this"},
	})
	if out.Data["custom_title"] != "second" {
		t.Fatalf("custom_title last-wins wrong: %v", out.Data["custom_title"])
	}
	if out.Data["ai_title"] != "ai title" {
		t.Fatalf("ai_title wrong: %v", out.Data["ai_title"])
	}
	if out.Data["git_branch"] != "main" {
		t.Fatalf("git_branch wrong: %v", out.Data["git_branch"])
	}
	if out.Data["last_prompt"] != "remember this" {
		t.Fatalf("last_prompt wrong: %v", out.Data["last_prompt"])
	}
}

func TestFoldSummaryCreatedAtLatchesFirst(t *testing.T) {
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"timestamp": "2024-01-01T00:00:00.000000Z"},
		{"timestamp": "2024-02-01T00:00:00.000000Z"},
	})
	got := out.Data["created_at"]
	want := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()
	switch v := got.(type) {
	case int64:
		if v != want {
			t.Fatalf("created_at = %d, want %d", v, want)
		}
	default:
		t.Fatalf("created_at wrong type: %T %v", got, got)
	}
}

func TestFoldSummaryTagSetAndClear(t *testing.T) {
	a := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "tag", "tag": "research"},
	})
	if a.Data["tag"] != "research" {
		t.Fatalf("tag set wrong: %v", a.Data["tag"])
	}
	b := foldSessionSummary(&a, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"type": "tag", "tag": ""},
	})
	if _, has := b.Data["tag"]; has {
		t.Fatalf("empty tag should clear: %v", b.Data["tag"])
	}
}

func TestFoldSummarySidechainLatched(t *testing.T) {
	out := foldSessionSummary(nil, SessionKey{ProjectKey: "p", SessionID: "s"}, []map[string]any{
		{"isSidechain": true},
		{"isSidechain": false}, // must NOT change it
	})
	if out.Data["is_sidechain"] != true {
		t.Fatalf("is_sidechain latch wrong: %v", out.Data["is_sidechain"])
	}
}

func TestSummaryEntryToSDKInfo(t *testing.T) {
	e := SessionStoreSummaryEntry{SessionID: "s", MTime: 1000, Data: map[string]any{
		"custom_title":        "My Title",
		"first_prompt":        "hi",
		"first_prompt_locked": true,
		"git_branch":          "main",
		"cwd":                 "/tmp",
		"created_at":          int64(1234),
	}}
	info := summaryEntryToSDKInfo(e, "")
	if info == nil {
		t.Fatal("info nil")
	}
	if info.Summary != "My Title" || info.CustomTitle != "My Title" || info.FirstPrompt != "hi" {
		t.Fatalf("summary wrong: %+v", info)
	}
	if info.GitBranch != "main" || info.CWD != "/tmp" || info.LastModified != 1000 {
		t.Fatalf("fields wrong: %+v", info)
	}
	if info.CreatedAt == nil || *info.CreatedAt != 1234 {
		t.Fatalf("created_at wrong: %+v", info.CreatedAt)
	}
}

func TestSummaryEntryToSDKInfoSidechainSkipped(t *testing.T) {
	e := SessionStoreSummaryEntry{SessionID: "s", MTime: 1, Data: map[string]any{"is_sidechain": true}}
	if info := summaryEntryToSDKInfo(e, ""); info != nil {
		t.Fatalf("expected nil, got %+v", info)
	}
}

// --- mirror batcher ---

type flakyStore struct {
	failures int32 // accessed atomically
	attempts atomic.Int32
	timeout  bool // if true, return context.DeadlineExceeded
	store    SessionStore
}

func (f *flakyStore) Append(ctx context.Context, key SessionKey, entries []map[string]any) error {
	n := f.attempts.Add(1)
	if int(n) <= int(atomic.LoadInt32(&f.failures)) {
		if f.timeout {
			return context.DeadlineExceeded
		}
		return errors.New("transient failure")
	}
	return f.store.Append(ctx, key, entries)
}

func (f *flakyStore) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	return f.store.Load(ctx, key)
}

func TestMirrorBatcherRetriesOnError(t *testing.T) {
	base := &memStore{data: map[SessionKey][]map[string]any{}}
	f := &flakyStore{failures: 2, store: base}
	b := NewTranscriptMirrorBatcher(f, "/projects", nil, "")
	b.MirrorFailedAttempts = 3
	b.MirrorRetryBackoff = 1 * time.Millisecond

	b.Enqueue("/projects/p/s.jsonl", []map[string]any{{"type": "user", "uuid": "u1"}})
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	if got := f.attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
	if len(base.data[SessionKey{ProjectKey: "p", SessionID: "s"}]) != 1 {
		t.Fatalf("entry not stored: %+v", base.data)
	}
}

func TestMirrorBatcherNoRetryOnTimeout(t *testing.T) {
	base := &memStore{data: map[SessionKey][]map[string]any{}}
	f := &flakyStore{failures: 5, timeout: true, store: base}
	var gotKey SessionKey
	var gotMsg string
	b := NewTranscriptMirrorBatcher(f, "/projects", func(key SessionKey, msg string) { gotKey, gotMsg = key, msg }, "")
	b.MirrorFailedAttempts = 5
	b.MirrorRetryBackoff = 1 * time.Millisecond

	b.Enqueue("/projects/p/s.jsonl", []map[string]any{{"type": "user"}})
	// Python contract: flush() never raises; failures surface via on_error.
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("Flush must not return the mirror error, got %v", err)
	}
	if gotMsg != context.DeadlineExceeded.Error() {
		t.Fatalf("expected DeadlineExceeded via OnError, got %q", gotMsg)
	}
	if gotKey != (SessionKey{ProjectKey: "p", SessionID: "s"}) {
		t.Fatalf("error key wrong: %+v", gotKey)
	}
	if n := f.attempts.Load(); n != 1 {
		t.Fatalf("timeouts should not retry: attempts=%d", n)
	}
}

func TestMirrorBatcherGivesUpAfterAttempts(t *testing.T) {
	base := &memStore{data: map[SessionKey][]map[string]any{}}
	f := &flakyStore{failures: 10, store: base}
	var gotMsg string
	b := NewTranscriptMirrorBatcher(f, "/projects", func(key SessionKey, msg string) { gotMsg = msg }, "")
	b.MirrorFailedAttempts = 3
	b.MirrorRetryBackoff = 1 * time.Millisecond

	b.Enqueue("/projects/p/s.jsonl", []map[string]any{{"type": "user"}})
	if err := b.Flush(context.Background()); err != nil {
		t.Fatalf("Flush must not return the mirror error, got %v", err)
	}
	if !strings.Contains(gotMsg, "transient failure") {
		t.Fatalf("expected final error via OnError, got %q", gotMsg)
	}
	if got := f.attempts.Load(); got != 3 {
		t.Fatalf("attempts = %d, want 3", got)
	}
}

func TestMirrorBatcherSerializesFlushes(t *testing.T) {
	// Two concurrent Flush calls must not interleave per-session Appends.
	// Run them sequentially and verify both flushes land: serialization
	// is correctness-by-mutex and the in-memory store will lose or
	// duplicate entries if the lock is broken.
	base := &memStore{data: map[SessionKey][]map[string]any{}}
	b := NewTranscriptMirrorBatcher(base, "/projects", nil, "")
	b.Enqueue("/projects/p/s.jsonl", []map[string]any{{"uuid": "u1"}})
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	b.Enqueue("/projects/p/s.jsonl", []map[string]any{{"uuid": "u2"}})
	if err := b.Flush(context.Background()); err != nil {
		t.Fatal(err)
	}
	got := base.data[SessionKey{ProjectKey: "p", SessionID: "s"}]
	if len(got) != 2 || got[0]["uuid"] != "u1" || got[1]["uuid"] != "u2" {
		t.Fatalf("order or count wrong: %+v", got)
	}
}

// --- delete cascade ---

type recDeleter struct {
	deleted []SessionKey
}

func (r *recDeleter) Append(ctx context.Context, key SessionKey, entries []map[string]any) error {
	return nil
}

func (r *recDeleter) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	return nil, nil
}

func (r *recDeleter) ListSubkeys(ctx context.Context, key SessionKey) ([]string, error) {
	return []string{"subagents/agent-a", "subagents/agent-b"}, nil
}

func (r *recDeleter) Delete(ctx context.Context, key SessionKey) error {
	r.deleted = append(r.deleted, key)
	return nil
}

func TestDeleteSessionViaStoreCascadesSubkeys(t *testing.T) {
	d := &recDeleter{}
	store := SessionStore(d)
	key := SessionKey{ProjectKey: "p", SessionID: "s"}
	if err := DeleteSessionViaStore(context.Background(), store, key); err != nil {
		t.Fatal(err)
	}
	if len(d.deleted) != 3 {
		t.Fatalf("expected 3 deletes (main + 2 subkeys), got %d: %+v", len(d.deleted), d.deleted)
	}
	if d.deleted[0] != key {
		t.Fatalf("first delete should be main key, got %+v", d.deleted[0])
	}
}

func TestDeleteSessionViaStoreNoCascadeOnSubpath(t *testing.T) {
	d := &recDeleter{}
	store := SessionStore(d)
	key := SessionKey{ProjectKey: "p", SessionID: "s", Subpath: "subagents/agent-a"}
	if err := DeleteSessionViaStore(context.Background(), store, key); err != nil {
		t.Fatal(err)
	}
	if len(d.deleted) != 1 {
		t.Fatalf("expected 1 delete, got %d: %+v", len(d.deleted), d.deleted)
	}
	if d.deleted[0] != key {
		t.Fatalf("subpath delete should target only that entry: %+v", d.deleted[0])
	}
}

func TestDeleteSessionViaStoreNoOpWithoutDeleter(t *testing.T) {
	// A store without Delete capability returns nil (no-op) rather than
	// erroring, matching the Python WORM-store behavior.
	store := &memStore{data: map[SessionKey][]map[string]any{}}
	if err := DeleteSessionViaStore(context.Background(), store, SessionKey{ProjectKey: "p", SessionID: "s"}); err != nil {
		t.Fatalf("expected no-op, got %v", err)
	}
}

// --- summarizer fast path ---

type recSummarizer struct {
	SessionStore
	summaries []SessionStoreSummaryEntry
}

func (r *recSummarizer) ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionStoreSummaryEntry, error) {
	return r.summaries, nil
}

func TestListSessionsFromStoreUsesSummarizer(t *testing.T) {
	store := &recSummarizer{
		SessionStore: &memStore{data: map[SessionKey][]map[string]any{}},
		summaries: []SessionStoreSummaryEntry{
			{SessionID: "s1", MTime: 2000, Data: map[string]any{"custom_title": "First", "first_prompt": "hi", "first_prompt_locked": true}},
			{SessionID: "s2", MTime: 1000, Data: map[string]any{"summary_hint": "Second", "first_prompt": "hello", "first_prompt_locked": true}},
		},
	}
	out, err := ListSessionsFromStore(context.Background(), store, "p", 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	if out[0].SessionID != "s1" || out[0].Summary != "First" {
		t.Fatalf("first wrong: %+v", out[0])
	}
	if out[1].SessionID != "s2" || out[1].Summary != "Second" {
		t.Fatalf("second wrong: %+v", out[1])
	}
}
