package server

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

func TestPracticeList(t *testing.T) {
	f := populatedCatalog()
	f.practice = []repo.ListPracticeItemsRow{
		{ID: "practice/disk-full", Title: "Disk full", EstMinutes: 20, Tags: []string{"storage"}, Difficulty: pgtype.Text{String: "medium", Valid: true}},
		{ID: "practice/lost-key", Title: "Lost key", EstMinutes: 10},
	}
	f.progress = []repo.ListLearnerTicketStatesRow{
		{ItemID: "foundations/b", State: "passed", LabState: pgtype.Text{String: "stopped", Valid: true}},
		{ItemID: "practice/lost-key", State: "started"},
	}
	rec := getCatalogRoute(t, f, "/v1/practice", false, false)
	if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
	}
	var got struct {
		Items []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, it := range got.Items {
		ids = append(ids, it["id"].(string))
	}
	want := []string{"practice/disk-full", "practice/lost-key", "foundations/a", "foundations/b", "support-l1/c"}
	if len(ids) != len(want) {
		t.Fatalf("ids %v want %v", ids, want)
	}
	for i := range want {
		if ids[i] != want[i] {
			t.Fatalf("ids %v want %v", ids, want)
		}
	}
	p := got.Items[0]
	if p["kind"] != "practice" || p["role"] != nil || p["difficulty"] != "medium" || p["state"] != nil || p["lab_state"] != nil || p["est_minutes"] != float64(20) {
		t.Fatalf("practice item: %v", p)
	}
	if got.Items[1]["difficulty"] != nil || got.Items[1]["state"] != "started" || got.Items[1]["tags"] == nil {
		t.Fatalf("second practice item: %v", got.Items[1])
	}
	k := got.Items[3]
	if k["kind"] != "ticket" || k["role"] != "foundations" || k["difficulty"] != nil || k["state"] != "passed" || k["lab_state"] != "stopped" {
		t.Fatalf("ticket item: %v", k)
	}
}

func TestPracticeSkipsLockedRolesAndNonTickets(t *testing.T) {
	f := populatedCatalog()
	f.paths[0].Status = "planned"
	rec := getCatalogRoute(t, f, "/v1/practice", false, false)
	if rec.Code != http.StatusOK || rec.Body.String() != "{\"items\":[]}\n" {
		t.Fatalf("%d %q", rec.Code, rec.Body)
	}
}

func TestPracticeNeedsSession(t *testing.T) {
	if rec := getCatalogRoute(t, populatedCatalog(), "/v1/practice", false, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anon: %d", rec.Code)
	}
}

func TestPracticeIsALearnerRead(t *testing.T) {
	if !isLearnerRead("GET /v1/practice") {
		t.Fatal("GET /v1/practice must take the read budget")
	}
}

func TestParseTicketPathAcceptsPractice(t *testing.T) {
	id, route, _ := parseTicketPath("/v1/tickets/practice/disk-full/bundle")
	if id != "practice/disk-full" || route != routeBundle {
		t.Fatalf("%q %v", id, route)
	}
}
