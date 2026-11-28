//go:build integration

package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"testing"
)

const wipeCatalog = "DELETE FROM hint_opens; DELETE FROM item_completions; DELETE FROM quiz_answers; DELETE FROM story_choices; DELETE FROM written_work; DELETE FROM ticket_progress; DELETE FROM catalog_items; DELETE FROM belt_awards; DELETE FROM belt_marks; " +
	"DELETE FROM path_roles; DELETE FROM roles; DELETE FROM paths; DELETE FROM catalog_state"

// freshCatalog empties the catalog tables before and after the test (the stack is not shared with a load job here).
func freshCatalog(t *testing.T) {
	t.Helper()
	asOwner(t, wipeCatalog)
	t.Cleanup(func() { asOwner(t, wipeCatalog) })
}

// seedCatalog loads one available path (foundations, support-l1, systems-l2), one planned path and
// four items: two foundations tickets, a foundations quiz and one support-l1 ticket.
func seedCatalog(t *testing.T) {
	t.Helper()
	org := "(SELECT id FROM orgs WHERE slug = 'public')"
	asOwner(t, `
INSERT INTO paths (org_id, slug, title, status, position) VALUES
  (`+org+`, 'systems-devops', 'Systems and DevOps', 'available', 1),
  (`+org+`, 'security', 'Security', 'planned', 2);
INSERT INTO roles (org_id, slug, title, summary, kind, has_exam) VALUES
  (`+org+`, 'foundations', 'Foundations', '', 'foundations', false),
  (`+org+`, 'support-l1', 'Support L1', 'Tickets, users, permissions, a calm reply', 'role', true),
  (`+org+`, 'systems-l2', 'Systems L2', '', 'role', true);
INSERT INTO path_roles (org_id, path_id, role_id, position)
  SELECT `+org+`, p.id, r.id, v.pos FROM (VALUES
    ('systems-devops', 'foundations', 1), ('systems-devops', 'support-l1', 2),
    ('systems-devops', 'systems-l2', 3), ('security', 'foundations', 1)) AS v(p, r, pos)
  JOIN paths p ON p.slug = v.p JOIN roles r ON r.slug = v.r;
INSERT INTO catalog_items (id, org_id, role_id, kind, position, title, est_minutes, tags, brief, hints, bundle_key, bundle_sha256, bundle_bytes, catalog_version, payload)
  SELECT v.id, `+org+`, r.id, v.kind, v.pos, v.title, v.mins, v.tags::text[],
         CASE WHEN v.kind = 'ticket' THEN 'secret brief' END, CASE WHEN v.kind = 'ticket' THEN ARRAY['secret hint'] END,
         CASE WHEN v.kind = 'ticket' THEN 'bundles/x.tar.gz' END,
         CASE WHEN v.kind = 'ticket' THEN repeat('a', 64) END, CASE WHEN v.kind = 'ticket' THEN 100 END, 'a1b2c3d4e5f6',
         CASE WHEN v.kind <> 'ticket' THEN '{}'::jsonb END
  FROM (VALUES
    ('foundations/a', 'foundations', 'ticket', 1, 'Find the log', 10, '{terminal}'),
    ('foundations/b', 'foundations', 'ticket', 2, 'Free the disk', 15, '{}'),
    ('foundations/q', 'foundations', 'quiz', 3, 'Quiz', 5, '{}'),
    ('support-l1/c', 'support-l1', 'ticket', 1, 'Fix the folder', 20, '{permissions}')
  ) AS v(id, role, kind, pos, title, mins, tags) JOIN roles r ON r.slug = v.role;
INSERT INTO catalog_state (id, org_id, catalog_version) VALUES (1, `+org+`, 'a1b2c3d4e5f6')`)
}

func setProgress(t *testing.T, item, state, lab string) {
	t.Helper()
	labv := "NULL"
	if lab != "" {
		labv = "'" + lab + "'"
	}
	passed := "NULL"
	if state != "started" {
		passed = "now()"
	}
	asOwner(t, "INSERT INTO ticket_progress (org_id, learner_id, item_id, state, lab_state, first_started_at, first_passed_at) "+
		"SELECT org_id, id, '"+item+"', '"+state+"', "+labv+", now(), "+passed+" FROM learners WHERE github_id = 1001")
}

func getAs(t *testing.T, tok, path string) (int, string, http.Header) {
	t.Helper()
	req, _ := http.NewRequest(http.MethodGet, apiURL(t)+path, nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := newBrowser(t).c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), resp.Header
}

func TestCatalogEmptyIntegration(t *testing.T) {
	freshCatalog(t)
	resetLearners(t, "1001")
	signUp(t, "ada")
	tok := cliToken(t, newCLIClient(t), "ada")
	code, body, h := getAs(t, tok, "/v1/catalog")
	if code != 200 || body != `{"catalog_version":null,"paths":[],"roles":[],"path_roles":[],"items":[],"marks":[]}`+"\n" || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("catalog: %d %q %q", code, body, h.Get("Cache-Control"))
	}
	if code, body, _ := getAs(t, tok, "/v1/path"); code != 200 || body != `{"path":null,"chosen":false,"roles":[]}`+"\n" {
		t.Fatalf("path: %d %q", code, body)
	}
}

func TestCatalogPopulatedIntegration(t *testing.T) {
	freshCatalog(t)
	seedCatalog(t)
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) SELECT id, v.s, v.b, v.t, 'sum', v.p FROM orgs, "+
		"(VALUES ('tidy-shell', 'yellow', 'Tidy shell', 1), ('feedback-round', 'white', 'Feedback round', 2), ('written-english', 'white', 'Written English', 1)) "+
		"AS v(s, b, t, p) WHERE slug = 'public'")
	resetLearners(t, "1001")
	signUp(t, "ada")
	tok := cliToken(t, newCLIClient(t), "ada")
	code, body, _ := getAs(t, tok, "/v1/catalog")
	var got struct {
		Marks []struct {
			Slug, Belt string
			Position   int
		}
		Version  string `json:"catalog_version"`
		Paths    []struct{ Slug string }
		Roles    []struct{ Slug, Summary string }
		PathRole []map[string]any `json:"path_roles"`
		Items    []struct {
			ID, Role, Kind, Title string
			Order                 int
			EstMinutes            int `json:"est_minutes"`
			Tags                  []string
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 {
		t.Fatalf("%d %v %s", code, err, body)
	}
	if got.Version != "a1b2c3d4e5f6" || len(got.Paths) != 2 || got.Paths[0].Slug != "systems-devops" || len(got.PathRole) != 4 || len(got.Items) != 4 {
		t.Fatalf("shape: %s", body)
	}
	if len(got.Roles) != 3 || got.Roles[1].Slug != "support-l1" || got.Roles[1].Summary != "Tickets, users, permissions, a calm reply" || got.Roles[0].Summary != "" {
		t.Fatalf("role summaries: %+v", got.Roles)
	}
	if i := got.Items[0]; i.ID != "foundations/a" || i.Order != 1 || i.EstMinutes != 10 || len(i.Tags) != 1 || i.Tags[0] != "terminal" {
		t.Fatalf("first item: %+v", i)
	}
	if len(got.Marks) != 3 || got.Marks[0].Slug != "written-english" || got.Marks[1].Slug != "feedback-round" || got.Marks[2].Belt != "yellow" || got.Marks[1].Position != 2 {
		t.Fatalf("marks in belt order: %+v", got.Marks)
	}
	if strings.Contains(body, "secret") || strings.Contains(body, "bundles/") {
		t.Fatalf("catalog leaks ticket content: %s", body)
	}
}

func TestPathProgressAndFoundationsRuleIntegration(t *testing.T) {
	freshCatalog(t)
	seedCatalog(t)
	resetLearners(t, "1001", "1002")
	signUp(t, "ada")
	signUp(t, "bob")
	tok := cliToken(t, newCLIClient(t), "ada")
	bobTok := cliToken(t, newCLIClient(t), "bob")

	type role struct {
		Slug, Summary  string
		Locked, Passed bool
		TicketsPassed  int `json:"tickets_passed"`
		TicketsTotal   int `json:"tickets_total"`
		Items          []struct {
			ID       string
			State    *string
			LabState *string `json:"lab_state"`
		}
	}
	path := func(tok string) (string, bool, []role) {
		code, body, _ := getAs(t, tok, "/v1/path")
		var got struct {
			Path   string
			Chosen bool
			Roles  []role
		}
		if err := json.Unmarshal([]byte(body), &got); err != nil || code != 200 {
			t.Fatalf("%d %v %s", code, err, body)
		}
		return got.Path, got.Chosen, got.Roles
	}

	p, chosen, roles := path(tok)
	if _, _, rs := path(tok); rs[1].Summary != "Tickets, users, permissions, a calm reply" || rs[0].Summary != "" {
		t.Fatalf("path role summaries: %+v", rs)
	}
	if p != "systems-devops" || chosen || len(roles) != 3 || roles[0].Passed || roles[0].TicketsTotal != 2 || roles[0].Items[0].State != nil {
		t.Fatalf("no progress: %s %v %+v", p, chosen, roles)
	}
	if roles[0].Locked || roles[1].Locked || !roles[2].Locked || roles[2].TicketsTotal != 0 {
		t.Fatalf("locked: %+v", roles)
	}

	setProgress(t, "foundations/a", "passed", "stopped")
	setProgress(t, "foundations/b", "started", "running")
	setProgress(t, "foundations/q", "passed", "") // a quiz never counts
	_, _, roles = path(tok)
	if roles[0].Passed || roles[0].TicketsPassed != 1 || *roles[0].Items[0].State != "passed" || *roles[0].Items[0].LabState != "stopped" ||
		*roles[0].Items[1].State != "started" || *roles[0].Items[1].LabState != "running" {
		t.Fatalf("partial: %+v", roles[0])
	}
	// Another learner's progress is not visible.
	if _, _, br := path(bobTok); br[0].TicketsPassed != 0 || br[0].Items[0].State != nil {
		t.Fatalf("bob sees ada's progress: %+v", br[0])
	}

	// Foundations passes when every ticket is passed or revisit; an exam role never does by tickets.
	asOwner(t, "UPDATE ticket_progress SET state = 'revisit', first_passed_at = now() WHERE item_id = 'foundations/b'")
	setProgress(t, "support-l1/c", "passed", "")
	_, _, roles = path(tok)
	if !roles[0].Passed || roles[0].TicketsPassed != 2 || roles[1].Passed || roles[1].TicketsPassed != 1 {
		t.Fatalf("passed: %+v", roles)
	}
}

func TestCatalogRoutesSuspendedLearnerIntegration(t *testing.T) {
	freshCatalog(t)
	seedCatalog(t)
	resetLearners(t, "1001")
	signUp(t, "ada")
	tok := cliToken(t, newCLIClient(t), "ada")
	if code, _, _ := getAs(t, "", "/v1/catalog"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
	asOwner(t, "UPDATE learners SET suspended_at = now() WHERE github_id = 1001")
	for _, route := range []string{"/v1/catalog", "/v1/path"} {
		if code, body, _ := getAs(t, tok, route); code != http.StatusForbidden || body != "{\"error\":\"account_suspended\"}\n" {
			t.Fatalf("%s suspended: %d %q", route, code, body)
		}
	}
}
