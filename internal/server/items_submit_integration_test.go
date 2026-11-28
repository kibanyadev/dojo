//go:build integration

package server_test

import (
	"encoding/json"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

const submitItemPath = "/v1/items/foundations/w/submit"

// submitState is the work row as the owner sees it: count, review state, tries.
func submitState(t *testing.T, item string) string {
	t.Helper()
	return asOwner(t, "SELECT submit_count || '|' || coalesce(review_state, '-') || '|' || review_tries FROM written_work w "+
		"JOIN learners l ON l.id = w.learner_id WHERE l.github_id = 1001 AND w.item_id = '"+item+"'")
}

func TestSubmitFlowIntegration(t *testing.T) {
	tok := writingFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	code, body, h, err := postItem(tok, apiURL(t), submitItemPath, `{"body":"  Hi Elena,\nthe team group owns it now.  "}`)
	var got struct {
		Item struct {
			Kind string
			Done bool
			Work struct {
				Draft       string
				Body        string
				SubmitCount int    `json:"submit_count"`
				ReviewState string `json:"review_state"`
				SubmittedAt string `json:"submitted_at"`
				Review      any
			}
		}
		Awarded []any
	}
	if err != nil || code != 200 || h.Get("Cache-Control") != "no-store" || json.Unmarshal([]byte(body), &got) != nil ||
		got.Item.Kind != "writing" || !got.Item.Done || got.Item.Work.Body != "Hi Elena,\nthe team group owns it now." ||
		got.Item.Work.Draft != got.Item.Work.Body || got.Item.Work.SubmitCount != 1 || got.Item.Work.ReviewState != "pending" ||
		got.Item.Work.SubmittedAt == "" || got.Item.Work.Review != nil || got.Awarded == nil || len(got.Awarded) != 0 {
		t.Fatalf("first submit: %d %s %v", code, body, err)
	}
	if ev := s.next("item", 5*time.Second); ev.data != `{"item_id":"foundations/w","kind":"writing"}` {
		t.Fatalf("event data: %s", ev.data)
	}
	if n := dbCount(t, "item_completions", "foundations/w", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows: %s", n)
	}
	first := asOwner(t, "SELECT first_submitted_at::text FROM written_work")
	if got := submitState(t, "foundations/w"); got != "1|pending|0" {
		t.Fatalf("row: %q", got)
	}

	// A resubmit counts, starts the review over and keeps the last finished review; it completes nothing again.
	asOwner(t, `UPDATE written_work SET review = '{"criteria":[],"next_step":"x"}'::jsonb, reviewed_at = now(), review_state = 'done', review_tries = 3`)
	code, body, _, _ = postItem(tok, apiURL(t), submitItemPath, `{"body":"second version"}`)
	if code != 200 || !strings.Contains(body, `"submit_count":2`) || !strings.Contains(body, `"review_state":"pending"`) ||
		!strings.Contains(body, `"body":"second version"`) || !strings.Contains(body, `"next_step":"x"`) {
		t.Fatalf("resubmit: %d %s", code, body)
	}
	if got := submitState(t, "foundations/w"); got != "2|pending|0" {
		t.Fatalf("row after resubmit: %q", got)
	}
	if got := asOwner(t, "SELECT first_submitted_at::text FROM written_work"); got != first {
		t.Fatalf("first_submitted_at moved: %q -> %q", first, got)
	}
	if n := dbCount(t, "item_completions", "foundations/w", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows after resubmit: %s", n)
	}
	s.noItemEvent(500 * time.Millisecond)

	// A draft saved after a submit touches the draft only: body, count, state and review stay.
	if code, body, _ := putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"working on v3"}`); code != 200 {
		t.Fatalf("draft after submit: %d %s", code, body)
	}
	if got := asOwner(t, "SELECT draft || '|' || body || '|' || submit_count || '|' || review_state || '|' || (review IS NOT NULL) FROM written_work"); got != "working on v3|second version|2|pending|true" {
		t.Fatalf("row after draft: %q", got)
	}
	if got := asOwner(t, "SELECT review::text FROM written_work"); !strings.Contains(got, `"next_step": "x"`) {
		t.Fatalf("review after draft: %q", got)
	}

	// Feedback submits the same way and completes on its own item.
	if code, body, _, _ = postItem(tok, apiURL(t), "/v1/items/foundations/f/submit", `{"body":"I will ask earlier."}`); code != 200 || !strings.Contains(body, `"kind":"feedback"`) {
		t.Fatalf("feedback submit: %d %s", code, body)
	}
	if ev := s.next("item", 5*time.Second); ev.data != `{"item_id":"foundations/f","kind":"feedback"}` {
		t.Fatalf("feedback event: %s", ev.data)
	}
}

func TestSubmitDailyCapIntegration(t *testing.T) {
	tok := writingFixture(t)
	t.Cleanup(func() { asOwner(t, "DELETE FROM ai_reviews") })
	calls := func(n int, outcome, age string) {
		asOwner(t, "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome, created_at) "+
			"SELECT org_id, id, 'foundations/w', 1, 'standin', 1, 1, '"+outcome+"', now() - interval '"+age+"' FROM learners l, generate_series(1, "+strconv.Itoa(n)+") WHERE github_id = 1001")
	}
	submit := func() string {
		t.Helper()
		code, body, _, err := postItem(tok, apiURL(t), submitItemPath, `{"body":"again"}`)
		if err != nil || code != 200 {
			t.Fatalf("submit: %d %s %v", code, body, err)
		}
		var v struct {
			Item struct {
				Work struct {
					ReviewState string `json:"review_state"`
				}
			}
		}
		if json.Unmarshal([]byte(body), &v) != nil {
			t.Fatal(body)
		}
		return v.Item.Work.ReviewState
	}

	// Calls that never reached the model and calls of earlier days do not count.
	calls(25, "error", "0 seconds")
	calls(25, "ok", "2 days")
	calls(19, "ok", "0 seconds")
	if got := submit(); got != "pending" {
		t.Fatalf("19 counted calls: %q", got)
	}
	calls(1, "invalid", "0 seconds")
	if got := submit(); got != "capped" {
		t.Fatalf("20 counted calls: %q", got)
	}
	if got := submitState(t, "foundations/w"); got != "2|capped|0" {
		t.Fatalf("row: %q", got)
	}
	if n := dbCount(t, "item_completions", "foundations/w", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("a capped submit still completes once: %s", n)
	}
	// The cap is per learner and the next day it starts over; a new submit from capped starts over.
	asOwner(t, "DELETE FROM ai_reviews")
	if got := submit(); got != "pending" {
		t.Fatalf("after the calls are gone: %q", got)
	}
}

func itoa(n int) string { return strings.TrimSpace(asOwnerNumber(n)) }

func asOwnerNumber(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestSubmitWhileReviewsPausedIntegration(t *testing.T) {
	tok := writingFixture(t)
	asOwner(t, "UPDATE system_flags SET value = 'false'::jsonb WHERE key = 'ai_reviews_enabled'")
	t.Cleanup(func() { asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'") })
	code, body, _, _ := postItem(tok, apiURL(t), submitItemPath, `{"body":"paused work"}`)
	if code != 200 || !strings.Contains(body, `"review_state":"paused"`) || !strings.Contains(body, `"done":true`) {
		t.Fatalf("paused submit: %d %s", code, body)
	}
	if got := submitState(t, "foundations/w"); got != "1|pending|0" {
		t.Fatalf("stored state is pending, never paused: %q", got)
	}
	asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'")
	if _, view := getItem(t, tok, "foundations/w"); !strings.Contains(view, `"review_state":"pending"`) {
		t.Fatalf("resumed: %s", view)
	}
}

func TestSubmitRefusalsIntegration(t *testing.T) {
	tok := writingFixture(t)
	for name, tt := range map[string]struct {
		path, body string
		code       int
		err        string
	}{
		"empty":         {submitItemPath, `{"body":""}`, 400, "bad_request"},
		"only spaces":   {submitItemPath, `{"body":" \n\t "}`, 400, "bad_request"},
		"4001":          {submitItemPath, `{"body":"` + strings.Repeat("a", 4001) + `"}`, 400, "bad_request"},
		"4001 astral":   {submitItemPath, `{"body":"` + strings.Repeat("😀", 4001) + `"}`, 400, "bad_request"},
		"NUL":           {submitItemPath, `{"body":"a\u0000b"}`, 400, "bad_request"},
		"missing":       {submitItemPath, `{}`, 400, "bad_request"},
		"extra field":   {submitItemPath, `{"body":"x","y":1}`, 400, "bad_request"},
		"not json":      {submitItemPath, `nope`, 400, "bad_request"},
		"quiz item":     {"/v1/items/foundations/q/submit", `{"body":"x"}`, 404, "not_found"},
		"story item":    {"/v1/items/foundations/s/submit", `{"body":"x"}`, 404, "not_found"},
		"ticket item":   {"/v1/items/foundations/a/submit", `{"body":"x"}`, 404, "not_found"},
		"unknown item":  {"/v1/items/foundations/nope/submit", `{"body":"x"}`, 404, "not_found"},
		"bare item id":  {"/v1/items/foundations/w", `{"body":"x"}`, 404, "not_found"},
		"extra segment": {"/v1/items/foundations/w/submit/x", `{"body":"x"}`, 404, "not_found"},
	} {
		code, body, _, err := postItem(tok, apiURL(t), tt.path, tt.body)
		if err != nil || code != tt.code || strings.TrimSpace(body) != `{"error":"`+tt.err+`"}` {
			t.Errorf("%s: %d %s %v", name, code, body, err)
		}
	}
	// The limits are inclusive: 4,000 characters, bytes irrelevant, surrounding space not counted.
	if code, body, _, _ := postItem(tok, apiURL(t), submitItemPath, `{"body":"  `+strings.Repeat("😀", 4000)+` "}`); code != 200 {
		t.Fatalf("4000 characters: %d %s", code, body)
	}
	if got := asOwner(t, "SELECT char_length(body) || '|' || char_length(draft) FROM written_work"); got != "4000|4000" {
		t.Fatalf("stored: %q", got)
	}
	asOwner(t, "DELETE FROM written_work")

	if code, _, _, _ := postItem("", apiURL(t), submitItemPath, `{"body":"x"}`); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	if code, body := ada.post(submitItemPath, `{"body":"x"}`, false); code != 403 || !strings.Contains(body, "csrf_rejected") {
		t.Fatalf("no csrf: %d %s", code, body)
	}
	if code, body := ada.post(submitItemPath, `{"body":"x"}`, true); code != 200 {
		t.Fatalf("cookie with csrf: %d %s", code, body)
	}
}

// Racing submits: every one lands, the count is exact, one completion and one event.
func TestSubmitConcurrentIntegration(t *testing.T) {
	tok := writingFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	const n = 10
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if code, body, _, err := postItem(tok, apiURL(t), submitItemPath, `{"body":"racing"}`); err != nil || code != 200 {
				t.Errorf("racing submit %d: %d %s %v", i, code, body, err)
			}
		}()
	}
	wg.Wait()
	s.next("item", 5*time.Second)
	s.noItemEvent(500 * time.Millisecond)
	if got := submitState(t, "foundations/w"); got != "10|pending|0" {
		t.Fatalf("row: %q", got)
	}
	if c := dbCount(t, "item_completions", "foundations/w", "1001"); strings.TrimSpace(c) != "1" {
		t.Fatalf("completions: %s", c)
	}
}

// A submit is the principal's own; another learner's work and cap are untouched.
func TestSubmitIsThePrincipalsOwnIntegration(t *testing.T) {
	tok := writingFixture(t)
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })
	asOwner(t, "INSERT INTO written_work (org_id, learner_id, item_id, draft, body, submit_count, review_state) SELECT org_id, id, 'foundations/w', 'bobs draft', 'bobs body', 1, 'done' FROM learners WHERE github_id = 1002")
	t.Cleanup(func() { asOwner(t, "DELETE FROM ai_reviews") })
	asOwner(t, "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome) "+
		"SELECT org_id, id, 'foundations/w', 1, 'standin', 1, 1, 'ok' FROM learners l, generate_series(1, 25) WHERE github_id = 1002")

	code, body, _, _ := postItem(tok, apiURL(t), submitItemPath, `{"body":"ada's"}`)
	if code != 200 || strings.Contains(body, "bobs") || !strings.Contains(body, `"review_state":"pending"`) {
		t.Fatalf("ada submit: %d %s", code, body)
	}
	if got := asOwner(t, "SELECT draft || '|' || body || '|' || submit_count || '|' || review_state FROM written_work w JOIN learners l ON l.id = w.learner_id WHERE l.github_id = 1002"); got != "bobs draft|bobs body|1|done" {
		t.Fatalf("bob's work changed: %q", got)
	}
	if n := dbCount(t, "item_completions", "foundations/w", "1002"); strings.TrimSpace(n) != "0" {
		t.Fatalf("bob completed: %s", n)
	}
}

func TestSubmitBetweenBudgetIntegration(t *testing.T) {
	tok := writingFixture(t)
	codes := map[int]int{}
	for i := 0; i < 75; i++ {
		code, _, h, _ := postItem(tok, apiURL(t), submitItemPath, `{"body":"x"}`)
		codes[code]++
		if code == 429 && h.Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[200] < 60 || codes[200] > 70 || codes[429] < 5 || codes[200]+codes[429] != 75 {
		t.Fatalf("budget: %v", codes)
	}
}
