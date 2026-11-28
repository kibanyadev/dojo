package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

type fakeDrafter struct {
	in    repo.SaveDraftIn
	calls int
	err   error
}

func (f *fakeDrafter) SaveDraft(_ context.Context, in repo.SaveDraftIn) (time.Time, error) {
	f.calls++
	f.in = in
	return time.Date(2026, 12, 2, 18, 9, 40, 0, time.UTC), f.err
}

func draftCall(t *testing.T, f *fakeDrafter, rl *RateLimits, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: writingFake(), Drafter: f}}
	New(fakeReady{}, auth, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

const draftPath = "/v1/items/support-l1/write-01/draft"

func TestDraftSuccess(t *testing.T) {
	f := &fakeDrafter{}
	rec := draftCall(t, f, NewRateLimits(1), http.MethodPut, draftPath, `{"body":"Hi Elena,\nthe group now owns it."}`, true)
	got := decode(t, rec)
	if got["draft_saved_at"] != "2026-12-02T18:09:40Z" || len(got) != 1 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("body: %s", rec.Body)
	}
	if f.in.ItemID != "support-l1/write-01" || f.in.Draft != "Hi Elena,\nthe group now owns it." || !f.in.Learner.Valid {
		t.Fatalf("args: %+v", f.in)
	}
}

func TestDraftLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"empty is allowed", `{"body":""}`, 200},
		{"4000 ascii", `{"body":"` + strings.Repeat("a", 4000) + `"}`, 200},
		{"4000 astral characters", `{"body":"` + strings.Repeat("😀", 4000) + `"}`, 200},
		{"4000 escaped", `{"body":"` + strings.Repeat(`é`, 4000) + `"}`, 200},
		{"4001", `{"body":"` + strings.Repeat("a", 4001) + `"}`, 400},
		{"4001 astral", `{"body":"` + strings.Repeat("😀", 4001) + `"}`, 400},
		{"NUL escaped", `{"body":"a\u0000b"}`, 400},
		{"missing body", `{}`, 400},
		{"null body", `{"body":null}`, 400},
		{"number body", `{"body":7}`, 400},
		{"unknown field", `{"body":"x","extra":1}`, 400},
		{"trailing value", `{"body":"x"}{"body":"y"}`, 400},
		{"not json", `nope`, 400},
		{"empty request", ``, 400},
		{"over the byte cap", `{"body":"` + strings.Repeat("a", 70000) + `"}`, 400},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakeDrafter{}
			rec := draftCall(t, f, NewRateLimits(1), http.MethodPut, draftPath, tt.body, true)
			if rec.Code != tt.want {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if tt.want == 400 && (f.calls != 0 || strings.TrimSpace(rec.Body.String()) != `{"error":"bad_request"}`) {
				t.Fatalf("reached the store or leaked: calls=%d %s", f.calls, rec.Body)
			}
		})
	}
}

func TestDraftErrors(t *testing.T) {
	tests := []struct {
		err  error
		code int
		body string
	}{
		{repo.ErrItemNotFound, 404, `"not_found"`},
		{repo.ErrLearnerGone, 401, `"unauthenticated"`},
		{errors.New("pq: secret detail"), 500, `"server_error"`},
	}
	for _, tt := range tests {
		rec := draftCall(t, &fakeDrafter{err: tt.err}, NewRateLimits(1), http.MethodPut, draftPath, `{"body":"x"}`, true)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%v: %d %s", tt.err, rec.Code, rec.Body)
		}
	}
}

func TestDraftRouting(t *testing.T) {
	for _, tt := range []struct{ method, path string }{
		{http.MethodPut, "/v1/items/support-l1/write-01"},
		{http.MethodPut, "/v1/items/support-l1/write-01/submit"},
		{http.MethodPut, "/v1/items/support-l1/write-01/answers"},
		{http.MethodPut, "/v1/items/support-l1/write-01/draft/x"},
		{http.MethodPut, "/v1/items/write-01/draft"},
		{http.MethodPut, "/v1/items/"},
		{http.MethodGet, draftPath},
		{http.MethodPost, draftPath},
	} {
		f := &fakeDrafter{}
		rec := draftCall(t, f, NewRateLimits(1), tt.method, tt.path, `{"body":"x"}`, true)
		if (rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed) || f.calls != 0 {
			t.Errorf("%s %s: %d %s", tt.method, tt.path, rec.Code, rec.Body)
		}
	}
}

func TestDraftWithoutDrafterIsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPut, draftPath, strings.NewReader(`{"body":"x"}`))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set(CSRFHeader, "1")
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: writingFake()}}
	New(fakeReady{}, auth, NewRateLimits(1), nil, nil).ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("%d", rec.Code)
	}
}

func TestDraftCookieNeedsCSRF(t *testing.T) {
	f := &fakeDrafter{}
	rec := draftCall(t, f, NewRateLimits(1), http.MethodPut, draftPath, `{"body":"x"}`, false)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_rejected") || f.calls != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestDraftBetweenBudget(t *testing.T) {
	rl := testLimits(1000)
	rl.Between = newMemLimiter(2, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	f := &fakeDrafter{}
	for i := 0; i < 2; i++ {
		if rec := draftCall(t, f, rl, http.MethodPut, draftPath, `{"body":"x"}`, true); rec.Code != 200 {
			t.Fatalf("draft %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := draftCall(t, f, rl, http.MethodPut, draftPath, `{"body":"x"}`, true)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || f.calls != 2 {
		t.Fatalf("over budget: %d %s calls=%d", rec.Code, rec.Body, f.calls)
	}
	// An unrouted PUT spends nothing.
	rl.Between = newMemLimiter(1, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	for i := 0; i < 3; i++ {
		if rec := draftCall(t, f, rl, http.MethodPut, "/v1/items/support-l1/write-01/other", `{}`, true); rec.Code != 404 {
			t.Fatalf("unrouted: %d", rec.Code)
		}
	}
	if rec := draftCall(t, f, rl, http.MethodPut, draftPath, `{"body":"x"}`, true); rec.Code != 200 {
		t.Fatalf("budget spent by 404s: %d", rec.Code)
	}
}
