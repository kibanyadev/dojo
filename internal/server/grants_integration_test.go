//go:build integration

package server_test

import (
	"net/http"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// psqlAs runs psql inside the db container: Postgres stays unpublished.
func psqlAs(t *testing.T, role, password, sql string) (string, error) {
	t.Helper()
	project := os.Getenv("DOJO_COMPOSE_PROJECT")
	if project == "" {
		t.Fatal("DOJO_COMPOSE_PROJECT is not set: run `make test-integration`")
	}
	ids, err := exec.Command("docker", "ps", "-q",
		"--filter", "label=com.docker.compose.project="+project,
		"--filter", "label=com.docker.compose.service=db").Output()
	id := strings.TrimSpace(string(ids))
	if err != nil || id == "" {
		t.Fatalf("find db container: %v %q", err, ids)
	}
	out, err := exec.Command("docker", "exec", "-e", "PGPASSWORD="+password, id,
		"psql", "-h", id, "-U", role, "-d", "dojo", "-v", "ON_ERROR_STOP=1", "-tA", "-c", sql).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

func envExample(t *testing.T, key string) string {
	t.Helper()
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(l, key+"="); ok {
			return v
		}
	}
	t.Fatalf("%s not in .env.example", key)
	return ""
}

// grantCases is the grants harness: every table PR appends its rows here, both the writes a role
// is granted and each one it is refused. A refused case must fail on permission denied (42501).
var grantCases = []struct {
	role    string
	sql     string
	allowed bool
}{
	{"dojo_api", "SELECT count(*) FROM orgs", true},
	{"dojo_api", "CREATE TABLE should_fail (id int)", false},
	{"dojo_api", "INSERT INTO orgs (slug, name) VALUES ('x', 'X')", false},
	{"dojo_admin", "SELECT count(*) FROM orgs", true},
	{"dojo_admin", "CREATE TABLE should_fail (id int)", false},
	{"dojo_admin", "INSERT INTO orgs (slug, name) VALUES ('x', 'X')", false},

	// learners and sessions. WHERE false: privileges are checked, no row is touched.
	{"dojo_api", "UPDATE learners SET email_source = 'learner', first_run_seen_at = now(), updated_at = now() WHERE false", true},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, email, email_source, age_confirmed_at, terms_accepted_at, terms_version) SELECT id, 1, 'a', 'abc', 'a@b.c', 'github', now(), now(), 'x' FROM orgs WHERE false", true},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, first_run_seen_at, age_confirmed_at, terms_accepted_at, terms_version) SELECT id, 1, 'a', 'abc', now(), now(), now(), 'x' FROM orgs WHERE false", false},
	{"dojo_admin", "SELECT email_source, first_run_seen_at FROM learners", true},
	{"dojo_admin", "UPDATE learners SET first_run_seen_at = now() WHERE false", false},
	{"dojo_api", "SELECT token_hash FROM sessions", true},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, language, age_confirmed_at, terms_accepted_at, terms_version, usage_stats) SELECT id, 1, 'a', 'abc', 'en', now(), now(), '2026-11-20', true FROM orgs WHERE false", true},
	{"dojo_api", "UPDATE learners SET language = 'es', usage_stats = false, analytics_consent = true, email = 'a@b.c', username = 'abcd', github_login = 'x' WHERE false", true},
	{"dojo_api", "INSERT INTO sessions (org_id, learner_id, token_hash) SELECT org_id, id, '\\x00'::bytea FROM learners WHERE false", true},
	{"dojo_api", "UPDATE sessions SET last_used_at = now(), revoked_at = now(), updated_at = now() WHERE false", true},
	{"dojo_api", "UPDATE learners SET suspended_at = now() WHERE false", false},
	{"dojo_api", "UPDATE learners SET github_id = 2 WHERE false", false},
	{"dojo_api", "UPDATE learners SET org_id = org_id WHERE false", false},
	{"dojo_api", "UPDATE learners SET age_confirmed_at = now() WHERE false", false},
	{"dojo_api", "UPDATE learners SET terms_accepted_at = now() WHERE false", false},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, suspended_at) SELECT id, 1, 'a', 'abc', now(), now(), now() FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE learners SET terms_version = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM learners WHERE false", false},
	{"dojo_api", "DELETE FROM sessions WHERE false", false},
	{"dojo_api", "UPDATE sessions SET token_hash = '\\x00' WHERE false", false},
	{"dojo_api", "UPDATE sessions SET learner_id = learner_id WHERE false", false},
	{"dojo_admin", "SELECT github_login, email, suspended_at FROM learners", true},
	{"dojo_admin", "UPDATE learners SET suspended_at = now(), updated_at = now() WHERE false", true},
	{"dojo_admin", "UPDATE learners SET email = 'x' WHERE false", false},
	{"dojo_admin", "UPDATE learners SET username = 'x' WHERE false", false},
	{"dojo_admin", "UPDATE learners SET github_id = 2 WHERE false", false},
	{"dojo_admin", "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at) SELECT id, 1, 'a', 'abc', now(), now() FROM orgs WHERE false", false},
	{"dojo_admin", "UPDATE learners SET terms_version = 'x' WHERE false", false},
	{"dojo_admin", "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, terms_version) SELECT id, 1, 'a', 'abc', now(), now(), 'x' FROM orgs WHERE false", false},
	{"dojo_admin", "DELETE FROM learners WHERE false", false},
	// chosen_path_id and path_waitlist (migration 00018): the API sets the choice, adds and removes
	// waitlist rows and never rewrites them; the operator service only reads.
	{"dojo_api", "UPDATE learners SET chosen_path_id = NULL, updated_at = now() WHERE false", true},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, language, age_confirmed_at, terms_accepted_at, terms_version, usage_stats, chosen_path_id) SELECT id, 1, 'a', 'abc', 'en', now(), now(), '2026-11-20', true, NULL FROM orgs WHERE false", true},
	{"dojo_api", "SELECT chosen_path_id FROM learners", true},
	{"dojo_api", "SELECT learner_id, path_id, org_id, created_at FROM path_waitlist", true},
	{"dojo_api", "INSERT INTO path_waitlist (org_id, learner_id, path_id) SELECT l.org_id, l.id, p.id FROM learners l, paths p WHERE false", true},
	{"dojo_api", "DELETE FROM path_waitlist WHERE false", true},
	{"dojo_api", "INSERT INTO path_waitlist (org_id, learner_id, path_id, created_at) SELECT l.org_id, l.id, p.id, now() FROM learners l, paths p WHERE false", false},
	{"dojo_api", "UPDATE path_waitlist SET updated_at = now() WHERE false", false},
	{"dojo_api", "UPDATE path_waitlist SET path_id = path_id WHERE false", false},
	{"dojo_admin", "SELECT chosen_path_id FROM learners", true},
	{"dojo_admin", "SELECT path_id, count(*) FROM path_waitlist GROUP BY path_id", true},
	{"dojo_admin", "UPDATE learners SET chosen_path_id = NULL WHERE false", false},
	{"dojo_admin", "INSERT INTO path_waitlist (org_id, learner_id, path_id) SELECT l.org_id, l.id, p.id FROM learners l, paths p WHERE false", false},
	{"dojo_admin", "DELETE FROM path_waitlist WHERE false", false},
	{"dojo_admin", "UPDATE path_waitlist SET updated_at = now() WHERE false", false},
	{"dojo_admin", "SELECT token_hash FROM sessions", false},
	{"dojo_admin", "SELECT count(*) FROM sessions", false},
	{"dojo_admin", "UPDATE sessions SET revoked_at = now() WHERE false", false},

	// cli_tokens: the API reads, inserts the device row and touches last_used_at and revoked_at;
	// the hash and the device fields never change; nothing is deleted; the operator service has no access.
	{"dojo_api", "SELECT token_hash, device_name, os, cli_version, last_used_at, revoked_at FROM cli_tokens", true},
	{"dojo_api", "INSERT INTO cli_tokens (org_id, learner_id, token_hash, device_name, os, cli_version) SELECT org_id, id, '\\x00'::bytea, 'd', 'linux', '1' FROM learners WHERE false", true},
	{"dojo_api", "UPDATE cli_tokens SET last_used_at = now(), revoked_at = now(), updated_at = now() WHERE false", true},
	{"dojo_api", "INSERT INTO cli_tokens (org_id, learner_id, token_hash, device_name, os, cli_version, revoked_at) SELECT org_id, id, '\\x00'::bytea, 'd', 'linux', '1', now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO cli_tokens (org_id, learner_id, token_hash, device_name, os, cli_version, last_used_at) SELECT org_id, id, '\\x00'::bytea, 'd', 'linux', '1', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET token_hash = '\\x00' WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET org_id = org_id WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET device_name = 'x' WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET os = 'x' WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET cli_version = 'x' WHERE false", false},
	{"dojo_api", "UPDATE cli_tokens SET created_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM cli_tokens WHERE false", false},
	{"dojo_admin", "SELECT count(*) FROM cli_tokens", false},
	{"dojo_admin", "SELECT token_hash FROM cli_tokens", false},
	{"dojo_admin", "UPDATE cli_tokens SET revoked_at = now() WHERE false", false},
	{"dojo_admin", "INSERT INTO cli_tokens (org_id, learner_id, token_hash, device_name, os, cli_version) SELECT org_id, id, '\\x00'::bytea, 'd', 'linux', '1' FROM learners WHERE false", false},
	{"dojo_admin", "DELETE FROM cli_tokens WHERE false", false},

	// telemetry_events: the API only inserts (no SELECT, UPDATE or DELETE; rows go by cascade or
	// prune_telemetry() as owner); the operator service has no access. `at` and `id` keep defaults.
	{"dojo_api", "INSERT INTO telemetry_events (org_id, learner_id, command, result, duration_ms, cli_version, os) SELECT id, NULL, 'check', 'ok', 1, 'v1', 'linux' FROM orgs WHERE false", true},
	{"dojo_api", "INSERT INTO telemetry_events (org_id, command, result, duration_ms, cli_version, os) SELECT id, 'check', 'ok', 1, 'v1', 'linux' FROM orgs WHERE false", true},
	{"dojo_api", "INSERT INTO telemetry_events (org_id, command, result, duration_ms, cli_version, os, at) SELECT id, 'check', 'ok', 1, 'v1', 'linux', now() FROM orgs WHERE false", false},
	{"dojo_api", "INSERT INTO telemetry_events (id, org_id, command, result, duration_ms, cli_version, os) SELECT gen_random_uuid(), id, 'check', 'ok', 1, 'v1', 'linux' FROM orgs WHERE false", false},
	{"dojo_api", "SELECT count(*) FROM telemetry_events", false},
	{"dojo_api", "SELECT learner_id FROM telemetry_events", false},
	{"dojo_api", "UPDATE telemetry_events SET result = 'x' WHERE false", false},
	{"dojo_api", "UPDATE telemetry_events SET learner_id = NULL WHERE false", false},
	{"dojo_api", "DELETE FROM telemetry_events WHERE false", false},
	{"dojo_admin", "SELECT count(*) FROM telemetry_events", false},
	{"dojo_admin", "INSERT INTO telemetry_events (org_id, command, result, duration_ms, cli_version, os) SELECT id, 'check', 'ok', 1, 'v1', 'linux' FROM orgs WHERE false", false},
	{"dojo_admin", "UPDATE telemetry_events SET result = 'x' WHERE false", false},
	{"dojo_admin", "DELETE FROM telemetry_events WHERE false", false},
	// prune_telemetry() is owner-only: no service role executes it, so none gains DELETE through it.
	{"dojo_api", "SELECT prune_telemetry()", false},
	{"dojo_admin", "SELECT prune_telemetry()", false},

	// operators, admin_sessions, admin_audit: dojo_api has no grant at all. dojo_admin writes
	// column-level (revoked_at is the only update) and can never delete; the audit is append-only.
	{"dojo_api", "SELECT count(*) FROM operators", false},
	{"dojo_api", "INSERT INTO operators (github_id, github_login) SELECT 1, 'a' WHERE false", false},
	{"dojo_api", "UPDATE operators SET revoked_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM operators WHERE false", false},
	{"dojo_api", "SELECT count(*) FROM admin_sessions", false},
	{"dojo_api", "SELECT token_hash FROM admin_sessions", false},
	{"dojo_api", "INSERT INTO admin_sessions (operator_id, token_hash) SELECT id, '\\x00'::bytea FROM operators WHERE false", false},
	{"dojo_api", "UPDATE admin_sessions SET revoked_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM admin_sessions WHERE false", false},
	{"dojo_api", "SELECT count(*) FROM admin_audit", false},
	{"dojo_api", "INSERT INTO admin_audit (operator_id, operator_login, action, target_type, target_id) SELECT id, 'a', 'x', 'y', 'z' FROM operators WHERE false", false},
	{"dojo_api", "UPDATE admin_audit SET reason = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM admin_audit WHERE false", false},
	{"dojo_admin", "SELECT github_id, github_login, added_by, added_at, revoked_at FROM operators", true},
	{"dojo_admin", "INSERT INTO operators (github_id, github_login, added_by) SELECT 1, 'a', id FROM operators WHERE false", true},
	{"dojo_admin", "UPDATE operators SET revoked_at = now() WHERE false", true},
	{"dojo_admin", "UPDATE operators SET github_id = 2 WHERE false", false},
	{"dojo_admin", "UPDATE operators SET github_login = 'x' WHERE false", false},
	{"dojo_admin", "UPDATE operators SET added_by = NULL WHERE false", false},
	{"dojo_admin", "UPDATE operators SET added_at = now() WHERE false", false},
	{"dojo_admin", "INSERT INTO operators (github_id, github_login, revoked_at) SELECT 1, 'a', now() WHERE false", false},
	{"dojo_admin", "INSERT INTO operators (id, github_id, github_login) SELECT gen_random_uuid(), 1, 'a' WHERE false", false},
	{"dojo_admin", "DELETE FROM operators WHERE false", false},
	{"dojo_admin", "SELECT token_hash, created_at, expires_at, revoked_at FROM admin_sessions", true},
	{"dojo_admin", "INSERT INTO admin_sessions (operator_id, token_hash) SELECT id, '\\x00'::bytea FROM operators WHERE false", true},
	{"dojo_admin", "UPDATE admin_sessions SET revoked_at = now() WHERE false", true},
	{"dojo_admin", "UPDATE admin_sessions SET expires_at = now() WHERE false", false},
	{"dojo_admin", "UPDATE admin_sessions SET token_hash = '\\x00' WHERE false", false},
	{"dojo_admin", "UPDATE admin_sessions SET operator_id = operator_id WHERE false", false},
	{"dojo_admin", "INSERT INTO admin_sessions (operator_id, token_hash, expires_at) SELECT id, '\\x00'::bytea, now() FROM operators WHERE false", false},
	{"dojo_admin", "DELETE FROM admin_sessions WHERE false", false},
	{"dojo_admin", "SELECT operator_login, action, target_type, target_id, reason, before, after, at FROM admin_audit", true},
	{"dojo_admin", "INSERT INTO admin_audit (operator_id, operator_login, action, target_type, target_id, reason, before, after) SELECT id, 'a', 'x', 'y', 'z', 'r', '{}'::jsonb, '{}'::jsonb FROM operators WHERE false", true},
	{"dojo_admin", "INSERT INTO admin_audit (operator_id, operator_login, action, target_type, target_id, at) SELECT id, 'a', 'x', 'y', 'z', now() FROM operators WHERE false", false},
	{"dojo_admin", "UPDATE admin_audit SET reason = 'x' WHERE false", false},
	{"dojo_admin", "UPDATE admin_audit SET before = NULL, after = NULL WHERE false", false},
	{"dojo_admin", "DELETE FROM admin_audit WHERE false", false},
	{"dojo_admin", "INSERT INTO admin_audit (operator_id, operator_login, action, target_type, target_id) SELECT id, 'a', 'x', 'y', 'z' FROM operators WHERE false ON CONFLICT (id) DO UPDATE SET reason = 'x'", false},
	{"dojo_admin", "TRUNCATE admin_audit", false},
	{"dojo_admin", "TRUNCATE operators CASCADE", false},
	{"dojo_admin", "TRUNCATE admin_sessions", false},
	{"dojo_api", "SELECT prune_admin_audit()", false},
	{"dojo_admin", "SELECT prune_admin_audit()", false},

	// email_outbox: both service roles select and insert the queueing columns; id, sent_at and
	// created_at keep defaults; no UPDATE and no DELETE: the sender changes a row only through
	// mark_email_sent(), which dojo_api alone may execute. email_log has no service grant at all.
	{"dojo_api", "SELECT id, learner_id, to_address, kind, ref, sent_at, created_at FROM email_outbox", true},
	{"dojo_api", "INSERT INTO email_outbox (learner_id, to_address, kind, ref) SELECT NULL, 'a@b.c', 'E1', 'x' WHERE false", true},
	{"dojo_api", "INSERT INTO email_outbox (learner_id, to_address, kind, ref, sent_at) SELECT NULL, 'a@b.c', 'E1', 'x', now() WHERE false", false},
	{"dojo_api", "INSERT INTO email_outbox (id, kind, ref) SELECT gen_random_uuid(), 'E1', 'x' WHERE false", false},
	{"dojo_api", "UPDATE email_outbox SET sent_at = now() WHERE false", false},
	{"dojo_api", "UPDATE email_outbox SET to_address = NULL WHERE false", false},
	{"dojo_api", "DELETE FROM email_outbox WHERE false", false},
	{"dojo_api", "TRUNCATE email_outbox", false},
	{"dojo_admin", "SELECT id, learner_id, to_address, kind, ref, sent_at, created_at FROM email_outbox", true},
	{"dojo_admin", "INSERT INTO email_outbox (learner_id, to_address, kind, ref) SELECT NULL, 'a@b.c', 'E1', 'x' WHERE false", true},
	{"dojo_admin", "INSERT INTO email_outbox (learner_id, to_address, kind, ref, created_at) SELECT NULL, 'a@b.c', 'E1', 'x', now() WHERE false", false},
	{"dojo_admin", "UPDATE email_outbox SET sent_at = now() WHERE false", false},
	{"dojo_admin", "DELETE FROM email_outbox WHERE false", false},
	{"dojo_admin", "TRUNCATE email_outbox", false},
	{"dojo_api", "SELECT mark_email_sent('0b0b0b0b-0000-4000-8000-000000000000')", true},
	{"dojo_admin", "SELECT mark_email_sent('0b0b0b0b-0000-4000-8000-000000000000')", false},
	{"dojo_api", "SELECT count(*) FROM email_log", false},
	{"dojo_api", "INSERT INTO email_log (org_id, learner_id, kind) SELECT id, gen_random_uuid(), 'E1' FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE email_log SET kind = 'E2' WHERE false", false},
	{"dojo_api", "DELETE FROM email_log WHERE false", false},
	{"dojo_admin", "SELECT count(*) FROM email_log", false},
	{"dojo_admin", "INSERT INTO email_log (org_id, learner_id, kind) SELECT id, gen_random_uuid(), 'E1' FROM orgs WHERE false", false},
	{"dojo_admin", "DELETE FROM email_log WHERE false", false},
	// events: the API inserts and reads (ON CONFLICT DO NOTHING RETURNING); append-only, no UPDATE or
	// DELETE; id and at keep defaults; no operator access. prune_events() is owner-only.
	{"dojo_api", "SELECT id, learner_id, client_event_id, type, payload, at, client_at FROM events", true},
	{"dojo_api", "INSERT INTO events (org_id, learner_id, client_event_id, type, payload, client_at) SELECT org_id, id, gen_random_uuid(), 'check.run', '{}'::jsonb, now() FROM learners WHERE false ON CONFLICT (learner_id, client_event_id) DO NOTHING RETURNING id", true},
	{"dojo_api", "INSERT INTO events (org_id, learner_id, client_event_id, type, client_at, at) SELECT org_id, id, gen_random_uuid(), 'check.run', now(), now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO events (id, org_id, learner_id, client_event_id, type, client_at) SELECT gen_random_uuid(), org_id, id, gen_random_uuid(), 'check.run', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE events SET payload = '{}'::jsonb WHERE false", false},
	{"dojo_api", "UPDATE events SET type = 'check.run' WHERE false", false},
	{"dojo_api", "DELETE FROM events WHERE false", false},
	{"dojo_api", "TRUNCATE events", false},
	{"dojo_admin", "SELECT count(*) FROM events", false},
	{"dojo_admin", "INSERT INTO events (org_id, learner_id, client_event_id, type, client_at) SELECT org_id, id, gen_random_uuid(), 'check.run', now() FROM learners WHERE false", false},
	{"dojo_admin", "UPDATE events SET payload = '{}'::jsonb WHERE false", false},
	{"dojo_admin", "DELETE FROM events WHERE false", false},
	{"dojo_api", "SELECT prune_events()", false},
	{"dojo_admin", "SELECT prune_events()", false},

	// hosted_default (migration 00020): the API updates it and never inserts it; the operator service only reads.
	{"dojo_api", "UPDATE learners SET hosted_default = true, updated_at = now() WHERE false", true},
	{"dojo_api", "SELECT hosted_default FROM learners", true},
	{"dojo_api", "INSERT INTO learners (org_id, github_id, github_login, username, age_confirmed_at, terms_accepted_at, terms_version, hosted_default) SELECT id, 1, 'a', 'abc', now(), now(), 'x', true FROM orgs WHERE false", false},
	{"dojo_admin", "SELECT hosted_default FROM learners", true},
	{"dojo_admin", "UPDATE learners SET hosted_default = true WHERE false", false},
	// doctor_results: the API reads, inserts and updates the projected columns, and deletes (the
	// unconfirm route); identity columns and created_at never change from it. dojo_admin only reads.
	{"dojo_api", "SELECT learner_id, check_id, created_at, updated_at, status, text, fix, source, checked_at FROM doctor_results", true},
	{"dojo_api", "INSERT INTO doctor_results (org_id, learner_id, check_id, status, text, fix, source, checked_at) SELECT org_id, id, 'memory', 'ok', 't', '', 'cli', now() FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO doctor_results (org_id, learner_id, check_id, status, text, fix, source, checked_at) SELECT org_id, id, 'memory', 'ok', 't', '', 'cli', now() FROM learners WHERE false ON CONFLICT (learner_id, check_id) DO UPDATE SET updated_at = now(), status = excluded.status, text = excluded.text, fix = excluded.fix, source = excluded.source, checked_at = excluded.checked_at WHERE doctor_results.checked_at <= excluded.checked_at", true},
	{"dojo_api", "UPDATE doctor_results SET updated_at = now(), status = 'ok', text = 't', fix = '', source = 'cli', checked_at = now() WHERE false", true},
	{"dojo_api", "DELETE FROM doctor_results WHERE false", true},
	{"dojo_api", "INSERT INTO doctor_results (org_id, learner_id, check_id, status, text, source, checked_at, created_at) SELECT org_id, id, 'memory', 'ok', 't', 'cli', now(), now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE doctor_results SET check_id = 'disk' WHERE false", false},
	{"dojo_api", "UPDATE doctor_results SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE doctor_results SET created_at = now() WHERE false", false},
	{"dojo_api", "TRUNCATE doctor_results", false},
	{"dojo_admin", "SELECT learner_id, check_id, status, text, fix, source, checked_at FROM doctor_results", true},
	{"dojo_admin", "INSERT INTO doctor_results (org_id, learner_id, check_id, status, text, source, checked_at) SELECT org_id, id, 'memory', 'ok', 't', 'cli', now() FROM learners WHERE false", false},
	{"dojo_admin", "UPDATE doctor_results SET status = 'ok' WHERE false", false},
	{"dojo_admin", "DELETE FROM doctor_results WHERE false", false},

	// ticket_progress: the API reads, inserts and updates the projected columns; identity columns,
	// created_at and passed_commit (M3) never change from it; no DELETE. dojo_admin only reads (A2).
	{"dojo_api", "SELECT learner_id, item_id, state, lab, lab_state, lab_state_at, first_started_at, last_seen_at, check_count, last_check_at, last_check_ok, last_check_passed, last_check_total, last_check_output, first_passed_at, passed_seconds, passed_commit FROM ticket_progress", true},
	{"dojo_api", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, lab, lab_state, lab_state_at, first_started_at, last_seen_at, check_count, last_check_at, last_check_ok, last_check_passed, last_check_total, last_check_output, first_passed_at, passed_seconds) SELECT org_id, id, 'a/b', 'started', 'local', 'starting', now(), now(), now(), 0, now(), true, 1, 2, 'x', now(), 1 FROM learners WHERE false", true},
	{"dojo_api", "UPDATE ticket_progress SET updated_at = now(), state = 'passed', lab = 'local', lab_state = 'running', lab_state_at = now(), first_started_at = now(), last_seen_at = now(), check_count = 1, last_check_at = now(), last_check_ok = true, last_check_passed = 1, last_check_total = 1, last_check_output = 'x', first_passed_at = now(), passed_seconds = 1 WHERE false", true},
	{"dojo_api", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at, passed_commit) SELECT org_id, id, 'a/b', 'started', now(), 'abc' FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at, created_at) SELECT org_id, id, 'a/b', 'started', now(), now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE ticket_progress SET updated_at = now(), passed_commit = repeat('a', 40), commit_state = 'pending', commit_tries = 0, commit_at = now(), commit_next_at = now(), commit_checked_at = now() WHERE false", true},
	{"dojo_api", "SELECT commit_state, commit_tries, commit_next_at, commit_checked_at, commit_at FROM ticket_progress", true},
	{"dojo_api", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at, commit_state) SELECT org_id, id, 'a/b', 'started', now(), 'pending' FROM learners WHERE false", false},

	// pending_commits (migration 00021): the API reads, inserts the five columns and deletes; a repeat is
	// ON CONFLICT DO NOTHING, so there is no UPDATE; the operator service only reads. prune_pending_commits() is owner-only.
	{"dojo_api", "SELECT id, learner_id, item_id, \"commit\", client_at, created_at FROM pending_commits", true},
	{"dojo_api", "INSERT INTO pending_commits (org_id, learner_id, item_id, \"commit\", client_at) SELECT org_id, id, 'a/b', repeat('a', 40), now() FROM learners WHERE false ON CONFLICT (learner_id, item_id) DO NOTHING", true},
	{"dojo_api", "DELETE FROM pending_commits WHERE false", true},
	{"dojo_api", "UPDATE pending_commits SET \"commit\" = repeat('b', 40) WHERE false", false},
	{"dojo_api", "INSERT INTO pending_commits (org_id, learner_id, item_id, \"commit\", client_at, created_at) SELECT org_id, id, 'a/b', repeat('a', 40), now(), now() FROM learners WHERE false", false},
	{"dojo_api", "TRUNCATE pending_commits", false},
	{"dojo_admin", "SELECT learner_id, item_id, \"commit\" FROM pending_commits", true},
	{"dojo_admin", "INSERT INTO pending_commits (org_id, learner_id, item_id, \"commit\", client_at) SELECT org_id, id, 'a/b', repeat('a', 40), now() FROM learners WHERE false", false},
	{"dojo_admin", "DELETE FROM pending_commits WHERE false", false},
	// ticket_reports (migration 00022): the API reads, inserts the eight columns and updates emailed_at (also what
	// FOR UPDATE SKIP LOCKED needs); id, timestamps and the rest never come from it; no DELETE. The operator service only reads.
	{"dojo_api", "SELECT id, org_id, learner_id, item_id, kind, cli_version, last_check_output, message, doctor_summary, emailed_at, created_at, updated_at FROM ticket_reports", true},
	{"dojo_api", "INSERT INTO ticket_reports (org_id, learner_id, item_id, kind, cli_version, last_check_output, message, doctor_summary) SELECT org_id, id, 'a/b', 'broken_ticket', 'v', 'o', 'm', 's' FROM learners WHERE false", true},
	{"dojo_api", "UPDATE ticket_reports SET updated_at = now(), emailed_at = now() WHERE false", true},
	{"dojo_api", "SELECT id FROM ticket_reports WHERE false FOR UPDATE SKIP LOCKED", true},
	{"dojo_api", "SELECT id FROM learners WHERE false FOR UPDATE", true},
	{"dojo_api", "INSERT INTO ticket_reports (org_id, learner_id, kind, message, emailed_at) SELECT org_id, id, 'setup_help', 'm', now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO ticket_reports (id, org_id, learner_id, kind, message) SELECT gen_random_uuid(), org_id, id, 'setup_help', 'm' FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO ticket_reports (org_id, learner_id, kind, message, created_at) SELECT org_id, id, 'setup_help', 'm', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE ticket_reports SET message = 'x' WHERE false", false},
	{"dojo_api", "UPDATE ticket_reports SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE ticket_reports SET doctor_summary = 'x' WHERE false", false},
	{"dojo_api", "UPDATE ticket_reports SET created_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM ticket_reports WHERE false", false},
	{"dojo_api", "TRUNCATE ticket_reports", false},
	{"dojo_admin", "SELECT learner_id, item_id, kind, cli_version, last_check_output, message, doctor_summary, emailed_at FROM ticket_reports", true},
	{"dojo_admin", "INSERT INTO ticket_reports (org_id, learner_id, kind, message) SELECT org_id, id, 'setup_help', 'm' FROM learners WHERE false", false},
	{"dojo_admin", "UPDATE ticket_reports SET emailed_at = now() WHERE false", false},
	{"dojo_admin", "DELETE FROM ticket_reports WHERE false", false},
	{"dojo_api", "SELECT prune_pending_commits()", false},
	{"dojo_admin", "SELECT prune_pending_commits()", false},
	{"dojo_api", "UPDATE ticket_progress SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE ticket_progress SET item_id = 'x' WHERE false", false},
	{"dojo_api", "UPDATE ticket_progress SET org_id = org_id WHERE false", false},
	{"dojo_api", "UPDATE ticket_progress SET created_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM ticket_progress WHERE false", false},
	{"dojo_api", "TRUNCATE ticket_progress", false},
	{"dojo_admin", "SELECT learner_id, item_id, state, lab_state, last_seen_at, last_check_ok, last_check_output, first_passed_at, passed_seconds FROM ticket_progress", true},
	{"dojo_admin", "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) SELECT org_id, id, 'a/b', 'started', now() FROM learners WHERE false", false},
	{"dojo_admin", "UPDATE ticket_progress SET state = 'passed' WHERE false", false},
	{"dojo_admin", "DELETE FROM ticket_progress WHERE false", false},

	// hint_opens: the API reads and inserts (opened_at keeps its default); no UPDATE, no DELETE; no operator access.
	{"dojo_api", "SELECT learner_id, item_id, n, opened_at FROM hint_opens", true},
	{"dojo_api", "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, 'a/b', 1 FROM learners WHERE false ON CONFLICT DO NOTHING", true},
	{"dojo_api", "INSERT INTO hint_opens (org_id, learner_id, item_id, n, opened_at) SELECT org_id, id, 'a/b', 1, now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE hint_opens SET n = 2 WHERE false", false},
	{"dojo_api", "UPDATE hint_opens SET opened_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM hint_opens WHERE false", false},
	{"dojo_api", "TRUNCATE hint_opens", false},
	{"dojo_admin", "SELECT count(*) FROM hint_opens", false},
	{"dojo_admin", "INSERT INTO hint_opens (org_id, learner_id, item_id, n) SELECT org_id, id, 'a/b', 1 FROM learners WHERE false", false},
	{"dojo_admin", "DELETE FROM hint_opens WHERE false", false},

	// delete_learner() is the one way an account goes: executable by both service roles, and an
	// unknown id is a no-op. Its EXECUTE list is also asserted in delete_learner_integration_test.go.
	{"dojo_api", "SELECT delete_learner(gen_random_uuid())", true},
	{"dojo_admin", "SELECT delete_learner(gen_random_uuid())", true},

	// Catalog tables (migration 00012): content written only by the load job as the migration role.
	// dojo_api reads; dojo_admin has no grant until a screen joins them.
	{"dojo_api", "SELECT id, slug, title, status, position FROM paths", true},
	{"dojo_api", "SELECT id, slug, title, kind, has_exam, advanced FROM roles", true},
	{"dojo_api", "SELECT path_id, role_id, position FROM path_roles", true},
	{"dojo_api", "SELECT id, role_id, kind, position, title, est_minutes, tags, brief, hints, awards_mark, payload, difficulty, catalog_version, bundle_key, bundle_sha256, bundle_bytes FROM catalog_items", true},
	{"dojo_api", "SELECT id, catalog_version FROM catalog_state", true},
	{"dojo_api", "INSERT INTO paths (org_id, slug, title, status, position) SELECT id, 'x', 'X', 'planned', 1 FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE paths SET title = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM paths WHERE false", false},
	{"dojo_api", "TRUNCATE paths CASCADE", false},
	{"dojo_api", "INSERT INTO roles (org_id, slug, title, kind, has_exam) SELECT id, 'x', 'X', 'role', false FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE roles SET title = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM roles WHERE false", false},
	{"dojo_api", "TRUNCATE roles CASCADE", false},
	{"dojo_api", "INSERT INTO path_roles (org_id, path_id, role_id, position) SELECT id, gen_random_uuid(), gen_random_uuid(), 1 FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE path_roles SET position = 2 WHERE false", false},
	{"dojo_api", "DELETE FROM path_roles WHERE false", false},
	{"dojo_api", "TRUNCATE path_roles", false},
	{"dojo_api", "INSERT INTO catalog_items (id, org_id, kind, position, title, est_minutes, catalog_version) SELECT 'a/b', id, 'practice', 1, 'X', 1, '000000000000' FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE catalog_items SET payload = '{}' WHERE false", false},
	{"dojo_api", "UPDATE catalog_items SET difficulty = 'easy' WHERE false", false},
	{"dojo_api", "UPDATE catalog_items SET title = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM catalog_items WHERE false", false},
	{"dojo_api", "TRUNCATE catalog_items", false},
	{"dojo_api", "INSERT INTO catalog_state (id, org_id, catalog_version) SELECT 1, id, '000000000000' FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE catalog_state SET catalog_version = '000000000000' WHERE false", false},
	{"dojo_api", "DELETE FROM catalog_state WHERE false", false},
	{"dojo_api", "TRUNCATE catalog_state", false},
	// dojo_admin (A2 progress view): reads roles and catalog_items, nothing else of the catalog.
	{"dojo_admin", "SELECT count(*) FROM paths", false},
	{"dojo_admin", "SELECT id, title, kind FROM roles", true},
	{"dojo_admin", "SELECT count(*) FROM path_roles", false},
	{"dojo_admin", "SELECT id, role_id, kind, title FROM catalog_items", true},
	{"dojo_admin", "SELECT count(*) FROM catalog_state", false},
	{"dojo_admin", "INSERT INTO paths (org_id, slug, title, status, position) SELECT id, 'x', 'X', 'planned', 1 FROM orgs WHERE false", false},
	{"dojo_admin", "UPDATE roles SET title = 'x' WHERE false", false},
	{"dojo_admin", "DELETE FROM roles WHERE false", false},
	{"dojo_admin", "UPDATE catalog_items SET title = 'x' WHERE false", false},
	{"dojo_admin", "DELETE FROM catalog_items WHERE false", false},
	{"dojo_admin", "TRUNCATE catalog_items", false},
	{"dojo_admin", "DELETE FROM catalog_state WHERE false", false},

	// belt_marks and item_completions (migration 00025): the load writes marks as the migration role; the API
	// inserts a completion once, never changes or removes it; dojo_admin reads both (A2 progress).
	{"dojo_api", "SELECT id, slug, belt, title, summary, position FROM belt_marks", true},
	{"dojo_api", "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'x', 'white', 'X', 'X', 1 FROM orgs WHERE false", false},
	{"dojo_api", "UPDATE belt_marks SET title = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM belt_marks WHERE false", false},
	{"dojo_api", "TRUNCATE belt_marks", false},
	{"dojo_api", "SELECT learner_id, item_id, completed_at FROM item_completions", true},
	{"dojo_api", "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, 'a/b' FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO item_completions (org_id, learner_id, item_id, completed_at) SELECT org_id, id, 'a/b', now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO item_completions (org_id, learner_id, item_id, created_at) SELECT org_id, id, 'a/b', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE item_completions SET updated_at = now() WHERE false", false},
	{"dojo_api", "UPDATE item_completions SET completed_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM item_completions WHERE false", false},
	{"dojo_api", "TRUNCATE item_completions", false},
	{"dojo_admin", "SELECT slug, belt, title, summary, position FROM belt_marks", true},
	{"dojo_admin", "SELECT learner_id, item_id, completed_at FROM item_completions", true},
	{"dojo_admin", "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, 'x', 'white', 'X', 'X', 1 FROM orgs WHERE false", false},
	{"dojo_admin", "UPDATE belt_marks SET title = 'x' WHERE false", false},
	{"dojo_admin", "INSERT INTO item_completions (org_id, learner_id, item_id) SELECT org_id, id, 'a/b' FROM learners WHERE false", false},
	{"dojo_admin", "UPDATE item_completions SET updated_at = now() WHERE false", false},
	{"dojo_admin", "DELETE FROM item_completions WHERE false", false},

	// quiz_answers and story_choices (migration 00026): the first answer never changes, a story choice is final,
	// dojo_admin has no grant.
	{"dojo_api", "SELECT learner_id, item_id, question_id, first_choice, first_correct, first_at, last_choice, last_correct, last_at FROM quiz_answers", true},
	{"dojo_api", "INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, first_at, last_choice, last_correct, last_at) SELECT org_id, id, 'a/b', 'q', 0, true, now(), 0, true, now() FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, last_choice, last_correct, created_at) SELECT org_id, id, 'a/b', 'q', 0, true, 0, true, now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE quiz_answers SET updated_at = now(), last_choice = 1, last_correct = false, last_at = now() WHERE false", true},
	{"dojo_api", "UPDATE quiz_answers SET first_choice = 1 WHERE false", false},
	{"dojo_api", "UPDATE quiz_answers SET first_correct = true WHERE false", false},
	{"dojo_api", "UPDATE quiz_answers SET first_at = now() WHERE false", false},
	{"dojo_api", "UPDATE quiz_answers SET question_id = 'x' WHERE false", false},
	{"dojo_api", "UPDATE quiz_answers SET item_id = 'x' WHERE false", false},
	{"dojo_api", "DELETE FROM quiz_answers WHERE false", false},
	{"dojo_api", "TRUNCATE quiz_answers", false},
	{"dojo_api", "SELECT learner_id, item_id, choice_id, chosen_at FROM story_choices", true},
	{"dojo_api", "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id) SELECT org_id, id, 'a/b', 'c' FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id, chosen_at) SELECT org_id, id, 'a/b', 'c', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE story_choices SET choice_id = 'x' WHERE false", false},
	{"dojo_api", "UPDATE story_choices SET updated_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM story_choices WHERE false", false},
	{"dojo_api", "TRUNCATE story_choices", false},
	{"dojo_admin", "SELECT count(*) FROM quiz_answers", false},
	{"dojo_admin", "SELECT count(*) FROM story_choices", false},
	{"dojo_admin", "INSERT INTO quiz_answers (org_id) SELECT id FROM orgs WHERE false", false},
	{"dojo_admin", "DELETE FROM story_choices WHERE false", false},

	// inbox_items (migration 00028): the API reads, inserts and updates the named columns, never deletes;
	// dojo_admin has nothing.
	{"dojo_api", "SELECT id, learner_id, kind, ref, title, body, url, read_at, push_state, push_tries, push_next_at, pushed_at, created_at, updated_at FROM inbox_items", true},
	{"dojo_api", "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, push_state, push_next_at) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b', 'pending', now() FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, id) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b', gen_random_uuid() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, read_at) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b', now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, updated_at) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b', now() FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, pushed_at) SELECT org_id, id, 'check', 'a/b', 't', 'b', '/t/a/b', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE inbox_items SET updated_at = now(), title = 't', body = 'b', url = '/x', read_at = now(), push_state = 'sent', push_tries = 1, push_next_at = now(), pushed_at = now() WHERE false", true},
	{"dojo_api", "UPDATE inbox_items SET kind = 'quiz' WHERE false", false},
	{"dojo_api", "UPDATE inbox_items SET ref = 'x' WHERE false", false},
	{"dojo_api", "UPDATE inbox_items SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE inbox_items SET id = id WHERE false", false},
	{"dojo_api", "UPDATE inbox_items SET created_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM inbox_items WHERE false", false},
	{"dojo_api", "TRUNCATE inbox_items", false},
	{"dojo_admin", "SELECT count(*) FROM inbox_items", false},
	{"dojo_admin", "INSERT INTO inbox_items (org_id) SELECT id FROM orgs WHERE false", false},
	{"dojo_admin", "UPDATE inbox_items SET read_at = now() WHERE false", false},
	{"dojo_admin", "DELETE FROM inbox_items WHERE false", false},
	// system_flags, written_work and ai_reviews (migration 00027): flags are read-only for both services until A3;
	// the review worker writes written_work and ai_reviews as dojo_api; dojo_admin has no grant on those two;
	// prune_ai_reviews() is owner-only.
	{"dojo_api", "SELECT key, value, updated_at FROM system_flags", true},
	{"dojo_admin", "SELECT key, value, updated_at FROM system_flags", true},
	{"dojo_api", "INSERT INTO system_flags (key, value) VALUES ('x', 'true')", false},
	{"dojo_api", "UPDATE system_flags SET value = 'false' WHERE false", false},
	{"dojo_api", "DELETE FROM system_flags WHERE false", false},
	{"dojo_admin", "INSERT INTO system_flags (key, value) VALUES ('x', 'true')", false},
	{"dojo_admin", "UPDATE system_flags SET value = 'false' WHERE false", false},
	{"dojo_api", "SELECT learner_id, item_id, draft, draft_saved_at, body, first_submitted_at, submitted_at, submit_count, review_state, review, review_tries, review_next_at, reviewed_at FROM written_work", true},
	{"dojo_api", "INSERT INTO written_work (org_id, learner_id, item_id, draft, draft_saved_at, body, first_submitted_at, submitted_at, submit_count, review_state, review_next_at) SELECT org_id, id, 'a/b', '', now(), 'x', now(), now(), 1, 'pending', now() FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO written_work (org_id, learner_id, item_id, review) SELECT org_id, id, 'a/b', '{}' FROM learners WHERE false", false},
	{"dojo_api", "INSERT INTO written_work (org_id, learner_id, item_id, review_tries) SELECT org_id, id, 'a/b', 1 FROM learners WHERE false", false},
	{"dojo_api", "UPDATE written_work SET updated_at = now(), draft = 'x', draft_saved_at = now(), body = 'x', first_submitted_at = now(), submitted_at = now(), submit_count = 2, review_state = 'done', review = '{}', review_tries = 1, review_next_at = now(), reviewed_at = now() WHERE false", true},
	{"dojo_api", "UPDATE written_work SET item_id = 'x' WHERE false", false},
	{"dojo_api", "UPDATE written_work SET learner_id = learner_id WHERE false", false},
	{"dojo_api", "UPDATE written_work SET created_at = now() WHERE false", false},
	{"dojo_api", "DELETE FROM written_work WHERE false", false},
	{"dojo_api", "TRUNCATE written_work", false},
	{"dojo_admin", "SELECT count(*) FROM written_work", false},
	{"dojo_admin", "INSERT INTO written_work (org_id) SELECT id FROM orgs WHERE false", false},
	{"dojo_admin", "DELETE FROM written_work WHERE false", false},
	{"dojo_api", "SELECT id, learner_id, item_id, created_at, submit_count, model, tokens_in, tokens_out, outcome FROM ai_reviews", true},
	{"dojo_api", "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome) SELECT org_id, id, 'a/b', 1, 'standin', 1, 1, 'ok' FROM learners WHERE false", true},
	{"dojo_api", "INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome, created_at) SELECT org_id, id, 'a/b', 1, 'standin', 1, 1, 'ok', now() FROM learners WHERE false", false},
	{"dojo_api", "UPDATE ai_reviews SET outcome = 'ok' WHERE false", false},
	{"dojo_api", "DELETE FROM ai_reviews WHERE false", false},
	{"dojo_api", "TRUNCATE ai_reviews", false},
	{"dojo_admin", "SELECT count(*) FROM ai_reviews", false},
	{"dojo_admin", "INSERT INTO ai_reviews (org_id) SELECT id FROM orgs WHERE false", false},
	{"dojo_api", "SELECT prune_ai_reviews()", false},
	{"dojo_admin", "SELECT prune_ai_reviews()", false},
}

// Connects by the container address: the image trusts loopback, other addresses use scram.
func TestRoleGrants(t *testing.T) {
	passwords := map[string]string{
		"dojo_api":   envExample(t, "DOJO_API_DB_PASSWORD"),
		"dojo_admin": envExample(t, "DOJO_ADMIN_DB_PASSWORD"),
	}
	for role, pw := range passwords {
		t.Run(role+"/wrong password refused", func(t *testing.T) {
			if out, err := psqlAs(t, role, "wrong-"+pw, "SELECT 1"); err == nil {
				t.Fatalf("login with a wrong password succeeded: %s", out)
			}
		})
	}
	for _, c := range grantCases {
		t.Run(c.role+"/"+c.sql, func(t *testing.T) {
			out, err := psqlAs(t, c.role, passwords[c.role], c.sql)
			switch {
			case c.allowed && err != nil:
				t.Fatalf("want allowed, got %v %q", err, out)
			case !c.allowed && (err == nil || !strings.Contains(out, "permission denied")):
				t.Fatalf("want permission denied, got %v %q", err, out)
			}
		})
	}
}

// The running API must connect as dojo_api, not as the migration role.
func TestAPIConnectsAsDojoAPI(t *testing.T) {
	if code, _ := do(t, http.MethodGet, apiURL(t)+"/v1/health"); code != http.StatusOK {
		t.Fatalf("health: %d", code)
	}
	out, err := psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"),
		"SELECT DISTINCT usename FROM pg_stat_activity WHERE application_name = 'dojo-api'")
	if err != nil || out != "dojo_api" {
		t.Fatalf("api connections by role: %v %q, want only dojo_api", err, out)
	}
}
