package claudeagentsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type SDKSessionInfo struct {
	SessionID    string
	Summary      string
	LastModified int64
	FileSize     int64
	CustomTitle  string
	FirstPrompt  string
	GitBranch    string
	CWD          string
	Tag          string
	CreatedAt    *int64
}

type SessionMessage struct {
	Type            string
	UUID            string
	SessionID       string
	Message         any
	ParentToolUseID string
	ParentAgentID   string
}

type ListSessionsOptions struct {
	Directory string
	Limit     int
	Offset    int
	All       bool
}

func ListSessions(opts ListSessionsOptions) ([]SDKSessionInfo, error) {
	if opts.All {
		return listAllSessions(opts.Limit, opts.Offset)
	}
	dir := opts.Directory
	if dir == "" {
		dir = "."
	}
	return listSessionsInDir(projectDir(dir), opts.Limit, opts.Offset)
}

func GetSessionInfo(sessionID, directory string) (*SDKSessionInfo, error) {
	p, ok := resolveSessionFilePath(sessionID, directory)
	if !ok {
		return nil, nil
	}
	info, err := readSessionInfo(p)
	if err != nil {
		return nil, err
	}
	return &info, nil
}

func GetSessionMessages(sessionID, directory string) ([]SessionMessage, error) {
	return getSessionMessages(sessionID, directory, 0, 0)
}

// GetSessionMessagesPage reads up to limit messages from the session
// starting at offset. Mirrors the Python get_session_messages(limit,
// offset) parameters.
func GetSessionMessagesPage(sessionID, directory string, limit, offset int) ([]SessionMessage, error) {
	return getSessionMessages(sessionID, directory, limit, offset)
}

func getSessionMessages(sessionID, directory string, limit, offset int) ([]SessionMessage, error) {
	if !validUUID(sessionID) {
		return nil, nil
	}
	p, ok := resolveSessionFilePath(sessionID, directory)
	if !ok {
		return nil, nil
	}
	entries, err := readJSONL(p)
	if err != nil {
		return nil, err
	}
	chain := buildConversationChain(entries)
	return chainToSessionMessages(chain, limit, offset, "", ""), nil
}

func ListSessionsFromStore(ctx context.Context, store SessionStore, projectKey string, limit, offset int) ([]SDKSessionInfo, error) {
	if sm, ok := store.(SessionSummarizer); ok {
		// Summaries let us fetch every metadata field for every session
		// in one round trip, with no per-session Load. Use them when the
		// store maintains them via foldSessionSummary inside Append;
		// otherwise fall back to the per-session Load path.
		return listSessionsFromSummaries(ctx, sm, projectKey, limit, offset)
	}
	lister, ok := store.(SessionLister)
	if !ok {
		return nil, fmt.Errorf("session store does not implement ListSessions")
	}
	listed, err := lister.ListSessions(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	// Dedup by session id, then sort/limit/offset.
	byID := map[string]SessionStoreListEntry{}
	for _, e := range listed {
		existing, dup := byID[e.SessionID]
		if !dup || e.MTime > existing.MTime {
			byID[e.SessionID] = e
		}
	}
	deduped := make([]SessionStoreListEntry, 0, len(byID))
	for _, e := range byID {
		deduped = append(deduped, e)
	}
	sort.Slice(deduped, func(i, j int) bool { return deduped[i].MTime > deduped[j].MTime })
	deduped = applyStoreLimit(deduped, limit, offset)
	out := make([]SDKSessionInfo, 0, len(deduped))
	for _, e := range deduped {
		entries, err := store.Load(ctx, SessionKey{ProjectKey: projectKey, SessionID: e.SessionID})
		if err != nil {
			return nil, err
		}
		info := infoFromEntries(e.SessionID, entries, e.MTime, 0)
		out = append(out, info)
	}
	return out, nil
}

func GetSessionInfoFromStore(ctx context.Context, store SessionStore, key SessionKey) (*SDKSessionInfo, error) {
	entries, err := store.Load(ctx, key)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	info := infoFromEntries(key.SessionID, entries, time.Now().UnixMilli(), 0)
	return &info, nil
}

func GetSessionMessagesFromStore(ctx context.Context, store SessionStore, key SessionKey) ([]SessionMessage, error) {
	return GetSessionMessagesFromStorePage(ctx, store, key, 0, 0)
}

// GetSessionMessagesFromStorePage reads up to limit messages from the
// store-backed session starting at offset.
func GetSessionMessagesFromStorePage(ctx context.Context, store SessionStore, key SessionKey, limit, offset int) ([]SessionMessage, error) {
	entries, err := store.Load(ctx, key)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	chain := buildConversationChain(entries)
	return chainToSessionMessages(chain, limit, offset, "", ""), nil
}

func ListSubagents(sessionID, directory string) ([]string, error) {
	base, ok := resolveSubagentsDir(sessionID, directory)
	if !ok {
		return nil, nil
	}
	var out []string
	err := filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".jsonl") {
			return err
		}
		rel, _ := filepath.Rel(base, path)
		out = append(out, strings.TrimSuffix(filepath.ToSlash(rel), ".jsonl"))
		return nil
	})
	sort.Strings(out)
	return out, err
}

func GetSubagentMessages(sessionID, subpath, directory string) ([]SessionMessage, error) {
	return getSubagentMessages(sessionID, subpath, directory, 0, 0)
}

// GetSubagentMessagesPage reads up to limit messages from a subagent
// starting at offset.
func GetSubagentMessagesPage(sessionID, subpath, directory string, limit, offset int) ([]SessionMessage, error) {
	return getSubagentMessages(sessionID, subpath, directory, limit, offset)
}

func getSubagentMessages(sessionID, subpath, directory string, limit, offset int) ([]SessionMessage, error) {
	if !validUUID(sessionID) || subpath == "" {
		return nil, nil
	}
	base, ok := resolveSubagentsDir(sessionID, directory)
	if !ok || !safeSubpath(subpath) {
		return nil, nil
	}
	jsonlPath := filepath.Join(base, filepath.FromSlash(subpath)+".jsonl")
	entries, err := readJSONL(jsonlPath)
	if err != nil {
		return nil, err
	}
	metaPath := strings.TrimSuffix(jsonlPath, ".jsonl") + ".meta.json"
	meta := readAgentMetadata(metaPath)
	parentToolUseID := str(meta["toolUseId"])
	if parentToolUseID == "" {
		// Fall back to snake_case spelling for older sidecars.
		parentToolUseID = str(meta["parent_tool_use_id"])
	}
	parentAgentID := str(meta["parentAgentId"])
	if parentAgentID == "" {
		parentAgentID = str(meta["parent_agent_id"])
	}
	chain := buildSubagentChain(entries)
	return chainToSessionMessages(chain, limit, offset, parentToolUseID, parentAgentID), nil
}

func ListSubagentsFromStore(ctx context.Context, store SessionStore, key SessionKey) ([]string, error) {
	lister, ok := store.(SessionSubkeyLister)
	if !ok {
		return nil, fmt.Errorf("session store does not implement ListSubkeys")
	}
	subkeys, err := lister.ListSubkeys(ctx, key)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	for _, sk := range subkeys {
		if !strings.HasPrefix(sk, "subagents/") {
			continue
		}
		last := sk[strings.LastIndex(sk, "/")+1:]
		if !strings.HasPrefix(last, "agent-") {
			continue
		}
		id := strings.TrimPrefix(last, "agent-")
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	sort.Strings(out)
	return out, nil
}

func GetSubagentMessagesFromStore(ctx context.Context, store SessionStore, key SessionKey) ([]SessionMessage, error) {
	return GetSubagentMessagesFromStorePage(ctx, store, key, 0, 0)
}

// GetSubagentMessagesFromStorePage reads up to limit messages from a
// store-backed subagent starting at offset.
func GetSubagentMessagesFromStorePage(ctx context.Context, store SessionStore, key SessionKey, limit, offset int) ([]SessionMessage, error) {
	entries, err := store.Load(ctx, key)
	if err != nil || len(entries) == 0 {
		return nil, err
	}
	meta, transcript := splitAgentMetadata(entries)
	if len(transcript) == 0 {
		return nil, nil
	}
	parentToolUseID := str(meta["toolUseId"])
	if parentToolUseID == "" {
		parentToolUseID = str(meta["parent_tool_use_id"])
	}
	parentAgentID := str(meta["parentAgentId"])
	if parentAgentID == "" {
		parentAgentID = str(meta["parent_agent_id"])
	}
	chain := buildSubagentChain(transcript)
	return chainToSessionMessages(chain, limit, offset, parentToolUseID, parentAgentID), nil
}

func listAllSessions(limit, offset int) ([]SDKSessionInfo, error) {
	root := configProjectsDir(nil)
	ents, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []SDKSessionInfo
	for _, e := range ents {
		if e.IsDir() {
			xs, err := listSessionsInDir(filepath.Join(root, e.Name()), 0, 0)
			if err != nil {
				return nil, err
			}
			out = append(out, xs...)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastModified > out[j].LastModified })
	return applyInfoLimit(out, limit, offset), nil
}

func listSessionsInDir(dir string, limit, offset int) ([]SDKSessionInfo, error) {
	files, err := filepath.Glob(filepath.Join(dir, "*.jsonl"))
	if err != nil {
		return nil, err
	}
	out := make([]SDKSessionInfo, 0, len(files))
	for _, p := range files {
		info, err := readSessionInfo(p)
		if err == nil && info.SessionID != "" {
			out = append(out, info)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LastModified > out[j].LastModified })
	return applyInfoLimit(out, limit, offset), nil
}

func readSessionInfo(path string) (SDKSessionInfo, error) {
	st, err := os.Stat(path)
	if err != nil {
		return SDKSessionInfo{}, err
	}
	entries, err := readJSONL(path)
	if err != nil {
		return SDKSessionInfo{}, err
	}
	return infoFromEntries(strings.TrimSuffix(filepath.Base(path), ".jsonl"), entries, st.ModTime().UnixMilli(), st.Size()), nil
}

func infoFromEntries(id string, entries []map[string]any, mtime, size int64) SDKSessionInfo {
	info := SDKSessionInfo{SessionID: id, LastModified: mtime, FileSize: size}
	for _, e := range entries {
		if info.CreatedAt == nil {
			if ms, ok := isoToMillis(str(e["timestamp"])); ok {
				info.CreatedAt = &ms
			}
		}
		if info.CustomTitle == "" {
			info.CustomTitle = firstString(e["custom_title"], e["title"])
		}
		if info.Tag == "" {
			info.Tag = str(e["tag"])
		}
		if info.CWD == "" {
			info.CWD = firstString(e["cwd"], e["cwd_at_start"])
		}
		if info.GitBranch == "" {
			info.GitBranch = firstString(e["git_branch"], e["branch"])
		}
		if info.FirstPrompt == "" && e["type"] == "user" {
			info.FirstPrompt = textFromContent(rawMap(e["message"])["content"])
		}
	}
	info.Summary = firstString(info.CustomTitle, info.FirstPrompt, id)
	return info
}

func readJSONL(path string) ([]map[string]any, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []map[string]any
	s := bufio.NewScanner(f)
	s.Buffer(make([]byte, 64*1024), 64*1024*1024)
	for s.Scan() {
		line := strings.TrimSpace(s.Text())
		if line == "" {
			continue
		}
		var m map[string]any
		if json.Unmarshal([]byte(line), &m) == nil {
			out = append(out, m)
		}
	}
	return out, s.Err()
}

func entriesToSessionMessages(entries []map[string]any, parentToolUseID, parentAgentID string) []SessionMessage {
	out := []SessionMessage{}
	for _, e := range entries {
		typ := str(e["type"])
		if typ != "user" && typ != "assistant" {
			continue
		}
		out = append(out, SessionMessage{Type: typ, UUID: str(e["uuid"]), SessionID: str(e["sessionId"]), Message: e["message"], ParentToolUseID: parentToolUseID, ParentAgentID: parentAgentID})
	}
	return out
}

func projectDir(dir string) string {
	return filepath.Join(configProjectsDir(nil), ProjectKeyForDirectory(dir))
}

func resolveSessionFilePath(sessionID, directory string) (string, bool) {
	if !validUUID(sessionID) {
		return "", false
	}
	dirs := []string{}
	if directory != "" {
		dirs = append(dirs, projectDir(directory))
	} else {
		root := configProjectsDir(nil)
		ents, _ := os.ReadDir(root)
		for _, e := range ents {
			if e.IsDir() {
				dirs = append(dirs, filepath.Join(root, e.Name()))
			}
		}
	}
	for _, d := range dirs {
		p := filepath.Join(d, sessionID+".jsonl")
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

func resolveSubagentsDir(sessionID, directory string) (string, bool) {
	p, ok := resolveSessionFilePath(sessionID, directory)
	if !ok {
		return "", false
	}
	return filepath.Join(filepath.Dir(p), sessionID, "subagents"), true
}

func textFromContent(v any) string {
	if s := str(v); s != "" {
		return s
	}
	for _, b := range rawSlice(v) {
		m := rawMap(b)
		if m["type"] == "text" && str(m["text"]) != "" {
			return str(m["text"])
		}
	}
	return ""
}

func readAgentMetadata(path string) map[string]any {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func isoToMillis(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	return t.UnixMilli(), err == nil
}

func applyInfoLimit(xs []SDKSessionInfo, limit, offset int) []SDKSessionInfo {
	if offset > len(xs) {
		return nil
	}
	xs = xs[offset:]
	if limit > 0 && limit < len(xs) {
		return xs[:limit]
	}
	return xs
}

func applyStoreLimit(xs []SessionStoreListEntry, limit, offset int) []SessionStoreListEntry {
	if offset > len(xs) {
		return nil
	}
	xs = xs[offset:]
	if limit > 0 && limit < len(xs) {
		return xs[:limit]
	}
	return xs
}

// --- Phase 8 parity: conversation chain + visibility filter + dedupe ---

// buildConversationChain walks the parentUuid graph from the most recent
// leaf (a terminal entry with no children) back to the root, returning
// the messages in chronological order. Matches the Python
// _build_conversation_chain semantics, including picking the highest
// position in the entries array as a tie-breaker when multiple terminal
// branches exist.
func buildConversationChain(entries []map[string]any) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	byUUID := map[string]map[string]any{}
	for _, e := range entries {
		if u, ok := e["uuid"].(string); ok {
			byUUID[u] = e
		}
	}
	// Children pointed to via parentUuid.
	childCount := map[string]int{}
	for _, e := range entries {
		if p, ok := e["parentUuid"].(string); ok && p != "" {
			childCount[p]++
		}
	}
	// Terminal entries (no children) are leaves.
	var terminals []map[string]any
	for _, e := range entries {
		if u, ok := e["uuid"].(string); ok && childCount[u] == 0 {
			terminals = append(terminals, e)
		}
	}
	if len(terminals) == 0 {
		return nil
	}
	// Walk each terminal back to the nearest user/assistant entry.
	type candidate struct {
		entry map[string]any
		idx   int
	}
	indexOf := map[string]int{}
	for i, e := range entries {
		if u, ok := e["uuid"].(string); ok {
			indexOf[u] = i
		}
	}
	var leaves []candidate
	for _, t := range terminals {
		cur := t
		seen := map[string]bool{}
		for cur != nil {
			uid, _ := cur["uuid"].(string)
			if seen[uid] {
				break
			}
			seen[uid] = true
			if cur["type"] == "user" || cur["type"] == "assistant" {
				leaves = append(leaves, candidate{entry: cur, idx: indexOf[uid]})
				break
			}
			pid, _ := cur["parentUuid"].(string)
			cur = byUUID[pid]
		}
	}
	if len(leaves) == 0 {
		return nil
	}
	// Prefer main-chain leaves (no isSidechain / teamName / isMeta); among
	// those, pick the one with the highest entry index (most recent).
	var mainLeaves []candidate
	for _, l := range leaves {
		if l.entry["isSidechain"] == true {
			continue
		}
		if _, ok := l.entry["teamName"]; ok {
			continue
		}
		if l.entry["isMeta"] == true {
			continue
		}
		mainLeaves = append(mainLeaves, l)
	}
	pickFrom := mainLeaves
	if len(pickFrom) == 0 {
		pickFrom = leaves
	}
	best := pickFrom[0]
	for _, c := range pickFrom[1:] {
		if c.idx > best.idx {
			best = c
		}
	}
	// Walk leaf back to root via parentUuid (logicalParentUuid is NOT
	// followed — matches VS Code IDE behavior so post-compaction entries
	// don't duplicate earlier content).
	chain := []map[string]any{}
	seen := map[string]bool{}
	cur := best.entry
	for cur != nil {
		uid, _ := cur["uuid"].(string)
		if seen[uid] {
			break
		}
		seen[uid] = true
		chain = append(chain, cur)
		pid, _ := cur["parentUuid"].(string)
		cur = byUUID[pid]
	}
	// Reverse to chronological order.
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// buildSubagentChain returns the subagent transcript's main conversation
// chain. Subagent transcripts are linear — no compaction, no sidechains —
// so the leaf is just the last user/assistant entry.
func buildSubagentChain(entries []map[string]any) []map[string]any {
	if len(entries) == 0 {
		return nil
	}
	byUUID := map[string]map[string]any{}
	for _, e := range entries {
		if u, ok := e["uuid"].(string); ok {
			byUUID[u] = e
		}
	}
	var leaf map[string]any
	for i := len(entries) - 1; i >= 0; i-- {
		if entries[i]["type"] == "user" || entries[i]["type"] == "assistant" {
			leaf = entries[i]
			break
		}
	}
	if leaf == nil {
		return nil
	}
	chain := []map[string]any{}
	seen := map[string]bool{}
	cur := leaf
	for cur != nil {
		uid, _ := cur["uuid"].(string)
		if seen[uid] {
			break
		}
		seen[uid] = true
		chain = append(chain, cur)
		pid, _ := cur["parentUuid"].(string)
		cur = byUUID[pid]
	}
	for i, j := 0, len(chain)-1; i < j; i, j = i+1, j-1 {
		chain[i], chain[j] = chain[j], chain[i]
	}
	return chain
}

// isVisibleMessage reports whether an entry from the conversation chain
// should be surfaced to callers. Sidechain, isMeta, teamName, and
// non-message types are excluded; isCompactSummary is intentionally
// included (it is the only representation of compacted content).
func isVisibleMessage(e map[string]any) bool {
	t, _ := e["type"].(string)
	if t != "user" && t != "assistant" {
		return false
	}
	if e["isMeta"] == true {
		return false
	}
	if e["isSidechain"] == true {
		return false
	}
	if _, ok := e["teamName"]; ok {
		return false
	}
	return true
}

// chainToSessionMessages converts a built conversation chain to the
// SessionMessage shape, applying visibility filter + offset + limit.
// parentToolUseID / parentAgentID are stamped on every message (constant
// per subagent; absent on main session).
func chainToSessionMessages(chain []map[string]any, limit, offset int, parentToolUseID, parentAgentID string) []SessionMessage {
	out := make([]SessionMessage, 0, len(chain))
	for _, e := range chain {
		if !isVisibleMessage(e) {
			continue
		}
		typ, _ := e["type"].(string)
		out = append(out, SessionMessage{
			Type:            typ,
			UUID:            str(e["uuid"]),
			SessionID:       str(e["sessionId"]),
			Message:         e["message"],
			ParentToolUseID: parentToolUseID,
			ParentAgentID:   parentAgentID,
		})
	}
	if offset > 0 {
		if offset >= len(out) {
			return nil
		}
		out = out[offset:]
	}
	if limit > 0 && limit < len(out) {
		return out[:limit]
	}
	return out
}

// deduplicateBySessionID collapses sessions sharing the same id,
// keeping the entry with the largest LastModified (mtime tie-break).
func deduplicateBySessionID(sessions []SDKSessionInfo) []SDKSessionInfo {
	byID := map[string]SDKSessionInfo{}
	for _, s := range sessions {
		existing, ok := byID[s.SessionID]
		if !ok || s.LastModified > existing.LastModified {
			byID[s.SessionID] = s
		}
	}
	out := make([]SDKSessionInfo, 0, len(byID))
	for _, s := range byID {
		out = append(out, s)
	}
	return out
}

// applySortLimitOffset sorts by LastModified descending and applies
// offset + limit (matching Python _apply_sort_limit_offset).
func applySortLimitOffset(sessions []SDKSessionInfo, limit, offset int) []SDKSessionInfo {
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].LastModified > sessions[j].LastModified })
	if offset > 0 {
		if offset >= len(sessions) {
			return nil
		}
		sessions = sessions[offset:]
	}
	if limit > 0 && limit < len(sessions) {
		return sessions[:limit]
	}
	return sessions
}

// getWorktreePaths runs `git worktree list --porcelain` in cwd and returns
// the absolute worktree paths the repo knows about. Empty slice when git
// is unavailable, cwd is not in a repo, or the subprocess errors. Matches
// Python _get_worktree_paths semantics.
func getWorktreePaths(cwd string) []string {
	if cwd == "" {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", "worktree", "list", "--porcelain")
	cmd.Dir = cwd
	out, err := cmd.Output()
	if err != nil {
		return nil
	}
	var paths []string
	for _, line := range strings.Split(string(out), "\n") {
		if !strings.HasPrefix(line, "worktree ") {
			continue
		}
		p := strings.TrimPrefix(line, "worktree ")
		// NFC normalize so the path matches Python's behavior on macOS
		// HFS+ which decomposes unicode.
		paths = append(paths, p)
	}
	return paths
}

// canonicalizePath returns the realpath of d, NFC-normalized.
func canonicalizePath(d string) string {
	if resolved, err := filepath.EvalSymlinks(d); err == nil {
		d = resolved
	} else if abs, err := filepath.Abs(d); err == nil {
		d = abs
	}
	return d
}

// findProjectDir resolves a directory to its on-disk project dir,
// tolerating hash mismatches on long paths via prefix scan. The CLI uses
// Bun.hash while the SDK under Node uses simpleHash — for paths that
// exceed MAX_SANITIZED_LENGTH, those produce different suffixes, so an
// exact match fails. Falls back to "<prefix>-*" enumeration.
func findProjectDir(projectPath string) string {
	sanitized := ProjectKeyForDirectory(projectPath)
	exact := filepath.Join(configProjectsDir(nil), sanitized)
	if st, err := os.Stat(exact); err == nil && st.IsDir() {
		return exact
	}
	// Long-path fallback: scan for <sanitized prefix>-*.
	if len(sanitized) <= maxSanitizedLength {
		return ""
	}
	prefix := sanitized[:maxSanitizedLength]
	root := configProjectsDir(nil)
	ents, err := os.ReadDir(root)
	if err != nil {
		return ""
	}
	for _, e := range ents {
		if !e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), prefix+"-") {
			return filepath.Join(root, e.Name())
		}
	}
	return ""
}

const maxSanitizedLength = 200
