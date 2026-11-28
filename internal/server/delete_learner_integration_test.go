//go:build integration

package server_test

import (
	"strings"
	"testing"
)

const (
	delVictimGH    = "920000001"
	delBystanderGH = "920000002"
	delVictimMail  = "delete-me@example.test"
	delBystandMail = "stays@example.test"
	delMarker      = "it-delete-learner"
)

func deleteFixtureCleanup(t *testing.T) {
	t.Helper()
	clean := func() {
		asOwner(t, "DELETE FROM email_outbox WHERE to_address IN ('"+delVictimMail+"', '"+delBystandMail+"') OR ref LIKE '"+delMarker+"%'")
		asOwner(t, "DELETE FROM telemetry_events WHERE cli_version = '"+delMarker+"'")
		asOwner(t, "DELETE FROM admin_audit WHERE action = '"+delMarker+"'")
		asOwner(t, "DELETE FROM operators WHERE github_id = 920000009")
		asOwner(t, "DELETE FROM learners WHERE github_id IN ("+delVictimGH+","+delBystanderGH+")")
		asOwner(t, "DELETE FROM belt_marks WHERE slug = 'del-mark'")
	}
	clean()
	t.Cleanup(clean)
}

// seedDeletableLearner inserts a learner with a session, a CLI token, a telemetry row and an unsent E1.
func seedDeletableLearner(t *testing.T, githubID, email, username string) string {
	t.Helper()
	id := asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, "+githubID+", '"+username+"', '"+username+"', '"+email+"', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public' RETURNING id")
	id = strings.Fields(id)[0]
	asOwner(t, "INSERT INTO sessions (org_id, learner_id, token_hash) SELECT org_id, id, decode(repeat('0"+githubID[len(githubID)-1:]+"', 32), 'hex') FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO cli_tokens (org_id, learner_id, token_hash, device_name, os, cli_version) SELECT org_id, id, decode(repeat('1"+githubID[len(githubID)-1:]+"', 32), 'hex'), 'd', 'linux', '1' FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO telemetry_events (org_id, learner_id, command, result, duration_ms, cli_version, os) SELECT org_id, id, 'check', 'ok', 1, '"+delMarker+"', 'linux' FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO email_outbox (learner_id, to_address, kind, ref) VALUES ('"+id+"', '"+email+"', 'E1', '"+delMarker+"-e1-"+username+"')")
	asOwner(t, "INSERT INTO events (org_id, learner_id, client_event_id, type, client_at) SELECT org_id, id, gen_random_uuid(), 'ticket.started', now() FROM learners WHERE id = '"+id+"'")
	progressItem(t, delMarker+"/"+username)
	asOwner(t, "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) SELECT org_id, id, '"+delMarker+"/"+username+"', 'started', now() FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, '"+delMarker+"/"+username+"', 1 FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, '"+delMarker+"/"+username+"' FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, last_choice, last_correct) SELECT org_id, id, '"+delMarker+"/"+username+"', 'q', 0, true, 0, true FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id) SELECT org_id, id, '"+delMarker+"/"+username+"', 'c' FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b' FROM learners WHERE id = '"+id+"'")
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'del-mark', 'white', 'M', 'S', 99 FROM orgs WHERE slug = 'public' ON CONFLICT (slug) DO NOTHING")
	asOwner(t, "INSERT INTO belt_awards (org_id, learner_id, mark_id, source_item_id) SELECT l.org_id, l.id, m.id, 'a/b' FROM learners l, belt_marks m WHERE l.id = '"+id+"' AND m.slug = 'del-mark'")
	return id
}

func countWhere(t *testing.T, table, where string) string {
	t.Helper()
	return asOwner(t, "SELECT count(*) FROM "+table+" WHERE "+where)
}

func TestDeleteLearnerRemovesOnlyThatLearner(t *testing.T) {
	deleteFixtureCleanup(t)
	victim := seedDeletableLearner(t, delVictimGH, delVictimMail, "del-victim")
	bystander := seedDeletableLearner(t, delBystanderGH, delBystandMail, "del-bystander")
	asOwner(t, "INSERT INTO operators (github_id, github_login) VALUES (920000009, 'del-op')")
	asOwner(t, "INSERT INTO admin_audit (operator_id, operator_login, action, target_type, target_id) "+
		"SELECT id, 'del-op', '"+delMarker+"', 'learner', '"+victim+"' FROM operators WHERE github_id = 920000009")

	// Called as dojo_api over a real connection: the definer function needs no broader grant.
	var deleted bool
	if err := apiConn(t).QueryRow(t.Context(), "SELECT delete_learner($1::uuid)", victim).Scan(&deleted); err != nil || !deleted {
		t.Fatalf("delete_learner as dojo_api: %v deleted=%v", err, deleted)
	}

	for _, table := range []struct{ name, col string }{
		{"learners", "id"}, {"sessions", "learner_id"}, {"cli_tokens", "learner_id"}, {"telemetry_events", "learner_id"},
		{"events", "learner_id"}, {"ticket_progress", "learner_id"}, {"hint_opens", "learner_id"},
		{"item_completions", "learner_id"}, {"quiz_answers", "learner_id"}, {"story_choices", "learner_id"}, {"inbox_items", "learner_id"},
		{"belt_awards", "learner_id"},
	} {
		if got := countWhere(t, table.name, table.col+" = '"+victim+"'"); got != "0" {
			t.Fatalf("%s rows left for the deleted learner: %s", table.name, got)
		}
		if got := countWhere(t, table.name, table.col+" = '"+bystander+"'"); got != "1" {
			t.Fatalf("%s rows of the bystander: %s, want 1", table.name, got)
		}
	}

	// Exactly one E8 with the copied address, no learner link; the unsent E1 is gone.
	if got := countWhere(t, "email_outbox", "kind = 'E8' AND ref = '"+victim+"' AND to_address = '"+delVictimMail+"' AND learner_id IS NULL AND sent_at IS NULL"); got != "1" {
		t.Fatalf("E8 rows: %s, want 1", got)
	}
	if got := countWhere(t, "email_outbox", "to_address = '"+delVictimMail+"'"); got != "1" {
		t.Fatalf("outbox rows holding the deleted address: %s, want only the E8", got)
	}
	if got := countWhere(t, "email_outbox", "learner_id = '"+bystander+"' AND kind = 'E1'"); got != "1" {
		t.Fatalf("bystander E1: %s, want 1", got)
	}

	// The audit row that names the learner survives.
	if got := countWhere(t, "admin_audit", "action = '"+delMarker+"' AND target_id = '"+victim+"'"); got != "1" {
		t.Fatalf("audit rows: %s, want 1", got)
	}
	if got := countWhere(t, "operators", "github_id = 920000009"); got != "1" {
		t.Fatalf("operator rows: %s, want 1", got)
	}
}

func TestDeleteLearnerTwiceAndUnknownAreNoOps(t *testing.T) {
	deleteFixtureCleanup(t)
	victim := seedDeletableLearner(t, delVictimGH, delVictimMail, "del-victim")
	if got := asOwner(t, "SELECT delete_learner('"+victim+"')"); got != "t" {
		t.Fatalf("first call: %q, want t", got)
	}
	if got := asOwner(t, "SELECT delete_learner('"+victim+"')"); got != "f" {
		t.Fatalf("second call: %q, want f", got)
	}
	if got := asOwner(t, "SELECT delete_learner(gen_random_uuid())"); got != "f" {
		t.Fatalf("unknown id: %q, want f", got)
	}
	if got := countWhere(t, "email_outbox", "kind = 'E8' AND ref = '"+victim+"'"); got != "1" {
		t.Fatalf("E8 rows after repeats: %s, want 1", got)
	}
}

func TestDeleteLearnerWithoutEmailQueuesNothing(t *testing.T) {
	deleteFixtureCleanup(t)
	id := asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, "+delVictimGH+", 'del-noemail', 'del-noemail', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public' RETURNING id")
	id = strings.Fields(id)[0]
	if got := asOwner(t, "SELECT delete_learner('"+id+"')"); got != "t" {
		t.Fatalf("delete: %q, want t", got)
	}
	if got := countWhere(t, "email_outbox", "ref = '"+id+"'"); got != "0" {
		t.Fatalf("E8 rows for a learner with no address: %s, want 0", got)
	}
}

func TestDeleteLearnerAsAdminRole(t *testing.T) {
	deleteFixtureCleanup(t)
	victim := seedDeletableLearner(t, delVictimGH, delVictimMail, "del-victim")
	out, err := psqlAs(t, "dojo_admin", envExample(t, "DOJO_ADMIN_DB_PASSWORD"), "SELECT delete_learner('"+victim+"')")
	if err != nil || out != "t" {
		t.Fatalf("delete_learner as dojo_admin: %v %q", err, out)
	}
}

func TestDeleteLearnerExecuteIsLimitedToServiceRoles(t *testing.T) {
	for role, want := range map[string]string{"dojo_api": "t", "dojo_admin": "t", "public": "f"} {
		got := asOwner(t, "SELECT has_function_privilege('"+role+"', 'delete_learner(uuid)', 'EXECUTE')")
		if got != want {
			t.Fatalf("%s EXECUTE on delete_learner: %q, want %q", role, got, want)
		}
	}
}

func TestDeleteLearnerIsDefinerWithPinnedSearchPath(t *testing.T) {
	got := asOwner(t, "SELECT prosecdef::text || ' ' || coalesce(array_to_string(proconfig, ','), '') FROM pg_proc WHERE proname = 'delete_learner'")
	if got != "true search_path=pg_catalog, public, pg_temp" {
		t.Fatalf("delete_learner definer/search_path: %q", got)
	}
}
