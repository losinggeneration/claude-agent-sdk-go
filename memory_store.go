package claudeagentsdk

import (
	"context"
	"sync"
	"time"
)

type InMemorySessionStore struct {
	mu   sync.Mutex
	data map[SessionKey][]map[string]any
}

func NewInMemorySessionStore() *InMemorySessionStore {
	return &InMemorySessionStore{data: map[SessionKey][]map[string]any{}}
}

func (s *InMemorySessionStore) Append(ctx context.Context, key SessionKey, entries []map[string]any) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.data == nil {
		s.data = map[SessionKey][]map[string]any{}
	}
	for _, e := range entries {
		s.data[key] = append(s.data[key], jsonCloneMap(e))
	}
	return nil
}

func (s *InMemorySessionStore) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	entries, ok := s.data[key]
	if !ok {
		return nil, nil
	}
	out := make([]map[string]any, 0, len(entries))
	for _, e := range entries {
		out = append(out, jsonCloneMap(e))
	}
	return out, nil
}

func (s *InMemorySessionStore) ListSessions(ctx context.Context, projectKey string) ([]SessionStoreListEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	seen := map[string]int64{}
	for key, entries := range s.data {
		if key.ProjectKey != projectKey || key.Subpath != "" {
			continue
		}
		mtime := time.Now().UnixMilli()
		for i := len(entries) - 1; i >= 0; i-- {
			if ms, ok := isoToMillis(str(entries[i]["timestamp"])); ok {
				mtime = ms
				break
			}
		}
		seen[key.SessionID] = mtime
	}
	out := make([]SessionStoreListEntry, 0, len(seen))
	for sid, mt := range seen {
		out = append(out, SessionStoreListEntry{SessionID: sid, MTime: mt})
	}
	return out, nil
}

func (s *InMemorySessionStore) Delete(ctx context.Context, key SessionKey) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for k := range s.data {
		if k.ProjectKey == key.ProjectKey && k.SessionID == key.SessionID && (key.Subpath == "" || k.Subpath == key.Subpath) {
			delete(s.data, k)
		}
	}
	return nil
}

func (s *InMemorySessionStore) ListSubkeys(ctx context.Context, key SessionKey) ([]string, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for k := range s.data {
		if k.ProjectKey == key.ProjectKey && k.SessionID == key.SessionID && k.Subpath != "" {
			out = append(out, k.Subpath)
		}
	}
	return out, nil
}

// ListSessionSummaries returns the incrementally-maintained summary
// sidecar for every session in the project. The store maintains the
// sidecar by calling FoldSessionSummary inside Append so adapters that
// route every mirror batch here can implement the fast list path without
// re-reading transcripts.
//
// The mtime is stamped at Append time using the last entry's ISO
// timestamp when available, falling back to wall-clock. This matches the
// storage-time convention required by list_sessions_from_store's
// freshness check.
func (s *InMemorySessionStore) ListSessionSummaries(ctx context.Context, projectKey string) ([]SessionStoreSummaryEntry, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	// Replay every main-transcript entry to fold into a summary. This is
	// the in-memory reference's job: it does not actually persist a
	// sidecar, so the fold runs on every call. Real adapters maintain the
	// sidecar inside Append and return the persisted copy here.
	bySession := map[string][]map[string]any{}
	var maxMtime int64
	for k, entries := range s.data {
		if k.ProjectKey != projectKey || k.Subpath != "" {
			continue
		}
		bySession[k.SessionID] = entries
		for _, e := range entries {
			if ms, ok := isoToMillis(str(e["timestamp"])); ok && ms > maxMtime {
				maxMtime = ms
			}
		}
	}
	out := make([]SessionStoreSummaryEntry, 0, len(bySession))
	now := time.Now().UnixMilli()
	for sid, entries := range bySession {
		summary := FoldSessionSummary(nil, SessionKey{ProjectKey: projectKey, SessionID: sid}, entries)
		if summary.MTime == 0 {
			summary.MTime = now
		}
		out = append(out, summary)
	}
	// Stable order: by session_id (matches the typical store-returned
	// ordering and avoids test flakiness).
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].SessionID < out[i].SessionID {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out, nil
}
