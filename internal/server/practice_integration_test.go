//go:build integration

package server_test

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// practiceFixture adds two practice items to the seeded catalog and signs ada up.
func practiceFixture(t *testing.T) (c *cliClient, tok string) {
	t.Helper()
	c, tok = ticketFixture(t)
	asOwner(t, "INSERT INTO catalog_items (id, org_id, kind, position, title, est_minutes, tags, brief, hints, bundle_key, bundle_sha256, bundle_bytes, catalog_version, difficulty) "+
		"SELECT v.id, o.id, 'practice', v.pos, v.title, 20, '{storage}', 'practice brief', ARRAY['p1','p2'], 'bundles/' || v.id || '/x.tar.gz', repeat('b', 64), 100, 'a1b2c3d4e5f6', v.diff "+
		"FROM orgs o, (VALUES ('practice/disk-full', 1, 'Disk full', 'medium'), ('practice/lost-key', 2, 'Lost key', NULL)) AS v(id, pos, title, diff) WHERE o.slug = 'public'")
	return c, tok
}

func TestPracticeIsTicketShapedIntegration(t *testing.T) {
	c, tok := practiceFixture(t)
	id := "practice/disk-full"

	code, body, _ := getAs(t, tok, "/v1/tickets/"+id)
	if code != 200 || !strings.Contains(body, `"role":null`) || !strings.Contains(body, `"hint_count":2`) {
		t.Fatalf("read: %d %s", code, body)
	}
	if code, body := openHint(c, tok, id, "1"); code != 200 || strings.TrimSpace(body) != `{"n":1,"text":"p1"}` {
		t.Fatalf("hint: %d %q", code, body)
	}
	if code, body, _ := getAs(t, tok, "/v1/tickets/"+id+"/bundle"); code != 200 || !strings.Contains(body, `"item_id":"practice/disk-full"`) {
		t.Fatalf("bundle: %d %s", code, body)
	}
	if code, body, _ := postRaw(t, tok, "application/json", `{"kind":"broken_ticket","item_id":"`+id+`","message":"It fails."}`); code != http.StatusCreated {
		t.Fatalf("report: %d %s", code, body)
	}

	ev := map[string]any{"id": evID(), "type": "ticket.started", "at": "2026-11-23T10:00:00Z", "item_id": id, "lab": "local", "catalog_version": "a1b2c3d4e5f6"}
	if code, body := post(t, c, tok, ev); code != 200 || strings.TrimSpace(body) != `{"accepted":1,"duplicates":0,"ignored":0}` {
		t.Fatalf("event: %d %q", code, body)
	}
	if got := asOwner(t, "SELECT state FROM ticket_progress WHERE item_id = '"+id+"'"); got != "started" {
		t.Fatalf("projection: %q", got)
	}
}

func TestNonTicketKindsStayOutOfTicketRoutesIntegration(t *testing.T) {
	c, tok := practiceFixture(t)
	if code, _, _ := getAs(t, tok, "/v1/tickets/foundations/q"); code != 404 {
		t.Fatalf("quiz as ticket: %d", code)
	}
	if code, _ := openHint(c, tok, "foundations/q", "1"); code != 404 {
		t.Fatalf("quiz hint: %d", code)
	}
	ev := map[string]any{"id": evID(), "type": "ticket.started", "at": "2026-11-23T10:00:00Z", "item_id": "foundations/q", "lab": "local", "catalog_version": "a1b2c3d4e5f6"}
	if code, body := post(t, c, tok, ev); code != 200 || !strings.Contains(body, `"ignored":1`) {
		t.Fatalf("quiz event: %d %q", code, body)
	}
	if code, _, _ := postRaw(t, tok, "application/json", `{"kind":"broken_ticket","item_id":"foundations/q","message":"x"}`); code == http.StatusCreated {
		t.Fatal("a report was accepted for a quiz")
	}
}

func TestPracticeActivityAndPathIntegration(t *testing.T) {
	_, tok := practiceFixture(t)
	pass(t, "1001", "practice/disk-full", "passed", "now()")
	pass(t, "1001", "foundations/a", "passed", "now() - interval '1 hour'")

	got := activity(t, tok, "")
	if got.Total != 2 || len(got.Recent) != 2 || got.Recent[0].TicketID != "practice/disk-full" {
		t.Fatalf("activity: %+v", got)
	}
	code, body, _ := getAs(t, tok, "/v1/path")
	if code != 200 || strings.Contains(body, "practice/") || !strings.Contains(body, `"tickets_passed":1`) {
		t.Fatalf("path counts practice: %d %s", code, body)
	}
	code, body, _ = getAs(t, tok, "/v1/catalog")
	if code != 200 || strings.Contains(body, "practice/") {
		t.Fatalf("catalog lists practice: %d %s", code, body)
	}
}

func TestPracticeListIntegration(t *testing.T) {
	_, tok := practiceFixture(t)
	setProgress(t, "foundations/b", "passed", "stopped")
	setProgress(t, "practice/lost-key", "started", "")

	code, body, h := getAs(t, tok, "/v1/practice")
	if code != 200 || h.Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q %s", code, h.Get("Cache-Control"), body)
	}
	var got struct {
		Items []struct {
			ID, Kind, Title string
			Role            *string
			Difficulty      *string
			State           *string
			LabState        *string `json:"lab_state"`
			Tags            []string
		}
	}
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, i := range got.Items {
		ids = append(ids, i.ID)
	}
	if strings.Join(ids, ",") != "practice/disk-full,practice/lost-key,foundations/a,foundations/b,support-l1/c" {
		t.Fatalf("ids: %v (the quiz must not be listed)", ids)
	}
	d, l, b := got.Items[0], got.Items[1], got.Items[3]
	if d.Kind != "practice" || d.Role != nil || *d.Difficulty != "medium" || d.State != nil || d.Title != "Disk full" || len(d.Tags) != 1 {
		t.Fatalf("disk-full: %+v", d)
	}
	if l.Difficulty != nil || *l.State != "started" {
		t.Fatalf("lost-key: %+v", l)
	}
	if b.Kind != "ticket" || *b.Role != "foundations" || b.Difficulty != nil || *b.State != "passed" || *b.LabState != "stopped" {
		t.Fatalf("foundations/b: %+v", b)
	}
	if code, _, _ := getAs(t, "", "/v1/practice"); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}

func TestPracticePassDoesNotFinishOnboardingIntegration(t *testing.T) {
	_, tok := practiceFixture(t)
	step := func() string {
		code, body, _ := getAs(t, tok, "/v1/onboarding")
		return readOnboarding(t, code, body).NextStep
	}
	pass(t, "1001", "practice/disk-full", "passed", "now()")
	if got := step(); got == "done" {
		t.Fatalf("a practice pass finished onboarding: %q", got)
	}
	pass(t, "1001", "foundations/a", "passed", "now()")
	if got := step(); got != "done" {
		t.Fatalf("a ticket pass must finish onboarding: %q", got)
	}
}
