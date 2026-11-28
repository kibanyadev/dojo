//go:build integration

package server_test

import (
	"encoding/json"
	"testing"
)

// seedBetween arranges foundations as story(1) a(2) quiz(3) b(5) quiz(6), adds a writing task after
// support-l1's ticket and a quiz in the locked systems-l2.
func seedBetween(t *testing.T) {
	t.Helper()
	seedCatalog(t)
	asOwner(t, `
UPDATE catalog_items SET position = 2 WHERE id = 'foundations/a';
UPDATE catalog_items SET position = 5 WHERE id = 'foundations/b';
INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, catalog_version, payload)
  SELECT v.id, r.org_id, r.id, v.kind, v.pos, v.title, 5, 'a1b2c3d4e5f6', '{}'::jsonb
  FROM (VALUES ('foundations/intro', 'foundations', 'story', 1, 'Intro'),
               ('foundations/q2', 'foundations', 'quiz', 6, 'Last quiz'),
               ('support-l1/w', 'support-l1', 'writing', 2, 'Reply'),
               ('systems-l2/z', 'systems-l2', 'quiz', 1, 'Locked quiz')) AS v(id, role, kind, pos, title)
  JOIN roles r ON r.slug = v.role`)
}

func complete(t *testing.T, item string) {
	t.Helper()
	asOwner(t, "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, '"+item+"' FROM learners WHERE github_id = 1001")
}

func TestBetweenIntegration(t *testing.T) {
	freshCatalog(t)
	seedBetween(t)
	resetLearners(t, "1001", "1002")
	signUp(t, "ada")
	signUp(t, "bob")
	tok := cliToken(t, newCLIClient(t), "ada")
	bobTok := cliToken(t, newCLIClient(t), "bob")

	between := func(tok string) (id, role string) {
		var got struct {
			Between *struct{ ID, Role string }
		}
		code, body, _ := getAs(t, tok, "/v1/today")
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 {
			t.Fatalf("%d %v %s", code, err, body)
		}
		if got.Between == nil {
			return "", ""
		}
		return got.Between.ID, got.Between.Role
	}
	items := func(tok string) map[string][]struct {
		ID   string
		Done bool
	} {
		var got struct {
			Roles []struct {
				Slug         string
				BetweenItems []struct {
					ID   string
					Done bool
				} `json:"between_items"`
			}
		}
		code, body, _ := getAs(t, tok, "/v1/path")
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 {
			t.Fatalf("%d %v %s", code, err, body)
		}
		out := map[string][]struct {
			ID   string
			Done bool
		}{}
		for _, r := range got.Roles {
			out[r.Slug] = r.BetweenItems
		}
		return out
	}
	check := func(step, wantID, wantRole string) {
		t.Helper()
		if id, role := between(tok); id != wantID || role != wantRole {
			t.Fatalf("%s: between %q %q, want %q %q", step, id, role, wantID, wantRole)
		}
	}

	check("before the first ticket", "foundations/intro", "foundations")
	complete(t, "foundations/intro")
	check("everything else locked", "", "")

	setProgress(t, "foundations/a", "passed", "stopped")
	check("first pass unlocks the quiz after it", "foundations/q", "foundations")
	complete(t, "foundations/q")
	check("last quiz still locked", "", "")
	if id, _ := between(bobTok); id != "foundations/intro" {
		t.Fatalf("bob sees ada's completions: %q", id)
	}

	setProgress(t, "foundations/b", "passed", "stopped")
	check("item after the last ticket", "foundations/q2", "foundations")
	setProgress(t, "support-l1/c", "passed", "")
	check("earlier role offered after moving on", "foundations/q2", "foundations")
	complete(t, "foundations/q2")
	check("next role", "support-l1/w", "support-l1")
	complete(t, "support-l1/w")
	check("locked role never offers", "", "")

	got := items(tok)
	if f := got["foundations"]; len(f) != 3 || f[0].ID != "foundations/intro" || !f[0].Done || f[1].ID != "foundations/q" ||
		f[2].ID != "foundations/q2" || len(got["systems-l2"]) != 1 || got["systems-l2"][0].Done {
		t.Fatalf("between_items: %+v", got)
	}
	if b := items(bobTok)["foundations"]; b[0].Done {
		t.Fatalf("bob sees ada's done: %+v", b)
	}
}
