package server

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

func row(id, role, kind string, pos int32) repo.ListCatalogItemsRow {
	return repo.ListCatalogItemsRow{ID: id, Role: role, Kind: kind, Position: pos, Title: id, EstMinutes: 5, Tags: []string{}}
}

func TestUnlockedItems(t *testing.T) {
	items := []repo.ListCatalogItemsRow{
		row("r/s", "r", "story", 1), row("r/a", "r", "ticket", 2), row("r/q1", "r", "quiz", 3),
		row("r/w", "r", "writing", 4), row("r/b", "r", "ticket", 5), row("r/q2", "r", "quiz", 6),
	}
	for _, tt := range []struct {
		name   string
		passed map[string]bool
		want   map[string]bool
	}{
		{"nothing passed", nil, map[string]bool{"r/s": true, "r/q1": false, "r/w": false, "r/q2": false}},
		{"first passed", map[string]bool{"r/a": true}, map[string]bool{"r/s": true, "r/q1": true, "r/w": true, "r/q2": false}},
		{"second passed only", map[string]bool{"r/b": true}, map[string]bool{"r/s": true, "r/q1": false, "r/w": false, "r/q2": true}},
		{"all passed", map[string]bool{"r/a": true, "r/b": true}, map[string]bool{"r/s": true, "r/q1": true, "r/w": true, "r/q2": true}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if got := unlockedItems(items, tt.passed); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	if got := unlockedItems([]repo.ListCatalogItemsRow{row("r/q", "r", "quiz", 1)}, nil); !got["r/q"] {
		t.Fatalf("role with no tickets: %v", got)
	}
}

// betweenCatalog: foundations has a story before its first ticket and a quiz after its last one,
// support-l1 a writing task after its only ticket, and systems-l2 (locked) only a quiz.
func betweenCatalog() *fakeCatalog {
	f := populatedCatalog()
	f.items = []repo.ListCatalogItemsRow{
		row("foundations/intro", "foundations", "story", 1), row("foundations/a", "foundations", "ticket", 2),
		row("foundations/q1", "foundations", "quiz", 3), row("foundations/b", "foundations", "ticket", 4),
		row("foundations/q", "foundations", "quiz", 5),
		row("support-l1/c", "support-l1", "ticket", 1), row("support-l1/w", "support-l1", "writing", 2),
		row("systems-l2/z", "systems-l2", "quiz", 1),
	}
	return f
}

func passedRows(ids ...string) []repo.ListLearnerTicketStatesRow {
	var out []repo.ListLearnerTicketStatesRow
	for _, id := range ids {
		out = append(out, repo.ListLearnerTicketStatesRow{ItemID: id, State: "passed"})
	}
	return out
}

func TestTodayBetween(t *testing.T) {
	for _, tt := range []struct {
		name     string
		passed   []string
		done     []string
		want     string
		wantRole string
	}{
		{"item before the first ticket", nil, nil, "foundations/intro", "foundations"},
		{"locked items offer nothing", nil, []string{"foundations/intro"}, "", ""},
		{"first pass unlocks the next", []string{"foundations/a"}, []string{"foundations/intro"}, "foundations/q1", "foundations"},
		{"done is skipped", []string{"foundations/a"}, []string{"foundations/intro", "foundations/q1"}, "", ""},
		{"trailing item after the last ticket", []string{"foundations/a", "foundations/b"}, []string{"foundations/intro", "foundations/q1"}, "foundations/q", "foundations"},
		{"earlier role still offered after moving on", []string{"foundations/a", "foundations/b", "support-l1/c"}, []string{"foundations/intro", "foundations/q1"}, "foundations/q", "foundations"},
		{"next role once the earlier is done", []string{"foundations/a", "foundations/b", "support-l1/c"}, []string{"foundations/intro", "foundations/q1", "foundations/q"}, "support-l1/w", "support-l1"},
		{"locked role never offers", []string{"foundations/a", "foundations/b", "support-l1/c"}, []string{"foundations/intro", "foundations/q1", "foundations/q", "support-l1/w"}, "", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			f := betweenCatalog()
			f.progress, f.done = passedRows(tt.passed...), tt.done
			got := getToday(t, f).Between
			if tt.want == "" {
				if got != nil {
					t.Fatalf("want null, got %+v", got)
				}
				return
			}
			if got == nil || got.ID != tt.want || got.Role != tt.wantRole || got.Title != tt.want || got.EstMinutes != 5 {
				t.Fatalf("got %+v", got)
			}
		})
	}
}

func TestTodayBetweenJSONAndBackwardCompat(t *testing.T) {
	f := betweenCatalog()
	f.items[0].Kind, f.items[0].EstMinutes = "quiz", 7
	rec := getCatalogRoute(t, f, "/v1/today", false, false)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(rec.Body.Bytes(), &raw); err != nil {
		t.Fatal(err)
	}
	if want := `{"id":"foundations/intro","kind":"quiz","role":"foundations","title":"foundations/intro","est_minutes":7}`; string(raw["between"]) != want {
		t.Fatalf("between: %s", raw["between"])
	}
	got := getToday(t, f)
	if got.Next == nil || got.Next.ID != "foundations/a" || got.Role.TicketsTotal != 2 {
		t.Fatalf("next stays tickets-only: %+v", got)
	}
	empty := getCatalogRoute(t, &fakeCatalog{}, "/v1/today", false, false)
	var m map[string]json.RawMessage
	if err := json.Unmarshal(empty.Body.Bytes(), &m); err != nil || string(m["between"]) != "null" {
		t.Fatalf("empty between: %v %s", err, empty.Body)
	}
}

func TestPathBetweenItems(t *testing.T) {
	f := betweenCatalog()
	f.progress = passedRows("foundations/a")
	f.done = []string{"foundations/q"}
	rec := getCatalogRoute(t, f, "/v1/path", false, false)
	var got struct {
		Roles []struct {
			Slug         string            `json:"slug"`
			TicketsTotal int               `json:"tickets_total"`
			Locked       bool              `json:"locked"`
			Items        []map[string]any  `json:"items"`
			Between      []json.RawMessage `json:"between_items"`
		} `json:"roles"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || len(got.Roles) != 3 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	fd := got.Roles[0]
	if len(fd.Items) != 2 || fd.TicketsTotal != 2 || len(fd.Between) != 3 {
		t.Fatalf("foundations: %+v", fd)
	}
	for i, want := range []string{
		`{"id":"foundations/intro","kind":"story","title":"foundations/intro","order":1,"done":false}`,
		`{"id":"foundations/q1","kind":"quiz","title":"foundations/q1","order":3,"done":false}`,
		`{"id":"foundations/q","kind":"quiz","title":"foundations/q","order":5,"done":true}`,
	} {
		if string(fd.Between[i]) != want {
			t.Fatalf("between_items[%d]: %s", i, fd.Between[i])
		}
	}
	if l2 := got.Roles[2]; !l2.Locked || len(l2.Between) != 1 {
		t.Fatalf("locked role keeps its items: %+v", l2)
	}
	if empty := getCatalogRoute(t, populatedCatalog(), "/v1/path", false, false); !json.Valid(empty.Body.Bytes()) ||
		!strings.Contains(empty.Body.String(), `"between_items":[]`) {
		t.Fatalf("empty list: %s", empty.Body)
	}
}
