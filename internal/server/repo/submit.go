package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// DailyReviewCap is the hidden soft cap of review calls per learner per UTC day (design 3.10).
const DailyReviewCap = 20

// SubmitWorkIn is one submit of POST /v1/items/{id}/submit. The body is already trimmed and checked.
type SubmitWorkIn struct {
	Learner pgtype.UUID
	ItemID  string
	Body    string
}

// SubmitWork stores the work and its completion in one transaction under the learner's class-1 lock,
// taken before anything is read. The review starts pending, or capped when the learner already used
// today's calls. The first submit inserts the completion and sends `event: item` at commit; a resubmit
// does neither. Any other kind, or an unknown id, is ErrItemNotFound.
func (s *Store) SubmitWork(ctx context.Context, in SubmitWorkIn) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if err := q.LockLearnerProgress(ctx, in.Learner); err != nil {
		return fmt.Errorf("lock: %w", err)
	}
	l, err := q.GetLearnerByID(ctx, in.Learner)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrLearnerGone
	}
	if err != nil {
		return fmt.Errorf("load learner: %w", err)
	}
	view, err := q.GetItemView(ctx, in.ItemID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && view.Kind != "writing" && view.Kind != "feedback") {
		return ErrItemNotFound
	}
	if err != nil {
		return fmt.Errorf("load item: %w", err)
	}
	calls, err := q.CountReviewCallsToday(ctx, l.ID)
	if err != nil {
		return fmt.Errorf("count reviews: %w", err)
	}
	state := "pending"
	if calls >= DailyReviewCap {
		state = "capped"
	}
	if err := q.SubmitWork(ctx, SubmitWorkParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID, Body: in.Body, State: state}); err != nil {
		return fmt.Errorf("write work: %w", err)
	}
	_, err = q.InsertItemCompletion(ctx, InsertItemCompletionParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID})
	first := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("write completion: %w", err)
	}
	if first {
		msg, _ := json.Marshal(map[string]string{"event": "item", "item_id": in.ItemID, "kind": view.Kind})
		if err := notifyLearner(ctx, tx, l.ID, msg); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
