package claudeagentsdk

// Incremental session-summary derivation for SessionStore adapters.
//
// foldSessionSummary lets a store maintain a per-session
// SessionStoreSummaryEntry sidecar inside Append so
// list_sessions_from_store can fetch all metadata in one call instead of N
// per-session Load calls. Every derived field is append-incremental
// (set-once or last-wins) so adapters never need to re-read previously
// appended entries.
//
// Mirrors the Python SDK's fold_session_summary.

import (
	"context"
	"regexp"
	"sort"
	"strings"
	"time"
)

// foldSessionSummary folds a batch of newly-appended entries into the
// running summary for key. Stores call this from inside Append to keep a
// SessionStoreSummaryEntry sidecar up to date without re-reading the
// transcript. prev is the previous summary for the same key (or nil for
// the first append).
//
// Do not call this for keys with a Subpath set: subagent transcripts must
// not contribute to the main session's summary. Guard with
//
//	if key.Subpath == "" { ... }
//
// before calling.
//
// All derived state lives in the opaque Data map; stores persist it
// verbatim and must not interpret it.
//
// MTime is NOT touched by the fold: it is the sidecar's storage write
// time and must be stamped by the adapter after persisting. For a new
// session (prev == nil) the fold returns MTime=0 as a placeholder; the
// adapter is expected to overwrite it.
//
// CreatedAt latches the first parseable entry timestamp.
// FoldSessionSummary is the public entry point for the append-incremental
// fold used by SessionStore adapters to maintain a SessionStoreSummaryEntry
// sidecar. Stores call this from inside Append to keep a sidecar up to
// date without re-reading the transcript. prev is the previous summary
// for the same key (or nil for the first append).
//
// Do not call this for keys with a Subpath set: subagent transcripts must
// not contribute to the main session's summary. Guard with
//
//	if key.Subpath == "" { ... }
//
// before calling.
//
// All derived state lives in the opaque Data map; stores persist it
// verbatim and must not interpret it.
//
// MTime is NOT touched by the fold: it is the sidecar's storage write
// time and must be stamped by the adapter after persisting. For a new
// session (prev == nil) the fold returns MTime=0 as a placeholder.
func FoldSessionSummary(prev *SessionStoreSummaryEntry, key SessionKey, entries []map[string]any) SessionStoreSummaryEntry {
	return foldSessionSummary(prev, key, entries)
}

func foldSessionSummary(prev *SessionStoreSummaryEntry, key SessionKey, entries []map[string]any) SessionStoreSummaryEntry {
	var summary SessionStoreSummaryEntry
	if prev != nil {
		summary = SessionStoreSummaryEntry{
			SessionID: prev.SessionID,
			MTime:     prev.MTime,
			Data:      copyMap(prev.Data),
		}
	} else {
		summary = SessionStoreSummaryEntry{
			SessionID: key.SessionID,
			MTime:     0,
			Data:      map[string]any{},
		}
	}
	data := summary.Data

	for _, entry := range entries {
		if _, seen := data["is_sidechain"]; !seen {
			data["is_sidechain"] = entry["isSidechain"] == true
		}
		if _, seen := data["created_at"]; !seen {
			if ms := isoToEpochMs(entry["timestamp"]); ms != nil {
				data["created_at"] = *ms
			}
		}
		if _, seen := data["cwd"]; !seen {
			if cwd, ok := entry["cwd"].(string); ok && cwd != "" {
				data["cwd"] = cwd
			}
		}

		foldFirstPrompt(data, entry)

		for _, mapping := range lastWinsFields {
			if v, ok := entry[mapping.src].(string); ok {
				data[mapping.dst] = v
			}
		}

		if entry["type"] == "tag" {
			if tag, ok := entry["tag"].(string); ok && tag != "" {
				data["tag"] = tag
			} else {
				delete(data, "tag")
			}
		}
	}

	return summary
}

type lastWinsMapping struct {
	src, dst string
}

// lastWinsFields maps entry keys to SessionStoreSummaryEntry data keys.
// Each appended entry overwrites the previous value when present.
var lastWinsFields = []lastWinsMapping{
	{"customTitle", "custom_title"},
	{"aiTitle", "ai_title"},
	{"lastPrompt", "last_prompt"},
	{"summary", "summary_hint"},
	{"gitBranch", "git_branch"},
}

// isoToEpochMs parses an ISO-8601 timestamp string to Unix epoch
// milliseconds, or nil when parsing fails or the input is not a string.
func isoToEpochMs(v any) *int64 {
	s, ok := v.(string)
	if !ok || s == "" {
		return nil
	}
	// Try a few common forms: RFC3339Nano (with offset), "Z" suffix,
	// naked "YYYY-MM-DDTHH:MM:SS.fffff". Go's time.Parse is happy with
	// most of these, but a trailing "Z" needs the manual swap.
	if strings.HasSuffix(s, "Z") {
		s = strings.TrimSuffix(s, "Z") + "+00:00"
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		// Fall back to the no-offset variant.
		t, err = time.Parse("2006-01-02T15:04:05.000000", s)
		if err != nil {
			return nil
		}
	}
	ms := t.UnixMilli()
	return &ms
}

// commandNameRe matches a slash-command prefix in a user message.
// Mirrors Python _COMMAND_NAME_RE.
var commandNameRe = regexp.MustCompile(`^/[a-zA-Z][a-zA-Z0-9_-]*`)

// skipFirstPromptRe mirrors Python _SKIP_FIRST_PROMPT_PATTERN. Skips
// strings that don't qualify as a real first prompt.
var skipFirstPromptRe = regexp.MustCompile(`^\s*$`)

// foldFirstPrompt replicates the Python _fold_first_prompt: sets
// first_prompt + first_prompt_locked on a real match, stashes a
// command_fallback for slash-command messages, skips tool-result,
// isMeta, isCompactSummary, and auto-generated patterns.
func foldFirstPrompt(data map[string]any, entry map[string]any) {
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		return
	}
	if entry["type"] != "user" {
		return
	}
	if entry["isMeta"] == true || entry["isCompactSummary"] == true {
		return
	}
	// Skip tool-result-carrying user messages.
	if blocks, ok := entry["message"].(map[string]any); ok {
		if content, ok := blocks["content"].([]any); ok {
			for _, b := range content {
				if bm, ok := b.(map[string]any); ok && bm["type"] == "tool_result" {
					return
				}
			}
		}
	}
	for _, raw := range entryTextBlocks(entry) {
		text := strings.TrimSpace(strings.ReplaceAll(raw, "\n", " "))
		if text == "" {
			continue
		}
		if m := commandNameRe.FindString(text); m != "" {
			if _, ok := data["command_fallback"]; !ok {
				data["command_fallback"] = strings.TrimPrefix(m, "/")
			}
			continue
		}
		if skipFirstPromptRe.MatchString(text) {
			continue
		}
		if len(text) > 200 {
			text = text[:200] + "\u2026"
		}
		data["first_prompt"] = text
		data["first_prompt_locked"] = true
		return
	}
}

// entryTextBlocks extracts text strings from a type=="user" entry's
// message content.
func entryTextBlocks(entry map[string]any) []string {
	msg, ok := entry["message"].(map[string]any)
	if !ok {
		return nil
	}
	content, ok := msg["content"]
	if !ok {
		return nil
	}
	var out []string
	if s, ok := content.(string); ok {
		return []string{s}
	}
	if blocks, ok := content.([]any); ok {
		for _, b := range blocks {
			if bm, ok := b.(map[string]any); ok && bm["type"] == "text" {
				if t, ok := bm["text"].(string); ok {
					out = append(out, t)
				}
			}
		}
	}
	return out
}

func copyMap(m map[string]any) map[string]any {
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// summaryEntryToSDKInfo converts a SessionStoreSummaryEntry to the
// existing SDKSessionInfo shape used by list_sessions /
// list_sessions_from_store. Returns nil for sidechain sessions or
// sessions with no extractable summary.
func summaryEntryToSDKInfo(entry SessionStoreSummaryEntry, projectPath string) *SDKSessionInfo {
	data := entry.Data
	if sidechain, _ := data["is_sidechain"].(bool); sidechain {
		return nil
	}
	var firstPrompt string
	if locked, _ := data["first_prompt_locked"].(bool); locked {
		if s, ok := data["first_prompt"].(string); ok {
			firstPrompt = s
		}
	} else if s, ok := data["command_fallback"].(string); ok {
		firstPrompt = s
	}
	var customTitle string
	if s, ok := data["custom_title"].(string); ok && s != "" {
		customTitle = s
	} else if s, ok := data["ai_title"].(string); ok && s != "" {
		customTitle = s
	}
	summary := customTitle
	if summary == "" {
		if s, ok := data["last_prompt"].(string); ok && s != "" {
			summary = s
		} else if s, ok := data["summary_hint"].(string); ok && s != "" {
			summary = s
		} else {
			summary = firstPrompt
		}
	}
	if summary == "" {
		return nil
	}
	info := &SDKSessionInfo{
		SessionID:    entry.SessionID,
		Summary:      summary,
		LastModified: entry.MTime,
	}
	if s, ok := data["custom_title"].(string); ok {
		info.CustomTitle = s
	} else if s, ok := data["ai_title"].(string); ok {
		info.CustomTitle = s
	}
	if s, ok := data["git_branch"].(string); ok {
		info.GitBranch = s
	}
	if s, ok := data["cwd"].(string); ok && s != "" {
		info.CWD = s
	} else if projectPath != "" {
		info.CWD = projectPath
	}
	if s, ok := data["tag"].(string); ok {
		info.Tag = s
	}
	if firstPrompt != "" {
		info.FirstPrompt = firstPrompt
	}
	if v, ok := data["created_at"]; ok {
		switch x := v.(type) {
		case int64:
			info.CreatedAt = &x
		case int:
			n := int64(x)
			info.CreatedAt = &n
		case float64:
			n := int64(x)
			info.CreatedAt = &n
		}
	}
	return info
}

func strPtr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

// listSessionsFromSummaries returns the SDKSessionInfo slice for a
// project_key using a SessionSummarizer instead of per-session Loads.
// Stores that maintain summaries via foldSessionSummary inside Append
// should implement SessionSummarizer; otherwise callers fall back to
// ListSessionsFromStore.
func listSessionsFromSummaries(ctx context.Context, sm SessionSummarizer, projectKey string, limit, offset int) ([]SDKSessionInfo, error) {
	entries, err := sm.ListSessionSummaries(ctx, projectKey)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].MTime > entries[j].MTime })
	entries = applySummaryLimit(entries, limit, offset)
	out := make([]SDKSessionInfo, 0, len(entries))
	for _, e := range entries {
		if info := summaryEntryToSDKInfo(e, ""); info != nil {
			out = append(out, *info)
		}
	}
	return out, nil
}

func applySummaryLimit(xs []SessionStoreSummaryEntry, limit, offset int) []SessionStoreSummaryEntry {
	if offset > len(xs) {
		return nil
	}
	xs = xs[offset:]
	if limit > 0 && limit < len(xs) {
		return xs[:limit]
	}
	return xs
}
