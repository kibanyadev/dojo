//go:build integration

package server_test

import (
	"strings"
	"testing"
)

const progressGH = "930000001"

func progressLearner(t *testing.T) string {
	t.Helper()
	clean := func() { asOwner(t, "DELETE FROM learners WHERE github_id = "+progressGH) }
	clean()
	t.Cleanup(clean)
	id := asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, "+progressGH+", 'progress-it', 'progress-it', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public' RETURNING id")
	return strings.Fields(id)[0]
}

// progressItem inserts a practice catalog item for progress rows to reference, and removes it,
// with any progress still pointing at it, when the test ends.
func progressItem(t *testing.T, id string) {
	t.Helper()
	clean := func() {
		asOwner(t, "DELETE FROM item_completions WHERE item_id = '"+id+"'")
		asOwner(t, "DELETE FROM hint_opens WHERE item_id = '"+id+"'")
		asOwner(t, "DELETE FROM quiz_answers WHERE item_id = '"+id+"'")
		asOwner(t, "DELETE FROM story_choices WHERE item_id = '"+id+"'")
		asOwner(t, "DELETE FROM ticket_progress WHERE item_id = '"+id+"'")
		asOwner(t, "DELETE FROM catalog_items WHERE id = '"+id+"'")
	}
	clean()
	t.Cleanup(clean)
	asOwner(t, "INSERT INTO catalog_items (id, org_id, kind, position, title, est_minutes, catalog_version, brief, hints, bundle_key, bundle_sha256, bundle_bytes) "+
		"SELECT '"+id+"', id, 'practice', 1, 'T', 1, '0123456789ab', 'brief', ARRAY['h'], 'bundles/practice/x/x.tar.gz', '"+ctSHA+"', 100 FROM orgs WHERE slug = 'public'")
}

// ownerExec runs one statement as the owner and returns its output and error, so a CHECK
// refusal can be asserted instead of failing the test.
func ownerExec(t *testing.T, sql string) (string, error) {
	t.Helper()
	return psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"), sql)
}

func wantCheckViolation(t *testing.T, name, sql string) {
	t.Helper()
	out, err := ownerExec(t, sql)
	if err == nil || !strings.Contains(out, "violates check constraint") {
		t.Fatalf("%s: want a CHECK violation, got %v %q", name, err, out)
	}
}

func wantOK(t *testing.T, name, sql string) {
	t.Helper()
	if out, err := ownerExec(t, sql); err != nil {
		t.Fatalf("%s: want ok, got %v %q", name, err, out)
	}
}

func tpInsert(learner, cols, vals string) string {
	return "INSERT INTO ticket_progress (org_id, learner_id, item_id, first_started_at, " + cols + ") " +
		"SELECT org_id, id, 'it/p', now(), " + vals + " FROM learners WHERE id = '" + learner + "'"
}

func TestTicketProgressChecks(t *testing.T) {
	l := progressLearner(t)
	progressItem(t, "it/p")
	progressItem(t, "it/ok")
	for name, c := range map[string]struct{ cols, vals string }{
		"bad state":              {"state", "'done'"},
		"bad lab":                {"state, lab", "'started', 'cloud'"},
		"bad lab_state":          {"state, lab_state", "'started', 'paused'"},
		"negative check_count":   {"state, check_count", "'started', -1"},
		"passed without total":   {"state, last_check_passed", "'started', 1"},
		"total without passed":   {"state, last_check_total", "'started', 1"},
		"passed above total":     {"state, last_check_passed, last_check_total", "'started', 3, 2"},
		"total above 1000":       {"state, last_check_passed, last_check_total", "'started', 0, 1001"},
		"output over 16384":      {"state, last_check_output", "'started', repeat('x', 16385)"},
		"passed without time":    {"state", "'passed'"},
		"revisit without time":   {"state", "'revisit'"},
		"negative passed_second": {"state, passed_seconds", "'started', -1"},
	} {
		wantCheckViolation(t, name, tpInsert(l, c.cols, c.vals))
	}
	// Boundaries that must pass: enums, the 16384-byte cap, passed equal to total.
	wantOK(t, "boundaries", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, lab, lab_state, first_started_at, last_check_passed, last_check_total, last_check_output, first_passed_at) "+
		"SELECT org_id, id, 'it/ok', 'revisit', 'hosted', 'expired', now(), 5, 5, repeat('x', 16384), now() FROM learners WHERE id = '"+l+"'")
	// The cap counts bytes: 8193 two-byte characters are 16386 bytes.
	wantCheckViolation(t, "output over 16384 bytes", tpInsert(l, "state, last_check_output", "'started', repeat('é', 8193)"))
	// One row per learner and item.
	out, err := ownerExec(t, "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at, first_passed_at) "+
		"SELECT org_id, id, 'it/ok', 'passed', now(), now() FROM learners WHERE id = '"+l+"'")
	if err == nil || !strings.Contains(out, "duplicate key") {
		t.Fatalf("second row for the same learner and item: %v %q", err, out)
	}
}

func TestHintOpensRangeAndKey(t *testing.T) {
	l := progressLearner(t)
	progressItem(t, "it/h")
	ins := func(n string) string {
		return "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, 'it/h', " + n + " FROM learners WHERE id = '" + l + "'"
	}
	for _, n := range []string{"0", "4", "-1"} {
		wantCheckViolation(t, "n="+n, ins(n))
	}
	for _, n := range []string{"1", "2", "3"} {
		wantOK(t, "n="+n, ins(n))
	}
	out, err := ownerExec(t, ins("2"))
	if err == nil || !strings.Contains(out, "duplicate key") {
		t.Fatalf("repeat hint: %v %q", err, out)
	}
	wantOK(t, "repeat with ON CONFLICT", ins("2")+" ON CONFLICT DO NOTHING")
}

// Progress must name a catalog item, and a catalog load may not drop an item that has progress.
func TestProgressReferencesTheCatalog(t *testing.T) {
	l := progressLearner(t)
	progressItem(t, "it/fk")
	for name, sql := range map[string]string{
		"ticket_progress": "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) SELECT org_id, id, 'it/ghost', 'started', now() FROM learners WHERE id = '" + l + "'",
		"hint_opens":      "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, 'it/ghost', 1 FROM learners WHERE id = '" + l + "'",
	} {
		if out, err := ownerExec(t, sql); err == nil || !strings.Contains(out, name+"_item_id_fkey") {
			t.Fatalf("%s with an unknown item: %v %q", name, err, out)
		}
	}
	wantOK(t, "progress", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) SELECT org_id, id, 'it/fk', 'started', now() FROM learners WHERE id = '"+l+"'")
	wantOK(t, "hint", "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, 'it/fk', 1 FROM learners WHERE id = '"+l+"'")
	out, err := ownerExec(t, "DELETE FROM catalog_items WHERE id = 'it/fk'")
	if err == nil || !strings.Contains(out, "violates foreign key constraint") {
		t.Fatalf("deleting an item with progress: %v %q", err, out)
	}
	if got := countWhere(t, "ticket_progress", "item_id = 'it/fk'"); got != "1" {
		t.Fatalf("progress rows after the refused delete: %q", got)
	}
}

func TestEventsChecksAndIdempotency(t *testing.T) {
	l := progressLearner(t)
	ins := func(typ, id, payload string) string {
		return "INSERT INTO events (org_id, learner_id, client_event_id, type, payload, client_at) SELECT org_id, id, " + id + ", '" + typ + "', " + payload + ", now() FROM learners WHERE id = '" + l + "'"
	}
	const id = "'0194f0f2-6c1e-7d3a-9b7e-2f4a1c8e5d10'"
	wantCheckViolation(t, "unknown type", ins("lab.exploded", id, "'{}'"))
	wantCheckViolation(t, "payload not an object", ins("check.run", id, "'[]'"))
	for _, typ := range []string{"ticket.started", "lab.started", "lab.failed", "lab.stopped", "check.run", "hint.opened"} {
		wantOK(t, typ, ins(typ, "gen_random_uuid()", "'{}'"))
	}
	wantOK(t, "first", ins("check.run", id, "'{}'"))
	out, err := ownerExec(t, ins("check.run", id, "'{}'"))
	if err == nil || !strings.Contains(out, "events_learner_client_event_key") {
		t.Fatalf("repeated client_event_id: %v %q", err, out)
	}
	wantOK(t, "duplicate with ON CONFLICT", ins("check.run", id, "'{}'")+" ON CONFLICT (learner_id, client_event_id) DO NOTHING")
}

func TestPruneEventsKeepsTwelveMonths(t *testing.T) {
	l := progressLearner(t)
	progressItem(t, "it/old")
	// Only this test's old rows may exist, so the count is exact.
	asOwner(t, "DELETE FROM events WHERE at < now() - interval '12 months'")
	for _, age := range []string{"13 months", "12 months 1 day", "400 days", "11 months 29 days", "1 hour"} {
		asOwner(t, "INSERT INTO events (org_id, learner_id, client_event_id, type, client_at, at) "+
			"SELECT org_id, id, gen_random_uuid(), 'check.run', now(), now() - interval '"+age+"' FROM learners WHERE id = '"+l+"'")
	}
	// ticket_progress and hint_opens are the record: never pruned.
	asOwner(t, "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at, created_at) "+
		"SELECT org_id, id, 'it/old', 'started', now() - interval '3 years', now() - interval '3 years' FROM learners WHERE id = '"+l+"'")

	if got := asOwner(t, "SELECT prune_events()"); got != "3" {
		t.Fatalf("prune_events() = %q, want 3", got)
	}
	if got := countWhere(t, "events", "learner_id = '"+l+"'"); got != "2" {
		t.Fatalf("events left: %q, want the 2 newer than 12 months", got)
	}
	if got := asOwner(t, "SELECT prune_events()"); got != "0" {
		t.Fatalf("second call = %q, want 0", got)
	}
	if got := countWhere(t, "ticket_progress", "learner_id = '"+l+"'"); got != "1" {
		t.Fatalf("ticket_progress rows: %q, want 1", got)
	}
}

func TestPruneEventsJob(t *testing.T) {
	l := progressLearner(t)
	asOwner(t, "DELETE FROM events WHERE at < now() - interval '12 months'")
	asOwner(t, "INSERT INTO events (org_id, learner_id, client_event_id, type, client_at, at) "+
		"SELECT org_id, id, gen_random_uuid(), 'check.run', now(), now() - interval '13 months' FROM learners WHERE id = '"+l+"'")
	if out := runPruneJob(t); !strings.Contains(out, "prune: events deleted 1") {
		t.Fatalf("job output: %q, want 1 deleted", out)
	}
}

func TestPruneEventsIsNotExecutableByServiceRoles(t *testing.T) {
	for _, role := range []string{"dojo_api", "dojo_admin", "public"} {
		if got := asOwner(t, "SELECT has_function_privilege('"+role+"', 'prune_events()', 'EXECUTE')"); got != "f" {
			t.Fatalf("%s can execute prune_events(): %q", role, got)
		}
	}
}
