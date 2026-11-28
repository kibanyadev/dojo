package server

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/ai"
	"github.com/kibanyadev/dojo/internal/catalog"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

const (
	defaultReviewInterval = 10
	defaultReviewBatch    = 5
	maxReviewTries        = 5
	// reviewTimeout bounds one adapter call, well inside the 5-minute lease.
	reviewTimeout = 60 * time.Second
)

// reviewDelays is the wait after the n-th failed attempt (design 3.10).
var reviewDelays = []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, time.Hour}

// ReviewStore is the worker's storage: a lease, the daily count and a guarded result write.
type ReviewStore interface {
	LeaseReviews(ctx context.Context, n int) ([]repo.ReviewLease, error)
	ReviewCallsToday(ctx context.Context, learner pgtype.UUID) (int64, error)
	FinishReview(ctx context.Context, r repo.ReviewResult) (bool, error)
}

// ReviewWorker reviews the pending written work of learners (design 3.10).
type ReviewWorker struct {
	Store   ReviewStore
	Adapter ai.Adapter
	// Enabled reads ai_reviews_enabled through the 30 s cache; while false nothing is claimed.
	Enabled func(context.Context) (bool, error)
	Batch   int
}

// ParseReview reads DOJO_REVIEW_INTERVAL_SECONDS (default 10) and DOJO_REVIEW_BATCH (default 5).
func ParseReview(get func(string) string) (time.Duration, int, error) {
	secs, err := envInt(get, "DOJO_REVIEW_INTERVAL_SECONDS", defaultReviewInterval, 3600)
	if err != nil {
		return 0, 0, err
	}
	batch, err := envInt(get, "DOJO_REVIEW_BATCH", defaultReviewBatch, 5)
	if err != nil {
		return 0, 0, err
	}
	return time.Duration(secs) * time.Second, batch, nil
}

// ParseAIAdapter picks the adapter of DOJO_AI_ADAPTER: standin (the default) outside pre and pro. bedrock
// and ollama arrive with PR 41.
func ParseAIAdapter(get func(string) string) (ai.Adapter, error) {
	switch name := strings.TrimSpace(get("DOJO_AI_ADAPTER")); name {
	case "", "standin":
		if env := get("DOJO_ENV"); env == "pre" || env == "pro" {
			return nil, fmt.Errorf("DOJO_AI_ADAPTER must not be standin in %s", env)
		}
		return ai.Standin{}, nil
	default:
		return nil, fmt.Errorf("DOJO_AI_ADAPTER %q is not available yet", name)
	}
}

// RunReviewWorker ticks until ctx ends; a failed tick is logged and the next one tries again.
func RunReviewWorker(ctx context.Context, w *ReviewWorker, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if _, err := w.Tick(ctx); err != nil && ctx.Err() == nil {
			slog.Error("review tick failed", "error", err)
		}
	}
}

// Tick claims a batch and reviews each row once. It returns how many rows it wrote.
func (w *ReviewWorker) Tick(ctx context.Context) (int, error) {
	on, err := w.Enabled(ctx)
	if err != nil || !on {
		return 0, err
	}
	leases, err := w.Store.LeaseReviews(ctx, w.Batch)
	if err != nil {
		return 0, err
	}
	wrote := 0
	for _, l := range leases {
		if ctx.Err() != nil {
			return wrote, ctx.Err()
		}
		res, write := w.review(ctx, l)
		if !write {
			continue
		}
		ok, err := w.Store.FinishReview(ctx, res)
		if err != nil {
			var pg *pgconn.PgError
			if errors.As(err, &pg) {
				slog.Error("review result not saved", "item", l.ItemID, "code", pg.Code)
			} else {
				slog.Error("review result not saved", "item", l.ItemID)
			}
			continue
		}
		if ok {
			wrote++
			slog.Info("review attempt", "item", l.ItemID, "state", res.State)
		}
	}
	return wrote, nil
}

// review decides one lease. It reports no write when the context ended mid-call: the lease is the wait.
func (w *ReviewWorker) review(ctx context.Context, l repo.ReviewLease) (repo.ReviewResult, bool) {
	calls, err := w.Store.ReviewCallsToday(ctx, l.LearnerID)
	if err != nil {
		slog.Error("review count failed", "item", l.ItemID)
		return repo.ReviewResult{}, false
	}
	if calls >= repo.DailyReviewCap {
		return repo.ReviewResult{Lease: l, State: "capped", Tries: l.Tries}, true
	}
	req, err := reviewRequest(l)
	if err != nil {
		return w.failed(l, nil, err), true
	}
	callCtx, cancel := context.WithTimeout(ctx, reviewTimeout)
	defer cancel()
	rev, usage, err := ai.Review(callCtx, w.Adapter, req)
	switch {
	case err == nil:
		doc, met := reviewDoc(rev)
		raw, _ := json.Marshal(doc) // plain fields: cannot fail
		return repo.ReviewResult{Lease: l, Call: callOf(usage, "ok"), State: "done", Tries: l.Tries, Review: raw, Met: met, Total: len(rev.Criteria)}, true
	case ctx.Err() != nil:
		return repo.ReviewResult{}, false
	case errors.Is(err, ai.ErrInputTooLarge):
		return repo.ReviewResult{Lease: l, State: "failed", Tries: l.Tries}, true
	case errors.Is(err, ai.ErrInvalidReply):
		return w.failed(l, callOf(usage, "invalid"), err), true
	}
	return w.failed(l, callOf(usage, "error"), err), true
}

// failed counts one attempt: a retry after the delay, or failed on the fifth.
func (w *ReviewWorker) failed(l repo.ReviewLease, call *repo.ReviewCall, _ error) repo.ReviewResult {
	tries := l.Tries + 1
	if tries >= maxReviewTries {
		return repo.ReviewResult{Lease: l, Call: call, State: "failed", Tries: maxReviewTries}
	}
	return repo.ReviewResult{Lease: l, Call: call, State: "pending", Tries: tries, DelaySeconds: reviewDelays[tries-1].Seconds()}
}

// callOf is the ai_reviews row of a call; an error with no usage records "unknown" and zero tokens.
func callOf(u ai.Usage, outcome string) *repo.ReviewCall {
	model := u.Model
	if model == "" {
		model = "unknown"
	}
	return &repo.ReviewCall{Model: model, TokensIn: int32(min(max(u.TokensIn, 0), 1<<30)), TokensOut: int32(min(max(u.TokensOut, 0), 1<<30)), Outcome: outcome} //nolint:gosec // clamped
}

// reviewRequest builds the adapter request from the item's payload: the prompt (writing) or scene (feedback)
// and the rubric.
func reviewRequest(l repo.ReviewLease) (ai.Request, error) {
	var p struct {
		Prompt string              `json:"prompt"`
		Scene  string              `json:"scene"`
		Rubric []catalog.Criterion `json:"rubric"`
	}
	if err := json.Unmarshal(l.Payload, &p); err != nil {
		return ai.Request{}, errors.New("review payload unreadable")
	}
	req := ai.Request{Prompt: p.Prompt, Body: l.Body}
	if l.Kind == "feedback" {
		req.Prompt = p.Scene
	}
	for _, c := range p.Rubric {
		req.Rubric = append(req.Rubric, ai.Criterion{ID: c.ID, Text: c.Text})
	}
	return req, nil
}

type reviewCriterion struct {
	ID       string `json:"id"`
	Text     string `json:"text"`
	Met      bool   `json:"met"`
	Evidence string `json:"evidence"`
	Note     string `json:"note"`
}

// reviewDoc is the stored review (design 3.10 item example) and how many criteria are met.
func reviewDoc(r ai.Result) (any, int) {
	out := struct {
		Criteria []reviewCriterion `json:"criteria"`
		NextStep string            `json:"next_step"`
	}{Criteria: []reviewCriterion{}, NextStep: r.NextStep}
	met := 0
	for _, c := range r.Criteria {
		out.Criteria = append(out.Criteria, reviewCriterion(c))
		if c.Met {
			met++
		}
	}
	return out, met
}
