package repo

import (
	"context"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// lastCheckApplies is the 3.8 ordering rule: a check event sets the last check unless it is older than
// the recorded one. cur is nil or has no check yet for a first check.
func lastCheckApplies(cur *TicketProgress, at time.Time) bool {
	return cur == nil || !cur.LastCheckAt.Valid || !at.Before(cur.LastCheckAt.Time)
}

// capAt is the event time the projection uses: the client's, never later than the server clock.
func capAt(at, now time.Time) time.Time {
	if at.After(now) {
		return now
	}
	return at
}

// checkInboxText builds the title, body and in-app URL of a check item (design 3.10). The id comes from
// the catalog row, never from the request, and each segment is escaped so the URL passes the table's
// same-origin CHECK.
func checkInboxText(id string, ok bool, passed, total pgtype.Int4) (title, body, path string) {
	parts := strings.Split(id, "/")
	slug := parts[len(parts)-1]
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	path = "/t/" + strings.Join(parts, "/")
	if ok {
		return "Ticket passed · " + slug, "Your fix survived the reboot.", path
	}
	body = "Open the ticket to see the output."
	if passed.Valid && total.Valid && total.Int32 > passed.Int32 {
		body = fmt.Sprintf("%d of %d checks still red.", total.Int32-passed.Int32, total.Int32)
	}
	return "Check failed · " + slug, body, path
}

// upsertCheckItems writes the check item of every ticket whose last check this batch updated, in the
// events transaction, and sends `event: inbox` once when it wrote any. A newer check resets the row to
// unread and queues its push (push_state pending, due now); the push worker comes later.
func upsertCheckItems(ctx context.Context, tx pgx.Tx, q *Queries, org, learner pgtype.UUID, rows map[string]*TicketProgress, items map[string]bool, now time.Time) error {
	for id := range items {
		p := rows[id]
		title, body, path := checkInboxText(id, p.LastCheckOk.Bool, p.LastCheckPassed, p.LastCheckTotal)
		if err := q.UpsertInboxCheck(ctx, UpsertInboxCheckParams{
			OrgID: org, LearnerID: learner, Ref: id, Title: title, Body: body, Url: path, At: ts(now),
		}); err != nil {
			return fmt.Errorf("upsert check item: %w", err)
		}
	}
	if len(items) == 0 {
		return nil
	}
	if err := notifyInbox(ctx, tx, learner); err != nil {
		return fmt.Errorf("notify inbox: %w", err)
	}
	return nil
}
