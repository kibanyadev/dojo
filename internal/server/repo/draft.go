package repo

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// SaveDraftIn is one autosave of PUT /v1/items/{id}/draft (design 3.10). The text is already checked.
type SaveDraftIn struct {
	Learner pgtype.UUID
	ItemID  string
	Draft   string
}

// SaveDraft upserts the learner's draft of a writing or feedback item and returns the save time. Any
// other kind, or an unknown id, is ErrItemNotFound; a learner deleted meanwhile is ErrLearnerGone.
func (s *Store) SaveDraft(ctx context.Context, in SaveDraftIn) (time.Time, error) {
	view, err := s.q.GetItemView(ctx, in.ItemID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && view.Kind != "writing" && view.Kind != "feedback") {
		return time.Time{}, ErrItemNotFound
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("load item: %w", err)
	}
	saved, err := s.q.UpsertDraft(ctx, UpsertDraftParams{ItemID: in.ItemID, Draft: in.Draft, LearnerID: in.Learner})
	if errors.Is(err, pgx.ErrNoRows) {
		return time.Time{}, ErrLearnerGone
	}
	if err != nil {
		return time.Time{}, fmt.Errorf("save draft: %w", err)
	}
	return saved.Time, nil
}
