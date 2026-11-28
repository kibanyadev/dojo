package repo

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// UnlockedItems is the unlocking rule of design 3.10, derived from positions. items are one role's
// items in position order; passed holds its tickets in state passed or revisit. A non-ticket item is
// unlocked when the nearest ticket before it is passed, or when no ticket comes before it. The result
// has an entry for every non-ticket item.
func UnlockedItems(items []ListCatalogItemsRow, passed map[string]bool) map[string]bool {
	out := map[string]bool{}
	open := true
	for _, it := range items {
		if it.Kind == "ticket" {
			open = passed[it.ID]
			continue
		}
		out[it.ID] = open
	}
	return out
}

// PassedTickets is the set of tickets a learner has passed, now or in a revisit.
func PassedTickets(prog []ListLearnerTicketStatesRow) map[string]bool {
	passed := make(map[string]bool, len(prog))
	for _, x := range prog {
		passed[x.ItemID] = x.State == "passed" || x.State == "revisit"
	}
	return passed
}

type roleUnlockItem = ListRoleUnlockItemsRow

// unlockInbox is one inbox row a first pass writes.
type unlockInbox struct {
	Kind, Ref, Title, Body, URL, PushState string
}

// unlockInboxItems returns the story and quiz rows that the first pass of ticket unlocks, in position
// order: the story, feedback and quiz items the learner has not completed that the rule of design 3.10
// opens with this ticket and did not open before it. Writing tasks get no row.
func unlockInboxItems(items []roleUnlockItem, ticket string) []unlockInbox {
	rows := make([]ListCatalogItemsRow, len(items))
	for i, it := range items {
		rows[i] = ListCatalogItemsRow{ID: it.ID, Kind: it.Kind}
	}
	before, after := UnlockedItems(rows, nil), UnlockedItems(rows, map[string]bool{ticket: true})
	var out []unlockInbox
	for _, it := range items {
		if it.Done || !after[it.ID] || before[it.ID] {
			continue
		}
		path := escapePath(it.ID)
		switch it.Kind {
		case "story", "feedback":
			what := "a message"
			if it.Kind == "feedback" {
				what = "feedback"
			}
			out = append(out, unlockInbox{"story", it.ID, clip(it.FromName+" sent you "+what, 200), clip(it.Title, 500), "/story/" + path, "pending"})
		case "quiz":
			out = append(out, unlockInbox{"quiz", it.ID, clip("Quiz ready: "+it.Title, 200),
				fmt.Sprintf("%d questions, about %d minutes", it.Questions, it.EstMinutes), "/quiz/" + path, "none"})
		}
	}
	return out
}

// clip keeps the first n characters, so a long catalog text never trips a column CHECK.
func clip(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

func escapePath(id string) string {
	parts := strings.Split(id, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return strings.Join(parts, "/")
}

// unlockOnFirstPass writes the inbox rows that the first pass of ticket unlocks (design 3.10) inside tx
// and reports whether it inserted any, so the caller sends `event: inbox` once at commit.
func unlockOnFirstPass(ctx context.Context, q *Queries, l Learner, ticket string, now pgtype.Timestamptz) (bool, error) {
	items, err := q.ListRoleUnlockItems(ctx, ListRoleUnlockItemsParams{LearnerID: l.ID, OrgID: l.OrgID, TicketID: ticket})
	if err != nil {
		return false, fmt.Errorf("list unlock items: %w", err)
	}
	inserted := false
	for _, u := range unlockInboxItems(items, ticket) {
		next := pgtype.Timestamptz{}
		if u.PushState == "pending" {
			next = now
		}
		n, err := q.InsertUnlockInboxItem(ctx, InsertUnlockInboxItemParams{
			OrgID: l.OrgID, LearnerID: l.ID, Kind: u.Kind, Ref: u.Ref, Title: u.Title, Body: u.Body, Url: u.URL,
			PushState: u.PushState, PushNextAt: next,
		})
		if err != nil {
			return false, fmt.Errorf("insert unlock inbox item: %w", err)
		}
		inserted = inserted || n > 0
	}
	return inserted, nil
}
