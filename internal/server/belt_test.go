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

func beltRow(belt, slug string, at string) repo.ListLearnerBeltMarksRow {
	r := repo.ListLearnerBeltMarksRow{Slug: slug, Belt: belt, Title: "T " + slug, Summary: "S " + slug}
	if at != "" {
		tm, _ := time.Parse(time.RFC3339, at)
		r.AwardedAt = pgtype.Timestamptz{Time: tm, Valid: true}
	}
	return r
}

type beltOut struct {
	Belt     string  `json:"belt"`
	Title    string  `json:"title"`
	Complete bool    `json:"complete"`
	Held     int     `json:"held"`
	Total    int     `json:"total"`
	Next     *string `json:"next_belt"`
	Marks    []struct {
		Slug      string  `json:"slug"`
		Title     string  `json:"title"`
		Summary   string  `json:"summary"`
		Held      bool    `json:"held"`
		AwardedAt *string `json:"awarded_at"`
	} `json:"marks"`
}

func TestBelt(t *testing.T) {
	const at = "2026-12-02T18:10:00Z"
	tests := []struct {
		name        string
		rows        []repo.ListLearnerBeltMarksRow
		belt, next  string // next "" means null
		complete    bool
		held, total int
		marks       int
	}{
		{"no marks in the catalog", nil, "white", "yellow", false, 0, 0, 0},
		{"nothing held", []repo.ListLearnerBeltMarksRow{beltRow("white", "a", ""), beltRow("white", "b", "")}, "white", "yellow", false, 0, 2, 2},
		{"partial", []repo.ListLearnerBeltMarksRow{beltRow("white", "a", at), beltRow("white", "b", "")}, "white", "yellow", false, 1, 2, 2},
		{"white held, yellow has marks", []repo.ListLearnerBeltMarksRow{beltRow("white", "a", at), beltRow("yellow", "y", "")}, "yellow", "orange", false, 0, 1, 1},
		{"only white has marks and is held", []repo.ListLearnerBeltMarksRow{beltRow("white", "a", at)}, "white", "", true, 1, 1, 1},
		{"gap belt skipped", []repo.ListLearnerBeltMarksRow{beltRow("white", "a", at), beltRow("green", "g", "")}, "green", "blue", false, 0, 1, 1},
		{"black held", []repo.ListLearnerBeltMarksRow{beltRow("black", "k", at)}, "black", "", true, 1, 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f := populatedCatalog()
			f.beltRows = tc.rows
			rec := getCatalogRoute(t, f, "/v1/belt", false, false)
			if rec.Code != http.StatusOK || rec.Header().Get("Cache-Control") != "no-store" {
				t.Fatalf("%d %q", rec.Code, rec.Header().Get("Cache-Control"))
			}
			var got beltOut
			if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			next := ""
			if got.Next != nil {
				next = *got.Next
			}
			if got.Belt != tc.belt || got.Next == nil != (tc.next == "") || next != tc.next || got.Complete != tc.complete ||
				got.Held != tc.held || got.Total != tc.total || len(got.Marks) != tc.marks || got.Marks == nil {
				t.Fatalf("%+v", got)
			}
		})
	}
}

func TestBeltFields(t *testing.T) {
	f := populatedCatalog()
	f.beltRows = []repo.ListLearnerBeltMarksRow{beltRow("white", "a", "2026-12-02T18:10:00Z"), beltRow("white", "b", "")}
	rec := getCatalogRoute(t, f, "/v1/belt", false, false)
	want := `{"belt":"white","title":"White belt","complete":false,"held":1,"total":2,"next_belt":"yellow","marks":[` +
		`{"slug":"a","title":"T a","summary":"S a","held":true,"awarded_at":"2026-12-02T18:10:00Z"},` +
		`{"slug":"b","title":"T b","summary":"S b","held":false,"awarded_at":null}]}` + "\n"
	if rec.Body.String() != want {
		t.Fatalf("got  %s\nwant %s", rec.Body.String(), want)
	}
}

func TestBeltReadFailure(t *testing.T) {
	f := populatedCatalog()
	f.err = errors.New("db down")
	if rec := getCatalogRoute(t, f, "/v1/belt", false, false); rec.Code != http.StatusInternalServerError {
		t.Fatalf("%d", rec.Code)
	}
}

func TestBeltIsALearnerRead(t *testing.T) {
	if !isLearnerRead("GET /v1/belt") {
		t.Fatal("GET /v1/belt must take the read budget")
	}
}
