package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

type fakeAnswerer struct {
	in    repo.AnswerQuizIn
	calls int
	out   repo.AnswerQuizOut
	err   error
}

func (f *fakeAnswerer) AnswerQuiz(_ context.Context, in repo.AnswerQuizIn) (repo.AnswerQuizOut, error) {
	f.calls++
	f.in = in
	return f.out, f.err
}

func answerCall(t *testing.T, f *fakeAnswerer, rl *RateLimits, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: quizFake(), Answerer: f}}
	New(fakeReady{}, auth, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

const answersPath = "/v1/items/support-l1/quiz-02/answers"

func TestAnswerSuccessShape(t *testing.T) {
	expl := "who only shows now"
	f := &fakeAnswerer{out: repo.AnswerQuizOut{Correct: true, Answer: 1, Notes: []string{"now", "past"}, Explain: &expl,
		Done: false, Answered: 1, Total: 3, FirstTryCorrect: 1}}
	rec := answerCall(t, f, NewRateLimits(1), answersPath, `{"question":"who-last","choice":1}`, true)
	got := decode(t, rec)
	if got["correct"] != true || got["answer"] != float64(1) || got["explain"] != expl || got["done"] != false ||
		got["answered"] != float64(1) || got["total"] != float64(3) || got["first_try_correct"] != float64(1) ||
		got["notes"].([]any)[1] != "past" {
		t.Fatalf("body: %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
	if f.in.ItemID != "support-l1/quiz-02" || f.in.QuestionID != "who-last" || f.in.Choice != 1 || !f.in.Learner.Valid {
		t.Fatalf("args: %+v", f.in)
	}
}

func TestAnswerExplainIsNullWithoutOne(t *testing.T) {
	rec := answerCall(t, &fakeAnswerer{out: repo.AnswerQuizOut{Notes: []string{}}}, NewRateLimits(1), answersPath, `{"question":"a","choice":0}`, true)
	if got := decode(t, rec); got["explain"] != nil {
		t.Fatalf("explain: %s", rec.Body)
	}
}

func TestAnswerBadBodiesNeverReachTheStore(t *testing.T) {
	for name, body := range map[string]string{
		"not json":       `nope`,
		"empty":          ``,
		"missing choice": `{"question":"a"}`,
		"missing id":     `{"choice":0}`,
		"empty id":       `{"question":"","choice":0}`,
		"string choice":  `{"question":"a","choice":"1"}`,
		"float choice":   `{"question":"a","choice":1.5}`,
		"huge choice":    `{"question":"a","choice":99999999999999999999}`,
		"null choice":    `{"question":"a","choice":null}`,
		"id not string":  `{"question":7,"choice":0}`,
		"extra field":    `{"question":"a","choice":0,"x":1}`,
		"two documents":  `{"question":"a","choice":0}{}`,
		"array":          `[]`,
		"huge body":      `{"question":"` + strings.Repeat("a", 5000) + `","choice":0}`,
	} {
		f := &fakeAnswerer{}
		rec := answerCall(t, f, NewRateLimits(1), answersPath, body, true)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"bad_request"`) || f.calls != 0 {
			t.Errorf("%s: %d %s calls=%d", name, rec.Code, rec.Body, f.calls)
		}
	}
}

func TestAnswerStoreErrorsMapToTheirCodes(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code int
		name string
	}{
		{repo.ErrItemNotFound, 404, "not_found"},
		{repo.ErrQuestionNotFound, 404, "not_found"},
		{repo.ErrChoiceRange, 400, "bad_request"},
		{repo.ErrLearnerGone, 401, "unauthenticated"},
		{context.DeadlineExceeded, 500, "server_error"},
	} {
		rec := answerCall(t, &fakeAnswerer{err: tt.err}, NewRateLimits(1), answersPath, `{"question":"a","choice":0}`, true)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), `"`+tt.name+`"`) {
			t.Errorf("%v: %d %s", tt.err, rec.Code, rec.Body)
		}
	}
}

func TestAnswerRoutesOtherPostsAreNotFound(t *testing.T) {
	for _, p := range []string{
		"/v1/items/support-l1/quiz-02",
		"/v1/items/support-l1/quiz-02/draft",
		"/v1/items/support-l1/quiz-02/submit",
		"/v1/items/support-l1/quiz-02/choice",
		"/v1/items/support-l1/quiz-02/other",
		"/v1/items/support-l1/quiz-02/answers/x",
		"/v1/items/quiz-02/answers",
		"/v1/items/",
	} {
		f := &fakeAnswerer{}
		rec := answerCall(t, f, NewRateLimits(1), p, `{"question":"a","choice":0}`, true)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) || f.calls != 0 {
			t.Errorf("%s: %d %s", p, rec.Code, rec.Body)
		}
	}
}

func TestAnswerCookieNeedsCSRF(t *testing.T) {
	f := &fakeAnswerer{}
	rec := answerCall(t, f, NewRateLimits(1), answersPath, `{"question":"a","choice":0}`, false)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_rejected") || f.calls != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestAnswerBetweenBudget(t *testing.T) {
	rl := testLimits(1000)
	rl.Between = newMemLimiter(2, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	f := &fakeAnswerer{}
	for i := 0; i < 2; i++ {
		if rec := answerCall(t, f, rl, answersPath, `{"question":"a","choice":0}`, true); rec.Code != 200 {
			t.Fatalf("answer %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := answerCall(t, f, rl, answersPath, `{"question":"a","choice":0}`, true)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), "rate_limited") || f.calls != 2 {
		t.Fatalf("over budget: %d %s retry=%q calls=%d", rec.Code, rec.Body, rec.Header().Get("Retry-After"), f.calls)
	}
	// An unrouted action spends nothing from the budget.
	rl.Between = newMemLimiter(1, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	for i := 0; i < 3; i++ {
		if rec := answerCall(t, f, rl, "/v1/items/support-l1/quiz-02/other", `{}`, true); rec.Code != 404 {
			t.Fatalf("unrouted: %d", rec.Code)
		}
	}
	if rec := answerCall(t, f, rl, answersPath, `{"question":"a","choice":0}`, true); rec.Code != 200 {
		t.Fatalf("budget spent by 404s: %d", rec.Code)
	}
}
