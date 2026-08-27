package claudeagentsdk

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"
)

type (
	SessionKey            struct{ ProjectKey, SessionID, Subpath string }
	SessionStoreListEntry struct {
		SessionID string
		MTime     int64
	}
)

type SessionStore interface {
	Append(context.Context, SessionKey, []map[string]any) error
	Load(context.Context, SessionKey) ([]map[string]any, error)
}
type SessionLister interface {
	ListSessions(context.Context, string) ([]SessionStoreListEntry, error)
}
type SessionSubkeyLister interface {
	ListSubkeys(context.Context, SessionKey) ([]string, error)
}

// SessionDeleter is an optional capability: implement Delete to support
// cascade delete via delete_session_via_store. Mirrors the Python SDK's
// probe-by-hasattr pattern; Go uses a type assertion.
type SessionDeleter interface {
	Delete(context.Context, SessionKey) error
}

// SessionSummarizer is an optional capability: implement
// ListSessionSummaries to support fast list_sessions_from_store without
// per-session Load calls.
type SessionSummarizer interface {
	ListSessionSummaries(context.Context, string) ([]SessionStoreSummaryEntry, error)
}

type MaterializedResume struct{ ConfigDir, ResumeSessionID string }

func (m *MaterializedResume) Cleanup() error {
	if m == nil || m.ConfigDir == "" {
		return nil
	}
	return os.RemoveAll(m.ConfigDir)
}

func ValidateSessionStoreOptions(o Options) error {
	if o.SessionStore == nil {
		return nil
	}
	if o.ContinueConversation && o.Resume == "" {
		if _, ok := o.SessionStore.(SessionLister); !ok {
			return fmt.Errorf("continue_conversation with session_store requires ListSessions")
		}
	}
	if o.EnableFileCheckpointing {
		return fmt.Errorf("session_store cannot be combined with enable_file_checkpointing")
	}
	return nil
}

func MaterializeResumeSession(ctx context.Context, o Options) (*MaterializedResume, error) {
	if o.SessionStore == nil || (o.Resume == "" && !o.ContinueConversation) {
		return nil, nil
	}
	timeout := time.Duration(o.LoadTimeoutMS) * time.Millisecond
	if timeout == 0 {
		timeout = 60 * time.Second
	}
	projectKey := ProjectKeyForDirectory(o.CWD)
	var sid string
	var entries []map[string]any
	var err error
	if o.Resume != "" {
		if !validUUID(o.Resume) {
			return nil, nil
		}
		sid = o.Resume
		entries, err = loadWithTimeout(ctx, o.SessionStore, SessionKey{ProjectKey: projectKey, SessionID: sid}, timeout)
	} else {
		lister := o.SessionStore.(SessionLister)
		list, e := listWithTimeout(ctx, lister, projectKey, timeout)
		if e != nil {
			return nil, e
		}
		sort.Slice(list, func(i, j int) bool { return list[i].MTime > list[j].MTime })
		for _, cand := range list {
			if !validUUID(cand.SessionID) {
				continue
			}
			got, e := loadWithTimeout(ctx, o.SessionStore, SessionKey{ProjectKey: projectKey, SessionID: cand.SessionID}, timeout)
			if e != nil {
				return nil, e
			}
			if len(got) == 0 || boolVal(got[0]["isSidechain"]) {
				continue
			}
			sid, entries = cand.SessionID, got
			break
		}
	}
	if err != nil {
		return nil, err
	}
	if len(entries) == 0 {
		return nil, nil
	}
	dir, err := os.MkdirTemp("", "claude-resume-")
	if err != nil {
		return nil, err
	}
	m := &MaterializedResume{ConfigDir: dir, ResumeSessionID: sid}
	defer func() {
		if err != nil {
			_ = m.Cleanup()
		}
	}()
	projectDir := filepath.Join(dir, "projects", projectKey)
	if err = os.MkdirAll(projectDir, 0o700); err != nil {
		return nil, err
	}
	if err = writeJSONL(filepath.Join(projectDir, sid+".jsonl"), entries); err != nil {
		return nil, err
	}
	copyAuthFiles(dir, o.Env)
	if sub, ok := o.SessionStore.(SessionSubkeyLister); ok {
		err = materializeSubkeys(ctx, o.SessionStore, sub, dir, projectDir, projectKey, sid, timeout)
	}
	return m, err
}

func loadWithTimeout(ctx context.Context, s SessionStore, key SessionKey, d time.Duration) ([]map[string]any, error) {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	e, err := s.Load(c, key)
	if err != nil {
		return nil, fmt.Errorf("SessionStore.Load failed during resume materialization: %w", err)
	}
	return e, nil
}

func listWithTimeout(ctx context.Context, s SessionLister, projectKey string, d time.Duration) ([]SessionStoreListEntry, error) {
	c, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	e, err := s.ListSessions(c, projectKey)
	if err != nil {
		return nil, fmt.Errorf("SessionStore.ListSessions failed during resume materialization: %w", err)
	}
	return e, nil
}

func writeJSONL(path string, entries []map[string]any) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, e := range entries {
		if err := enc.Encode(e); err != nil {
			return err
		}
	}
	return nil
}

func materializeSubkeys(ctx context.Context, store SessionStore, lister SessionSubkeyLister, tmpBase, projectDir, projectKey, sid string, timeout time.Duration) error {
	c, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	subs, err := lister.ListSubkeys(c, SessionKey{ProjectKey: projectKey, SessionID: sid})
	if err != nil {
		return fmt.Errorf("SessionStore.ListSubkeys failed during resume materialization: %w", err)
	}
	sessionDir := filepath.Join(projectDir, sid)
	for _, sub := range subs {
		if !safeSubpath(sub) {
			continue
		}
		entries, err := loadWithTimeout(ctx, store, SessionKey{ProjectKey: projectKey, SessionID: sid, Subpath: sub}, timeout)
		if err != nil {
			return err
		}
		if len(entries) == 0 {
			continue
		}
		meta, transcript := splitAgentMetadata(entries)
		if len(transcript) > 0 {
			target := filepath.Join(sessionDir, sub) + ".jsonl"
			if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
				return err
			}
			if err := writeJSONL(target, transcript); err != nil {
				return err
			}
		}
		if meta != nil {
			delete(meta, "type")
			target := strings.TrimSuffix(filepath.Join(sessionDir, sub)+".jsonl", ".jsonl") + ".meta.json"
			b, _ := json.Marshal(meta)
			if err := os.WriteFile(target, b, 0o600); err != nil {
				return err
			}
		}
	}
	return nil
}

func splitAgentMetadata(entries []map[string]any) (map[string]any, []map[string]any) {
	var meta map[string]any
	out := []map[string]any{}
	for _, e := range entries {
		if e["type"] == "agent_metadata" {
			meta = e
		} else {
			out = append(out, e)
		}
	}
	return meta, out
}

func safeSubpath(p string) bool {
	if p == "" || filepath.IsAbs(p) || strings.Contains(p, "\x00") {
		return false
	}
	for _, part := range regexp.MustCompile(`[\\/]`).Split(p, -1) {
		if part == "." || part == ".." || part == "" {
			return false
		}
	}
	return true
}

func configProjectsDir(env map[string]string) string {
	if env != nil && env["CLAUDE_CONFIG_DIR"] != "" {
		return filepath.Join(env["CLAUDE_CONFIG_DIR"], "projects")
	}
	if v := os.Getenv("CLAUDE_CONFIG_DIR"); v != "" {
		return filepath.Join(v, "projects")
	}
	return filepath.Join(os.Getenv("HOME"), ".claude", "projects")
}

func ProjectKeyForDirectory(dir string) string {
	if dir == "" {
		dir = "."
	}
	abs, err := filepath.Abs(dir)
	if err == nil {
		dir = abs
	}
	real, err := filepath.EvalSymlinks(dir)
	if err == nil {
		dir = real
	}
	return sanitizePath(dir)
}

func sanitizePath(s string) string {
	re := regexp.MustCompile(`[^a-zA-Z0-9]`)
	out := re.ReplaceAllString(s, "-")
	if len(out) <= 200 {
		return out
	}
	return out[:200] + "-" + simpleHash(s)
}

func simpleHash(s string) string {
	var h int32
	for _, r := range s {
		h = h*31 + int32(r)
	}
	if h < 0 {
		h = -h
	}
	const chars = "0123456789abcdefghijklmnopqrstuvwxyz"
	if h == 0 {
		return "0"
	}
	var b []byte
	for h > 0 {
		b = append([]byte{chars[h%36]}, b...)
		h /= 36
	}
	return string(b)
}

func validUUID(s string) bool {
	return regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`).MatchString(s)
}

func copyAuthFiles(dst string, env map[string]string) {
	src := os.Getenv("CLAUDE_CONFIG_DIR")
	if env != nil && env["CLAUDE_CONFIG_DIR"] != "" {
		src = env["CLAUDE_CONFIG_DIR"]
	}
	if src == "" {
		src = filepath.Join(os.Getenv("HOME"), ".claude")
	}
	copyFile(filepath.Join(src, ".credentials.json"), filepath.Join(dst, ".credentials.json"), redactRefreshToken)
	copyFile(filepath.Join(src, "settings.json"), filepath.Join(dst, "settings.json"), stripResumeSettings)
	copyFile(filepath.Join(src, "cowork_settings.json"), filepath.Join(dst, "cowork_settings.json"), stripResumeSettings)
	claudeJSON := filepath.Join(os.Getenv("HOME"), ".claude.json")
	if env != nil && env["CLAUDE_CONFIG_DIR"] != "" {
		claudeJSON = filepath.Join(env["CLAUDE_CONFIG_DIR"], ".claude.json")
	}
	copyFile(claudeJSON, filepath.Join(dst, ".claude.json"), nil)
}

func copyFile(src, dst string, xf func([]byte) []byte) {
	b, err := os.ReadFile(src)
	if err != nil {
		return
	}
	if xf != nil {
		b = xf(b)
	}
	_ = os.WriteFile(dst, b, 0o600)
}

func redactRefreshToken(b []byte) []byte {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b
	}
	if oauth := rawMap(m["claudeAiOauth"]); oauth != nil {
		delete(oauth, "refreshToken")
	}
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}

func stripResumeSettings(b []byte) []byte {
	var m map[string]any
	if json.Unmarshal(b, &m) != nil {
		return b
	}
	delete(m, "enabledPlugins")
	delete(m, "extraKnownMarketplaces")
	if env := rawMap(m["env"]); env != nil {
		delete(env, "CLAUDE_CONFIG_DIR")
	}
	out, err := json.Marshal(m)
	if err != nil {
		return b
	}
	return out
}

type TranscriptMirrorBatcher struct {
	Store             SessionStore
	ProjectsDir       string
	OnError           func(SessionKey, string)
	MaxPendingEntries int
	MaxPendingBytes   int

	// Retry tuning. MirrorFailedAttempts is the total attempts per batch
	// (default 3: one try + two retries). MirrorRetryBackoff is the
	// initial sleep, doubled each attempt. Timeouts are not retried — a
	// pending call may still land — so retry classifies the error and
	// bails on context.DeadlineExceeded / net.Error.Timeout.
	MirrorFailedAttempts int
	MirrorRetryBackoff   time.Duration

	mu      sync.Mutex
	pending []mirrorEntry
	flushMu sync.Mutex // serializes flushes so per-session order is preserved
}
type mirrorEntry struct {
	path    string
	entries []map[string]any
}

func NewTranscriptMirrorBatcher(store SessionStore, projectsDir string, onError func(SessionKey, string), flushMode string) *TranscriptMirrorBatcher {
	b := &TranscriptMirrorBatcher{Store: store, ProjectsDir: projectsDir, OnError: onError, MaxPendingEntries: 500, MaxPendingBytes: 1 << 20, MirrorFailedAttempts: 3, MirrorRetryBackoff: 100 * time.Millisecond}
	if flushMode == "eager" {
		b.MaxPendingEntries = 0
		b.MaxPendingBytes = 0
	}
	return b
}

func (b *TranscriptMirrorBatcher) Enqueue(filePath string, entries []map[string]any) {
	b.mu.Lock()
	b.pending = append(b.pending, mirrorEntry{filePath, entries})
	should := len(b.pending) > b.MaxPendingEntries
	b.mu.Unlock()
	if should {
		go b.Flush(context.Background())
	}
}

func (b *TranscriptMirrorBatcher) Flush(ctx context.Context) error {
	// Drain under the entry mutex so concurrent enqueues don't race;
	// serialize the actual store calls under flushMu so two flushes can't
	// reorder entries for the same session.
	b.mu.Lock()
	items := b.pending
	b.pending = nil
	b.mu.Unlock()

	if len(items) == 0 {
		return nil
	}

	b.flushMu.Lock()
	defer b.flushMu.Unlock()

	by := map[string][]map[string]any{}
	for _, it := range items {
		by[it.path] = append(by[it.path], it.entries...)
	}
	for p, es := range by {
		key, ok := FilePathToSessionKey(p, b.ProjectsDir)
		if !ok {
			continue
		}
		if err := b.appendWithRetry(ctx, key, es); err != nil && b.OnError != nil {
			b.OnError(key, err.Error())
		}
	}
	return nil
}

// appendWithRetry calls Store.Append with up to MirrorFailedAttempts
// total tries. A context error (deadline / cancel) is not retried — the
// in-flight call may still land — so it short-circuits on the first hit.
// Other errors retry with exponential backoff starting at MirrorRetryBackoff.
func (b *TranscriptMirrorBatcher) appendWithRetry(ctx context.Context, key SessionKey, entries []map[string]any) error {
	attempts := b.MirrorFailedAttempts
	if attempts < 1 {
		attempts = 1
	}
	backoff := b.MirrorRetryBackoff
	if backoff == 0 {
		backoff = 100 * time.Millisecond
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		err := b.Store.Append(ctx, key, entries)
		if err == nil {
			return nil
		}
		if isTimeout(err) || ctx.Err() != nil {
			return err
		}
		lastErr = err
		if i == attempts-1 {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
		backoff *= 2
	}
	return fmt.Errorf("SessionStore.Append failed after %d attempts: %w", attempts, lastErr)
}

// isTimeout reports whether err is a deadline / cancellation / network
// timeout error that should not be retried.
func isTimeout(err error) bool {
	if err == nil {
		return false
	}
	if err == context.DeadlineExceeded || err == context.Canceled {
		return true
	}
	type timeouter interface{ Timeout() bool }
	if t, ok := err.(timeouter); ok && t.Timeout() {
		return true
	}
	return false
}

func FilePathToSessionKey(filePath, projectsDir string) (SessionKey, bool) {
	rel, err := filepath.Rel(projectsDir, filePath)
	if err != nil || strings.HasPrefix(rel, "..") {
		return SessionKey{}, false
	}
	rel = strings.TrimSuffix(filepath.ToSlash(rel), ".jsonl")
	parts := strings.Split(rel, "/")
	if len(parts) < 2 {
		return SessionKey{}, false
	}
	key := SessionKey{ProjectKey: parts[0], SessionID: parts[1]}
	if len(parts) > 2 {
		key.Subpath = strings.Join(parts[2:], "/")
	}
	return key, true
}

func entriesFromAny(v any) []map[string]any {
	out := []map[string]any{}
	for _, e := range rawSlice(v) {
		if m := rawMap(e); m != nil {
			out = append(out, m)
		}
	}
	return out
}
