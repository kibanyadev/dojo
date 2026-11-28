//go:build integration

package server_test

import (
	"strings"
	"testing"
	"time"
)

const checkItemSQL = "SELECT count(*) || '|' || coalesce(min(title), '-') || '|' || coalesce(min(body), '-') || '|' || coalesce(min(url), '-') || '|' || coalesce(bool_and(read_at IS NULL)::text, '-') || '|' || coalesce(min(push_state), '-') " +
	"FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE l.github_id = 1001 AND kind = 'check'"

func TestInboxCheckUpsertsOneRowPerTicketIntegration(t *testing.T) {
	c, tok, ada := streamFixture(t)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z"), evCheck(evID(), "2026-11-23T10:05:00Z", "fail", "x"))
	s.next("progress", 5*time.Second)
	s.next("inbox", 5*time.Second)
	if got := asOwner(t, checkItemSQL); got != "1|Check failed · one|1 of 2 checks still red.|/t/ev-role/one|true|pending" {
		t.Fatalf("fail item: %q", got)
	}
	first := asOwner(t, "SELECT updated_at FROM inbox_items WHERE kind = 'check'")
	asOwner(t, "UPDATE inbox_items SET read_at = now(), push_state = 'sent', push_tries = 2, pushed_at = now()")

	post(t, c, tok, evCheck(evID(), "2026-11-23T10:10:00Z", "pass", "ok"))
	s.next("progress", 5*time.Second)
	s.next("inbox", 5*time.Second)
	if got := asOwner(t, checkItemSQL); got != "1|Ticket passed · one|Your fix survived the reboot.|/t/ev-role/one|true|pending" {
		t.Fatalf("pass item: %q", got)
	}
	if got := asOwner(t, "SELECT push_tries || '|' || (pushed_at IS NULL) FROM inbox_items WHERE kind = 'check'"); got != "0|true" {
		t.Fatalf("push reset: %q", got)
	}
	if second := asOwner(t, "SELECT updated_at FROM inbox_items WHERE kind = 'check'"); second == first {
		t.Fatalf("updated_at did not move: %q", second)
	}
}

func TestInboxCheckOlderEventDoesNotUpsertIntegration(t *testing.T) {
	c, tok := eventsFixture(t)
	post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z"), evCheck(evID(), "2026-11-23T10:20:00Z", "fail", "newer"))
	asOwner(t, "UPDATE inbox_items SET read_at = now()")
	before := asOwner(t, "SELECT updated_at FROM inbox_items WHERE kind = 'check'")
	post(t, c, tok, evCheck(evID(), "2026-11-23T10:10:00Z", "pass", "older"))
	if got := asOwner(t, checkItemSQL); got != "1|Check failed · one|1 of 2 checks still red.|/t/ev-role/one|false|pending" {
		t.Fatalf("older event changed the item: %q", got)
	}
	if after := asOwner(t, "SELECT updated_at FROM inbox_items WHERE kind = 'check'"); after != before {
		t.Fatalf("updated_at moved: %q -> %q", before, after)
	}
}

func TestInboxCheckPracticeUnknownCountsAndIsolationIntegration(t *testing.T) {
	c, tok := eventsFixture(t)
	t.Cleanup(func() {
		asOwner(t, "DELETE FROM learners WHERE github_id = 1001")
		asOwner(t, "DELETE FROM catalog_items WHERE id = 'practice/ev-drill'")
	})
	asOwner(t, "INSERT INTO catalog_items (id, org_id, kind, position, title, est_minutes, catalog_version, brief, hints, bundle_key, bundle_sha256, bundle_bytes) "+
		"SELECT 'practice/ev-drill', id, 'practice', 99, 'P', 5, '0123456789ab', 'b', ARRAY['h'], 'bundles/practice/ev-drill/x.tar.gz', '"+ctSHA+"', 100 FROM orgs WHERE slug = 'public'")
	asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public'")
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })

	ev := map[string]any{"id": evID(), "type": "check.run", "at": "2026-11-23T10:05:00Z", "item_id": "practice/ev-drill", "result": "fail", "duration_ms": 10}
	if code, body := post(t, c, tok, ev); code != 200 {
		t.Fatalf("%d %q", code, body)
	}
	if got := asOwner(t, checkItemSQL); got != "1|Check failed · ev-drill|Open the ticket to see the output.|/t/practice/ev-drill|true|pending" {
		t.Fatalf("practice item: %q", got)
	}
	if got := strings.TrimSpace(asOwner(t, "SELECT count(*) FROM inbox_items i JOIN learners l ON l.id = i.learner_id WHERE l.github_id = 1002")); got != "0" {
		t.Fatalf("another learner got %s items", got)
	}
	// An event for an id outside the catalog is ignored and writes nothing.
	gone := map[string]any{"id": evID(), "type": "check.run", "at": "2026-11-23T10:06:00Z", "item_id": "gone-role/old", "result": "pass", "duration_ms": 1}
	post(t, c, tok, gone)
	if got := strings.TrimSpace(asOwner(t, "SELECT count(*) FROM inbox_items")); got != "1" {
		t.Fatalf("items: %s", got)
	}
}
