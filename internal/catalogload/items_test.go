package catalogload

import (
	"encoding/json"
	"testing"

	"github.com/kibanyadev/dojo/internal/catalog"
)

func TestItemRowsEveryKind(t *testing.T) {
	zero := 0
	cat := &catalog.Catalog{
		Tickets:  []*catalog.Ticket{{ID: "r/t", Role: "r", Order: 1, Title: "T", Brief: "b", Hints: []string{"h"}}},
		Practice: []*catalog.Ticket{{ID: "practice/a", Title: "A", Brief: "b", Hints: []string{}, Difficulty: "hard"}, {ID: "practice/b", Title: "B", Brief: "b", Hints: []string{}}},
		Writings: []*catalog.Writing{{Item: catalog.Item{ID: "r/w", Role: "r", Order: 2, Title: "W"}, Prompt: "p",
			Rubric: []catalog.Criterion{{ID: "x", Text: "X"}}, AwardsMark: "m"}},
		Quizzes: []*catalog.Quiz{{Item: catalog.Item{ID: "r/q", Role: "r", Order: 3, Title: "Q"},
			Questions: []catalog.Question{{ID: "q1", Text: "?", Choices: []catalog.QuizChoice{{Text: "a", Note: "n"}}, Answer: &zero}}}},
		Stories: []*catalog.Story{{Item: catalog.Item{ID: "r/s", Role: "r", Order: 4, Title: "S"}, From: catalog.From{Name: "E", Role: "F"}, Scene: "sc",
			Choices: []catalog.StoryChoice{{ID: "c", Text: "t", Verdict: "good", Reaction: "r"}}}},
		Feedbacks: []*catalog.Feedback{{Item: catalog.Item{ID: "r/f", Role: "r", Order: 5, Title: "F"}, From: catalog.From{Name: "C", Role: "L"}, Scene: "sc",
			Rubric: []catalog.Criterion{{ID: "y", Text: "Y"}}}},
	}
	rows, err := itemRows(cat, make([]*catalog.Bundle, 3))
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 7 {
		t.Fatalf("rows = %d", len(rows))
	}
	by := map[string]itemRow{}
	for _, r := range rows {
		by[r.ID] = r
	}
	if r := by["r/t"]; r.Kind != "ticket" || r.Role == nil || *r.Role != "r" || r.Payload != nil || r.Brief == nil || r.Difficulty != nil || r.Position != 1 {
		t.Errorf("ticket = %+v", r)
	}
	if a, b := by["practice/a"], by["practice/b"]; a.Kind != "practice" || a.Role != nil || a.Position != 1 || b.Position != 2 ||
		a.Difficulty == nil || *a.Difficulty != "hard" || b.Difficulty != nil || a.Payload != nil || a.Brief == nil || a.Hints == nil {
		t.Errorf("practice = %+v %+v", a, b)
	}
	for id, want := range map[string]string{
		"r/w": `{"prompt":"p","rubric":[{"id":"x","text":"X"}]}`,
		"r/q": `{"questions":[{"id":"q1","text":"?","choices":[{"text":"a","note":"n"}],"answer":0}]}`,
		"r/s": `{"choices":[{"id":"c","text":"t","verdict":"good","reaction":"r"}],"from":{"name":"E","role":"F"},"scene":"sc"}`,
		"r/f": `{"from":{"name":"C","role":"L"},"rubric":[{"id":"y","text":"Y"}],"scene":"sc"}`,
	} {
		r := by[id]
		if r.Payload == nil || !json.Valid([]byte(*r.Payload)) || *r.Payload != want {
			t.Errorf("%s payload = %v, want %s", id, r.Payload, want)
		}
		if r.Brief != nil || r.Hints != nil || r.Difficulty != nil || r.Bundle != nil {
			t.Errorf("%s keeps ticket columns: %+v", id, r)
		}
	}
	if m := by["r/w"].AwardsMark; m == nil || *m != "m" || by["r/q"].AwardsMark != nil || by["r/f"].AwardsMark != nil {
		t.Errorf("awards_mark wrong: %v %v %v", by["r/w"].AwardsMark, by["r/q"].AwardsMark, by["r/f"].AwardsMark)
	}
}

func TestDropAndKindChecksCoverEveryLearnerTable(t *testing.T) {
	for _, tbl := range []string{"ticket_progress", "hint_opens", "item_completions", "quiz_answers", "story_choices"} {
		if !contains(learnerTables, tbl) {
			t.Errorf("learnerTables misses %s", tbl)
		}
	}
}

func contains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}
