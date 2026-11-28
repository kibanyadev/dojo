//go:build integration

package server_test

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/kibanyadev/dojo/internal/ai"
	"github.com/kibanyadev/dojo/internal/server"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

// The API container's review worker ticks once an hour (compose.it.yaml), so these tests run the worker in
// process against the same database, as dojo_api, with the stand-in adapter.

func reviewStore(t *testing.T) *repo.Store {
	t.Helper()
	store, err := repo.Open(context.Background(), apiDBURL(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(store.Close)
	return store
}

func reviewWorker(t *testing.T, store *repo.Store) *server.ReviewWorker {
	t.Helper()
	return &server.ReviewWorker{Store: store, Adapter: ai.Standin{}, Batch: 5,
		Enabled: server.NewReviewsEnabled(store.Queries(), 0)}
}

func reviewFixture(t *testing.T) string {
	t.Helper()
	tok := writingFixture(t)
	clean := func() {
		asOwner(t, "DELETE FROM ai_reviews")
		asOwner(t, "DELETE FROM inbox_items")
	}
	clean()
	t.Cleanup(clean)
	return tok
}

func submitWork(t *testing.T, tok, item, body string) {
	t.Helper()
	code, resp, _, err := postItem(tok, apiURL(t), "/v1/items/"+item+"/submit", `{"body":"`+body+`"}`)
	if err != nil || code != 200 {
		t.Fatalf("submit: %d %s %v", code, resp, err)
	}
}

func tick(t *testing.T, w *server.ReviewWorker) int {
	t.Helper()
	n, err := w.Tick(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return n
}

const workSQL = "SELECT submit_count || '|' || coalesce(review_state, '-') || '|' || review_tries FROM written_work WHERE item_id = 'foundations/w'"

func TestReviewWorkerWritesTheReviewIntegration(t *testing.T) {
	tok := reviewFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)
	submitWork(t, tok, "foundations/w", "We found the group.\\nnothing more")
	s.next("item", 5*time.Second)

	w := reviewWorker(t, reviewStore(t))
	if n := tick(t, w); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if got := asOwner(t, workSQL); got != "1|done|0" {
		t.Fatalf("row: %q", got)
	}
	if got := asOwner(t, "SELECT review->'criteria'->0->>'evidence' || '|' || (reviewed_at IS NOT NULL) FROM written_work"); got != "We found the group.|true" {
		t.Fatalf("review: %q", got)
	}
	if got := asOwner(t, "SELECT model || '|' || outcome || '|' || submit_count || '|' || (learner_id IS NOT NULL) FROM ai_reviews"); got != "standin|ok|1|true" {
		t.Fatalf("call row: %q", got)
	}
	if got := asOwner(t, "SELECT kind || '|' || ref || '|' || title || '|' || body || '|' || url || '|' || push_state || '|' || (read_at IS NULL) FROM inbox_items"); got != "review_ready|foundations/w|Review ready: Tell Elena|1 of 2 criteria|/w/foundations/w|none|true" {
		t.Fatalf("inbox item: %q", got)
	}
	if ev := s.next("review", 5*time.Second); ev.data != `{"item_id":"foundations/w","review_state":"done"}` {
		t.Fatalf("review event: %s", ev.data)
	}
	s.next("inbox", 5*time.Second)

	// Nothing is left to claim, and a new review upserts the same item, unread again.
	if n := tick(t, w); n != 0 {
		t.Fatalf("second tick wrote %d", n)
	}
	asOwner(t, "UPDATE inbox_items SET read_at = now(), updated_at = now() - interval '1 hour'")
	submitWork(t, tok, "foundations/w", "found and next")
	tick(t, w)
	if got := asOwner(t, "SELECT count(*) || '|' || bool_and(read_at IS NULL) || '|' || bool_and(updated_at > now() - interval '1 minute') || '|' || min(body) FROM inbox_items"); got != "1|true|true|2 of 2 criteria" {
		t.Fatalf("upserted item: %q", got)
	}
	// The other learners have no item (isolation).
	if got := asOwner(t, "SELECT count(*) FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE l.github_id <> 1001"); got != "0" {
		t.Fatalf("foreign inbox rows: %s", got)
	}
}

func TestReviewWorkerRetriesThenFailsIntegration(t *testing.T) {
	tok := reviewFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)
	submitWork(t, tok, "foundations/w", "[[standin:invalid]]")
	s.next("item", 5*time.Second)
	w := reviewWorker(t, reviewStore(t))

	if n := tick(t, w); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if got := asOwner(t, workSQL); got != "1|pending|1" {
		t.Fatalf("after an invalid reply: %q", got)
	}
	if got := asOwner(t, "SELECT outcome || '|' || (review_next_at > now() + interval '20 seconds' AND review_next_at < now() + interval '40 seconds') FROM written_work w, ai_reviews"); got != "invalid|true" {
		t.Fatalf("backoff: %q", got)
	}
	if n := tick(t, w); n != 0 {
		t.Fatalf("a row in backoff was claimed: %d", n)
	}

	// Four failed attempts so far; the fifth fails the review, with an event and no inbox item.
	submitWork(t, tok, "foundations/w", "[[standin:error]]")
	asOwner(t, "UPDATE written_work SET review_tries = 4, review_next_at = now()")
	if n := tick(t, w); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if got := asOwner(t, workSQL); got != "2|failed|5" {
		t.Fatalf("after the fifth attempt: %q", got)
	}
	if got := asOwner(t, "SELECT count(*) || '|' || count(*) FILTER (WHERE outcome = 'error' AND model = 'unknown') FROM ai_reviews"); got != "2|1" {
		t.Fatalf("calls: %q", got)
	}
	if ev := s.next("review", 5*time.Second); ev.data != `{"item_id":"foundations/w","review_state":"failed"}` {
		t.Fatalf("event: %s", ev.data)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items"); got != "0" {
		t.Fatalf("inbox rows: %s", got)
	}
}

func TestReviewWorkerLeaseIntegration(t *testing.T) {
	tok := reviewFixture(t)
	submitWork(t, tok, "foundations/w", "one")
	submitWork(t, tok, "foundations/f", "two")
	store := reviewStore(t)
	other := reviewStore(t)

	var wg sync.WaitGroup
	var a, b []repo.ReviewLease
	for _, c := range []struct {
		s   *repo.Store
		out *[]repo.ReviewLease
	}{{store, &a}, {other, &b}} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			l, err := c.s.LeaseReviews(context.Background(), 1)
			if err != nil {
				t.Error(err)
			}
			*c.out = l
		}()
	}
	wg.Wait()
	if len(a) != 1 || len(b) != 1 || a[0].ItemID == b[0].ItemID {
		t.Fatalf("two tasks claimed %+v and %+v", a, b)
	}
	if a[0].Title == "" || a[0].SubmitCount != 1 || len(a[0].Payload) == 0 {
		t.Fatalf("lease: %+v", a[0])
	}
	// Leased rows wait 5 minutes; an expired lease is claimed again.
	if l, _ := store.LeaseReviews(context.Background(), 5); len(l) != 0 {
		t.Fatalf("claimed leased rows: %d", len(l))
	}
	asOwner(t, "UPDATE written_work SET review_next_at = now() - interval '1 second' WHERE item_id = 'foundations/w'")
	if l, _ := store.LeaseReviews(context.Background(), 5); len(l) != 1 || l[0].ItemID != "foundations/w" {
		t.Fatalf("expired lease: %+v", l)
	}
}

func TestReviewWorkerDiscardsAStaleResultIntegration(t *testing.T) {
	tok := reviewFixture(t)
	submitWork(t, tok, "foundations/w", "first")
	store := reviewStore(t)
	leases, err := store.LeaseReviews(context.Background(), 5)
	if err != nil || len(leases) != 1 {
		t.Fatalf("lease: %v %d", err, len(leases))
	}
	submitWork(t, tok, "foundations/w", "second")
	ok, err := store.FinishReview(context.Background(), repo.ReviewResult{Lease: leases[0], State: "done",
		Call: &repo.ReviewCall{Model: "standin", TokensIn: 1, TokensOut: 1, Outcome: "ok"}, Review: []byte(`{"criteria":[],"next_step":""}`), Total: 0})
	if err != nil || ok {
		t.Fatalf("stale write: %v %v", ok, err)
	}
	if got := asOwner(t, "SELECT submit_count || '|' || review_state || '|' || review_tries || '|' || (review IS NULL) FROM written_work"); got != "2|pending|0|true" {
		t.Fatalf("row: %q", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM ai_reviews"); got != "1" {
		t.Fatalf("the call is still recorded: %s", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items"); got != "0" {
		t.Fatalf("inbox rows: %s", got)
	}
}

func TestReviewWorkerPausedAndCappedIntegration(t *testing.T) {
	tok := reviewFixture(t)
	submitWork(t, tok, "foundations/w", "found")
	w := reviewWorker(t, reviewStore(t))

	asOwner(t, "UPDATE system_flags SET value = 'false'::jsonb WHERE key = 'ai_reviews_enabled'")
	t.Cleanup(func() { asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'") })
	before := asOwner(t, "SELECT review_next_at::text FROM written_work")
	if n := tick(t, w); n != 0 {
		t.Fatalf("paused tick wrote %d", n)
	}
	if got := asOwner(t, "SELECT review_next_at::text FROM written_work"); got != before || asOwner(t, workSQL) != "1|pending|0" {
		t.Fatalf("a paused worker touched the row: %q", got)
	}
	asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'")

	// The count reached 20 after the submit: the worker caps at claim time and makes no call.
	asOwner(t, "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome) "+
		"SELECT org_id, id, 'foundations/w', 1, 'standin', 1, 1, 'ok' FROM learners l, generate_series(1, 20) WHERE github_id = 1001")
	if n := tick(t, w); n != 1 {
		t.Fatalf("wrote %d", n)
	}
	if got := asOwner(t, workSQL); got != "1|capped|0" {
		t.Fatalf("row: %q", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM ai_reviews"); got != "20" {
		t.Fatalf("a capped row made a call: %s", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items"); got != "0" {
		t.Fatalf("inbox rows: %s", got)
	}
}
