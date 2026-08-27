package claudeagentsdk

// Conformance suite for SessionStore adapters. Mirrors the Python
// testing/session_store_conformance.py contract set so any custom
// adapter can run the same battery of behavioral assertions.
//
// Tests for optional methods (list_sessions, list_session_summaries,
// delete, list_subkeys) are skipped when not implemented on the store.

import (
	"context"
	"reflect"
	"sort"
	"testing"
)

// sessionStoreFactory builds a fresh store per test so each contract gets
// isolation. Mirrors Python's make_store callable.
type sessionStoreFactory func() SessionStore

// runSessionStoreConformance exercises the 14 contracts from the Python
// conformance suite, adapted to Go's t.Run table-test shape.
func runSessionStoreConformance(t *testing.T, make sessionStoreFactory, skipOptional map[string]bool) {
	probe := make()
	has := func(m string) bool {
		if skipOptional[m] {
			return false
		}
		// Probe is a value; use the type assertion approach.
		switch m {
		case "list_sessions":
			_, ok := probe.(SessionLister)
			return ok
		case "list_session_summaries":
			_, ok := probe.(SessionSummarizer)
			return ok
		case "delete":
			_, ok := probe.(SessionDeleter)
			return ok
		case "list_subkeys":
			_, ok := probe.(SessionSubkeyLister)
			return ok
		}
		return false
	}
	hasListSessions := has("list_sessions")
	hasListSummaries := has("list_session_summaries")
	hasDelete := has("delete")
	hasListSubkeys := has("list_subkeys")

	key := SessionKey{ProjectKey: "proj", SessionID: "sess"}
	e := func(d map[string]any) map[string]any {
		out := map[string]any{"type": "x"}
		for k, v := range d {
			out[k] = v
		}
		return out
	}

	t.Run("1_append_then_load_returns_same_entries", func(t *testing.T) {
		s := make()
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "b", "n": 1}), e(map[string]any{"uuid": "a", "n": 2})})
		got, err := s.Load(context.Background(), key)
		if err != nil {
			t.Fatal(err)
		}
		want := []map[string]any{e(map[string]any{"uuid": "b", "n": 1}), e(map[string]any{"uuid": "a", "n": 2})}
		if !entriesEqual(got, want) {
			t.Fatalf("entry mismatch:\n got:  %+v\n want: %+v", got, want)
		}
	})

	t.Run("2_load_unknown_key_returns_nil", func(t *testing.T) {
		s := make()
		got, err := s.Load(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "nope"})
		if err != nil {
			t.Fatal(err)
		}
		if got != nil {
			t.Fatalf("expected nil, got %v", got)
		}
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "x"})})
		got, _ = s.Load(context.Background(), SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "nope"})
		if got != nil {
			t.Fatalf("expected nil for unknown subpath, got %v", got)
		}
	})

	t.Run("3_multiple_append_calls_preserve_order", func(t *testing.T) {
		s := make()
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "z", "n": 1})})
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "a", "n": 2}), e(map[string]any{"uuid": "m", "n": 3})})
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "b", "n": 4})})
		got, _ := s.Load(context.Background(), key)
		wantOrder := []string{"z", "a", "m", "b"}
		if len(got) != len(wantOrder) {
			t.Fatalf("len: got %d want %d", len(got), len(wantOrder))
		}
		for i, u := range wantOrder {
			if got[i]["uuid"] != u {
				t.Fatalf("order: idx %d got %v want %s", i, got[i]["uuid"], u)
			}
		}
	})

	t.Run("4_empty_append_is_no_op", func(t *testing.T) {
		s := make()
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "a"})})
		_ = s.Append(context.Background(), key, nil)
		got, _ := s.Load(context.Background(), key)
		if len(got) != 1 {
			t.Fatalf("len: got %d want 1", len(got))
		}
	})

	t.Run("5_subpath_keys_stored_independently", func(t *testing.T) {
		s := make()
		sub := SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-1"}
		_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"uuid": "m"})})
		_ = s.Append(context.Background(), sub, []map[string]any{e(map[string]any{"uuid": "s"})})
		main, _ := s.Load(context.Background(), key)
		subGot, _ := s.Load(context.Background(), sub)
		if len(main) != 1 || main[0]["uuid"] != "m" {
			t.Fatalf("main wrong: %+v", main)
		}
		if len(subGot) != 1 || subGot[0]["uuid"] != "s" {
			t.Fatalf("sub wrong: %+v", subGot)
		}
	})

	t.Run("6_project_key_isolation", func(t *testing.T) {
		s := make()
		_ = s.Append(context.Background(), SessionKey{ProjectKey: "A", SessionID: "s1"}, []map[string]any{e(map[string]any{"from": "A"})})
		_ = s.Append(context.Background(), SessionKey{ProjectKey: "B", SessionID: "s1"}, []map[string]any{e(map[string]any{"from": "B"})})
		gotA, _ := s.Load(context.Background(), SessionKey{ProjectKey: "A", SessionID: "s1"})
		gotB, _ := s.Load(context.Background(), SessionKey{ProjectKey: "B", SessionID: "s1"})
		if len(gotA) != 1 || gotA[0]["from"] != "A" {
			t.Fatalf("A wrong: %+v", gotA)
		}
		if len(gotB) != 1 || gotB[0]["from"] != "B" {
			t.Fatalf("B wrong: %+v", gotB)
		}
		if hasListSessions {
			lister := s.(SessionLister)
			la, _ := lister.ListSessions(context.Background(), "A")
			lb, _ := lister.ListSessions(context.Background(), "B")
			if len(la) != 1 || len(lb) != 1 {
				t.Fatalf("list isolation wrong: A=%d B=%d", len(la), len(lb))
			}
		}
	})

	if hasListSessions {
		t.Run("7_list_sessions_returns_session_ids", func(t *testing.T) {
			s := make()
			lister := s.(SessionLister)
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "a"}, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "b"}, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "other", SessionID: "c"}, []map[string]any{e(map[string]any{"n": 1})})
			got, err := lister.ListSessions(context.Background(), "proj")
			if err != nil {
				t.Fatal(err)
			}
			ids := []string{}
			for _, e := range got {
				ids = append(ids, e.SessionID)
			}
			sort.Strings(ids)
			if len(ids) != 2 || ids[0] != "a" || ids[1] != "b" {
				t.Fatalf("ids wrong: %v", ids)
			}
			for _, e := range got {
				if e.MTime < 1e12 {
					t.Fatalf("mtime not in epoch-ms: %d", e.MTime)
				}
			}
			empty, _ := lister.ListSessions(context.Background(), "never-appended")
			if len(empty) != 0 {
				t.Fatalf("expected empty, got %v", empty)
			}
		})

		t.Run("8_list_sessions_excludes_subpaths", func(t *testing.T) {
			s := make()
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "main"}, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "main", Subpath: "subagents/agent-1"}, []map[string]any{e(map[string]any{"n": 1})})
			lister := s.(SessionLister)
			got, _ := lister.ListSessions(context.Background(), "proj")
			if len(got) != 1 || got[0].SessionID != "main" {
				t.Fatalf("expected just main, got %+v", got)
			}
		})
	}

	if hasListSummaries {
		t.Run("14_list_session_summaries", func(t *testing.T) {
			s := make()
			sm := s.(SessionSummarizer)
			summaryKey := SessionKey{ProjectKey: "proj", SessionID: "summ-sess"}
			_ = s.Append(context.Background(), summaryKey, []map[string]any{
				e(map[string]any{"timestamp": "2024-01-01T00:00:00.000Z", "customTitle": "first"}),
				e(map[string]any{"timestamp": "2024-01-01T00:00:01.000Z"}),
			})
			_ = s.Append(context.Background(), summaryKey, []map[string]any{
				e(map[string]any{"timestamp": "2024-01-01T00:00:02.000Z", "customTitle": "second"}),
			})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "other", SessionID: "elsewhere"}, []map[string]any{
				e(map[string]any{"timestamp": "2024-01-01T00:00:00.000Z"}),
			})
			summaries, err := sm.ListSessionSummaries(context.Background(), "proj")
			if err != nil {
				t.Fatal(err)
			}
			byID := map[string]SessionStoreSummaryEntry{}
			for _, e := range summaries {
				byID[e.SessionID] = e
			}
			if len(byID) != 1 {
				t.Fatalf("expected 1 session, got %d", len(byID))
			}
			summ := byID["summ-sess"]
			if summ.MTime < 1e12 {
				t.Fatalf("sidecar mtime not in epoch-ms: %d", summ.MTime)
			}
			// Clock alignment: sidecar mtime must be >= list_sessions mtime.
			if hasListSessions {
				lister := s.(SessionLister)
				ls, _ := lister.ListSessions(context.Background(), "proj")
				var lsTime int64
				for _, e := range ls {
					if e.SessionID == "summ-sess" {
						lsTime = e.MTime
					}
				}
				if summ.MTime < lsTime {
					t.Fatalf("sidecar mtime %d < list_sessions mtime %d", summ.MTime, lsTime)
				}
			}
			// data is opaque; round-trips through FoldSessionSummary.
			refolded := FoldSessionSummary(&summ, summaryKey, []map[string]any{
				e(map[string]any{"timestamp": "2024-01-01T00:00:03.000Z"}),
			})
			if refolded.SessionID != "summ-sess" {
				t.Fatalf("session_id lost in fold: %s", refolded.SessionID)
			}
			if refolded.MTime != summ.MTime {
				t.Fatalf("fold overwrote mtime: was %d got %d", summ.MTime, refolded.MTime)
			}
			// Subagent appends must not affect main session summary.
			_ = s.Append(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "summ-sess", Subpath: "subagents/agent-1"}, []map[string]any{
				e(map[string]any{"timestamp": "2024-01-01T00:00:09.000Z", "customTitle": "subagent"}),
			})
			afterSub, _ := sm.ListSessionSummaries(context.Background(), "proj")
			var afterSumm SessionStoreSummaryEntry
			for _, e := range afterSub {
				if e.SessionID == "summ-sess" {
					afterSumm = e
				}
			}
			if afterSumm.Data["custom_title"] != summ.Data["custom_title"] {
				t.Fatalf("subagent append leaked into main summary: %v vs %v", afterSumm.Data["custom_title"], summ.Data["custom_title"])
			}
			empty, _ := sm.ListSessionSummaries(context.Background(), "never-appended")
			if len(empty) != 0 {
				t.Fatalf("expected empty, got %v", empty)
			}
			if hasDelete {
				d := s.(SessionDeleter)
				_ = d.Delete(context.Background(), summaryKey)
				after, _ := sm.ListSessionSummaries(context.Background(), "proj")
				if len(after) != 0 {
					t.Fatalf("summary not cleared on delete: %v", after)
				}
			}
		})
	}

	if hasDelete {
		t.Run("9_delete_then_load_returns_nil", func(t *testing.T) {
			s := make()
			d := s.(SessionDeleter)
			_ = d.Delete(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "never-written"})
			_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"n": 1})})
			_ = d.Delete(context.Background(), key)
			got, _ := s.Load(context.Background(), key)
			if got != nil {
				t.Fatalf("expected nil, got %v", got)
			}
		})

		t.Run("10_delete_main_cascades_subkeys", func(t *testing.T) {
			s := make()
			d := s.(SessionDeleter)
			sub1 := SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-1"}
			sub2 := SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-2"}
			other := SessionKey{ProjectKey: "proj", SessionID: "sess2"}
			otherProj := SessionKey{ProjectKey: "other-proj", SessionID: key.SessionID}
			_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), sub1, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), sub2, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), other, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), otherProj, []map[string]any{e(map[string]any{"n": 1})})
			_ = d.Delete(context.Background(), key)
			if got, _ := s.Load(context.Background(), key); got != nil {
				t.Fatalf("main not deleted: %v", got)
			}
			if got, _ := s.Load(context.Background(), sub1); got != nil {
				t.Fatalf("sub1 not cascaded: %v", got)
			}
			if got, _ := s.Load(context.Background(), sub2); got != nil {
				t.Fatalf("sub2 not cascaded: %v", got)
			}
			if got, _ := s.Load(context.Background(), other); len(got) != 1 {
				t.Fatalf("other in same project was deleted: %v", got)
			}
			if got, _ := s.Load(context.Background(), otherProj); len(got) != 1 {
				t.Fatalf("other-proj session was deleted: %v", got)
			}
		})

		t.Run("11_delete_subpath_only_removes_that_subkey", func(t *testing.T) {
			s := make()
			d := s.(SessionDeleter)
			sub1 := SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-1"}
			sub2 := SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-2"}
			_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), sub1, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), sub2, []map[string]any{e(map[string]any{"n": 1})})
			_ = d.Delete(context.Background(), sub1)
			if got, _ := s.Load(context.Background(), sub1); got != nil {
				t.Fatalf("sub1 not deleted: %v", got)
			}
			if got, _ := s.Load(context.Background(), sub2); len(got) != 1 {
				t.Fatalf("sub2 deleted by mistake: %v", got)
			}
			if got, _ := s.Load(context.Background(), key); len(got) != 1 {
				t.Fatalf("main deleted by mistake: %v", got)
			}
		})
	}

	if hasListSubkeys {
		t.Run("12_list_subkeys_returns_subpaths", func(t *testing.T) {
			s := make()
			sl := s.(SessionSubkeyLister)
			_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-1"}, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: key.ProjectKey, SessionID: key.SessionID, Subpath: "subagents/agent-2"}, []map[string]any{e(map[string]any{"n": 1})})
			_ = s.Append(context.Background(), SessionKey{ProjectKey: key.ProjectKey, SessionID: "other-sess", Subpath: "subagents/agent-x"}, []map[string]any{e(map[string]any{"n": 1})})
			got, _ := sl.ListSubkeys(context.Background(), key)
			sort.Strings(got)
			want := []string{"subagents/agent-1", "subagents/agent-2"}
			if len(got) != len(want) {
				t.Fatalf("len: got %v want %v", got, want)
			}
			for i := range got {
				if got[i] != want[i] {
					t.Fatalf("idx %d: got %s want %s", i, got[i], want[i])
				}
			}
		})

		t.Run("13_list_subkeys_excludes_main", func(t *testing.T) {
			s := make()
			sl := s.(SessionSubkeyLister)
			_ = s.Append(context.Background(), key, []map[string]any{e(map[string]any{"n": 1})})
			got, _ := sl.ListSubkeys(context.Background(), key)
			if len(got) != 0 {
				t.Fatalf("expected empty, got %v", got)
			}
			got, _ = sl.ListSubkeys(context.Background(), SessionKey{ProjectKey: "proj", SessionID: "never-appended"})
			if len(got) != 0 {
				t.Fatalf("expected empty for unknown session, got %v", got)
			}
		})
	}
}

// TestInMemorySessionStoreConformance runs the full conformance suite
// against the in-memory reference implementation. Real adapters can
// invoke runSessionStoreConformance from their own test files with a
// factory that returns their adapter.
func TestInMemorySessionStoreConformance(t *testing.T) {
	runSessionStoreConformance(t, func() SessionStore {
		return NewInMemorySessionStore()
	}, nil)
	// Reference impl: optional methods all implemented.
	runSessionStoreConformance(t, func() SessionStore {
		return NewInMemorySessionStore()
	}, map[string]bool{
		"list_sessions":          false,
		"list_session_summaries": false,
		"delete":                 false,
		"list_subkeys":           false,
	})
}

// TestMinimalStoreConformance runs the conformance suite against a store
// that implements only the required methods (Append + Load), verifying
// that optional tests are correctly skipped.
func TestMinimalStoreConformance(t *testing.T) {
	runSessionStoreConformance(t, func() SessionStore {
		return minimalStore{data: map[SessionKey][]map[string]any{}}
	}, nil)
}

type minimalStore struct {
	data map[SessionKey][]map[string]any
}

func (m minimalStore) Append(ctx context.Context, key SessionKey, entries []map[string]any) error {
	m.data[key] = append(m.data[key], entries...)
	return nil
}

func (m minimalStore) Load(ctx context.Context, key SessionKey) ([]map[string]any, error) {
	return m.data[key], nil
}

// entriesEqual compares two entry slices for conformance assertions,
// treating int and float64 as numerically equal so the in-memory store's
// JSON-roundtrip-clone behavior (which widens int to float64) does not
// trigger false mismatches against the raw int values used in test setup.
func entriesEqual(a, b []map[string]any) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for k, av := range a[i] {
			bv, ok := b[i][k]
			if !ok {
				return false
			}
			if !valueEqual(av, bv) {
				return false
			}
		}
	}
	return true
}

func valueEqual(a, b any) bool {
	if reflect.DeepEqual(a, b) {
		return true
	}
	af, aok := numericFloat(a)
	bf, bok := numericFloat(b)
	if aok && bok {
		return af == bf
	}
	return false
}

func numericFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case int32:
		return float64(x), true
	case float64:
		return x, true
	case float32:
		return float64(x), true
	}
	return 0, false
}
