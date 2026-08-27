package claudeagentsdk

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Tests for the Phase 8 session-listing parity: conversation-chain
// building, visibility filter, dedup, sort/offset, and store-backed
// subagent message recovery.

func TestBuildConversationChainBasic(t *testing.T) {
	entries := []map[string]any{
		{"type": "user", "uuid": "u1", "parentUuid": nil, "message": map[string]any{"role": "user", "content": "hi"}},
		{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "message": map[string]any{"role": "assistant", "content": "hello"}},
		{"type": "user", "uuid": "u2", "parentUuid": "a1", "message": map[string]any{"role": "user", "content": "how are you"}},
		{"type": "assistant", "uuid": "a2", "parentUuid": "u2", "message": map[string]any{"role": "assistant", "content": "fine"}},
	}
	chain := buildConversationChain(entries)
	if len(chain) != 4 {
		t.Fatalf("want 4-chain, got %d", len(chain))
	}
	if chain[0]["uuid"] != "u1" || chain[3]["uuid"] != "a2" {
		t.Fatalf("order wrong: %+v", chain)
	}
}

func TestBuildConversationChainSkipsProgress(t *testing.T) {
	// progress entries sit between u1 and u2; chain must follow u2 back
	// through progress -> u1.
	entries := []map[string]any{
		{"type": "user", "uuid": "u1", "parentUuid": nil},
		{"type": "progress", "uuid": "p1", "parentUuid": "u1"},
		{"type": "user", "uuid": "u2", "parentUuid": "p1"},
	}
	chain := buildConversationChain(entries)
	if len(chain) != 3 {
		t.Fatalf("want all 3 (progress kept), got %d: %+v", len(chain), chain)
	}
	if chain[1]["type"] != "progress" {
		t.Fatalf("middle entry should be progress: %+v", chain[1])
	}
}

func TestBuildConversationChainPicksMainLeaf(t *testing.T) {
	// Two terminal branches: a sidechain leaf and a main leaf. The main
	// leaf wins.
	entries := []map[string]any{
		{"type": "user", "uuid": "u1"},
		{"type": "assistant", "uuid": "a1", "parentUuid": "u1"},
		{"type": "user", "uuid": "u2", "parentUuid": "a1"},
		{"type": "user", "uuid": "u3", "parentUuid": "u1", "isSidechain": true},
	}
	chain := buildConversationChain(entries)
	last := chain[len(chain)-1]
	if last["uuid"] != "u2" {
		t.Fatalf("expected u2 as main leaf, got %v", last["uuid"])
	}
}

func TestBuildConversationChainIgnoresLogicalParent(t *testing.T) {
	// logicalParentUuid should not be followed; only parentUuid chains.
	entries := []map[string]any{
		{"type": "user", "uuid": "u1"},
		{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "logicalParentUuid": "old-uuid"},
	}
	chain := buildConversationChain(entries)
	if len(chain) != 2 {
		t.Fatalf("want 2, got %d", len(chain))
	}
}

func TestIsVisibleMessage(t *testing.T) {
	cases := []struct {
		name string
		in   map[string]any
		want bool
	}{
		{"user ok", map[string]any{"type": "user"}, true},
		{"assistant ok", map[string]any{"type": "assistant"}, true},
		{"meta excluded", map[string]any{"type": "user", "isMeta": true}, false},
		{"sidechain excluded", map[string]any{"type": "user", "isSidechain": true}, false},
		{"team excluded", map[string]any{"type": "user", "teamName": "alpha"}, false},
		{"progress excluded", map[string]any{"type": "progress"}, false},
		{"compact included", map[string]any{"type": "user", "isCompactSummary": true}, true},
	}
	for _, c := range cases {
		if got := isVisibleMessage(c.in); got != c.want {
			t.Errorf("%s: want %v, got %v", c.name, c.want, got)
		}
	}
}

func TestDeduplicateBySessionID(t *testing.T) {
	xs := []SDKSessionInfo{
		{SessionID: "a", LastModified: 100},
		{SessionID: "b", LastModified: 200},
		{SessionID: "a", LastModified: 150}, // newer
		{SessionID: "b", LastModified: 50},  // older
	}
	out := deduplicateBySessionID(xs)
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	got := map[string]int64{}
	for _, s := range out {
		got[s.SessionID] = s.LastModified
	}
	if got["a"] != 150 || got["b"] != 200 {
		t.Fatalf("mtime tie-break wrong: %+v", got)
	}
}

func TestApplySortLimitOffset(t *testing.T) {
	xs := []SDKSessionInfo{
		{SessionID: "a", LastModified: 100},
		{SessionID: "b", LastModified: 300},
		{SessionID: "c", LastModified: 200},
	}
	out := applySortLimitOffset(xs, 0, 0)
	if out[0].SessionID != "b" || out[1].SessionID != "c" || out[2].SessionID != "a" {
		t.Fatalf("sort wrong: %+v", out)
	}
	out = applySortLimitOffset(xs, 1, 1)
	if len(out) != 1 || out[0].SessionID != "c" {
		t.Fatalf("limit/offset wrong: %+v", out)
	}
	out = applySortLimitOffset(xs, 0, 0)
	out = applySortLimitOffset(out, 0, 10)
	if out != nil {
		t.Fatalf("overshoot offset should be empty: %+v", out)
	}
}

func TestFindProjectDirExact(t *testing.T) {
	tmp := t.TempDir()
	// configProjectsDir reads $HOME/.claude/projects, so reproduce that
	// structure rather than putting the project dir at the temp root.
	root := filepath.Join(tmp, ".claude", "projects")
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	proj := ProjectKeyForDirectory(tmp)
	if err := os.MkdirAll(filepath.Join(root, proj), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", tmp)
	t.Setenv("CLAUDE_CONFIG_DIR", "")
	got := findProjectDir(tmp)
	if got == "" {
		t.Fatal("expected exact match")
	}
	if filepath.Base(got) != proj {
		t.Fatalf("got %q, want %q", filepath.Base(got), proj)
	}
}

func TestListSessionsFromStoreDedupesByID(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	pk := ProjectKeyForDirectory(".")
	store := memStore{
		data: map[SessionKey][]map[string]any{
			{ProjectKey: pk, SessionID: sid}: {{"type": "user", "uuid": "u1"}},
		},
		list: []SessionStoreListEntry{
			{SessionID: sid, MTime: 100},
			{SessionID: sid, MTime: 200}, // newer
		},
	}
	out, err := ListSessionsFromStore(context.Background(), store, pk, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 {
		t.Fatalf("want deduped 1, got %d", len(out))
	}
	if out[0].LastModified != 200 {
		t.Fatalf("mtime tie-break wrong: %d", out[0].LastModified)
	}
}

// --- store-backed chain + subagent metadata recovery ---

type chainStore struct {
	memStore
	main map[SessionKey][]map[string]any
}

func (c chainStore) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	if v, ok := c.main[key]; ok {
		return v, nil
	}
	return c.memStore.Load(ctx, key)
}

func TestGetSessionMessagesFromStoreChainBuilding(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	pk := ProjectKeyForDirectory(".")
	key := SessionKey{ProjectKey: pk, SessionID: sid}
	store := chainStore{
		memStore: memStore{data: map[SessionKey][]map[string]any{}},
		main: map[SessionKey][]map[string]any{
			key: {
				{"type": "user", "uuid": "u1", "sessionId": sid, "message": map[string]any{"role": "user", "content": "hi"}},
				{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": sid, "message": map[string]any{"role": "assistant", "content": "hello"}},
				{"type": "user", "uuid": "u2", "parentUuid": "a1", "sessionId": sid, "message": map[string]any{"role": "user", "content": "how are you"}},
				// metadata-only noise should be filtered out
				{"type": "custom-title", "sessionId": sid, "customTitle": "Test"},
				// isMeta must be excluded from the chain output
				{"type": "user", "uuid": "m1", "isMeta": true, "message": map[string]any{"role": "user", "content": "skip"}},
			},
		},
	}
	out, err := GetSessionMessagesFromStore(context.Background(), store, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 (no metadata, no isMeta), got %d: %+v", len(out), out)
	}
	if out[0].UUID != "u1" || out[2].UUID != "u2" {
		t.Fatalf("chain order wrong: %+v", out)
	}
}

func TestGetSubagentMessagesFromStoreRecoversParentIDs(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	key := SessionKey{
		ProjectKey: ProjectKeyForDirectory("."),
		SessionID:  sid,
		Subpath:    "subagents/agent-abc",
	}
	store := chainStore{
		memStore: memStore{data: map[SessionKey][]map[string]any{}},
		main: map[SessionKey][]map[string]any{
			key: {
				{"type": "agent_metadata", "toolUseId": "tool-1", "parentAgentId": "parent-1"},
				{"type": "user", "uuid": "u1", "sessionId": sid, "message": map[string]any{"role": "user", "content": "do thing"}},
				{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": sid, "message": map[string]any{"role": "assistant", "content": "doing"}},
			},
		},
	}
	out, err := GetSubagentMessagesFromStore(context.Background(), store, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 (agent_metadata filtered), got %d: %+v", len(out), out)
	}
	for _, m := range out {
		if m.ParentToolUseID != "tool-1" || m.ParentAgentID != "parent-1" {
			t.Errorf("parent ids not stamped: %+v", m)
		}
	}
}

func TestGetSubagentMessagesFromStoreSnakeCaseFallback(t *testing.T) {
	// Older sidecars may use snake_case keys.
	sid := "550e8400-e29b-41d4-a716-446655440000"
	key := SessionKey{
		ProjectKey: ProjectKeyForDirectory("."),
		SessionID:  sid,
		Subpath:    "subagents/agent-abc",
	}
	store := chainStore{
		memStore: memStore{data: map[SessionKey][]map[string]any{}},
		main: map[SessionKey][]map[string]any{
			key: {
				{"type": "agent_metadata", "parent_tool_use_id": "tool-2", "parent_agent_id": "parent-2"},
				{"type": "user", "uuid": "u1", "sessionId": sid, "message": map[string]any{}},
			},
		},
	}
	out, err := GetSubagentMessagesFromStore(context.Background(), store, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].ParentToolUseID != "tool-2" || out[0].ParentAgentID != "parent-2" {
		t.Fatalf("snake_case fallback wrong: %+v", out)
	}
}

func TestListSubagentsFromStoreFiltersAgentPrefix(t *testing.T) {
	sid := "550e8400-e29b-41d4-a716-446655440000"
	key := SessionKey{ProjectKey: ProjectKeyForDirectory("."), SessionID: sid}
	store := &listSubkeyStore{keys: []string{
		"subagents/agent-abc",
		"subagents/agent-def",
		"subagents/workflows/run-1/agent-ghi", // nested
		"some/other/path",                     // not subagents/
		"subagents/not-an-agent.jsonl",        // doesn't start with agent-
	}}
	out, err := ListSubagentsFromStore(context.Background(), store, key)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("want 3 (abc, def, ghi), got %d: %v", len(out), out)
	}
	want := map[string]bool{"abc": true, "def": true, "ghi": true}
	for _, id := range out {
		if !want[id] {
			t.Errorf("unexpected id: %s", id)
		}
	}
}

type listSubkeyStore struct {
	keys []string
}

func (s *listSubkeyStore) Append(ctx context.Context, key SessionKey, entries []map[string]any) error {
	return nil
}

func (s *listSubkeyStore) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	return nil, nil
}

func (s *listSubkeyStore) ListSubkeys(ctx context.Context, key SessionKey) ([]string, error) {
	return s.keys, nil
}

// --- filesystem path: GetSessionMessages walks the chain ---

func TestGetSessionMessagesChainFromFile(t *testing.T) {
	root := t.TempDir()
	pk := ProjectKeyForDirectory(root)
	projectDir := filepath.Join(root, ".claude", "projects", pk)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "550e8400-e29b-41d4-a716-446655440000"
	p := filepath.Join(projectDir, sid+".jsonl")
	f, _ := os.Create(p)
	enc := json.NewEncoder(f)
	_ = enc.Encode(map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": sid})
	_ = enc.Encode(map[string]any{"type": "progress", "uuid": "p1", "parentUuid": "u1", "sessionId": sid})
	_ = enc.Encode(map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "p1", "sessionId": sid, "message": map[string]any{"role": "assistant", "content": "ok"}})
	_ = enc.Encode(map[string]any{"type": "custom-title", "sessionId": sid, "customTitle": "test"}) // must be filtered
	_ = f.Close()
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	out, err := GetSessionMessages(sid, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2 (u1 + a1; progress kept since it's in chain), got %d: %+v", len(out), out)
	}
	if out[0].UUID != "u1" || out[1].UUID != "a1" {
		t.Fatalf("order wrong: %+v", out)
	}
}

func TestGetSubagentMessagesChainFromFile(t *testing.T) {
	root := t.TempDir()
	pk := ProjectKeyForDirectory(root)
	projectDir := filepath.Join(root, ".claude", "projects", pk)
	sid := "550e8400-e29b-41d4-a716-446655440000"
	sessionDir := filepath.Join(projectDir, sid, "subagents")
	if err := os.MkdirAll(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	// The resolver requires the main session file to exist (its presence is
	// what locates the session directory), so create it alongside the
	// subagent transcript.
	main := filepath.Join(projectDir, sid+".jsonl")
	if err := os.WriteFile(main, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	subpath := "agent-abc"
	transcript := filepath.Join(sessionDir, subpath+".jsonl")
	f, _ := os.Create(transcript)
	enc := json.NewEncoder(f)
	_ = enc.Encode(map[string]any{"type": "user", "uuid": "u1", "parentUuid": nil, "sessionId": sid, "message": map[string]any{}})
	_ = enc.Encode(map[string]any{"type": "assistant", "uuid": "a1", "parentUuid": "u1", "sessionId": sid, "message": map[string]any{}})
	_ = f.Close()
	// Sidecar with snake_case keys.
	_ = os.WriteFile(filepath.Join(sessionDir, subpath+".meta.json"),
		[]byte(`{"parent_tool_use_id":"tool-1","parent_agent_id":"parent-1"}`), 0o600)
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	out, err := GetSubagentMessages(sid, subpath, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("want 2, got %d", len(out))
	}
	for _, m := range out {
		if m.ParentToolUseID != "tool-1" || m.ParentAgentID != "parent-1" {
			t.Errorf("parent ids missing: %+v", m)
		}
	}
}

func TestGetSessionMessagesPageRespectsLimit(t *testing.T) {
	root := t.TempDir()
	// resolveSessionFilePath looks up the file under
	// $HOME/.claude/projects/<ProjectKeyForDirectory(root)>, so the file
	// must live there, not under a literal name like "p".
	pk := ProjectKeyForDirectory(root)
	projectDir := filepath.Join(root, ".claude", "projects", pk)
	if err := os.MkdirAll(projectDir, 0o700); err != nil {
		t.Fatal(err)
	}
	sid := "550e8400-e29b-41d4-a716-446655440000"
	p := filepath.Join(projectDir, sid+".jsonl")
	f, _ := os.Create(p)
	enc := json.NewEncoder(f)
	for i := 0; i < 10; i++ {
		var parent any = nil
		if i > 0 {
			parent = "u" + string(rune('0'+i-1))
		}
		_ = enc.Encode(map[string]any{"type": "user", "uuid": "u" + string(rune('0'+i)), "parentUuid": parent, "sessionId": sid})
	}
	_ = f.Close()
	t.Setenv("HOME", root)
	t.Setenv("CLAUDE_CONFIG_DIR", "")

	all, err := GetSessionMessages(sid, root)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 10 {
		t.Fatalf("expected 10 from full read, got %d", len(all))
	}

	out, err := GetSessionMessagesPage(sid, root, 3, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("limit 3 wrong: got %d", len(out))
	}
	out, err = GetSessionMessagesPage(sid, root, 0, 8)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("offset 8 of 10 wrong: got %d", len(out))
	}
}

// skip in CI if git is unavailable
func TestGetWorktreePathsNonRepo(t *testing.T) {
	got := getWorktreePaths(t.TempDir())
	if len(got) != 0 {
		t.Fatalf("expected no worktrees outside a repo, got %v", got)
	}
	// Sanity: a directory that doesn't exist should also return empty.
	if got := getWorktreePaths("/this/path/should/not/exist/at/all"); len(got) != 0 {
		t.Fatalf("expected empty for missing path, got %v", got)
	}
}

func TestCanonicalizePath(t *testing.T) {
	tmp := t.TempDir()
	link := filepath.Join(tmp, "link")
	_ = os.Symlink(tmp, link)
	got := canonicalizePath(link)
	// On macOS / some Linux filesystems, realpath of a symlink resolves.
	// On others, it may not. We just need NFC + best-effort absolute.
	if got == "" || strings.Contains(got, "\x00") {
		t.Fatalf("canonicalize returned bogus path: %q", got)
	}
}
