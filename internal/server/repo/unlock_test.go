package repo

import (
	"reflect"
	"strings"
	"testing"
)

func item(id, kind string) ListCatalogItemsRow { return ListCatalogItemsRow{ID: id, Kind: kind} }

func TestUnlockedItems(t *testing.T) {
	items := []ListCatalogItemsRow{
		item("r/s", "story"), item("r/a", "ticket"), item("r/q1", "quiz"),
		item("r/w", "writing"), item("r/b", "ticket"), item("r/q2", "quiz"),
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
			if got := UnlockedItems(items, tt.passed); !reflect.DeepEqual(got, tt.want) {
				t.Fatalf("got %v want %v", got, tt.want)
			}
		})
	}
	if got := UnlockedItems([]ListCatalogItemsRow{item("r/q", "quiz")}, nil); !got["r/q"] {
		t.Fatalf("role with no tickets: %v", got)
	}
}

func TestPassedTickets(t *testing.T) {
	got := PassedTickets([]ListLearnerTicketStatesRow{
		{ItemID: "a", State: "passed"}, {ItemID: "b", State: "revisit"}, {ItemID: "c", State: "started"},
	})
	if !got["a"] || !got["b"] || got["c"] {
		t.Fatalf("got %v", got)
	}
}

func unlockRows() []roleUnlockItem {
	return []roleUnlockItem{
		{ID: "r/intro", Kind: "story", Title: "Intro", FromName: "Elena"},
		{ID: "r/a", Kind: "ticket"},
		{ID: "r/q1", Kind: "quiz", Title: "Users and logs", EstMinutes: 5, Questions: 4},
		{ID: "r/w", Kind: "writing", Title: "Write up"},
		{ID: "r/s1", Kind: "story", Title: "A ask", FromName: "Carmen"},
		{ID: "r/f", Kind: "feedback", Title: "Feedback round", FromName: "Carmen"},
		{ID: "r/b", Kind: "ticket"},
		{ID: "r/q2", Kind: "quiz", Title: "Later", EstMinutes: 3, Questions: 3},
		{ID: "r/done", Kind: "quiz", Title: "Done", EstMinutes: 3, Questions: 3, Done: true},
	}
}

func TestUnlockInboxItems(t *testing.T) {
	got := unlockInboxItems(unlockRows(), "r/a")
	var refs []string
	for _, g := range got {
		refs = append(refs, g.Kind+":"+g.Ref)
	}
	// The story before the first ticket, the writing task, the next ticket's items and a completed
	// item never appear; the feedback item does, as a story.
	if want := []string{"quiz:r/q1", "story:r/s1", "story:r/f"}; !reflect.DeepEqual(refs, want) {
		t.Fatalf("after r/a: %v want %v", refs, want)
	}
	if got := unlockInboxItems(unlockRows(), "r/b"); len(got) != 1 || got[0].Ref != "r/q2" {
		t.Fatalf("after r/b (the completed quiz is skipped): %+v", got)
	}
	if got := unlockInboxItems(unlockRows(), "r/none"); len(got) != 0 {
		t.Fatalf("unknown ticket: %+v", got)
	}
}

func TestUnlockInboxTexts(t *testing.T) {
	got := unlockInboxItems(unlockRows(), "r/a")
	q, s, f := got[0], got[1], got[2]
	if q.Title != "Quiz ready: Users and logs" || q.Body != "4 questions, about 5 minutes" || q.URL != "/quiz/r/q1" || q.PushState != "none" {
		t.Fatalf("quiz: %+v", q)
	}
	if s.Title != "Carmen sent you a message" || s.Body != "A ask" || s.URL != "/story/r/s1" || s.PushState != "pending" {
		t.Fatalf("story: %+v", s)
	}
	if f.Title != "Carmen sent you feedback" || f.URL != "/story/r/f" || f.PushState != "pending" {
		t.Fatalf("feedback: %+v", f)
	}
}

func TestUnlockInboxFitsTheChecks(t *testing.T) {
	long := strings.Repeat("é", 300)
	rows := []roleUnlockItem{
		{ID: "r/a", Kind: "ticket"},
		{ID: "r/s", Kind: "story", Title: long, FromName: long},
		{ID: "r/q", Kind: "quiz", Title: long, EstMinutes: 240, Questions: 10},
	}
	for _, g := range unlockInboxItems(rows, "r/a") {
		if n := len([]rune(g.Title)); n < 1 || n > 200 {
			t.Fatalf("title %d runes", n)
		}
		if n := len([]rune(g.Body)); n < 1 || n > 500 {
			t.Fatalf("body %d runes", n)
		}
		if g.URL[0] != '/' || strings.HasPrefix(g.URL, "//") || strings.ContainsAny(g.URL, "\\\x00\n") {
			t.Fatalf("url %q", g.URL)
		}
	}
}

func TestUnlockURLEscapesEachSegment(t *testing.T) {
	rows := []roleUnlockItem{{ID: "r/a", Kind: "ticket"}, {ID: "r/x y\\z", Kind: "quiz", Title: "T", Questions: 1, EstMinutes: 1}}
	got := unlockInboxItems(rows, "r/a")
	if len(got) != 1 || got[0].URL != "/quiz/r/x%20y%5Cz" {
		t.Fatalf("%+v", got)
	}
}
