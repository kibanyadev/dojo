package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/catalog"
)

var (
	// ErrChoiceNotFound means the story does not list that choice.
	ErrChoiceNotFound = errors.New("choice not found")
	// ErrAlreadyChosen means the learner already picked a different choice; a pick is final.
	ErrAlreadyChosen = errors.New("story already chosen")
)

// ChooseStoryIn is one pick of a story moment (design 3.10).
type ChooseStoryIn struct {
	Learner  pgtype.UUID
	ItemID   string
	ChoiceID string
}

// StoryChosen is the picked choice with its authored verdict and reaction.
type StoryChosen struct {
	ID       string    `json:"id"`
	Verdict  string    `json:"verdict"`
	Reaction string    `json:"reaction"`
	ChosenAt time.Time `json:"chosen_at"`
}

// ChooseStoryOut is the answer of POST /v1/items/{id}/choice.
type ChooseStoryOut struct {
	Chosen StoryChosen `json:"chosen"`
}

// ChooseStory records the pick and the completion under the learner's class-1 lock, taken before
// anything is read, so two picks racing cannot both insert. The same choice again answers with the
// stored pick and sends nothing; a different one is ErrAlreadyChosen. `event: item` goes out at commit.
func (s *Store) ChooseStory(ctx context.Context, in ChooseStoryIn) (ChooseStoryOut, error) {
	var out ChooseStoryOut
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return out, fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	q := s.q.WithTx(tx)

	if err := q.LockLearnerProgress(ctx, in.Learner); err != nil {
		return out, fmt.Errorf("lock: %w", err)
	}
	l, err := q.GetLearnerByID(ctx, in.Learner)
	if errors.Is(err, pgx.ErrNoRows) {
		return out, ErrLearnerGone
	}
	if err != nil {
		return out, fmt.Errorf("load learner: %w", err)
	}
	view, err := q.GetItemView(ctx, in.ItemID)
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && view.Kind != "story") {
		return out, ErrItemNotFound
	}
	if err != nil {
		return out, fmt.Errorf("load item: %w", err)
	}
	var story catalog.Story
	if err := json.Unmarshal(view.Payload, &story); err != nil {
		return out, fmt.Errorf("story payload: %w", err)
	}
	var picked *catalog.StoryChoice
	for i := range story.Choices {
		if story.Choices[i].ID == in.ChoiceID {
			picked = &story.Choices[i]
		}
	}
	if picked == nil {
		return out, ErrChoiceNotFound
	}
	out.Chosen = StoryChosen{ID: picked.ID, Verdict: picked.Verdict, Reaction: picked.Reaction}

	prev, err := q.GetStoryChoice(ctx, GetStoryChoiceParams{LearnerID: l.ID, ItemID: in.ItemID})
	switch {
	case err == nil:
		if prev.ChoiceID != in.ChoiceID {
			return out, ErrAlreadyChosen
		}
		out.Chosen.ChosenAt = prev.ChosenAt.Time.UTC().Truncate(time.Second)
		return out, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return out, fmt.Errorf("read choice: %w", err)
	}

	at, err := q.InsertStoryChoice(ctx, InsertStoryChoiceParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID, ChoiceID: in.ChoiceID})
	if err != nil {
		return out, fmt.Errorf("write choice: %w", err)
	}
	out.Chosen.ChosenAt = at.Time.UTC().Truncate(time.Second)
	if _, err := q.InsertItemCompletion(ctx, InsertItemCompletionParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID}); err != nil {
		return out, fmt.Errorf("write completion: %w", err)
	}
	msg, _ := json.Marshal(map[string]string{"event": "item", "item_id": in.ItemID, "kind": "story"})
	if err := notifyLearner(ctx, tx, l.ID, msg); err != nil {
		return out, fmt.Errorf("notify: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}
