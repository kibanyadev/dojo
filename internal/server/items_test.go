package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

func TestParseItemPath(t *testing.T) {
	tests := []struct {
		path   string
		id     string
		action string
		ok     bool
	}{
		{"/v1/items/support-l1/quiz-02", "support-l1/quiz-02", "", true},
		{"/v1/items/support-l1/quiz-02/answers", "support-l1/quiz-02", "answers", true},
		{"/v1/items/support-l1/write-01/draft", "support-l1/write-01", "draft", true},
		{"/v1/items/support-l1/write-01/submit", "support-l1/write-01", "submit", true},
		{"/v1/items/support-l1/story-01/choice", "support-l1/story-01", "choice", true},
		{"/v1/items/practice/disk-full", "practice/disk-full", "", true},
		{"/v1/items/support-l1/quiz-02/other", "", "", false},
		{"/v1/items/support-l1/quiz-02/answers/x", "", "", false},
		{"/v1/items/support-l1/quiz-02/", "", "", false},
		{"/v1/items/support-l1/answers", "", "", false},
		{"/v1/items/support-l1/choice/draft", "", "", false},
		{"/v1/items/quiz-02", "", "", false},
		{"/v1/items/Bad_ID/x", "", "", false},
		{"/v1/items/", "", "", false},
		{"/v1/other/a/b", "", "", false},
	}
	for _, tt := range tests {
		id, action, ok := parseItemPath(tt.path)
		if id != tt.id || action != tt.action || ok != tt.ok {
			t.Errorf("%s: got (%q, %q, %v), want (%q, %q, %v)", tt.path, id, action, ok, tt.id, tt.action, tt.ok)
		}
	}
}

type fakeItems struct {
	view    repo.GetItemViewRow
	missing bool
	done    bool
	answers []repo.ListQuizAnswersRow
	choice  *repo.GetStoryChoiceRow
	err     error
}

func (f *fakeItems) GetItemView(_ context.Context, id string) (repo.GetItemViewRow, error) {
	if f.err != nil {
		return repo.GetItemViewRow{}, f.err
	}
	if f.missing || id != f.view.ID {
		return repo.GetItemViewRow{}, pgx.ErrNoRows
	}
	return f.view, nil
}

func (f *fakeItems) ItemIsDone(context.Context, repo.ItemIsDoneParams) (bool, error) {
	return f.done, nil
}

func (f *fakeItems) ListQuizAnswers(context.Context, repo.ListQuizAnswersParams) ([]repo.ListQuizAnswersRow, error) {
	return f.answers, nil
}

func (f *fakeItems) GetStoryChoice(context.Context, repo.GetStoryChoiceParams) (repo.GetStoryChoiceRow, error) {
	if f.choice == nil {
		return repo.GetStoryChoiceRow{}, pgx.ErrNoRows
	}
	return *f.choice, nil
}

const quizPayload = `{"questions":[
 {"id":"who-last","text":"Who logged in last night?","answer":1,"explain":"who only shows now",
  "choices":[{"text":"` + "`who`" + `","note":"now"},{"text":"` + "`last`" + `","note":"past logins"}]},
 {"id":"sudo","text":"Second?","answer":0,"choices":[{"text":"a","note":"na"},{"text":"b","note":"nb"}]},
 {"id":"third","text":"Third?","answer":1,"choices":[{"text":"c","note":"nc"},{"text":"d","note":"nd"}]}]}`

const storyPayload = `{"from":{"name":"Elena","role":"Finance"},"scene":"Hi, the folder broke.","choices":[
 {"id":"calm","text":"Looking now","verdict":"good","reaction":"Elena relaxes."},
 {"id":"rude","text":"Not my job","verdict":"poor","reaction":"Elena escalates."}]}`

func quizFake() *fakeItems {
	return &fakeItems{view: repo.GetItemViewRow{ID: "support-l1/quiz-02", Role: "support-l1", Kind: "quiz",
		Title: "Users and logs", EstMinutes: 5, Payload: []byte(quizPayload)}}
}

func storyFake() *fakeItems {
	return &fakeItems{view: repo.GetItemViewRow{ID: "support-l1/story-01", Role: "support-l1", Kind: "story",
		Title: "Elena writes", EstMinutes: 3, Tags: []string{"people"}, Payload: []byte(storyPayload)}}
}

func itemCall(t *testing.T, f *fakeItems, method, path string, anon bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, nil)
	if !anon {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Items: &Items{Reader: f}}
	New(fakeReady{}, auth, NewRateLimits(1), nil, nil).ServeHTTP(rec, req)
	return rec
}

func decode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &m); err != nil || rec.Code != 200 {
		t.Fatalf("%d %v %s", rec.Code, err, rec.Body)
	}
	return m
}

func TestQuizUnansweredSendsTextsOnly(t *testing.T) {
	rec := itemCall(t, quizFake(), http.MethodGet, "/v1/items/support-l1/quiz-02", false)
	got := decode(t, rec)
	if got["kind"] != "quiz" || got["role"] != "support-l1" || got["done"] != false || got["answered"] != float64(0) ||
		got["total"] != float64(3) || got["first_try_correct"] != float64(0) || got["tags"] == nil {
		t.Fatalf("body: %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
	for _, leak := range []string{"past logins", `"note`, "who only shows", "explain", `"answer"`} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("unanswered quiz leaks %q: %s", leak, rec.Body)
		}
	}
	q := got["questions"].([]any)[0].(map[string]any)
	if q["id"] != "who-last" || q["answer_state"] != nil || q["choices"].([]any)[1] != "`last`" {
		t.Fatalf("question: %v", q)
	}
}

func TestQuizAnsweredRevealsOnlyThatQuestion(t *testing.T) {
	f := quizFake()
	f.answers = []repo.ListQuizAnswersRow{
		{QuestionID: "who-last", FirstChoice: 0, FirstCorrect: false, LastChoice: 1, LastCorrect: true},
		{QuestionID: "sudo", FirstChoice: 0, FirstCorrect: true, LastChoice: 0, LastCorrect: true},
		{QuestionID: "gone", FirstChoice: 1, FirstCorrect: true, LastChoice: 1, LastCorrect: true},
	}
	rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/quiz-02", false)
	got := decode(t, rec)
	if got["answered"] != float64(2) || got["first_try_correct"] != float64(1) || got["total"] != float64(3) {
		t.Fatalf("stats: %s", rec.Body)
	}
	qs := got["questions"].([]any)
	st := qs[0].(map[string]any)["answer_state"].(map[string]any)
	if st["first_choice"] != float64(0) || st["first_correct"] != false || st["last_choice"] != float64(1) ||
		st["last_correct"] != true || st["answer"] != float64(1) || st["explain"] != "who only shows now" ||
		st["notes"].([]any)[1] != "past logins" {
		t.Fatalf("answer_state: %v", st)
	}
	if st2 := qs[1].(map[string]any)["answer_state"].(map[string]any); st2["explain"] != nil {
		t.Fatalf("explain without one must be null: %v", st2)
	}
	if qs[2].(map[string]any)["answer_state"] != nil {
		t.Fatalf("unanswered question carries a state: %v", qs[2])
	}
	if strings.Contains(rec.Body.String(), `"nc"`) || strings.Contains(rec.Body.String(), "gone") {
		t.Fatalf("leak of an unanswered note or a removed question: %s", rec.Body)
	}
}

func TestStoryUnchosenHidesVerdicts(t *testing.T) {
	rec := itemCall(t, storyFake(), http.MethodGet, "/v1/items/support-l1/story-01", false)
	got := decode(t, rec)
	from := got["from"].(map[string]any)
	if got["kind"] != "story" || from["name"] != "Elena" || from["role"] != "Finance" || got["scene"] != "Hi, the folder broke." ||
		got["chosen"] != nil || got["done"] != false || len(got["choices"].([]any)) != 2 {
		t.Fatalf("body: %s", rec.Body)
	}
	c := got["choices"].([]any)[0].(map[string]any)
	if len(c) != 2 || c["id"] != "calm" || c["text"] != "Looking now" {
		t.Fatalf("choice must be id and text only: %v", c)
	}
	for _, leak := range []string{"verdict", "reaction", "relaxes", "escalates"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("unchosen story leaks %q: %s", leak, rec.Body)
		}
	}
}

func TestStoryChosenShowsOnlyThatReaction(t *testing.T) {
	f := storyFake()
	f.done = true
	f.choice = &repo.GetStoryChoiceRow{ChoiceID: "rude",
		ChosenAt: pgtype.Timestamptz{Time: time.Date(2026, 12, 2, 18, 10, 0, 0, time.UTC), Valid: true}}
	rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/story-01", false)
	got := decode(t, rec)
	ch := got["chosen"].(map[string]any)
	if got["done"] != true || ch["id"] != "rude" || ch["verdict"] != "poor" || ch["reaction"] != "Elena escalates." ||
		ch["chosen_at"] != "2026-12-02T18:10:00Z" {
		t.Fatalf("chosen: %s", rec.Body)
	}
	if strings.Contains(rec.Body.String(), "relaxes") || strings.Contains(rec.Body.String(), `"good"`) {
		t.Fatalf("leak of the other choice: %s", rec.Body)
	}
}

func TestStoryChosenIdGoneFromCatalog(t *testing.T) {
	f := storyFake()
	f.choice = &repo.GetStoryChoiceRow{ChoiceID: "removed", ChosenAt: pgtype.Timestamptz{Time: time.Unix(0, 0).UTC(), Valid: true}}
	got := decode(t, itemCall(t, f, http.MethodGet, "/v1/items/support-l1/story-01", false))
	ch := got["chosen"].(map[string]any)
	if ch["id"] != "removed" || ch["verdict"] != "" || ch["reaction"] != "" {
		t.Fatalf("chosen: %v", ch)
	}
}

func TestItemErrors(t *testing.T) {
	tests := []struct {
		name         string
		method, path string
		anon         bool
		f            func() *fakeItems
		code         int
		body         string
	}{
		{"anonymous", "GET", "/v1/items/support-l1/quiz-02", true, quizFake, 401, "unauthenticated"},
		{"unknown id", "GET", "/v1/items/support-l1/nope", false, quizFake, 404, "not_found"},
		{"malformed id", "GET", "/v1/items/Bad_ID/x", false, quizFake, 404, "not_found"},
		{"one segment", "GET", "/v1/items/support-l1", false, quizFake, 404, "not_found"},
		{"unknown action", "GET", "/v1/items/support-l1/quiz-02/other", false, quizFake, 404, "not_found"},
		{"action is not a read", "GET", "/v1/items/support-l1/quiz-02/answers", false, quizFake, 404, "not_found"},
		{"four segments", "GET", "/v1/items/support-l1/quiz-02/answers/x", false, quizFake, 404, "not_found"},
		{"database error", "GET", "/v1/items/support-l1/quiz-02", false, func() *fakeItems {
			f := quizFake()
			f.err = context.DeadlineExceeded
			return f
		}, 500, "server_error"},
		{"corrupt payload", "GET", "/v1/items/support-l1/quiz-02", false, func() *fakeItems {
			f := quizFake()
			f.view.Payload = []byte(`[]`)
			return f
		}, 500, "server_error"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := itemCall(t, tt.f(), tt.method, tt.path, tt.anon)
			if rec.Code != tt.code || !strings.Contains(rec.Body.String(), `"`+tt.body+`"`) {
				t.Fatalf("%d %s, want %d %s", rec.Code, rec.Body, tt.code, tt.body)
			}
		})
	}
}

func TestItemReadsShareTheReadBudget(t *testing.T) {
	s := &fakeSessions{row: activeRow(time.Now(), false)}
	h := New(fakeReady{}, Auth{Store: s, Catalog: populatedCatalog(), Items: &Items{Reader: quizFake()}}, testLimits(3), nil, nil)
	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
		rec := httptest.NewRecorder()
		h.ServeHTTP(rec, req)
		return rec.Code
	}
	var codes []int
	for _, p := range []string{"/v1/items/support-l1/quiz-02", "/v1/catalog", "/v1/items/support-l1/quiz-02", "/v1/items/support-l1/quiz-02"} {
		codes = append(codes, get(p))
	}
	if codes[0] != 200 || codes[1] != 200 || codes[2] != 200 || codes[3] != 429 {
		t.Fatalf("codes %v", codes)
	}
	s.row.LearnerID = pgtype.UUID{Bytes: [16]byte{7}, Valid: true}
	if c := get("/v1/items/support-l1/quiz-02"); c != 200 {
		t.Fatalf("other learner: %d, want 200", c)
	}
}
