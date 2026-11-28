package server

import (
	"encoding/json"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

func seen(item, state string, at time.Time) repo.ListLearnerTicketStatesRow {
	return repo.ListLearnerTicketStatesRow{ItemID: item, State: state, LastSeenAt: pgtype.Timestamptz{Time: at, Valid: true}}
}

func getToday(t *testing.T, f *fakeCatalog) todayResponse {
	t.Helper()
	rec := getCatalogRoute(t, f, "/v1/today", false, false)
	var got todayResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	return got
}

func TestTodaySelection(t *testing.T) {
	t0 := time.Date(2026, 11, 24, 10, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name       string
		progress   []repo.ListLearnerTicketStatesRow
		current    string
		next       string
		role       string
		rolePassed int
		allPassed  bool
	}{
		{"no progress", nil, "", "foundations/a", "foundations", 0, false},
		{"started a", []repo.ListLearnerTicketStatesRow{seen("foundations/a", "started", t0)},
			"foundations/a", "foundations/b", "foundations", 0, false},
		{"latest last_seen wins", []repo.ListLearnerTicketStatesRow{
			seen("foundations/a", "started", t0), seen("foundations/b", "started", t0.Add(time.Hour))},
			"foundations/b", "foundations/a", "foundations", 0, false},
		{"passed a, nothing started", []repo.ListLearnerTicketStatesRow{seen("foundations/a", "passed", t0)},
			"", "foundations/b", "foundations", 1, false},
		{"current in a later role", []repo.ListLearnerTicketStatesRow{
			seen("foundations/a", "passed", t0), seen("foundations/b", "passed", t0), seen("support-l1/c", "started", t0)},
			"support-l1/c", "", "support-l1", 0, false},
		{"next skips passed into next role", []repo.ListLearnerTicketStatesRow{
			seen("foundations/a", "passed", t0), seen("foundations/b", "revisit", t0)},
			"", "support-l1/c", "support-l1", 0, false},
		{"quiz progress ignored", []repo.ListLearnerTicketStatesRow{seen("foundations/q", "started", t0)},
			"", "foundations/a", "foundations", 0, false},
		{"all passed", []repo.ListLearnerTicketStatesRow{
			seen("foundations/a", "passed", t0), seen("foundations/b", "revisit", t0), seen("support-l1/c", "passed", t0)},
			"", "", "support-l1", 1, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := populatedCatalog()
			f.progress = tt.progress
			got := getToday(t, f)
			id := func(x *todayTicket) string {
				if x == nil {
					return ""
				}
				return x.ID
			}
			if id(got.Current) != tt.current || id(got.Next) != tt.next || got.AllPassed != tt.allPassed {
				t.Fatalf("current %q next %q all %v", id(got.Current), id(got.Next), got.AllPassed)
			}
			if got.Role == nil || got.Role.Slug != tt.role || got.Role.TicketsPassed != tt.rolePassed {
				t.Fatalf("role: %+v", got.Role)
			}
		})
	}
}

func TestTodayTicketFields(t *testing.T) {
	f := populatedCatalog()
	f.progress = []repo.ListLearnerTicketStatesRow{{ItemID: "foundations/a", State: "started",
		LabState: pgtype.Text{String: "running", Valid: true}, LastSeenAt: pgtype.Timestamptz{Time: time.Now(), Valid: true}}}
	got := getToday(t, f)
	c, n := got.Current, got.Next
	if c.Title != "A" || c.State == nil || *c.State != "started" || c.LabState == nil || *c.LabState != "running" ||
		n.Title != "B" || n.State != nil || n.LabState != nil || got.Role.TicketsTotal != 2 || got.Role.Title != "Foundations" {
		t.Fatalf("%+v %+v %+v", c, n, got.Role)
	}
}

func TestTodayNoPathAndNoTickets(t *testing.T) {
	rec := getCatalogRoute(t, &fakeCatalog{}, "/v1/today", false, false)
	if want := `{"current":null,"next":null,"role":null,"all_passed":false,"first_run":null,"between":null}` + "\n"; rec.Body.String() != want {
		t.Fatalf("empty: %q", rec.Body)
	}
	f := populatedCatalog()
	f.items = nil
	got := getToday(t, f)
	if got.Current != nil || got.Next != nil || got.Role != nil || got.AllPassed {
		t.Fatalf("no tickets: %+v", got)
	}
}

func TestTodayFirstRun(t *testing.T) {
	if got := getToday(t, populatedCatalog()); got.FirstRun != nil {
		t.Fatalf("no first pass: %+v", got.FirstRun)
	}
	at := time.Date(2026, 11, 25, 9, 30, 0, 0, time.UTC)
	f := populatedCatalog()
	f.firstRun = &repo.GetLearnerFirstRunRow{ItemID: "foundations/a", Title: "A", FirstPassedAt: pgtype.Timestamptz{Time: at, Valid: true}}
	got := getToday(t, f).FirstRun
	if got == nil || got.ID != "foundations/a" || got.Title != "A" || !got.FirstPassedAt.Equal(at) || got.PassedCommitState != nil {
		t.Fatalf("first run: %+v", got)
	}
	f.firstRun.CommitState = pgtype.Text{String: "pending", Valid: true}
	if got := getToday(t, f).FirstRun; got == nil || got.PassedCommitState == nil || *got.PassedCommitState != "pending" {
		t.Fatalf("commit state: %+v", got)
	}
	f.err = errors.New("db down")
	if rec := getCatalogRoute(t, f, "/v1/today", false, false); rec.Code != http.StatusInternalServerError {
		t.Fatalf("read failure: %d", rec.Code)
	}
}

func TestTodayRoute(t *testing.T) {
	if rec := getCatalogRoute(t, populatedCatalog(), "/v1/today", false, true); rec.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", rec.Code)
	}
	if rec := getCatalogRoute(t, populatedCatalog(), "/v1/today", true, false); rec.Code != http.StatusForbidden {
		t.Fatalf("suspended: %d", rec.Code)
	}
}
