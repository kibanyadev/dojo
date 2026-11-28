//go:build integration

package server_test

import (
	"encoding/json"
	"testing"
)

type beltBody struct {
	Belt     string  `json:"belt"`
	Title    string  `json:"title"`
	Complete bool    `json:"complete"`
	Held     int     `json:"held"`
	Total    int     `json:"total"`
	Next     *string `json:"next_belt"`
	Marks    []struct {
		Slug      string  `json:"slug"`
		Held      bool    `json:"held"`
		AwardedAt *string `json:"awarded_at"`
	} `json:"marks"`
}

func getBelt(t *testing.T, tok string) beltBody {
	t.Helper()
	code, body, h := getAs(t, tok, "/v1/belt")
	if code != 200 || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("belt: %d %q %s", code, h.Get("Cache-Control"), body)
	}
	var b beltBody
	if err := json.Unmarshal([]byte(body), &b); err != nil {
		t.Fatalf("decode %q: %v", body, err)
	}
	return b
}

// awardMark gives a learner (by github id) a mark directly, as the submit transaction would.
func awardMark(t *testing.T, githubID, slug, at string) {
	t.Helper()
	asOwner(t, "INSERT INTO belt_awards (org_id, learner_id, mark_id, source_item_id, awarded_at) "+
		"SELECT l.org_id, l.id, m.id, 'foundations/w', '"+at+"' FROM learners l, belt_marks m WHERE l.github_id = "+githubID+" AND m.slug = '"+slug+"'")
}

func TestBeltReadIntegration(t *testing.T) {
	tok := awardFixture(t) // ada (1001); white marks written-english and second-mark
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'yellow-one', 'yellow', 'Yellow one', 'Y', 1 FROM orgs WHERE slug = 'public'")
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })

	if code, _, _ := getAs(t, "", "/v1/belt"); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}

	// No awards: white is current, nothing held, yellow next.
	b := getBelt(t, tok)
	if b.Belt != "white" || b.Title != "White belt" || b.Complete || b.Held != 0 || b.Total != 2 || b.Next == nil || *b.Next != "yellow" || len(b.Marks) != 2 {
		t.Fatalf("no awards: %+v", b)
	}
	for _, m := range b.Marks {
		if m.Held || m.AwardedAt != nil {
			t.Fatalf("mark held with no award: %+v", m)
		}
	}

	// Bob's award is not ada's.
	awardMark(t, "1002", "second-mark", "2026-12-01T10:00:00Z")
	if b := getBelt(t, tok); b.Held != 0 {
		t.Fatalf("other learner's award visible: %+v", b)
	}

	// Partial.
	awardMark(t, "1001", "second-mark", "2026-12-02T18:10:00Z")
	b = getBelt(t, tok)
	if b.Belt != "white" || b.Held != 1 || b.Complete {
		t.Fatalf("partial: %+v", b)
	}
	for _, m := range b.Marks {
		if m.Slug == "second-mark" && (!m.Held || m.AwardedAt == nil || *m.AwardedAt != "2026-12-02T18:10:00Z") {
			t.Fatalf("awarded mark: %+v", m)
		}
		if m.Slug != "second-mark" && (m.Held || m.AwardedAt != nil) {
			t.Fatalf("unawarded mark: %+v", m)
		}
	}

	// White held, yellow has a mark: yellow is current.
	awardMark(t, "1001", "written-english", "2026-12-03T09:00:00Z")
	b = getBelt(t, tok)
	if b.Belt != "yellow" || b.Title != "Yellow belt" || b.Held != 0 || b.Total != 1 || b.Next == nil || *b.Next != "orange" || b.Complete {
		t.Fatalf("yellow current: %+v", b)
	}

	// Every belt with marks held: the last of them, complete, no next.
	awardMark(t, "1001", "yellow-one", "2026-12-04T09:00:00Z")
	b = getBelt(t, tok)
	if b.Belt != "yellow" || !b.Complete || b.Held != 1 || b.Total != 1 || b.Next != nil {
		t.Fatalf("complete: %+v", b)
	}
}

func TestBeltEmptyCatalogIntegration(t *testing.T) {
	tok := awardFixture(t)
	asOwner(t, "DELETE FROM catalog_items WHERE awards_mark IS NOT NULL; DELETE FROM belt_marks")
	b := getBelt(t, tok)
	if b.Belt != "white" || b.Complete || b.Total != 0 || b.Held != 0 || b.Next == nil || *b.Next != "yellow" || b.Marks == nil || len(b.Marks) != 0 {
		t.Fatalf("empty: %+v", b)
	}
}
