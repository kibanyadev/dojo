package repo

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/jackc/pgx/v5/pgtype"
)

// ReviewLease is one pending review claimed by a worker task (design 3.10), with its item.
type ReviewLease struct {
	OrgID       pgtype.UUID
	LearnerID   pgtype.UUID
	ItemID      string
	Body        string
	SubmitCount int32
	Tries       int32
	Kind        string
	Title       string
	Payload     []byte
}

// leaseReviewsSQL is the lease of design 3.10, wrapped to also return the item. The 5 minutes are the wait
// of a row whose worker dies, and of a throttled call.
const leaseReviewsSQL = `
WITH leased AS (
    UPDATE written_work SET review_next_at = now() + interval '5 minutes'
    WHERE (learner_id, item_id) IN (
        SELECT learner_id, item_id FROM written_work
        WHERE review_state = 'pending' AND review_next_at <= now()
        ORDER BY review_next_at LIMIT $1 FOR UPDATE SKIP LOCKED)
    RETURNING org_id, learner_id, item_id, body, submit_count, review_tries)
SELECT w.org_id, w.learner_id, w.item_id, coalesce(w.body, ''), w.submit_count, w.review_tries::int4, i.kind, i.title, i.payload
FROM leased w JOIN catalog_items i ON i.id = w.item_id`

// LeaseReviews claims up to n due pending reviews for 5 minutes. Rows another task holds are skipped.
func (s *Store) LeaseReviews(ctx context.Context, n int) ([]ReviewLease, error) {
	rows, err := s.pool.Query(ctx, leaseReviewsSQL, n)
	if err != nil {
		return nil, fmt.Errorf("lease reviews: %w", err)
	}
	defer rows.Close()
	var out []ReviewLease
	for rows.Next() {
		var l ReviewLease
		if err := rows.Scan(&l.OrgID, &l.LearnerID, &l.ItemID, &l.Body, &l.SubmitCount, &l.Tries, &l.Kind, &l.Title, &l.Payload); err != nil {
			return nil, fmt.Errorf("lease reviews: %w", err)
		}
		out = append(out, l)
	}
	return out, rows.Err()
}

// ReviewCallsToday is the learner's review calls of the current UTC day that counted toward the cap.
func (s *Store) ReviewCallsToday(ctx context.Context, learner pgtype.UUID) (int64, error) {
	n, err := s.q.CountReviewCallsToday(ctx, learner)
	if err != nil {
		return 0, fmt.Errorf("count reviews: %w", err)
	}
	return n, nil
}

// ReviewCall is one adapter call: it becomes one ai_reviews row and never holds learner text.
type ReviewCall struct {
	Model     string
	TokensIn  int32
	TokensOut int32
	Outcome   string // ok, invalid or error
}

// ReviewResult is what a worker attempt decided. State is the new review_state: done (Review, Met and Total
// set), pending (a counted attempt, retry after DelaySeconds), failed or capped. Call is nil when no call
// was made.
type ReviewResult struct {
	Lease        ReviewLease
	Call         *ReviewCall
	State        string
	Tries        int32
	DelaySeconds float64
	Review       []byte
	Met, Total   int
}

const finishReviewSQL = `
UPDATE written_work SET updated_at = now(), review_state = $4::text, review_tries = $5,
    review = CASE WHEN $4::text = 'done' THEN $6::jsonb ELSE review END,
    reviewed_at = CASE WHEN $4::text = 'done' THEN now() ELSE reviewed_at END,
    review_next_at = CASE WHEN $4::text = 'pending' THEN now() + make_interval(secs => $7::float8) ELSE review_next_at END
WHERE learner_id = $1 AND item_id = $2 AND review_state = 'pending' AND submit_count = $3`

const insertReviewCallSQL = `
INSERT INTO ai_reviews (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome)
VALUES ($1, (SELECT id FROM learners WHERE id = $2), $3, $4, $5, $6, $7, $8)`

const upsertReviewReadySQL = `
INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url)
VALUES ($1, $2, 'review_ready', $3, $4, $5, $6)
ON CONFLICT (learner_id, kind, ref) DO UPDATE
SET title = EXCLUDED.title, body = EXCLUDED.body, url = EXCLUDED.url, read_at = NULL, updated_at = now()`

// FinishReview records the call (always, learner_id read through a subselect so a learner deleted meanwhile
// gives null) and writes the result only while the row is still pending at the submit_count that was read,
// so a resubmit that landed meanwhile is never overwritten. A stored review, a failure or a cap sends
// `event: review` at commit; a stored review also upserts the learner's `review_ready` item, unread again,
// and sends `event: inbox`. It reports whether the row was updated.
func (s *Store) FinishReview(ctx context.Context, r ReviewResult) (bool, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	l := r.Lease
	if c := r.Call; c != nil {
		if _, err := tx.Exec(ctx, insertReviewCallSQL, l.OrgID, l.LearnerID, l.ItemID, l.SubmitCount, c.Model, c.TokensIn, c.TokensOut, c.Outcome); err != nil {
			return false, fmt.Errorf("record call: %w", err)
		}
	}
	tag, err := tx.Exec(ctx, finishReviewSQL, l.LearnerID, l.ItemID, l.SubmitCount, r.State, r.Tries, r.Review, r.DelaySeconds)
	if err != nil {
		return false, fmt.Errorf("finish review: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return false, tx.Commit(ctx)
	}
	if r.State != "pending" {
		msg, _ := json.Marshal(map[string]string{"event": "review", "item_id": l.ItemID, "review_state": r.State})
		if err := notifyLearner(ctx, tx, l.LearnerID, msg); err != nil {
			return false, fmt.Errorf("notify: %w", err)
		}
	}
	if r.State == "done" {
		title, body := "Review ready: "+l.Title, fmt.Sprintf("%d of %d criteria", r.Met, r.Total)
		if _, err := tx.Exec(ctx, upsertReviewReadySQL, l.OrgID, l.LearnerID, l.ItemID, title, body, reviewURL(l.Kind, l.ItemID)); err != nil {
			return false, fmt.Errorf("inbox item: %w", err)
		}
		if err := notifyInbox(ctx, tx, l.LearnerID); err != nil {
			return false, fmt.Errorf("notify: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("commit: %w", err)
	}
	return true, nil
}

// reviewURL is the in-app route of the reviewed item: /w/<id> for writing, /story/<id> for feedback, each
// path segment escaped.
func reviewURL(kind, id string) string {
	prefix := "/w/"
	if kind == "feedback" {
		prefix = "/story/"
	}
	parts := strings.Split(id, "/")
	for i, p := range parts {
		parts[i] = url.PathEscape(p)
	}
	return prefix + strings.Join(parts, "/")
}
