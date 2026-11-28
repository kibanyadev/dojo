package server

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/ai"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

type fakeReviewStore struct {
	leases   []repo.ReviewLease
	calls    int64
	finished []repo.ReviewResult
	leased   int
}

func (f *fakeReviewStore) LeaseReviews(context.Context, int) ([]repo.ReviewLease, error) {
	f.leased++
	return f.leases, nil
}
func (f *fakeReviewStore) ReviewCallsToday(context.Context, pgtype.UUID) (int64, error) {
	return f.calls, nil
}
func (f *fakeReviewStore) FinishReview(_ context.Context, r repo.ReviewResult) (bool, error) {
	f.finished = append(f.finished, r)
	return true, nil
}

const reviewPayload = `{"prompt":"Explain.","rubric":[{"id":"found","text":"What you found"},{"id":"next","text":"What she does now"}]}`

func reviewLease(body string, tries int32) repo.ReviewLease {
	return repo.ReviewLease{ItemID: "r/w", Kind: "writing", Title: "Tell Elena", Body: body, SubmitCount: 3, Tries: tries, Payload: []byte(reviewPayload)}
}

func newWorker(s *fakeReviewStore, a ai.Adapter, enabled bool) *ReviewWorker {
	return &ReviewWorker{Store: s, Adapter: a, Batch: 5, Enabled: func(context.Context) (bool, error) { return enabled, nil }}
}

type bigAdapter struct{}

func (bigAdapter) Review(context.Context, ai.Request) (ai.RawReply, error) {
	return ai.RawReply{}, errors.New("must not be called")
}

func TestReviewWorkerOutcomes(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		tries     int32
		calls     int64
		wantState string
		wantTries int32
		wantDelay time.Duration
		wantCall  string // outcome of the ai_reviews row, "" for none
	}{
		{"ok", "found it\nnext step", 0, 0, "done", 0, 0, "ok"},
		{"invalid first", "[[standin:invalid]]", 0, 0, "pending", 1, 30 * time.Second, "invalid"},
		{"error second", "[[standin:error]]", 1, 0, "pending", 2, 2 * time.Minute, "error"},
		{"error third", "[[standin:error]]", 2, 0, "pending", 3, 10 * time.Minute, "error"},
		{"error fourth", "[[standin:error]]", 3, 0, "pending", 4, time.Hour, "error"},
		{"error fifth fails", "[[standin:error]]", 4, 0, "failed", 5, 0, "error"},
		{"invalid fifth fails", "[[standin:invalid]]", 4, 0, "failed", 5, 0, "invalid"},
		{"cap at claim", "found", 0, 20, "capped", 0, 0, ""},
		{"just under the cap", "found", 0, 19, "done", 0, 0, "ok"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &fakeReviewStore{leases: []repo.ReviewLease{reviewLease(tc.body, tc.tries)}, calls: tc.calls}
			n, err := newWorker(s, ai.Standin{}, true).Tick(context.Background())
			if err != nil || n != 1 || len(s.finished) != 1 {
				t.Fatalf("tick: %d %v %d", n, err, len(s.finished))
			}
			r := s.finished[0]
			if r.State != tc.wantState || r.Tries != tc.wantTries || r.DelaySeconds != tc.wantDelay.Seconds() {
				t.Fatalf("result: %+v", r)
			}
			switch {
			case tc.wantCall == "" && r.Call != nil, tc.wantCall != "" && (r.Call == nil || r.Call.Outcome != tc.wantCall):
				t.Fatalf("call: %+v", r.Call)
			}
			if r.Lease.SubmitCount != 3 {
				t.Fatal("the guard needs the submit_count read at claim")
			}
		})
	}
}

func TestReviewWorkerStoresTheReview(t *testing.T) {
	s := &fakeReviewStore{leases: []repo.ReviewLease{reviewLease("We found it.\nnothing else", 0)}}
	if _, err := newWorker(s, ai.Standin{}, true).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	r := s.finished[0]
	want := `{"criteria":[{"id":"found","text":"What you found","met":true,"evidence":"We found it.","note":""},` +
		`{"id":"next","text":"What she does now","met":false,"evidence":"","note":"Not found: What she does now"}],` +
		`"next_step":"Add: What she does now"}`
	if string(r.Review) != want || r.Met != 1 || r.Total != 2 || r.Call.Model != "standin" || r.Call.TokensOut != 50 {
		t.Fatalf("review: %s %+v", r.Review, r)
	}
}

func TestReviewWorkerPausedClaimsNothing(t *testing.T) {
	s := &fakeReviewStore{leases: []repo.ReviewLease{reviewLease("x", 0)}}
	n, err := newWorker(s, ai.Standin{}, false).Tick(context.Background())
	if err != nil || n != 0 || s.leased != 0 || len(s.finished) != 0 {
		t.Fatalf("paused: %d %v leased=%d", n, err, s.leased)
	}
}

func TestReviewWorkerInputTooLargeFailsWithoutACall(t *testing.T) {
	s := &fakeReviewStore{leases: []repo.ReviewLease{reviewLease(strings.Repeat("a", 20000), 0)}}
	if _, err := newWorker(s, bigAdapter{}, true).Tick(context.Background()); err != nil {
		t.Fatal(err)
	}
	if r := s.finished[0]; r.State != "failed" || r.Call != nil {
		t.Fatalf("result: %+v", r)
	}
}

func TestReviewWorkerFeedbackUsesTheScene(t *testing.T) {
	l := reviewLease("x", 0)
	l.Kind, l.Payload = "feedback", []byte(`{"scene":"A scene.","rubric":[{"id":"a","text":"A"}]}`)
	req, err := reviewRequest(l)
	if err != nil || req.Prompt != "A scene." || len(req.Rubric) != 1 {
		t.Fatalf("request: %+v %v", req, err)
	}
}

func TestParseReview(t *testing.T) {
	get := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	d, b, err := ParseReview(get(nil))
	if err != nil || d != 10*time.Second || b != 5 {
		t.Fatalf("defaults: %v %d %v", d, b, err)
	}
	if _, _, err := ParseReview(get(map[string]string{"DOJO_REVIEW_BATCH": "0"})); err == nil {
		t.Fatal("batch 0 accepted")
	}
	if _, err := ParseAIAdapter(get(map[string]string{"DOJO_ENV": "pro"})); err == nil {
		t.Fatal("standin accepted in pro")
	}
	if _, err := ParseAIAdapter(get(map[string]string{"DOJO_AI_ADAPTER": "bedrock"})); err == nil {
		t.Fatal("bedrock accepted before PR 41")
	}
	if a, err := ParseAIAdapter(get(nil)); err != nil || a == nil {
		t.Fatalf("default adapter: %v", err)
	}
}
