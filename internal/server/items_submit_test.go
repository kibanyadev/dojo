package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

type fakeSubmitter struct {
	in      repo.SubmitWorkIn
	calls   int
	err     error
	awarded []repo.Awarded
}

func (f *fakeSubmitter) SubmitWork(_ context.Context, in repo.SubmitWorkIn) ([]repo.Awarded, error) {
	f.calls++
	f.in = in
	return f.awarded, f.err
}

func TestSubmitAnswersAwardedItems(t *testing.T) {
	id := pgtype.UUID{Bytes: [16]byte{0x01, 0x94, 15: 7}, Valid: true}
	f := &fakeSubmitter{awarded: []repo.Awarded{{Kind: "mark", InboxID: id, Slug: "written-english"}, {Kind: "belt", InboxID: id, Slug: "white"}}}
	rec := submitCall(t, f, NewRateLimits(1), http.MethodPost, submitPath, `{"body":"Hi"}`, true)
	got, _ := decode(t, rec)["awarded"].([]any)
	if len(got) != 2 {
		t.Fatalf("body: %s", rec.Body)
	}
	first, _ := got[0].(map[string]any)
	second, _ := got[1].(map[string]any)
	if len(first) != 3 || first["kind"] != "mark" || first["slug"] != "written-english" || first["inbox_id"] != uuidText(id) ||
		second["kind"] != "belt" || second["slug"] != "white" {
		t.Fatalf("awarded: %v", got)
	}
}

const submitPath = "/v1/items/support-l1/write-01/submit"

func submitCall(t *testing.T, f *fakeSubmitter, rl *RateLimits, method, path, body string, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	req.Header.Set("Content-Type", "application/json")
	if csrf {
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: writingFake(), Submitter: f}}
	New(fakeReady{}, auth, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

func TestSubmitSuccessShape(t *testing.T) {
	f := &fakeSubmitter{}
	rec := submitCall(t, f, NewRateLimits(1), http.MethodPost, submitPath, `{"body":"  Hi Elena,\nthe group owns it.\n "}`, true)
	got := decode(t, rec)
	item, _ := got["item"].(map[string]any)
	awarded, isList := got["awarded"].([]any)
	if len(got) != 2 || item["kind"] != "writing" || item["id"] != "support-l1/write-01" || !isList || len(awarded) != 0 {
		t.Fatalf("body: %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
	if f.calls != 1 || f.in.ItemID != "support-l1/write-01" || f.in.Body != "Hi Elena,\nthe group owns it." || !f.in.Learner.Valid {
		t.Fatalf("args: %+v calls=%d", f.in, f.calls)
	}
}

func TestSubmitLimits(t *testing.T) {
	tests := []struct {
		name string
		body string
		want int
	}{
		{"one character", `{"body":"x"}`, 200},
		{"4000 ascii", `{"body":"` + strings.Repeat("a", 4000) + `"}`, 200},
		{"4000 astral characters", `{"body":"` + strings.Repeat("😀", 4000) + `"}`, 200},
		{"4000 after trimming", `{"body":"   ` + strings.Repeat("a", 4000) + `   "}`, 200},
		{"4001", `{"body":"` + strings.Repeat("a", 4001) + `"}`, 400},
		{"4001 astral", `{"body":"` + strings.Repeat("😀", 4001) + `"}`, 400},
		{"empty", `{"body":""}`, 400},
		{"only spaces", `{"body":" \t\n  "}`, 400},
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
			f := &fakeSubmitter{}
			rec := submitCall(t, f, NewRateLimits(1), http.MethodPost, submitPath, tt.body, true)
			if rec.Code != tt.want {
				t.Fatalf("%d %s", rec.Code, rec.Body)
			}
			if tt.want == 400 && (f.calls != 0 || strings.TrimSpace(rec.Body.String()) != `{"error":"bad_request"}`) {
				t.Fatalf("reached the store or leaked: calls=%d %s", f.calls, rec.Body)
			}
		})
	}
}

func TestSubmitErrors(t *testing.T) {
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
		rec := submitCall(t, &fakeSubmitter{err: tt.err}, NewRateLimits(1), http.MethodPost, submitPath, `{"body":"learner words"}`, true)
		if rec.Code != tt.code || !strings.Contains(rec.Body.String(), tt.body) ||
			strings.Contains(rec.Body.String(), "secret") || strings.Contains(rec.Body.String(), "learner words") {
			t.Errorf("%v: %d %s", tt.err, rec.Code, rec.Body)
		}
	}
}

func TestSubmitRouting(t *testing.T) {
	for _, tt := range []struct{ method, path string }{
		{http.MethodPost, "/v1/items/support-l1/write-01"},
		{http.MethodPost, "/v1/items/support-l1/write-01/submit/x"},
		{http.MethodPost, "/v1/items/write-01/submit"},
		{http.MethodGet, submitPath},
		{http.MethodPut, submitPath},
	} {
		f := &fakeSubmitter{}
		rec := submitCall(t, f, NewRateLimits(1), tt.method, tt.path, `{"body":"x"}`, true)
		if (rec.Code != http.StatusNotFound && rec.Code != http.StatusMethodNotAllowed) || f.calls != 0 {
			t.Errorf("%s %s: %d %s", tt.method, tt.path, rec.Code, rec.Body)
		}
	}
}

func TestSubmitWithoutSubmitterIsNotFound(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, submitPath, strings.NewReader(`{"body":"x"}`))
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

func TestSubmitCookieNeedsCSRF(t *testing.T) {
	f := &fakeSubmitter{}
	rec := submitCall(t, f, NewRateLimits(1), http.MethodPost, submitPath, `{"body":"x"}`, false)
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "csrf_rejected") || f.calls != 0 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestSubmitBetweenBudget(t *testing.T) {
	rl := testLimits(1000)
	rl.Between = newMemLimiter(2, time.Minute, 100, (&clock{t: time.Unix(1000, 0)}).now)
	f := &fakeSubmitter{}
	for i := 0; i < 2; i++ {
		if rec := submitCall(t, f, rl, http.MethodPost, submitPath, `{"body":"x"}`, true); rec.Code != 200 {
			t.Fatalf("submit %d: %d %s", i, rec.Code, rec.Body)
		}
	}
	rec := submitCall(t, f, rl, http.MethodPost, submitPath, `{"body":"x"}`, true)
	if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" || f.calls != 2 {
		t.Fatalf("over budget: %d %s calls=%d", rec.Code, rec.Body, f.calls)
	}
}
