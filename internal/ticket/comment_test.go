package ticket

import (
	"database/sql"
	"strings"
	"testing"

	"github.com/NovaSeele/Seelegent/internal/api"
)

// lastComment sets the status of the newest comment entry and returns it.
func lastComment(t *testing.T, e *env, status string) api.OutboxEntry {
	t.Helper()
	var last api.OutboxEntry
	if err := e.db.Write(t.Context(), func(tx *sql.Tx) error {
		return tx.QueryRowContext(t.Context(), `SELECT id FROM github_outbox WHERE kind = 'comment' ORDER BY id DESC
			LIMIT 1`).Scan(&last.ID)
	}); err != nil {
		t.Fatal(err)
	}
	if status != "" {
		if err := e.db.Write(t.Context(), func(tx *sql.Tx) error {
			_, err := tx.ExecContext(t.Context(), `UPDATE github_outbox SET status = ? WHERE id = ?`, status, last.ID)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	return last
}

func commentEntry(t *testing.T, e *env, id string, entryID int64) api.OutboxEntry {
	t.Helper()
	tk, err := e.s.Ticket(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range tk.Outbox {
		if o.ID == entryID {
			return o
		}
	}
	t.Fatalf("no outbox entry %d", entryID)
	return api.OutboxEntry{}
}

// TestCommentNeverPostedTwice is the review's G9: a comment carries a hidden marker naming its outbox entry, and an
// attempt a stop cut off (unknown) is matched by that marker before anything is posted again.
func TestCommentNeverPostedTwice(t *testing.T) {
	e := setupFlow(t)
	ctx := t.Context()
	r, err := e.s.Connect(ctx, api.RepoConnectRequest{Path: e.repo})
	if err != nil {
		t.Fatal(err)
	}
	e.gh.mu.Lock()
	e.gh.issues = []map[string]any{{"number": 3, "title": "Cache", "state": "open",
		"html_url": e.gh.srv.URL + "/octo/demo/issues/3", "updated_at": "2026-11-08T09:00:00Z"}}
	e.gh.mu.Unlock()
	if _, err := e.s.Sync(ctx, r.ID); err != nil {
		t.Fatal(err)
	}
	tr, err := e.s.Triage(ctx, r.ID, "#3", api.TriageRequest{Action: api.TriageAssign})
	if err != nil {
		t.Fatal(err)
	}
	id := tr.Ticket.ID
	posted := func() []string {
		e.gh.mu.Lock()
		defer e.gh.mu.Unlock()
		return append([]string(nil), e.gh.comments...)
	}

	if _, err := e.s.Comment(ctx, id, api.CommentRequest{Body: "First look"}); err != nil {
		t.Fatal(err)
	}
	first := lastComment(t, e, "")
	if got := posted(); len(got) != 1 || !strings.HasSuffix(got[0], "\n\n"+commentMarker(first.ID)) {
		t.Fatalf("comments = %q", got)
	}

	// The daemon stopped after GitHub took the comment but before the entry was done: the recheck finds it.
	lastComment(t, e, api.OutboxUnknown)
	if err := e.s.recheck(ctx, id); err != nil {
		t.Fatal(err)
	}
	if o := commentEntry(t, e, id, first.ID); o.Status != api.OutboxDone || o.Result["recovered"] != true {
		t.Fatalf("recovered entry = %+v", o)
	}

	// A retry of the same text after such a stop: matched by the marker, not posted again.
	if _, err := e.s.Comment(ctx, id, api.CommentRequest{Body: "Second look"}); err != nil {
		t.Fatal(err)
	}
	second := lastComment(t, e, api.OutboxUnknown)
	if _, err := e.s.Comment(ctx, id, api.CommentRequest{Body: "Second look"}); err != nil {
		t.Fatal(err)
	}
	if got := posted(); len(got) != 2 {
		t.Fatalf("posted twice: %q", got)
	}
	if o := commentEntry(t, e, id, second.ID); o.Status != api.OutboxDone || o.Result["recovered"] != true {
		t.Fatalf("retried entry = %+v", o)
	}
	// A retry whose earlier attempt never reached GitHub posts it, once, under the same entry.
	e.gh.mu.Lock()
	e.gh.comments = e.gh.comments[:1]
	e.gh.mu.Unlock()
	lastComment(t, e, api.OutboxUnknown)
	if _, err := e.s.Comment(ctx, id, api.CommentRequest{Body: "Second look"}); err != nil {
		t.Fatal(err)
	}
	if got := posted(); len(got) != 2 || !strings.HasSuffix(got[1], commentMarker(second.ID)) {
		t.Fatalf("comments = %q", got)
	}
	// Another text is another comment.
	if _, err := e.s.Comment(ctx, id, api.CommentRequest{Body: "Third look"}); err != nil || len(posted()) != 3 {
		t.Fatalf("third comment: %v", err)
	}
}
