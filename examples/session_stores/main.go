// Session stores example.
//
// Run with: go run ./examples/session_stores
//
// Demonstrates the in-memory store, session listing, info, messages,
// subagents, and the import / export flow that round-trips a session
// between local disk and a SessionStore adapter.
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	claude "github.com/losinggeneration/claude-agent-sdk-go"
)

func main() {
	root := "/tmp/claude-agent-sdk-example"
	if err := os.MkdirAll(root, 0o700); err != nil {
		log.Fatal(err)
	}
	defer os.RemoveAll(root)

	// Inventory what sessions already exist on disk.
	fmt.Println("=== Listing existing sessions ===")
	dir := "/path/to/your/project"
	sessions, err := claude.ListSessions(claude.ListSessionsOptions{Directory: dir})
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("found %d sessions\n\n", len(sessions))
	for _, s := range sessions {
		fmt.Printf("  %s — %s (modified %d)\n", s.SessionID, s.Summary, s.LastModified)
	}

	// Read messages from one of them (or skip if empty).
	if len(sessions) == 0 {
		fmt.Println("no sessions to inspect; exiting")
		return
	}
	sid := sessions[0].SessionID
	fmt.Printf("\n=== Messages from %s ===\n", sid)
	msgs, err := claude.GetSessionMessages(sid, dir)
	if err != nil {
		log.Fatal(err)
	}
	for _, m := range msgs {
		fmt.Printf("  [%s] uuid=%s\n", m.Type, m.UUID)
	}

	// Import into the in-memory store.
	fmt.Println("\n=== Importing into in-memory store ===")
	store := claude.NewInMemorySessionStore()
	if err := claude.ImportSessionToStore(context.Background(), sid, dir, store, true); err != nil {
		log.Fatal(err)
	}

	// List via the store-backed path.
	projectKey := claude.ProjectKeyForDirectory(dir)
	fromStore, err := claude.ListSessionsFromStore(context.Background(), store, projectKey, 10, 0)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("store now reports %d session(s)\n", len(fromStore))
	for _, s := range fromStore {
		fmt.Printf("  %s — %s (modified %d)\n", s.SessionID, s.Summary, s.LastModified)
	}

	// Subagents for that session (empty if there are none).
	fmt.Println("\n=== Subagents ===")
	key := claude.SessionKey{ProjectKey: projectKey, SessionID: sid}
	agents, err := claude.ListSubagentsFromStore(context.Background(), store, key)
	if err != nil {
		log.Fatal(err)
	}
	fmt.Printf("%d subagent(s)\n", len(agents))
	for _, a := range agents {
		fmt.Printf("  %s\n", a)
	}

	// Fold a new entry into the summary sidecar; mirrors what a real
	// adapter would do inside Append.
	var anyStore claude.SessionStore = store
	if summaries, ok := anyStore.(claude.SessionSummarizer); ok {
		existing, _ := summaries.ListSessionSummaries(context.Background(), projectKey)
		if len(existing) == 0 {
			fmt.Println("\nno summaries yet")
		} else {
			prev := existing[0]
			updated := claude.FoldSessionSummary(&prev, key, []map[string]any{
				{"type": "tag", "tag": "review"},
			})
			fmt.Printf("\nfold produced tag=%v\n", updated.Data["tag"])
		}
	} else {
		fmt.Println("\nstore does not implement SessionSummarizer; skipping fold demo")
	}

	fmt.Printf("\nexample complete; inspected %s\n", root)
}
