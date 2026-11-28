//go:build integration

package server_test

import (
	"strconv"
	"testing"
	"time"
)

const (
	ulStoryPayload = `{"from":{"name":"Elena","role":"Finance"},"scene":"s","choices":[{"id":"a","text":"t","verdict":"good","reaction":"r"},{"id":"b","text":"t","verdict":"poor","reaction":"r"}]}`
	ulQuizPayload  = `{"questions":[{"id":"q1"},{"id":"q2"},{"id":"q3"}]}`
)

// unlockFixture seeds ul-role: story intro (1), ticket a (2), quiz q1 (3), writing w (4), story s1 (5),
// feedback f (6), ticket b (7), quiz q2 (8); plus practice ul-role-p/x. ada has a CLI token.
func unlockFixture(t *testing.T) (c *cliClient, tok string) {
	t.Helper()
	clean := func() {
		asOwner(t, "DELETE FROM learners WHERE github_id IN (1001, 1002)")
		asOwner(t, "DELETE FROM catalog_items WHERE id LIKE 'ul-role/%'")
		asOwner(t, "DELETE FROM roles WHERE slug = 'ul-role'")
	}
	clean()
	t.Cleanup(clean)
	asOwner(t, "INSERT INTO roles (org_id, slug, title, kind, has_exam) SELECT id, 'ul-role', 'R', 'role', false FROM orgs WHERE slug = 'public'")
	item := func(id, kind string, pos int, est int, payload string) {
		cols, vals := "payload", "'"+payload+"'::jsonb"
		if kind == "ticket" {
			cols, vals = "brief, hints, bundle_key, bundle_sha256, bundle_bytes", "'brief', ARRAY['h'], 'bundles/"+id+"/x.tar.gz', '"+ctSHA+"', 100"
		}
		if kind == "writing" {
			vals = "'{}'::jsonb"
		}
		asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, catalog_version, "+cols+") "+
			"SELECT '"+id+"', o.id, r.id, '"+kind+"', "+strconv.Itoa(pos)+", 'Title "+id+"', "+strconv.Itoa(est)+", '0123456789ab', "+vals+
			" FROM orgs o, roles r WHERE o.slug = 'public' AND r.slug = 'ul-role'")
	}
	item("ul-role/intro", "story", 1, 3, ulStoryPayload)
	item("ul-role/a", "ticket", 2, 5, "")
	item("ul-role/q1", "quiz", 3, 6, ulQuizPayload)
	item("ul-role/w", "writing", 4, 10, "")
	item("ul-role/s1", "story", 5, 3, ulStoryPayload)
	item("ul-role/f", "feedback", 6, 4, ulStoryPayload)
	item("ul-role/b", "ticket", 7, 5, "")
	item("ul-role/q2", "quiz", 8, 7, ulQuizPayload)
	signUp(t, "ada")
	c = newCLIClient(t)
	return c, cliToken(t, c, "ada")
}

func ulPass(t *testing.T, c *cliClient, tok, item string) {
	t.Helper()
	at := "2026-11-23T10:00:00Z"
	chk := map[string]any{"id": evID(), "type": "check.run", "at": at, "item_id": item, "result": "pass", "duration_ms": 10}
	if code, body := post(t, c, tok, evStarted(evID(), item, at), chk); code != 200 {
		t.Fatalf("pass %s: %d %s", item, code, body)
	}
}

const unlockRowsSQL = "SELECT coalesce(string_agg(kind || ':' || ref || '|' || title || '|' || body || '|' || url || '|' || push_state, ';' ORDER BY ref), '') " +
	"FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE i.kind IN ('story','quiz') AND l.github_id = 1001"

func TestFirstPassUnlocksStoryAndQuizIntegration(t *testing.T) {
	c, tok := unlockFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	// Before any pass the inbox is empty: the intro story is unlocked from the start and gets no row.
	if got := asOwner(t, unlockRowsSQL); got != "" {
		t.Fatalf("before: %q", got)
	}
	ulPass(t, c, tok, "ul-role/a")
	drainUntilInbox(t, s)
	want := "story:ul-role/f|Elena sent you feedback|Title ul-role/f|/story/ul-role/f|pending;" +
		"quiz:ul-role/q1|Quiz ready: Title ul-role/q1|3 questions, about 6 minutes|/quiz/ul-role/q1|none;" +
		"story:ul-role/s1|Elena sent you a message|Title ul-role/s1|/story/ul-role/s1|pending"
	if got := asOwner(t, unlockRowsSQL); got != want {
		t.Fatalf("after a:\n got %q\nwant %q", got, want)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE kind = 'story' AND push_state = 'pending' AND push_next_at IS NOT NULL"); got != "2" {
		t.Fatalf("push lease: %s", got)
	}

	// A second pass of the same ticket (revisit then pass), a repeated check and a repeated batch add nothing.
	asOwner(t, "UPDATE ticket_progress SET first_passed_at = '2026-11-23T10:00:00Z' WHERE learner_id IN (SELECT id FROM learners WHERE github_id = 1001)")
	asOwner(t, "UPDATE inbox_items SET read_at = now()")
	post(t, c, tok, evStarted(evID(), "ul-role/a", "2026-11-23T11:00:00Z"))
	ulPass(t, c, tok, "ul-role/a")
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE kind IN ('story','quiz') AND read_at IS NULL"); got != "0" {
		t.Fatalf("a revisit pass wrote rows: %s unread", got)
	}
	// The check item still sends progress and event: inbox here; drain them, the unlock rows prove nothing new was announced.
	drainUntilInbox(t, s)

	// The last ticket unlocks the quiz after it, and only it.
	ulPass(t, c, tok, "ul-role/b")
	drainUntilInbox(t, s)
	if got := asOwner(t, "SELECT string_agg(ref, ',' ORDER BY ref) FROM inbox_items WHERE kind IN ('story','quiz') AND read_at IS NULL"); got != "ul-role/q2" {
		t.Fatalf("after b: %q", got)
	}
}

func TestUnlockIgnoresCompletedItemsReloadsAndOtherLearnersIntegration(t *testing.T) {
	c, tok := unlockFixture(t)
	asOwner(t, "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, 'ul-role/q1' FROM learners WHERE github_id = 1001")
	// A catalog reload that places an item after a ticket the learner passed gives no row.
	ulPass(t, c, tok, "ul-role/a")
	if got := asOwner(t, "SELECT string_agg(ref, ',' ORDER BY ref) FROM inbox_items WHERE kind IN ('story','quiz')"); got != "ul-role/f,ul-role/s1" {
		t.Fatalf("completed quiz must not be announced: %q", got)
	}
	asOwner(t, "INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, catalog_version, payload) "+
		"SELECT 'ul-role/late', o.id, r.id, 'quiz', 9, 'Late', 4, '0123456789ab', '"+ulQuizPayload+"'::jsonb FROM orgs o, roles r WHERE o.slug = 'public' AND r.slug = 'ul-role'")
	asOwner(t, "UPDATE catalog_items SET position = position + 10 WHERE id = 'ul-role/late'")
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE kind IN ('story','quiz')"); got != "2" {
		t.Fatalf("reload wrote rows: %s", got)
	}

}

func TestUnlockStaysWithinTheLearnerIntegration(t *testing.T) {
	c, tok := unlockFixture(t)
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	ulPass(t, c, tok, "ul-role/a")
	if got := asOwner(t, "SELECT count(*) FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE i.kind IN ('story','quiz') AND l.github_id = 1002"); got != "0" {
		t.Fatalf("bob has %s rows", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE i.kind IN ('story','quiz') AND l.github_id = 1001"); got != "3" {
		t.Fatalf("ada has %s rows", got)
	}
}

// drainUntilInbox skips progress events (the check projection sends them too) until an inbox event arrives.
func drainUntilInbox(t *testing.T, s *sseStream) {
	t.Helper()
	deadline := time.After(5 * time.Second)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				t.Fatal("stream closed while waiting for inbox")
			}
			if ev.name == "inbox" {
				return
			}
		case <-deadline:
			t.Fatal("no inbox event within 5s")
		}
	}
}
