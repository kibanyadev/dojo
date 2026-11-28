package catalogload

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kibanyadev/dojo/internal/catalog"
)

// learnerTables are the tables that hold learner rows keyed by catalog_items.id through item_id.
// A catalog item with a row in any of them cannot be dropped or change kind. Add written_work here
// when its migration lands.
var learnerTables = []string{"ticket_progress", "hint_opens", "item_completions", "quiz_answers", "story_choices"}

// itemRow is one catalog_items row; a nil pointer or slice is written as NULL.
type itemRow struct {
	ID         string
	Role       *string
	Kind       string
	Position   int
	Title      string
	EstMinutes int
	Tags       []string
	Brief      *string
	Hints      []string
	Payload    *string
	AwardsMark *string
	Difficulty *string
	Bundle     *catalog.Bundle
}

// itemRows turns every catalog item into its row. bundles holds the role tickets' bundles in
// cat.Tickets order, then the practice bundles in cat.Practice order. Practice is numbered 1..n in
// id order and every column a kind lacks stays nil.
func itemRows(cat *catalog.Catalog, bundles []*catalog.Bundle) ([]itemRow, error) {
	var rows []itemRow
	for i, t := range cat.Tickets {
		role := t.Role
		rows = append(rows, ticketRow(t, "ticket", &role, t.Order, bundles[i]))
	}
	for i, t := range cat.Practice {
		r := ticketRow(t, "practice", nil, i+1, bundles[len(cat.Tickets)+i])
		if t.Difficulty != "" {
			d := t.Difficulty
			r.Difficulty = &d
		}
		rows = append(rows, r)
	}
	add := func(it catalog.Item, kind, awards string, payload any) error {
		b, err := json.Marshal(payload)
		if err != nil {
			return fmt.Errorf("%s: payload: %w", it.ID, err)
		}
		p, role := string(b), it.Role
		r := itemRow{ID: it.ID, Role: &role, Kind: kind, Position: it.Order, Title: it.Title,
			EstMinutes: it.EstMinutes, Tags: nonNil(it.Tags), Payload: &p}
		if awards != "" {
			r.AwardsMark = &awards
		}
		rows = append(rows, r)
		return nil
	}
	for _, w := range cat.Writings {
		if err := add(w.Item, "writing", w.AwardsMark, map[string]any{"prompt": w.Prompt, "rubric": w.Rubric}); err != nil {
			return nil, err
		}
	}
	for _, q := range cat.Quizzes {
		if err := add(q.Item, "quiz", "", map[string]any{"questions": q.Questions}); err != nil {
			return nil, err
		}
	}
	for _, s := range cat.Stories {
		if err := add(s.Item, "story", "", map[string]any{"from": s.From, "scene": s.Scene, "choices": s.Choices}); err != nil {
			return nil, err
		}
	}
	for _, f := range cat.Feedbacks {
		if err := add(f.Item, "feedback", f.AwardsMark, map[string]any{"from": f.From, "scene": f.Scene, "rubric": f.Rubric}); err != nil {
			return nil, err
		}
	}
	return rows, nil
}

func ticketRow(t *catalog.Ticket, kind string, role *string, position int, b *catalog.Bundle) itemRow {
	brief := t.Brief
	return itemRow{ID: t.ID, Role: role, Kind: kind, Position: position, Title: t.Title, EstMinutes: t.EstMinutes,
		Tags: nonNil(t.Tags), Brief: &brief, Hints: nonNil(t.Hints), Bundle: b}
}

// hasLearnerRows is the SQL condition, for a catalog_items row aliased c, that any learner row exists.
func hasLearnerRows() string {
	parts := make([]string, len(learnerTables))
	for i, t := range learnerTables {
		parts[i] = "EXISTS (SELECT 1 FROM " + t + " x WHERE x.item_id = c.id)"
	}
	return strings.Join(parts, " OR ")
}
