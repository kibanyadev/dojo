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

type fakeChooser struct {
	in    repo.ChooseStoryIn
	calls int
	out   repo.ChooseStoryOut
	err   error
}

func (f *fakeChooser) ChooseStory(_ context.Context, in repo.ChooseStoryIn) (repo.ChooseStoryOut, error) {
	f.calls++
	f.in = in
	return f.out, f.err
}

const choicePath = "/v1/items/support-l1/story-01/choice"

func storyChoiceCall(t *testing.T, f *fakeChooser, rl *RateLimits, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: storyFake(), Chooser: f}}
	New(fakeReady{}, auth, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

func TestChoiceSuccessShape(t *testing.T) {
	f := &fakeChooser{out: repo.ChooseStoryOut{Chosen: repo.StoryChosen{ID: "calm", Verdict: "good",
		Reaction: "Elena relaxes.", ChosenAt: time.Date(2026, 12, 2, 18, 10, 0, 0, time.UTC)}}}
	rec := storyChoiceCall(t, f, NewRateLimits(1), choicePath, `{"choice":"calm"}`, true)
	got := decode(t, rec)
	ch, _ := got["chosen"].(map[string]any)
	if len(got) != 1 || ch["id"] != "calm" || ch["verdict"] != "good" || ch["reaction"] != "Elena relaxes." ||
		ch["chosen_at"] != "2026-12-02T18:10:00Z" || len(ch) != 4 {
		t.Fatalf("body: %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
	if f.in.ItemID != "support-l1/story-01" || f.in.ChoiceID != "calm" || !f.in.Learner.Valid {
		t.Fatalf("args: %+v", f.in)
	}
}

func TestChoiceBadBodiesNeverReachTheStore(t *testing.T) {
	for name, body := range map[string]string{
		"not json":      `nope`,
		"empty":         ``,
		"missing":       `{}`,
		"empty id":      `{"choice":""}`,
		"number":        `{"choice":1}`,
		"null":          `{"choice":null}`,
		"extra field":   `{"choice":"calm","x":1}`,
		"two documents": `{"choice":"calm"}{}`,
		"array":         `[]`,
		"huge body":     `{"choice":"` + strings.Repeat("a", 5000) + `"}`,
	} {
		f := &fakeChooser{}
		rec := storyChoiceCall(t, f, NewRateLimits(1), choicePath, body, true)
		if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), `"bad_request"`) || f.calls != 0 {
			t.Errorf("%s: %d %s calls=%d", name, rec.Code, rec.Body, f.calls)
		}
	}
}

func TestChoiceMalformedIDIsNotFoundBeforeTheStore(t *testing.T) {
	for _, id := range []string{"Calm", "a_b", "-a", "a b", strings.Repeat("a", 33), "x'; DROP TABLE story_choices;--"} {
		f := &fakeChooser{}
		rec := storyChoiceCall(t, f, NewRateLimits(1), choicePath, `{"choice":"`+id+`"}`, true)
		if rec.Code != http.StatusNotFound || !strings.Contains(rec.Body.String(), `"not_found"`) || f.calls != 0 {
			t.Errorf("%q: %d %s calls=%d", id, rec.Code, rec.Body, f.calls)
		}
	}
}

func TestChoiceStoreErrorsMapToTheirCodes(t *testing.T) {
	for _, tt := range []struct {
		err  error
		code int
		name string
	}{
		{repo.ErrItemNotFound, 404, "not_found"},
		{repo.ErrChoiceNotFound, 404, "not_found"},
		{repo.ErrAlreadyChosen, 409, "already_chosen"},
		{repo.ErrLearnerGone, 401, "unauthenticated"},
		{context.DeadlineExceeded, 500, "server_error"},
	} {
		rec := storyChoiceCall(t, &fakeChooser{err: tt.err}, NewRateLimits(1), choicePath, `{"choice":"calm"}`, true)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), `"`+tt.name+`"`) {
			t.Errorf("%v: %d %s", tt.err, rec.Code, rec.Body)
		}
	}
}

func TestChoiceWithoutChooserIsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, choicePath, strings.NewReader(`{"choice":"calm"}`))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CSRFHeader, "1")
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: storyFake()}}
	New(fakeReady{}, auth, NewRateLimits(1), nil, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestChoiceCookieNeedsCSRF(t *testing.T) {
	f := &fakeChooser{}
	rec := storyChoiceCall(t, f, NewRateLimits(1), choicePath, `{"choice":"calm"}`, false)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_rejected") || f.calls != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestChoiceBetweenBudget(t *testing.T) {
	rl := testLimits(1000)
	rl.Between = newMemLimiter(2, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	f := &fakeChooser{}
	for i := 0; i < 2; i++ {
		if rec := storyChoiceCall(t, f, rl, choicePath, `{"choice":"calm"}`, true); rec.Code != 200 {
			t.Fatalf("choice %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := storyChoiceCall(t, f, rl, choicePath, `{"choice":"calm"}`, true)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || f.calls != 2 {
		t.Fatalf("over budget: %d %s calls=%d", rec.Code, rec.Body, f.calls)
	}
}

func TestChoiceRouteOtherActionsStayNotFound(t *testing.T) {
	for _, p := range []string{"/v1/items/support-l1/story-01/draft", "/v1/items/support-l1/story-01/submit", "/v1/items/support-l1/story-01"} {
		f := &fakeChooser{}
		if rec := storyChoiceCall(t, f, NewRateLimits(1), p, `{"choice":"calm"}`, true); rec.Code != 404 || f.calls != 0 {
			t.Errorf("%s: %d", p, rec.Code)
		}
	}
}
