package claudeagentsdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the Phase 8/9 mutation parity: sanitize_unicode,
// _derive_title_from_entries, fork edge cases (UUID remap, parentUuid
// chain walk, " (fork)" suffix), and import_session_to_store.

func TestSanitizeUnicodeStripsDangerous(t *testing.T) {
	in := "hello\u200bworld\u202e!\ufeff"
	out := sanitizeUnicode(in)
	if strings.Contains(out, "\u200b") || strings.Contains(out, "\u202e") || strings.Contains(out, "\ufeff") {
		t.Fatalf("expected zero-width chars stripped, got %q", out)
	}
	if !strings.HasPrefix(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("legit text mangled: %q", out)
	}
}

func TestSanitizeUnicodeStripsPrivateUse(t *testing.T) {
	in := "ok\ue000private\ue001"
	out := sanitizeUnicode(in)
	if strings.ContainsAny(out, "\ue000\ue001") {
		t.Fatalf("private-use chars not stripped: %q", out)
	}
}

func TestSanitizeUnicodeKeepsWhitespace(t *testing.T) {
	in := "line1\nline2\tcol3"
	out := sanitizeUnicode(in)
	if out != in {
		t.Fatalf("whitespace should be preserved: %q != %q", out, in)
	}
}

func TestSanitizeUnicodeIterates(t *testing.T) {
	// Force an iteration: every pass must shrink or stabilize.
	in := "abc\u200b"
	out := sanitizeUnicode(in)
	if strings.Contains(out, "\u200b") {
		t.Fatalf("iteration should strip zero-width: %q", out)
	}
}

func TestDeriveTitleFromEntries(t *testing.T) {
	// custom wins over ai; last custom wins.
	entries := []map[string]any{
		{"customTitle": "first"},
		{"aiTitle": "ai one"},
		{"customTitle": "second"},
		{"aiTitle": "ai two"},
	}
	if got := deriveTitleFromEntries(entries); got != "second" {
		t.Fatalf("custom title wrong: %q", got)
	}
	// No custom: fall back to last ai.
	if got := deriveTitleFromEntries([]map[string]any{{"aiTitle": "only"}}); got != "only" {
		t.Fatalf("ai title fallback wrong: %q", got)
	}
}

func TestDeriveTitleFromEntriesFallsBackToFirstPrompt(t *testing.T) {
	entries := []map[string]any{
		{"type": "user", "message": map[string]any{"content": "real prompt"}},
		{"type": "user", "message": map[string]any{"content": "ignored later"}},
	}
	if got := deriveTitleFromEntries(entries); got != "real prompt" {
		t.Fatalf("first prompt wrong: %q", got)
	}
}

func TestBuildForkLinesUUIDRemap(t *testing.T) {
	transcript := []map[string]any{
		{"type": "user", "uuid": "u1", "parentUuid": nil, "timestamp": "2024-01-01T00:00:00Z", "message": map[string]any{"role": "user", "content": "hi"}},
		{"type": "assistant", "uuid": "u2", "parentUuid": "u1", "timestamp": "2024-01-01T00:00:01Z", "message": map[string]any{"role": "assistant", "content": "hello"}},
	}
	newID, lines, err := buildForkLines(transcript, nil, "src", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if newID == "src" || newID == "" {
		t.Fatalf("new session id wrong: %q", newID)
	}
	if len(lines) != 3 { // 2 transcript + 1 title
		t.Fatalf("want 3 lines (2 transcript + title), got %d", len(lines))
	}
	if lines[0]["sessionId"] != newID || lines[1]["sessionId"] != newID {
		t.Fatalf("sessionId not rewritten: %+v", lines)
	}
	if lines[0]["uuid"] == "u1" || lines[1]["uuid"] == "u2" {
		t.Fatalf("UUIDs not remapped: %+v %+v", lines[0]["uuid"], lines[1]["uuid"])
	}
	if lines[0]["parentUuid"] != nil {
		t.Fatalf("first entry should have nil parentUuid, got %v", lines[0]["parentUuid"])
	}
	if lines[1]["parentUuid"] == "u1" || lines[1]["parentUuid"] == nil {
		t.Fatalf("second entry's parentUuid should map to first's new UUID, got %v", lines[1]["parentUuid"])
	}
	// Last entry's timestamp should be the new "now"; earlier entries keep theirs.
	if lines[0]["timestamp"] != "2024-01-01T00:00:00Z" {
		t.Fatalf("early entry timestamp overwrote: %v", lines[0]["timestamp"])
	}
}

func TestBuildForkLinesSkipsProgress(t *testing.T) {
	// progress entries are kept in the uuid map (for parent walking) but
	// not emitted to the output.
	transcript := []map[string]any{
		{"type": "user", "uuid": "u1", "parentUuid": nil, "timestamp": "t"},
		{"type": "progress", "uuid": "p1", "parentUuid": "u1", "timestamp": "t"},
		{"type": "assistant", "uuid": "u2", "parentUuid": "p1", "timestamp": "t"},
	}
	_, lines, err := buildForkLines(transcript, nil, "src", "", "")
	if err != nil {
		t.Fatal(err)
	}
	// Expect user + assistant + title.
	if len(lines) != 3 {
		t.Fatalf("want 3 (skipping progress), got %d", len(lines))
	}
	for _, l := range lines {
		if l["type"] == "progress" {
			t.Fatalf("progress entry leaked: %+v", l)
		}
	}
}

func TestBuildForkLinesUpToMessageID(t *testing.T) {
	transcript := []map[string]any{
		{"type": "user", "uuid": "u1", "parentUuid": nil, "timestamp": "t1"},
		{"type": "assistant", "uuid": "u2", "parentUuid": "u1", "timestamp": "t2"},
		{"type": "user", "uuid": "u3", "parentUuid": "u2", "timestamp": "t3"},
	}
	_, lines, err := buildForkLines(transcript, nil, "src", "u2", "")
	if err != nil {
		t.Fatal(err)
	}
	// Expect u1 + u2 + title (u3 dropped, progress skipped).
	if len(lines) != 3 {
		t.Fatalf("want 3 lines (2 transcript + title), got %d", len(lines))
	}
	if lines[0]["uuid"] == "u3" || lines[1]["uuid"] == "u3" {
		t.Fatalf("u3 leaked past cutoff: %+v", lines)
	}
}

func TestBuildForkLinesUpToMessageIDNotFound(t *testing.T) {
	_, _, err := buildForkLines([]map[string]any{{"type": "user", "uuid": "u1"}}, nil, "src", "missing", "")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not-found error, got %v", err)
	}
}

func TestBuildForkLinesDropsSidechain(t *testing.T) {
	transcript := []map[string]any{
		{"type": "user", "uuid": "u1", "isSidechain": true},
	}
	_, _, err := buildForkLines(transcript, nil, "src", "", "")
	if err == nil || !strings.Contains(err.Error(), "no messages") {
		t.Fatalf("expected no-messages error, got %v", err)
	}
}

func TestBuildForkLinesTitleDerivation(t *testing.T) {
	transcript := []map[string]any{
		{"customTitle": "Original"},
		{"type": "user", "message": map[string]any{"content": "ignored for derived"}},
	}
	// No explicit title: derived title gets " (fork)" suffix.
	_, lines, _ := buildForkLines(transcript, nil, "src", "", "")
	title := lines[len(lines)-1]
	if title["type"] != "custom-title" {
		t.Fatalf("last entry not a title: %+v", title)
	}
	if title["customTitle"] != "Original (fork)" {
		t.Fatalf("derived title wrong: %v", title["customTitle"])
	}
	// Explicit title overrides.
	_, lines, _ = buildForkLines(transcript, nil, "src", "", "My Fork")
	title = lines[len(lines)-1]
	if title["customTitle"] != "My Fork" {
		t.Fatalf("explicit title wrong: %v", title["customTitle"])
	}
}

func TestBuildForkLinesEmptySession(t *testing.T) {
	_, _, err := buildForkLines(nil, nil, "src", "", "")
	if err == nil {
		t.Fatal("expected error for empty transcript")
	}
}

func TestParseForkTranscript(t *testing.T) {
	entries := []map[string]any{
		{"type": "user", "uuid": "u1"},
		{"type": "attachment", "uuid": "a1"},
		{"type": "progress", "uuid": "p1"},
		{"type": "system", "uuid": "s1"},
		{"type": "queue-operation", "uuid": "q1"}, // not a transcript type
		{"type": "content-replacement", "sessionId": "src", "replacements": []any{"r1", "r2"}},
		{"type": "content-replacement", "sessionId": "other", "replacements": []any{"r3"}},
	}
	transcript, replacements := parseForkTranscript(entries, "src")
	if len(transcript) != 4 { // user, attachment, progress, system
		t.Fatalf("transcript count wrong: %d", len(transcript))
	}
	if len(replacements) != 2 {
		t.Fatalf("replacements wrong (should only include matching sessionId): %v", replacements)
	}
}

// --- import_session_to_store ---

func TestImportSessionToStoreInvalidID(t *testing.T) {
	store := &memStore{data: map[SessionKey][]map[string]any{}}
	err := ImportSessionToStore(context.Background(), "not-a-uuid", ".", store, false)
	if err == nil || !strings.Contains(err.Error(), "invalid session_id") {
		t.Fatalf("expected invalid session_id error, got %v", err)
	}
}

func TestImportSessionToStoreHappyPath(t *testing.T) {
	// Build a session file at the resolution path (~/.claude/projects/<proj>).
	root := t.TempDir()
	// The file must live under the resolver's derived project key, and the
	// import stores under the on-disk directory name (resolved.parent.name
	// in Python), so both use the derived key here.
	projectKey := ProjectKeyForDirectory(root)
	projectDir := filepath.Join(root, ".claude", "projects", projectKey)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "550e8400-e29b-41d4-a716-446655440000"
	main := filepath.Join(projectDir, sid+".jsonl")
	entries := []map[string]any{
		{"type": "user", "uuid": "u1"},
		{"type": "assistant", "uuid": "u2"},
	}
	f, err := os.Create(main)
	if err != nil {
		t.Fatal(err)
	}
	enc := json.NewEncoder(f)
	for _, e := range entries {
		_ = enc.Encode(e)
	}
	_ = f.Close()
	// Add a subagent transcript + sidecar.
	sub := filepath.Join(projectDir, sid, "subagents", "agent-a")
	if err := os.MkdirAll(sub, 0o700); err != nil {
		t.Fatal(err)
	}
	subJSONL := filepath.Join(sub, "transcript.jsonl")
	f, _ = os.Create(subJSONL)
	_ = json.NewEncoder(f).Encode(map[string]any{"type": "user", "uuid": "su1"})
	_ = f.Close()
	meta := []byte(`{"agentId":"a1","agentType":"general-purpose"}`)
	_ = os.WriteFile(filepath.Join(sub, "transcript.meta.json"), meta, 0o600)

	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	store := &memStore{data: map[SessionKey][]map[string]any{}}
	if err := ImportSessionToStore(context.Background(), sid, root, store, true); err != nil {
		t.Fatal(err)
	}

	mainKey := SessionKey{ProjectKey: projectKey, SessionID: sid}
	got := store.data[mainKey]
	if len(got) != 2 {
		t.Fatalf("main transcript wrong: %d entries, want 2: %+v", len(got), got)
	}

	subKey := SessionKey{ProjectKey: projectKey, SessionID: sid, Subpath: "subagents/agent-a/transcript"}
	got = store.data[subKey]
	if len(got) != 2 { // 1 transcript + 1 agent_metadata
		t.Fatalf("subagent transcript wrong: %+v", got)
	}
	found := false
	for _, e := range got {
		if e["type"] == "agent_metadata" && e["agentId"] == "a1" {
			found = true
		}
	}
	if !found {
		t.Fatalf("agent_metadata sidecar not imported: %+v", got)
	}
}

func TestCollectJSONLFilesSorted(t *testing.T) {
	root := t.TempDir()
	for _, n := range []string{"b.jsonl", "a.jsonl", "c.jsonl"} {
		_ = os.WriteFile(filepath.Join(root, n), []byte("{}\n"), 0o600)
	}
	// Subdir file should come after top-level.
	sub := filepath.Join(root, "sub")
	_ = os.Mkdir(sub, 0o700)
	_ = os.WriteFile(filepath.Join(sub, "z.jsonl"), []byte("{}\n"), 0o600)

	got := collectJSONLFiles(root)
	if len(got) != 4 {
		t.Fatalf("want 4 files, got %d: %v", len(got), got)
	}
	if filepath.Base(got[0]) != "a.jsonl" || filepath.Base(got[1]) != "b.jsonl" || filepath.Base(got[2]) != "c.jsonl" {
		t.Fatalf("sort order wrong at top level: %v", got)
	}
	if filepath.Base(got[3]) != "z.jsonl" {
		t.Fatalf("subdir entry wrong: %v", got[3])
	}
}

// --- fork integration: local file path ---

func TestForkSessionLocalFileUUIDRemap(t *testing.T) {
	root := t.TempDir()
	pk := ProjectKeyForDirectory(root)
	projectDir := filepath.Join(root, ".claude", "projects", pk)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "550e8400-e29b-41d4-a716-446655440000"
	srcPath := filepath.Join(projectDir, sid+".jsonl")
	f, _ := os.Create(srcPath)
	enc := json.NewEncoder(f)
	_ = enc.Encode(map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "timestamp": "t1", "isSidechain": false})
	_ = enc.Encode(map[string]any{"type": "assistant", "uuid": "u2", "parentUuid": "u1", "timestamp": "t2", "isSidechain": false})
	_ = f.Close()
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	res, err := ForkSession(sid, "", root)
	if err != nil {
		t.Fatal(err)
	}
	if res.SessionID == sid {
		t.Fatalf("new id must differ from source")
	}
	forkPath := filepath.Join(projectDir, res.SessionID+".jsonl")
	if _, err := os.Stat(forkPath); err != nil {
		t.Fatalf("fork file missing: %v", err)
	}
	got, err := readJSONL(forkPath)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range got {
		if e["sessionId"] != res.SessionID {
			t.Fatalf("sessionId not rewritten: %v", e["sessionId"])
		}
		if e["type"] == "user" && e["uuid"] == "u1" {
			t.Fatalf("user uuid not remapped: %v", e["uuid"])
		}
		if e["type"] == "assistant" && e["uuid"] == "u2" {
			t.Fatalf("assistant uuid not remapped: %v", e["uuid"])
		}
	}
}
