//go:build integration

package server_test

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
)

type awardedItem struct {
	Kind    string
	InboxID string `json:"inbox_id"`
	Slug    string
}

// awardFixture is the writing fixture plus a second white mark earned by foundations/w2, so the white
// belt has two marks and completes on the second.
func awardFixture(t *testing.T) string {
	t.Helper()
	tok := writingFixture(t)
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'second-mark', 'white', 'Second mark', 'Say what comes next', 2 FROM orgs WHERE slug = 'public'")
	asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, catalog_version, payload, awards_mark) "+
		"SELECT 'foundations/w2', org_id, id, 'writing', 7, 'Standup', 5, '{writing}', 'a1b2c3d4e5f6', '"+itemWritingPayload+"'::jsonb, 'second-mark' FROM roles WHERE slug = 'foundations'")
	return tok
}

func submitAwards(t *testing.T, tok, item string) (int, []awardedItem, string) {
	t.Helper()
	code, body, _, err := postItem(tok, apiURL(t), "/v1/items/"+item+"/submit", `{"body":"My reply."}`)
	if err != nil {
		t.Fatal(err)
	}
	var out struct{ Awarded []awardedItem }
	if code == 200 {
		if err := json.Unmarshal([]byte(body), &out); err != nil || out.Awarded == nil {
			t.Fatalf("decode %q: %v", body, err)
		}
	}
	return code, out.Awarded, body
}

func inboxCount(t *testing.T, where string) string {
	t.Helper()
	return asOwner(t, "SELECT count(*) FROM inbox_items WHERE "+where)
}

func TestAwardFlowIntegration(t *testing.T) {
	tok := awardFixture(t)

	// The first submit awards the mark and says so; the belt has another mark, so no belt item yet.
	code, got, body := submitAwards(t, tok, "foundations/w")
	if code != 200 || len(got) != 1 || got[0].Kind != "mark" || got[0].Slug != "written-english" || got[0].InboxID == "" {
		t.Fatalf("first submit: %d %s", code, body)
	}
	if !strings.Contains(body, `"held":true`) {
		t.Fatalf("the item view reads the award: %s", body)
	}
	markID := got[0].InboxID
	row := asOwner(t, "SELECT kind || '|' || ref || '|' || title || '|' || body || '|' || url || '|' || push_state || '|' || (read_at IS NULL) FROM inbox_items WHERE id = '"+markID+"'")
	if want := "mark|written-english|Belt mark: Written English for tickets|Write a reply|/celebrate/" + markID + "|none|true"; row != want {
		t.Fatalf("mark item: %q, want %q", row, want)
	}
	if n := asOwner(t, "SELECT count(*) || '|' || min(source_item_id) FROM belt_awards"); n != "1|foundations/w" {
		t.Fatalf("awards: %q", n)
	}

	// A resubmit awards nothing and adds no item.
	if code, got, body = submitAwards(t, tok, "foundations/w"); code != 200 || len(got) != 0 {
		t.Fatalf("resubmit: %d %s", code, body)
	}
	if n := inboxCount(t, "kind IN ('mark', 'belt')"); n != "1" {
		t.Fatalf("items after resubmit: %s", n)
	}

	// The feedback item awards no mark.
	if code, got, body = submitAwards(t, tok, "foundations/f"); code != 200 || len(got) != 0 {
		t.Fatalf("feedback: %d %s", code, body)
	}

	// The last mark of the belt adds the belt item as well.
	code, got, body = submitAwards(t, tok, "foundations/w2")
	if code != 200 || len(got) != 2 || got[0].Kind != "mark" || got[0].Slug != "second-mark" || got[1].Kind != "belt" || got[1].Slug != "white" {
		t.Fatalf("last mark: %d %s", code, body)
	}
	row = asOwner(t, "SELECT title || '|' || body || '|' || url || '|' || push_state FROM inbox_items WHERE id = '"+got[1].InboxID+"'")
	if want := "Two marks. Yellow belt tied on.|White belt, 2 of 2 marks|/celebrate/" + got[1].InboxID + "|none"; row != want {
		t.Fatalf("belt item: %q, want %q", row, want)
	}

	// Resubmitting either item adds nothing more, and no mark or belt item holds the placeholder url.
	if _, got, _ = submitAwards(t, tok, "foundations/w2"); len(got) != 0 {
		t.Fatalf("second resubmit: %v", got)
	}
	if n := inboxCount(t, "kind IN ('mark', 'belt')"); n != "3" {
		t.Fatalf("items: %s", n)
	}
	if n := inboxCount(t, "kind IN ('mark', 'belt') AND url IS DISTINCT FROM '/celebrate/' || id::text"); n != "0" {
		t.Fatalf("items with a url that is not their own: %s", n)
	}

	// The inbox detail reads the award.
	code, detail, _ := getAs(t, tok, "/v1/inbox/"+markID)
	var d struct {
		Detail struct {
			Held  bool
			Total int
		}
	}
	if code != 200 || json.Unmarshal([]byte(detail), &d) != nil || !d.Detail.Held || d.Detail.Total != 2 {
		t.Fatalf("mark detail: %d %s", code, detail)
	}
}

func TestAwardNeverDependsOnTheReview(t *testing.T) {
	tok := awardFixture(t)
	t.Cleanup(func() { asOwner(t, "DELETE FROM ai_reviews") })
	// Today's cap is used up, so the review is capped; the award does not wait for any review.
	asOwner(t, "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome) "+
		"SELECT org_id, id, 'foundations/w', 1, 'standin', 1, 1, 'ok' FROM learners l, generate_series(1, 20) WHERE github_id = 1001")
	code, got, body := submitAwards(t, tok, "foundations/w")
	if code != 200 || len(got) != 1 || !strings.Contains(body, `"review_state":"capped"`) {
		t.Fatalf("capped submit: %d %s", code, body)
	}
	// A failed review afterwards takes nothing back and a later submit adds nothing.
	asOwner(t, "UPDATE written_work SET review_state = 'failed'")
	if n := asOwner(t, "SELECT count(*) FROM belt_awards"); n != "1" {
		t.Fatalf("awards after a failed review: %s", n)
	}
	if _, got, _ = submitAwards(t, tok, "foundations/w"); len(got) != 0 {
		t.Fatalf("resubmit after a failed review: %v", got)
	}
}

func TestAwardConcurrentSubmitsAwardOnce(t *testing.T) {
	tok := awardFixture(t)
	const n = 8
	var wg sync.WaitGroup
	var mu sync.Mutex
	awardedResponses := 0
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, body, _, err := postItem(tok, apiURL(t), "/v1/items/foundations/w/submit", `{"body":"Same time."}`)
			var out struct{ Awarded []awardedItem }
			if err != nil || code != 200 || json.Unmarshal([]byte(body), &out) != nil {
				t.Errorf("submit: %d %s %v", code, body, err)
				return
			}
			mu.Lock()
			if len(out.Awarded) > 0 {
				awardedResponses++
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if awardedResponses != 1 {
		t.Fatalf("responses that awarded: %d, want 1", awardedResponses)
	}
	if got := asOwner(t, "SELECT (SELECT count(*) FROM belt_awards) || '|' || (SELECT count(*) FROM inbox_items WHERE kind = 'mark')"); got != "1|1" {
		t.Fatalf("awards and items: %s", got)
	}
}

func TestAwardIsolationIntegration(t *testing.T) {
	ada := awardFixture(t)
	resetLearners(t, "1002")
	signUp(t, "bob")
	bob := cliToken(t, newCLIClient(t), "bob")

	_, got, body := submitAwards(t, ada, "foundations/w")
	if len(got) != 1 {
		t.Fatalf("ada submit: %s", body)
	}
	// Bob sees the item unheld, has no items of his own, and cannot open ada's.
	if _, view := getItem(t, bob, "foundations/w"); !strings.Contains(view, `"held":false`) {
		t.Fatalf("bob's view: %s", view)
	}
	if code, _, _ := getAs(t, bob, "/v1/inbox/"+got[0].InboxID); code != 404 {
		t.Fatalf("bob opens ada's mark item: %d", code)
	}
	if n := asOwner(t, "SELECT count(*) FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE l.github_id = 1002"); n != "0" {
		t.Fatalf("bob's items: %s", n)
	}
	// His own submit awards his own mark and ada's rows stay one each.
	if _, mine, body := submitAwards(t, bob, "foundations/w"); len(mine) != 1 || mine[0].InboxID == got[0].InboxID {
		t.Fatalf("bob submit: %s", body)
	}
	if n := asOwner(t, "SELECT count(*) FROM belt_awards"); n != "2" {
		t.Fatalf("awards: %s", n)
	}
}

// A mark or belt item inserted with the placeholder url and never pointed at its id must not survive:
// a transaction that fails between the insert and the update leaves nothing behind.
func TestCelebrateItemRollsBackWithoutItsURL(t *testing.T) {
	awardFixture(t)
	conn := apiConn(t)
	ctx := context.Background()
	tx, err := conn.Begin(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec(ctx, "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url) "+
		"SELECT org_id, id, 'mark', 'written-english', 't', 'b', '/celebrate' FROM learners WHERE github_id = 1001"); err != nil {
		t.Fatal(err)
	}
	if err := tx.Rollback(ctx); err != nil {
		t.Fatal(err)
	}
	if n := inboxCount(t, "kind IN ('mark', 'belt')"); n != "0" {
		t.Fatalf("rows after the rollback: %s", n)
	}
}
