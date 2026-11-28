package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/catalog"
)

var (
	// ErrItemNotFound means no item of the route's kind (quiz or story) has that id.
	ErrItemNotFound = errors.New("item not found")
	// ErrQuestionNotFound means the quiz does not list that question.
	ErrQuestionNotFound = errors.New("question not found")
	// ErrChoiceRange means the choice is not one of the question's choices.
	ErrChoiceRange = errors.New("choice out of range")
)

// AnswerQuizIn is one answer to a quiz question (design 3.10).
type AnswerQuizIn struct {
	Learner    pgtype.UUID
	ItemID     string
	QuestionID string
	Choice     int
}

// AnswerQuizOut is the answer of POST /v1/items/{id}/answers.
type AnswerQuizOut struct {
	Correct         bool     `json:"correct"`
	Answer          int      `json:"answer"`
	Notes           []string `json:"notes"`
	Explain         *string  `json:"explain"`
	Done            bool     `json:"done"`
	Answered        int      `json:"answered"`
	Total           int      `json:"total"`
	FirstTryCorrect int      `json:"first_try_correct"`
}

// AnswerQuiz records one answer under the learner's class-1 lock, taken before anything is read, so two
// answers racing on the last question cannot both miss the completion. The first answer to a question
// keeps first_*; every answer writes last_*. The item_completions row is inserted when every question
// the quiz lists now has an answer, and then `event: item` is sent at commit.
func (s *Store) AnswerQuiz(ctx context.Context, in AnswerQuizIn) (AnswerQuizOut, error) {
	var out AnswerQuizOut
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
	if errors.Is(err, pgx.ErrNoRows) || (err == nil && view.Kind != "quiz") {
		return out, ErrItemNotFound
	}
	if err != nil {
		return out, fmt.Errorf("load item: %w", err)
	}
	var quiz catalog.Quiz
	if err := json.Unmarshal(view.Payload, &quiz); err != nil {
		return out, fmt.Errorf("quiz payload: %w", err)
	}
	var asked *catalog.Question
	for i := range quiz.Questions {
		if quiz.Questions[i].ID == in.QuestionID {
			asked = &quiz.Questions[i]
		}
	}
	if asked == nil {
		return out, ErrQuestionNotFound
	}
	if in.Choice < 0 || in.Choice >= len(asked.Choices) {
		return out, ErrChoiceRange
	}
	if asked.Answer == nil {
		// Learner-sent ids stay out of errors: they end up in logs.
		return out, errors.New("quiz question has no answer")
	}
	out.Correct = in.Choice == *asked.Answer
	if err := q.UpsertQuizAnswer(ctx, UpsertQuizAnswerParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID,
		QuestionID: in.QuestionID, Choice: int16(in.Choice), //nolint:gosec // range-checked above: 0 to 4
		Correct: out.Correct}); err != nil {
		return out, fmt.Errorf("write answer: %w", err)
	}

	rows, err := q.ListQuizAnswers(ctx, ListQuizAnswersParams{LearnerID: l.ID, ItemID: in.ItemID})
	if err != nil {
		return out, fmt.Errorf("list answers: %w", err)
	}
	firstCorrect := make(map[string]bool, len(rows))
	for _, r := range rows {
		firstCorrect[r.QuestionID] = r.FirstCorrect
	}
	out.Total = len(quiz.Questions)
	for _, qu := range quiz.Questions {
		if fc, ok := firstCorrect[qu.ID]; ok {
			out.Answered++
			if fc {
				out.FirstTryCorrect++
			}
		}
	}
	out.Answer = *asked.Answer
	out.Notes = make([]string, len(asked.Choices))
	for i, c := range asked.Choices {
		out.Notes[i] = c.Note
	}
	if asked.Explain != "" {
		e := asked.Explain
		out.Explain = &e
	}

	out.Done, err = q.ItemIsDone(ctx, ItemIsDoneParams{LearnerID: l.ID, ItemID: in.ItemID})
	if err != nil {
		return out, fmt.Errorf("check done: %w", err)
	}
	if !out.Done && out.Answered == out.Total {
		_, err := q.InsertItemCompletion(ctx, InsertItemCompletionParams{OrgID: l.OrgID, LearnerID: l.ID, ItemID: in.ItemID})
		if errors.Is(err, pgx.ErrNoRows) { // the lock makes this unreachable; never announce twice
			out.Done = true
			return out, tx.Commit(ctx)
		}
		if err != nil {
			return out, fmt.Errorf("write completion: %w", err)
		}
		out.Done = true
		msg, _ := json.Marshal(map[string]string{"event": "item", "item_id": in.ItemID, "kind": "quiz"})
		if err := notifyLearner(ctx, tx, l.ID, msg); err != nil {
			return out, fmt.Errorf("notify: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return out, fmt.Errorf("commit: %w", err)
	}
	return out, nil
}
