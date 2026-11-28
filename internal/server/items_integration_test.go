//go:build integration

package server_test

import (
	"encoding/json"
	"strings"
	"testing"
)

const itemQuizPayload = `{"questions":[
 {"id":"who-last","text":"Who logged in?","answer":1,"explain":"who is now only",
  "choices":[{"text":"who","note":"note-who"},{"text":"last","note":"note-last"}]},
 {"id":"second","text":"Second?","answer":0,"choices":[{"text":"a","note":"note-a"},{"text":"b","note":"note-b"}]}]}`

const itemStoryPayload = `{"from":{"name":"Elena","role":"Finance"},"scene":"The folder broke.","choices":[
 {"id":"calm","text":"Looking now","verdict":"good","reaction":"reaction-calm"},
 {"id":"rude","text":"Not my job","verdict":"poor","reaction":"reaction-rude"}]}`

// itemFixture seeds the catalog with a quiz and a story in foundations and signs ada up.
func itemFixture(t *testing.T) string {
	t.Helper()
	freshCatalog(t)
	seedCatalog(t)
	asOwner(t, "UPDATE catalog_items SET payload = '"+itemQuizPayload+"'::jsonb WHERE id = 'foundations/q'")
	asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, catalog_version, payload) "+
		"SELECT 'foundations/s', org_id, id, 'story', 4, 'Elena writes', 3, '{people}', 'a1b2c3d4e5f6', '"+itemStoryPayload+"'::jsonb "+
		"FROM roles WHERE slug = 'foundations'")
	resetLearners(t, "1001")
	signUp(t, "ada")
	return cliToken(t, newCLIClient(t), "ada")
}

func getItem(t *testing.T, tok, id string) (int, string) {
	t.Helper()
	code, body, h := getAs(t, tok, "/v1/items/"+id)
	if code == 200 && h.Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", h.Get("Cache-Control"))
	}
	return code, body
}

func TestItemQuizIntegration(t *testing.T) {
	tok := itemFixture(t)
	code, body := getItem(t, tok, "foundations/q")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, leak := range []string{"note-", "who is now only", `"answer"`} {
		if strings.Contains(body, leak) {
			t.Fatalf("leak %q before answering: %s", leak, body)
		}
	}
	var got struct {
		Kind, Role string
		Done       bool
		Answered   int
		Total      int
		Questions  []struct {
			ID          string
			AnswerState map[string]any `json:"answer_state"`
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || got.Kind != "quiz" || got.Role != "foundations" || got.Done ||
		got.Answered != 0 || got.Total != 2 || len(got.Questions) != 2 || got.Questions[0].AnswerState != nil {
		t.Fatalf("fresh quiz: %v %s", err, body)
	}

	asOwner(t, "INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, last_choice, last_correct) "+
		"SELECT org_id, id, 'foundations/q', 'who-last', 0, false, 1, true FROM learners WHERE github_id = 1001")
	asOwner(t, "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, 'foundations/q' FROM learners WHERE github_id = 1001")
	_, body = getItem(t, tok, "foundations/q")
	if !strings.Contains(body, "note-who") || !strings.Contains(body, "note-last") || !strings.Contains(body, "who is now only") ||
		strings.Contains(body, "note-a") || !strings.Contains(body, `"done":true`) || !strings.Contains(body, `"answered":1`) ||
		!strings.Contains(body, `"first_try_correct":0`) {
		t.Fatalf("answered quiz: %s", body)
	}
}

func TestItemStoryIntegration(t *testing.T) {
	tok := itemFixture(t)
	code, body := getItem(t, tok, "foundations/s")
	if code != 200 || strings.Contains(body, "reaction-") || strings.Contains(body, "verdict") || !strings.Contains(body, `"chosen":null`) {
		t.Fatalf("fresh story: %d %s", code, body)
	}
	asOwner(t, "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id) SELECT org_id, id, 'foundations/s', 'rude' FROM learners WHERE github_id = 1001")
	_, body = getItem(t, tok, "foundations/s")
	if !strings.Contains(body, "reaction-rude") || strings.Contains(body, "reaction-calm") || !strings.Contains(body, `"verdict":"poor"`) {
		t.Fatalf("chosen story: %s", body)
	}
}

func TestItemNotFoundIntegration(t *testing.T) {
	tok := itemFixture(t)
	for _, id := range []string{"foundations/a", "foundations/nope", "foundations/q/answers", "foundations/q/other", "foundations", "foundations/q/answers/x"} {
		if code, body := getItem(t, tok, id); code != 404 || strings.TrimSpace(body) != `{"error":"not_found"}` {
			t.Fatalf("%s: %d %s", id, code, body)
		}
	}
	if code, _, _ := getAs(t, "", "/v1/items/foundations/q"); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}

// Each learner reads only their own answers.
func TestItemAnswersAreThePrincipalsOwn(t *testing.T) {
	tok := itemFixture(t)
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })
	asOwner(t, "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id) SELECT org_id, id, 'foundations/s', 'calm' FROM learners WHERE github_id = 1002")
	if _, body := getItem(t, tok, "foundations/s"); !strings.Contains(body, `"chosen":null`) {
		t.Fatalf("saw another learner's choice: %s", body)
	}
}
