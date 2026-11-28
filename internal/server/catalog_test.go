package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

type fakeCatalog struct {
	version  string // empty = no catalog_state row
	paths    []repo.ListCatalogPathsRow
	roles    []repo.ListCatalogRolesRow
	pathRole []repo.ListCatalogPathRolesRow
	items    []repo.ListCatalogItemsRow
	marks    []repo.ListCatalogMarksRow
	practice []repo.ListPracticeItemsRow
	progress []repo.ListLearnerTicketStatesRow
	done     []string
	days     []repo.ListLearnerActivityDaysRow
	total    int32
	recent   []repo.ListLearnerRecentPassesRow
	gotTZ    string
	chosen   string // the learner's chosen path slug
	firstRun *repo.GetLearnerFirstRunRow
	err      error
}

func (f *fakeCatalog) GetCatalogVersion(context.Context) (string, error) {
	if f.err != nil {
		return "", f.err
	}
	if f.version == "" {
		return "", pgx.ErrNoRows
	}
	return f.version, nil
}

func (f *fakeCatalog) GetLearnerChosenPathSlug(context.Context, pgtype.UUID) (string, error) {
	return f.chosen, f.err
}

func (f *fakeCatalog) ListCatalogPaths(context.Context) ([]repo.ListCatalogPathsRow, error) {
	return f.paths, f.err
}

func (f *fakeCatalog) ListCatalogRoles(context.Context) ([]repo.ListCatalogRolesRow, error) {
	return f.roles, f.err
}

func (f *fakeCatalog) ListCatalogPathRoles(context.Context) ([]repo.ListCatalogPathRolesRow, error) {
	return f.pathRole, f.err
}

func (f *fakeCatalog) ListCatalogItems(context.Context) ([]repo.ListCatalogItemsRow, error) {
	return f.items, f.err
}

func (f *fakeCatalog) ListPracticeItems(context.Context) ([]repo.ListPracticeItemsRow, error) {
	return f.practice, f.err
}

func (f *fakeCatalog) ListCatalogMarks(context.Context) ([]repo.ListCatalogMarksRow, error) {
	return f.marks, f.err
}

func (f *fakeCatalog) ListLearnerTicketStates(context.Context, pgtype.UUID) ([]repo.ListLearnerTicketStatesRow, error) {
	return f.progress, f.err
}

func (f *fakeCatalog) ListLearnerCompletedItems(context.Context, pgtype.UUID) ([]string, error) {
	return f.done, f.err
}

func (f *fakeCatalog) GetLearnerFirstRun(context.Context, pgtype.UUID) (repo.GetLearnerFirstRunRow, error) {
	if f.err != nil {
		return repo.GetLearnerFirstRunRow{}, f.err
	}
	if f.firstRun == nil {
		return repo.GetLearnerFirstRunRow{}, pgx.ErrNoRows
	}
	return *f.firstRun, nil
}

func populatedCatalog() *fakeCatalog {
	return &fakeCatalog{
		version: "a1b2c3d4e5f6",
		paths: []repo.ListCatalogPathsRow{
			{Slug: "systems-devops", Title: "Systems and DevOps", Status: "available", Position: 1},
			{Slug: "security", Title: "Security", Status: "planned", Position: 2},
		},
		roles: []repo.ListCatalogRolesRow{
			{Slug: "foundations", Title: "Foundations", Kind: "foundations"},
			{Slug: "support-l1", Title: "Support L1", Summary: "Tickets, users, permissions, a calm reply", Kind: "role", HasExam: true},
			{Slug: "systems-l2", Title: "Systems L2", Kind: "role", HasExam: true},
		},
		pathRole: []repo.ListCatalogPathRolesRow{
			{Path: "systems-devops", Role: "foundations", Position: 1},
			{Path: "systems-devops", Role: "support-l1", Position: 2},
			{Path: "systems-devops", Role: "systems-l2", Position: 3},
			{Path: "security", Role: "foundations", Position: 1},
		},
		items: []repo.ListCatalogItemsRow{
			{ID: "foundations/a", Role: "foundations", Position: 1, Kind: "ticket", Title: "A", EstMinutes: 10, Tags: []string{"x"}},
			{ID: "foundations/b", Role: "foundations", Position: 2, Kind: "ticket", Title: "B", EstMinutes: 15, Tags: []string{}},
			{ID: "foundations/q", Role: "foundations", Position: 3, Kind: "quiz", Title: "Q", EstMinutes: 5, Tags: []string{}},
			{ID: "support-l1/c", Role: "support-l1", Position: 1, Kind: "ticket", Title: "C", EstMinutes: 20, Tags: []string{}},
		},
	}
}

func getCatalogRoute(t *testing.T, f *fakeCatalog, path string, suspend, anon bool) *httptest.ResponseRecorder {
	t.Helper()
	s := &fakeSessions{row: activeRow(time.Now(), suspend)}
	req := httptest.NewRequest(http.MethodGet, path, nil)
	if !anon {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	}
	rec := httptest.NewRecorder()
	New(fakeReady{}, Auth{Store: s, Catalog: f}, NewRateLimits(1), nil, nil).ServeHTTP(rec, req)
	return rec
}

func TestCatalogEmpty(t *testing.T) {
	rec := getCatalogRoute(t, &fakeCatalog{}, "/v1/catalog", false, false)
	want := `{"catalog_version":null,"paths":[],"roles":[],"path_roles":[],"items":[],"marks":[]}` + "\n"
	if rec.Code != http.StatusOK || rec.Body.String() != want || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %q %q", rec.Code, rec.Body, rec.Header().Get("Cache-Control"))
	}
}

func TestCatalogPopulated(t *testing.T) {
	rec := getCatalogRoute(t, populatedCatalog(), "/v1/catalog", false, false)
	var got struct {
		Version  string           `json:"catalog_version"`
		Paths    []map[string]any `json:"paths"`
		Roles    []map[string]any `json:"roles"`
		PathRole []map[string]any `json:"path_roles"`
		Items    []map[string]any `json:"items"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	if got.Version != "a1b2c3d4e5f6" || len(got.Paths) != 2 || len(got.Roles) != 3 || len(got.PathRole) != 4 || len(got.Items) != 4 {
		t.Fatalf("shape: %s", rec.Body)
	}
	if got.Roles[1]["summary"] != "Tickets, users, permissions, a calm reply" || got.Roles[0]["summary"] != "" {
		t.Fatalf("role summaries: %v", got.Roles)
	}
	it := got.Items[0]
	if it["id"] != "foundations/a" || it["role"] != "foundations" || it["order"] != float64(1) || it["kind"] != "ticket" ||
		it["title"] != "A" || it["est_minutes"] != float64(10) || len(it["tags"].([]any)) != 1 {
		t.Fatalf("item: %v", it)
	}
	if got.Items[1]["tags"] == nil {
		t.Fatalf("empty tags must be [], not null: %v", got.Items[1])
	}
	for _, k := range []string{"brief", "hints"} {
		if _, ok := it[k]; ok {
			t.Fatalf("catalog leaks %s", k)
		}
	}
}

func TestCatalogMarks(t *testing.T) {
	f := populatedCatalog()
	f.marks = []repo.ListCatalogMarksRow{
		{Slug: "written-english", Belt: "white", Title: "Written English for tickets", Summary: "Write a reply", Position: 1},
		{Slug: "tidy-shell", Belt: "yellow", Title: "Tidy shell", Summary: "Clean up", Position: 1},
	}
	rec := getCatalogRoute(t, f, "/v1/catalog", false, false)
	var got struct {
		Marks []map[string]any `json:"marks"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || len(got.Marks) != 2 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	m := got.Marks[0]
	if m["slug"] != "written-english" || m["belt"] != "white" || m["title"] != "Written English for tickets" ||
		m["summary"] != "Write a reply" || m["position"] != float64(1) || len(m) != 5 {
		t.Fatalf("mark: %v", m)
	}
}

func getPath(t *testing.T, f *fakeCatalog) (path *string, chosen bool, roles []pathRole) {
	t.Helper()
	rec := getCatalogRoute(t, f, "/v1/path", false, false)
	var got struct {
		Path   *string    `json:"path"`
		Chosen bool       `json:"chosen"`
		Roles  []pathRole `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	return got.Path, got.Chosen, got.Roles
}

func TestPathFoundationsRule(t *testing.T) {
	s := func(v string) pgtype.Text { return pgtype.Text{String: v, Valid: true} }
	for _, tt := range []struct {
		name         string
		progress     []repo.ListLearnerTicketStatesRow
		passed       bool
		done         int
		firstState   string
		firstLabNull bool
	}{
		{"no progress", nil, false, 0, "", true},
		{"one passed one started", []repo.ListLearnerTicketStatesRow{
			{ItemID: "foundations/a", State: "passed", LabState: s("stopped")},
			{ItemID: "foundations/b", State: "started", LabState: s("running")}}, false, 1, "passed", false},
		{"passed and revisit", []repo.ListLearnerTicketStatesRow{
			{ItemID: "foundations/a", State: "revisit"}, {ItemID: "foundations/b", State: "passed"}}, true, 2, "revisit", true},
		{"quiz progress does not count", []repo.ListLearnerTicketStatesRow{
			{ItemID: "foundations/q", State: "passed"}}, false, 0, "", true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := populatedCatalog()
			f.progress = tt.progress
			path, chosen, roles := getPath(t, f)
			if path == nil || *path != "systems-devops" || chosen || len(roles) != 3 {
				t.Fatalf("path %v chosen %v roles %d", path, chosen, len(roles))
			}
			fd := roles[0]
			if fd.Slug != "foundations" || fd.Passed != tt.passed || fd.TicketsPassed != tt.done || fd.TicketsTotal != 2 ||
				fd.Locked || len(fd.Items) != 2 || fd.Items[0].Order != 1 {
				t.Fatalf("foundations: %+v", fd)
			}
			if (fd.Items[0].State == nil) != (tt.firstState == "") || (tt.firstState != "" && *fd.Items[0].State != tt.firstState) ||
				(fd.Items[0].LabState == nil) != tt.firstLabNull {
				t.Fatalf("first item: %+v", fd.Items[0])
			}
		})
	}
}

func TestPathLockedAndExamRoles(t *testing.T) {
	f := populatedCatalog()
	f.progress = []repo.ListLearnerTicketStatesRow{{ItemID: "support-l1/c", State: "passed"}}
	_, _, roles := getPath(t, f)
	// An exam role is never passed by tickets alone; a role with no ticket items is locked.
	if roles[1].Passed || roles[1].Locked || roles[1].TicketsPassed != 1 || roles[1].TicketsTotal != 1 {
		t.Fatalf("support-l1: %+v", roles[1])
	}
	if !roles[2].Locked || roles[2].Passed || roles[2].TicketsTotal != 0 || roles[2].Items == nil || len(roles[2].Items) != 0 {
		t.Fatalf("systems-l2: %+v", roles[2])
	}
}

func TestPathWithoutAnAvailablePath(t *testing.T) {
	path, chosen, roles := getPath(t, &fakeCatalog{})
	if path != nil || chosen || roles == nil || len(roles) != 0 {
		t.Fatalf("%v %v %v", path, chosen, roles)
	}
}

func TestPathReportsTheChosenPath(t *testing.T) {
	f := populatedCatalog()
	if _, chosen, _ := getPath(t, f); chosen {
		t.Fatal("chosen without a choice")
	}
	f.chosen = "systems-devops"
	if path, chosen, _ := getPath(t, f); !chosen || path == nil || *path != "systems-devops" {
		t.Fatalf("%v %v", path, chosen)
	}
	// A choice that is not an available path in the catalog falls back to the default, unchosen.
	f.chosen = "security"
	if path, chosen, _ := getPath(t, f); chosen || path == nil || *path != "systems-devops" {
		t.Fatalf("%v %v", path, chosen)
	}
}

func TestCatalogRoutesRefuse(t *testing.T) {
	for _, route := range []string{"/v1/catalog", "/v1/path", "/v1/today", "/v1/activity"} {
		if rec := getCatalogRoute(t, populatedCatalog(), route, false, true); rec.Code != 401 || rec.Body.String() != "{\"error\":\"unauthenticated\"}\n" {
			t.Fatalf("%s anonymous: %d %s", route, rec.Code, rec.Body)
		}
		if rec := getCatalogRoute(t, populatedCatalog(), route, true, false); rec.Code != 403 || rec.Body.String() != "{\"error\":\"account_suspended\"}\n" {
			t.Fatalf("%s suspended: %d %s", route, rec.Code, rec.Body)
		}
		if rec := getCatalogRoute(t, &fakeCatalog{err: errors.New("db down")}, route, false, false); rec.Code != 500 || rec.Body.String() != "{\"error\":\"server_error\"}\n" {
			t.Fatalf("%s db error: %d %s", route, rec.Code, rec.Body)
		}
	}
}

func TestReadsShareOneBudgetPerLearner(t *testing.T) {
	s := &fakeSessions{row: activeRow(time.Now(), false)}
	h := New(fakeReady{}, Auth{Store: s, Catalog: populatedCatalog()}, testLimits(3), nil, nil)
	var codes []int
	for _, route := range []string{"/v1/catalog", "/v1/path", "/v1/today", "/v1/activity", "/v1/catalog"} {
		req := httptest.NewRequest(http.MethodGet, route, nil)
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		codes = append(codes, rec.Code)
		if rec.Code == 429 && rec.Header().Get("Retry-After") == "" {
			t.Fatal("429 without Retry-After")
		}
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 200 || codes[3] != 429 {
		t.Fatalf("codes %v", codes)
	}
}
