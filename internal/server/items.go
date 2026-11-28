package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/catalog"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

const itemsPrefix = "/v1/items/"

// itemActions are the action words after an item id (design 3.10); the reads here take none.
var itemActions = map[string]bool{"draft": true, "submit": true, "answers": true, "choice": true}

// parseItemPath splits a /v1/items/ subtree path by segment count: two segments are an item id, three
// are an id and one of the four action words. Any other count, an unknown action or a malformed id
// is not a route. The id is validated before any SQL.
func parseItemPath(p string) (id, action string, ok bool) {
	rest, found := strings.CutPrefix(p, itemsPrefix)
	if !found {
		return "", "", false
	}
	parts := strings.Split(rest, "/")
	if len(parts) != 2 && len(parts) != 3 {
		return "", "", false
	}
	id = parts[0] + "/" + parts[1]
	if !catalog.ValidTicketID(id) {
		return "", "", false
	}
	if len(parts) == 3 {
		if !itemActions[parts[2]] {
			return "", "", false
		}
		return id, parts[2], true
	}
	return id, "", true
}

// ItemReader reads what GET /v1/items/{id} answers.
type ItemReader interface {
	GetItemView(ctx context.Context, id string) (repo.GetItemViewRow, error)
	ItemIsDone(ctx context.Context, arg repo.ItemIsDoneParams) (bool, error)
	ListQuizAnswers(ctx context.Context, arg repo.ListQuizAnswersParams) ([]repo.ListQuizAnswersRow, error)
	GetStoryChoice(ctx context.Context, arg repo.GetStoryChoiceParams) (repo.GetStoryChoiceRow, error)
	GetWrittenWork(ctx context.Context, arg repo.GetWrittenWorkParams) (repo.GetWrittenWorkRow, error)
	GetSystemFlag(ctx context.Context, key string) ([]byte, error)
}

// DraftSaver stores one autosave of PUT /v1/items/{id}/draft.
type DraftSaver interface {
	SaveDraft(ctx context.Context, in repo.SaveDraftIn) (time.Time, error)
}

// QuizAnswerer records one answer of POST /v1/items/{id}/answers.
type QuizAnswerer interface {
	AnswerQuiz(ctx context.Context, in repo.AnswerQuizIn) (repo.AnswerQuizOut, error)
}

// StoryChooser records the pick of POST /v1/items/{id}/choice.
type StoryChooser interface {
	ChooseStory(ctx context.Context, in repo.ChooseStoryIn) (repo.ChooseStoryOut, error)
}

// Items serves GET /v1/items/{id} for quiz and story items, the quiz answers and the story choice
// (design 3.10).
type Items struct {
	Reader ItemReader
	// Answerer backs POST /v1/items/{id}/answers; nil leaves the action not found.
	Answerer QuizAnswerer
	// Chooser backs POST /v1/items/{id}/choice; nil leaves the action not found.
	Chooser StoryChooser
	// Drafter backs PUT /v1/items/{id}/draft; nil leaves the action not found.
	Drafter DraftSaver
}

const maxAnswerBody = 1 << 10

type answerRequest struct {
	Question *string `json:"question"`
	Choice   *int    `json:"choice"`
}

// postItemRoute is the POST /v1/items/ dispatcher. Only answers and choice are served so far; every
// other action word, a bare id and a malformed path are not found.
func (a Auth) postItemRoute(w http.ResponseWriter, r *http.Request) {
	id, action, ok := parseItemPath(r.URL.Path)
	switch {
	case ok && action == "answers" && a.Items.Answerer != nil:
		a.Items.answer(w, r, id)
	case ok && action == "choice" && a.Items.Chooser != nil:
		a.Items.choose(w, r, id)
	default:
		writeError(w, http.StatusNotFound, "not_found")
	}
}

type choiceRequest struct {
	Choice *string `json:"choice"`
}

func (it *Items) choose(w http.ResponseWriter, r *http.Request, id string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnswerBody))
	dec.DisallowUnknownFields()
	var req choiceRequest
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF || req.Choice == nil || *req.Choice == "" {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	if !catalog.ValidLocalID(*req.Choice) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, _ := principalOf(r.Context())
	out, err := it.Chooser.ChooseStory(context.WithoutCancel(r.Context()),
		repo.ChooseStoryIn{Learner: p.learner, ItemID: id, ChoiceID: *req.Choice})
	switch {
	case errors.Is(err, repo.ErrItemNotFound), errors.Is(err, repo.ErrChoiceNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, repo.ErrAlreadyChosen):
		writeError(w, http.StatusConflict, "already_chosen")
	case errors.Is(err, repo.ErrLearnerGone):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	case err != nil:
		slog.Error("story choice failed", "route", r.Pattern, "error", err)
		writeError(w, http.StatusInternalServerError, "server_error")
	default:
		writeNoStore(w, out)
	}
}

func (it *Items) answer(w http.ResponseWriter, r *http.Request, id string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxAnswerBody))
	dec.DisallowUnknownFields()
	var req answerRequest
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF ||
		req.Question == nil || *req.Question == "" || req.Choice == nil {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	p, _ := principalOf(r.Context())
	out, err := it.Answerer.AnswerQuiz(context.WithoutCancel(r.Context()),
		repo.AnswerQuizIn{Learner: p.learner, ItemID: id, QuestionID: *req.Question, Choice: *req.Choice})
	switch {
	case errors.Is(err, repo.ErrItemNotFound), errors.Is(err, repo.ErrQuestionNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, repo.ErrChoiceRange):
		writeError(w, http.StatusBadRequest, "bad_request")
	case errors.Is(err, repo.ErrLearnerGone):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	case err != nil:
		slog.Error("quiz answer failed", "route", r.Pattern, "error", err)
		writeError(w, http.StatusInternalServerError, "server_error")
	default:
		writeNoStore(w, out)
	}
}

type itemHead struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Role       string   `json:"role"`
	Title      string   `json:"title"`
	EstMinutes int32    `json:"est_minutes"`
	Tags       []string `json:"tags"`
	Done       bool     `json:"done"`
}

type quizAnswerState struct {
	FirstChoice  int16    `json:"first_choice"`
	FirstCorrect bool     `json:"first_correct"`
	LastChoice   int16    `json:"last_choice"`
	LastCorrect  bool     `json:"last_correct"`
	Answer       int      `json:"answer"`
	Notes        []string `json:"notes"`
	Explain      *string  `json:"explain"`
}

type quizQuestionView struct {
	ID          string           `json:"id"`
	Text        string           `json:"text"`
	Choices     []string         `json:"choices"`
	AnswerState *quizAnswerState `json:"answer_state"`
}

type quizView struct {
	itemHead
	Answered        int                `json:"answered"`
	Total           int                `json:"total"`
	FirstTryCorrect int                `json:"first_try_correct"`
	Questions       []quizQuestionView `json:"questions"`
}

type storyChoiceView struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}

type storyChosen struct {
	ID       string `json:"id"`
	Verdict  string `json:"verdict"`
	Reaction string `json:"reaction"`
	ChosenAt string `json:"chosen_at"`
}

type storyView struct {
	itemHead
	From    catalog.From      `json:"from"`
	Scene   string            `json:"scene"`
	Choices []storyChoiceView `json:"choices"`
	Chosen  *storyChosen      `json:"chosen"`
}

// buildQuiz answers from the stored payload. A question's answer, notes and explanation go out only
// once that question has an answer row; answers to a question the quiz no longer lists are ignored.
func buildQuiz(head itemHead, payload []byte, answers []repo.ListQuizAnswersRow) (quizView, error) {
	var p catalog.Quiz
	if err := json.Unmarshal(payload, &p); err != nil {
		return quizView{}, fmt.Errorf("quiz payload: %w", err)
	}
	byQ := make(map[string]repo.ListQuizAnswersRow, len(answers))
	for _, a := range answers {
		byQ[a.QuestionID] = a
	}
	out := quizView{itemHead: head, Total: len(p.Questions), Questions: make([]quizQuestionView, 0, len(p.Questions))}
	for _, q := range p.Questions {
		qv := quizQuestionView{ID: q.ID, Text: q.Text, Choices: make([]string, len(q.Choices))}
		for i, c := range q.Choices {
			qv.Choices[i] = c.Text
		}
		if a, ok := byQ[q.ID]; ok && q.Answer != nil {
			st := &quizAnswerState{FirstChoice: a.FirstChoice, FirstCorrect: a.FirstCorrect, LastChoice: a.LastChoice,
				LastCorrect: a.LastCorrect, Answer: *q.Answer, Notes: make([]string, len(q.Choices))}
			for i, c := range q.Choices {
				st.Notes[i] = c.Note
			}
			if q.Explain != "" {
				e := q.Explain
				st.Explain = &e
			}
			qv.AnswerState = st
			out.Answered++
			if a.FirstCorrect {
				out.FirstTryCorrect++
			}
		}
		out.Questions = append(out.Questions, qv)
	}
	return out, nil
}

// buildStory answers from the stored payload; only the picked choice's verdict and reaction go out.
// A pick whose id the catalog no longer lists keeps its id and time with empty verdict and reaction.
func buildStory(head itemHead, payload []byte, chosen *repo.GetStoryChoiceRow) (storyView, error) {
	var p catalog.Story
	if err := json.Unmarshal(payload, &p); err != nil {
		return storyView{}, fmt.Errorf("story payload: %w", err)
	}
	out := storyView{itemHead: head, From: p.From, Scene: p.Scene, Choices: make([]storyChoiceView, 0, len(p.Choices))}
	for _, c := range p.Choices {
		out.Choices = append(out.Choices, storyChoiceView{ID: c.ID, Text: c.Text})
	}
	if chosen != nil {
		ch := &storyChosen{ID: chosen.ChoiceID, ChosenAt: *timePtr(chosen.ChosenAt)}
		for _, c := range p.Choices {
			if c.ID == chosen.ChoiceID {
				ch.Verdict, ch.Reaction = c.Verdict, c.Reaction
			}
		}
		out.Chosen = ch
	}
	return out, nil
}

// getItemRoute is the GET /v1/items/ dispatcher. Every action word is a write, so a GET on one is
// not found, and so is a ticket, practice or writing id until those kinds are served.
func (a Auth) getItemRoute(w http.ResponseWriter, r *http.Request) {
	id, action, ok := parseItemPath(r.URL.Path)
	if !ok || action != "" {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.Items.read(w, r, id)
}

func (it *Items) read(w http.ResponseWriter, r *http.Request, id string) {
	p, _ := principalOf(r.Context())
	ctx := r.Context()
	view, err := it.Reader.GetItemView(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		readFailed(w, r, err)
		return
	}
	done, err := it.Reader.ItemIsDone(ctx, repo.ItemIsDoneParams{LearnerID: p.learner, ItemID: id})
	if err != nil {
		readFailed(w, r, err)
		return
	}
	head := itemHead{ID: view.ID, Kind: view.Kind, Role: view.Role, Title: view.Title, EstMinutes: view.EstMinutes,
		Tags: view.Tags, Done: done}
	if head.Tags == nil {
		head.Tags = []string{}
	}
	var out any
	switch view.Kind {
	case "quiz":
		var answers []repo.ListQuizAnswersRow
		if answers, err = it.Reader.ListQuizAnswers(ctx, repo.ListQuizAnswersParams{LearnerID: p.learner, ItemID: id}); err == nil {
			out, err = buildQuiz(head, view.Payload, answers)
		}
	case "story":
		var chosen *repo.GetStoryChoiceRow
		row, gerr := it.Reader.GetStoryChoice(ctx, repo.GetStoryChoiceParams{LearnerID: p.learner, ItemID: id})
		switch {
		case gerr == nil:
			chosen = &row
		case !errors.Is(gerr, pgx.ErrNoRows):
			err = gerr
		}
		if err == nil {
			out, err = buildStory(head, view.Payload, chosen)
		}
	case "writing", "feedback":
		out, err = it.buildWritten(ctx, head, view, p.learner)
	default:
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		readFailed(w, r, err)
		return
	}
	writeNoStore(w, out)
}

type awardsMarkView struct {
	Slug  string `json:"slug"`
	Title string `json:"title"`
	Held  bool   `json:"held"`
}

// workView is the learner's own row; review is the last finished review as stored.
type workView struct {
	Draft        string          `json:"draft"`
	DraftSavedAt string          `json:"draft_saved_at"`
	Body         *string         `json:"body"`
	SubmittedAt  *string         `json:"submitted_at"`
	SubmitCount  int32           `json:"submit_count"`
	ReviewState  *string         `json:"review_state"`
	ReviewedAt   *string         `json:"reviewed_at"`
	Review       json.RawMessage `json:"review"`
}

type writingView struct {
	itemHead
	Prompt     string              `json:"prompt"`
	Rubric     []catalog.Criterion `json:"rubric"`
	AwardsMark *awardsMarkView     `json:"awards_mark"`
	Work       *workView           `json:"work"`
}

type feedbackView struct {
	itemHead
	From       catalog.From        `json:"from"`
	Scene      string              `json:"scene"`
	Rubric     []catalog.Criterion `json:"rubric"`
	AwardsMark *awardsMarkView     `json:"awards_mark"`
	Work       *workView           `json:"work"`
}

// buildWritten answers a writing or feedback item. Until belt_awards exists (PR 21) a mark is never held.
func (it *Items) buildWritten(ctx context.Context, head itemHead, view repo.GetItemViewRow, learner pgtype.UUID) (any, error) {
	var mark *awardsMarkView
	if view.AwardsMarkSlug.Valid {
		mark = &awardsMarkView{Slug: view.AwardsMarkSlug.String, Title: view.AwardsMarkTitle.String}
	}
	work, err := it.readWork(ctx, learner, head.ID)
	if err != nil {
		return nil, err
	}
	if view.Kind == "writing" {
		var p catalog.Writing
		if err := json.Unmarshal(view.Payload, &p); err != nil {
			return nil, fmt.Errorf("writing payload: %w", err)
		}
		return writingView{itemHead: head, Prompt: p.Prompt, Rubric: nonNilRubric(p.Rubric), AwardsMark: mark, Work: work}, nil
	}
	var p catalog.Feedback
	if err := json.Unmarshal(view.Payload, &p); err != nil {
		return nil, fmt.Errorf("feedback payload: %w", err)
	}
	return feedbackView{itemHead: head, From: p.From, Scene: p.Scene, Rubric: nonNilRubric(p.Rubric), AwardsMark: mark, Work: work}, nil
}

func nonNilRubric(r []catalog.Criterion) []catalog.Criterion {
	if r == nil {
		return []catalog.Criterion{}
	}
	return r
}

// readWork is the learner's work, nil before the first draft or submit. A pending review reads as
// paused while ai_reviews_enabled is false; that is derived here and never stored.
func (it *Items) readWork(ctx context.Context, learner pgtype.UUID, id string) (*workView, error) {
	row, err := it.Reader.GetWrittenWork(ctx, repo.GetWrittenWorkParams{LearnerID: learner, ItemID: id})
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	w := &workView{Draft: row.Draft, DraftSavedAt: *timePtr(row.DraftSavedAt), SubmittedAt: timePtr(row.SubmittedAt),
		SubmitCount: row.SubmitCount, ReviewedAt: timePtr(row.ReviewedAt), Review: row.Review}
	if row.Body.Valid {
		w.Body = &row.Body.String
	}
	if row.ReviewState.Valid {
		state := row.ReviewState.String
		if state == "pending" {
			raw, err := it.Reader.GetSystemFlag(ctx, "ai_reviews_enabled")
			enabled := true
			switch {
			case errors.Is(err, pgx.ErrNoRows):
			case err != nil:
				return nil, err
			default:
				if err := json.Unmarshal(raw, &enabled); err != nil {
					return nil, fmt.Errorf("ai_reviews_enabled: %w", err)
				}
			}
			if !enabled {
				state = "paused"
			}
		}
		w.ReviewState = &state
	}
	return w, nil
}

// maxDraftChars and maxDraftBody: 4,000 characters (design 3.10), so at most 12 bytes each when
// every one is a JSON-escaped surrogate pair, plus the envelope; the byte cap only bounds the read.
const (
	maxDraftChars = 4000
	maxDraftBody  = 64 << 10
)

type draftRequest struct {
	Body *string `json:"body"`
}

// putItemRoute is the PUT /v1/items/ dispatcher: only draft is served, and only for a writing or
// feedback item (the store answers not found for any other kind).
func (a Auth) putItemRoute(w http.ResponseWriter, r *http.Request) {
	id, action, ok := parseItemPath(r.URL.Path)
	if !ok || action != "draft" || a.Items.Drafter == nil {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	a.Items.saveDraft(w, r, id)
}

func (it *Items) saveDraft(w http.ResponseWriter, r *http.Request, id string) {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxDraftBody))
	dec.DisallowUnknownFields()
	var req draftRequest
	if err := dec.Decode(&req); err != nil || dec.Decode(&struct{}{}) != io.EOF || req.Body == nil ||
		utf8.RuneCountInString(*req.Body) > maxDraftChars || strings.ContainsRune(*req.Body, 0) {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	p, _ := principalOf(r.Context())
	saved, err := it.Drafter.SaveDraft(context.WithoutCancel(r.Context()), repo.SaveDraftIn{Learner: p.learner, ItemID: id, Draft: *req.Body})
	switch {
	case errors.Is(err, repo.ErrItemNotFound):
		writeError(w, http.StatusNotFound, "not_found")
	case errors.Is(err, repo.ErrLearnerGone):
		writeError(w, http.StatusUnauthorized, "unauthenticated")
	case err != nil:
		slog.Error("draft save failed", "route", r.Pattern, "error", err)
		writeError(w, http.StatusInternalServerError, "server_error")
	default:
		writeNoStore(w, map[string]string{"draft_saved_at": saved.UTC().Format(time.RFC3339)})
	}
}
