//go:build integration

package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

const itemWritingPayload = `{"prompt":"Elena asked why the folder broke.","rubric":[{"id":"found","text":"What you found"},{"id":"next","text":"What she does now"}]}`

const itemFeedbackPayload = `{"from":{"name":"Carmen","role":"Team lead"},"scene":"Your last reply was short.","rubric":[{"id":"ack","text":"Acknowledge it"},{"id":"plan","text":"Say what changes"}]}`

// writingFixture adds a writing task (awarding a mark) and a feedback item to the quiz fixture.
func writingFixture(t *testing.T) string {
	t.Helper()
	tok := itemFixture(t)
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'written-english', 'white', 'Written English for tickets', 'Write a reply', 1 FROM orgs WHERE slug = 'public'")
	asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, catalog_version, payload, awards_mark) "+
		"SELECT 'foundations/w', org_id, id, 'writing', 5, 'Tell Elena', 10, '{writing}', 'a1b2c3d4e5f6', '"+itemWritingPayload+"'::jsonb, 'written-english' FROM roles WHERE slug = 'foundations'")
	asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, catalog_version, payload) "+
		"SELECT 'foundations/f', org_id, id, 'feedback', 6, 'Feedback round', 8, '{}', 'a1b2c3d4e5f6', '"+itemFeedbackPayload+"'::jsonb FROM roles WHERE slug = 'foundations'")
	return tok
}

// putItem sends PUT with the bearer token.
func putItem(t *testing.T, tok, path, body string) (int, string, http.Header) {
	t.Helper()
	req, err := http.NewRequest(http.MethodPut, apiURL(t)+path, strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), resp.Header
}

func TestWritingViewIntegration(t *testing.T) {
	tok := writingFixture(t)
	code, body := getItem(t, tok, "foundations/w")
	var got struct {
		Kind       string
		Prompt     string
		Done       bool
		Rubric     []map[string]string
		AwardsMark map[string]any `json:"awards_mark"`
		Work       any
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 || got.Kind != "writing" || got.Prompt == "" || got.Done ||
		len(got.Rubric) != 2 || got.AwardsMark["slug"] != "written-english" || got.AwardsMark["title"] != "Written English for tickets" ||
		got.AwardsMark["held"] != false || got.Work != nil {
		t.Fatalf("fresh writing: %d %v %s", code, err, body)
	}

	_, body = getItem(t, tok, "foundations/f")
	if !strings.Contains(body, `"kind":"feedback"`) || !strings.Contains(body, `"name":"Carmen"`) || !strings.Contains(body, `"awards_mark":null`) || !strings.Contains(body, `"work":null`) {
		t.Fatalf("fresh feedback: %s", body)
	}

	// A stored review is the last finished one and stays while a new one is pending; paused is derived.
	asOwner(t, "INSERT INTO written_work (org_id, learner_id, item_id, draft, body, first_submitted_at, submitted_at, submit_count, review_state, review_next_at) "+
		"SELECT org_id, id, 'foundations/w', 'v2', 'v1', now(), now(), 2, 'pending', now() FROM learners WHERE github_id = 1001")
	asOwner(t, `UPDATE written_work SET review = '{"criteria":[],"next_step":"Add one line."}'::jsonb, reviewed_at = now()`)
	_, body = getItem(t, tok, "foundations/w")
	if !strings.Contains(body, `"review_state":"pending"`) || !strings.Contains(body, `"next_step":"Add one line."`) || !strings.Contains(body, `"body":"v1"`) || !strings.Contains(body, `"draft":"v2"`) {
		t.Fatalf("pending work: %s", body)
	}
	asOwner(t, "UPDATE system_flags SET value = 'false'::jsonb WHERE key = 'ai_reviews_enabled'")
	t.Cleanup(func() { asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'") })
	_, body = getItem(t, tok, "foundations/w")
	if !strings.Contains(body, `"review_state":"paused"`) {
		t.Fatalf("paused: %s", body)
	}
	asOwner(t, "UPDATE system_flags SET value = 'true'::jsonb WHERE key = 'ai_reviews_enabled'")
	if _, body = getItem(t, tok, "foundations/w"); !strings.Contains(body, `"review_state":"pending"`) {
		t.Fatalf("resumed: %s", body)
	}
}

func TestSystemFlagsSeedIntegration(t *testing.T) {
	if got := asOwner(t, "SELECT value::text FROM system_flags WHERE key = 'ai_reviews_enabled'"); got != "true" {
		t.Fatalf("seed: %q", got)
	}
}

func TestDraftIntegration(t *testing.T) {
	tok := writingFixture(t)
	code, body, h := putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"Hi Elena,\nfirst try"}`)
	var saved struct {
		At string `json:"draft_saved_at"`
	}
	if err := json.Unmarshal([]byte(body), &saved); err != nil || code != 200 || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("first save: %d %s", code, body)
	}
	if _, err := time.Parse(time.RFC3339, saved.At); err != nil {
		t.Fatalf("draft_saved_at: %q", saved.At)
	}
	_, view := getItem(t, tok, "foundations/w")
	if !strings.Contains(view, `"draft":"Hi Elena,\nfirst try"`) || !strings.Contains(view, `"draft_saved_at":"`+saved.At+`"`) ||
		!strings.Contains(view, `"body":null`) || !strings.Contains(view, `"submit_count":0`) || !strings.Contains(view, `"review_state":null`) {
		t.Fatalf("view after save: %s", view)
	}

	// The same row is updated; a draft never touches a submitted body or its review.
	asOwner(t, "UPDATE written_work SET body = 'sent', submitted_at = now(), first_submitted_at = now(), submit_count = 1, review_state = 'capped'")
	if code, body, _ = putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"second"}`); code != 200 {
		t.Fatalf("second save: %d %s", code, body)
	}
	if got := asOwner(t, "SELECT count(*) || '|' || min(draft) || '|' || min(body) || '|' || min(review_state) FROM written_work"); got != "1|second|sent|capped" {
		t.Fatalf("row after second save: %q", got)
	}

	// Feedback takes a draft too, and an empty one.
	if code, body, _ = putItem(t, tok, "/v1/items/foundations/f/draft", `{"body":""}`); code != 200 {
		t.Fatalf("feedback draft: %d %s", code, body)
	}

	// The limit is 4,000 characters, not bytes; over it never reaches the CHECK.
	if code, body, _ = putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"`+strings.Repeat("😀", 4000)+`"}`); code != 200 {
		t.Fatalf("4000 characters: %d %s", code, body)
	}
	for name, b := range map[string]string{"4001": `{"body":"` + strings.Repeat("a", 4001) + `"}`, "NUL": `{"body":"a\u0000b"}`, "missing": `{}`, "extra": `{"body":"x","y":1}`} {
		if code, body, _ = putItem(t, tok, "/v1/items/foundations/w/draft", b); code != 400 || strings.TrimSpace(body) != `{"error":"bad_request"}` {
			t.Fatalf("%s: %d %s", name, code, body)
		}
	}
	if got := asOwner(t, "SELECT char_length(draft) FROM written_work WHERE item_id = 'foundations/w'"); got != "4000" {
		t.Fatalf("draft after refusals: %q", got)
	}
}

func TestDraftNotFoundIntegration(t *testing.T) {
	tok := writingFixture(t)
	for _, id := range []string{"foundations/q", "foundations/s", "foundations/a", "foundations/nope", "foundations/w/x"} {
		if code, body, _ := putItem(t, tok, "/v1/items/"+id+"/draft", `{"body":"x"}`); code != 404 || strings.TrimSpace(body) != `{"error":"not_found"}` {
			t.Fatalf("%s: %d %s", id, code, body)
		}
	}
	if got := asOwner(t, "SELECT count(*) FROM written_work"); got != "0" {
		t.Fatalf("rows written for a refused item: %q", got)
	}
	if code, _, _ := putItem(t, "", "/v1/items/foundations/w/draft", `{"body":"x"}`); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}

// A draft is the principal's own; another learner's view stays empty.
func TestDraftIsThePrincipalsOwnIntegration(t *testing.T) {
	tok := writingFixture(t)
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })
	asOwner(t, "INSERT INTO written_work (org_id, learner_id, item_id, draft) SELECT org_id, id, 'foundations/w', 'bobs secret' FROM learners WHERE github_id = 1002")
	if _, body := getItem(t, tok, "foundations/w"); strings.Contains(body, "bobs secret") || !strings.Contains(body, `"work":null`) {
		t.Fatalf("saw another learner's draft: %s", body)
	}
	if code, _, _ := putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"ada"}`); code != 200 {
		t.Fatal(code)
	}
	if got := asOwner(t, "SELECT draft FROM written_work w JOIN learners l ON l.id = w.learner_id WHERE l.github_id = 1002"); got != "bobs secret" {
		t.Fatalf("bob's draft: %q", got)
	}
}

func TestDraftBetweenBudgetIntegration(t *testing.T) {
	tok := writingFixture(t)
	codes := map[int]int{}
	for i := 0; i < 75; i++ {
		code, _, h := putItem(t, tok, "/v1/items/foundations/w/draft", `{"body":"x"}`)
		codes[code]++
		if code == 429 && h.Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[200] < 60 || codes[200] > 70 || codes[429] < 5 || codes[200]+codes[429] != 75 {
		t.Fatalf("budget: %v", codes)
	}
}

// Deleting a learner removes the work and keeps only the token counts of the review calls.
func TestWrittenWorkErasureIntegration(t *testing.T) {
	writingFixture(t)
	asOwner(t, "INSERT INTO written_work (org_id, learner_id, item_id, draft) SELECT org_id, id, 'foundations/w', 'mine' FROM learners WHERE github_id = 1001")
	asOwner(t, "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome) "+
		"SELECT org_id, id, 'foundations/w', 1, 'standin', 10, 50, 'ok' FROM learners WHERE github_id = 1001")
	t.Cleanup(func() { asOwner(t, "DELETE FROM ai_reviews WHERE model = 'standin'") })
	id := asOwner(t, "SELECT id FROM learners WHERE github_id = 1001")
	if got := asOwner(t, "SELECT delete_learner('"+id+"')"); got != "t" {
		t.Fatalf("delete: %q", got)
	}
	if got := asOwner(t, "SELECT (SELECT count(*) FROM written_work) || '|' || (SELECT count(*) FROM ai_reviews WHERE model = 'standin' AND learner_id IS NULL)"); got != "0|1" {
		t.Fatalf("after delete: %q", got)
	}
}

func TestPruneAIReviewsKeepsTwelveMonths(t *testing.T) {
	asOwner(t, "DELETE FROM ai_reviews")
	t.Cleanup(func() { asOwner(t, "DELETE FROM ai_reviews") })
	for _, age := range []string{"13 months", "12 months 1 day", "11 months 29 days", "1 hour"} {
		asOwner(t, "INSERT INTO ai_reviews (org_id, item_id, submit_count, model, tokens_in, tokens_out, outcome, created_at) "+
			"SELECT id, 'a/b', 1, 'standin', 1, 1, 'ok', now() - interval '"+age+"' FROM orgs WHERE slug = 'public'")
	}
	if out := runPruneJob(t); !strings.Contains(out, "prune: ai_reviews deleted 2") {
		t.Fatalf("job output: %q, want 2 deleted", out)
	}
	if got := asOwner(t, "SELECT count(*) FROM ai_reviews"); got != "2" {
		t.Fatalf("rows left: %q", got)
	}
	if got := asOwner(t, "SELECT prune_ai_reviews()"); got != "0" {
		t.Fatalf("second call: %q", got)
	}
	for _, role := range []string{"dojo_api", "dojo_admin", "public"} {
		if got := asOwner(t, "SELECT has_function_privilege('"+role+"', 'prune_ai_reviews()', 'EXECUTE')"); got != "f" {
			t.Fatalf("%s can execute prune_ai_reviews(): %q", role, got)
		}
	}
}

// The CHECKs behind the handler limits, refused by the database itself.
func TestWrittenWorkChecksIntegration(t *testing.T) {
	writingFixture(t)
	insert := func(cols, vals string) error {
		_, err := psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"),
			"INSERT INTO written_work (org_id, learner_id, item_id, "+cols+") SELECT org_id, id, 'foundations/w', "+vals+" FROM learners WHERE github_id = 1001")
		return err
	}
	for name, c := range map[string][2]string{
		"draft over 4000": {"draft", "repeat('a', 4001)"},
		"body over 4000":  {"body", "repeat('a', 4001)"},
		"bad state":       {"review_state", "'paused'"},
		"tries over 5":    {"review_tries", "6"},
		"review not obj":  {"review", "'[]'::jsonb"},
	} {
		if err := insert(c[0], c[1]); err == nil {
			t.Errorf("%s: accepted", name)
			asOwner(t, "DELETE FROM written_work")
		}
	}
	if err := insert("draft, body, review_state", "repeat('a', 4000), repeat('b', 4000), 'pending'"); err != nil {
		t.Fatalf("limits are inclusive: %v", err)
	}
}
