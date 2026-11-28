//go:build integration

package server_test

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

// postItem sends POST with the bearer token; it is safe to call from a goroutine.
func postItem(tok, base, path, body string) (int, string, http.Header, error) {
	req, err := http.NewRequest(http.MethodPost, base+path, strings.NewReader(body))
	if err != nil {
		return 0, "", nil, err
	}
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	out, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(out), resp.Header, nil
}

func answer(t *testing.T, tok, item, question string, choice int) (int, map[string]any, string) {
	t.Helper()
	code, body, _, err := postItem(tok, apiURL(t), "/v1/items/"+item+"/answers",
		`{"question":"`+question+`","choice":`+string(rune('0'+choice))+`}`)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	_ = json.Unmarshal([]byte(body), &m)
	return code, m, body
}

func dbCount(t *testing.T, table, item string, githubID string) string {
	t.Helper()
	return asOwner(t, "SELECT count(*) FROM "+table+" t JOIN learners l ON l.id = t.learner_id WHERE l.github_id = "+githubID+" AND t.item_id = '"+item+"'")
}

// noItemEvent fails when an `item` event arrives within d.
func (s *sseStream) noItemEvent(d time.Duration) {
	s.t.Helper()
	end := time.After(d)
	for {
		select {
		case ev, ok := <-s.events:
			if ok && ev.name == "item" {
				s.t.Fatalf("unexpected item event %s", ev.data)
			}
		case <-end:
			return
		}
	}
}

func TestQuizAnswersFlowIntegration(t *testing.T) {
	tok := itemFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	// who-last: right answer is choice 1. A wrong first answer, then the right one.
	code, got, body := answer(t, tok, "foundations/q", "who-last", 0)
	if code != 200 || got["correct"] != false || got["answer"] != float64(1) || got["done"] != false || got["answered"] != float64(1) ||
		got["total"] != float64(2) || got["first_try_correct"] != float64(0) || got["explain"] != "who is now only" ||
		len(got["notes"].([]any)) != 2 || got["notes"].([]any)[1] != "note-last" {
		t.Fatalf("first answer: %d %s", code, body)
	}
	code, got, body = answer(t, tok, "foundations/q", "who-last", 1)
	if code != 200 || got["correct"] != true || got["done"] != false || got["answered"] != float64(1) || got["first_try_correct"] != float64(0) {
		t.Fatalf("second answer: %d %s", code, body)
	}
	if row := asOwner(t, "SELECT first_choice, first_correct, last_choice, last_correct FROM quiz_answers"); strings.TrimSpace(row) != "0|f|1|t" {
		t.Fatalf("first kept, last updated: %q", row)
	}
	if n := dbCount(t, "item_completions", "foundations/q", "1001"); strings.TrimSpace(n) != "0" {
		t.Fatalf("completion before the last question: %s", n)
	}
	s.noItemEvent(300 * time.Millisecond)

	code, got, body = answer(t, tok, "foundations/q", "second", 0)
	if code != 200 || got["correct"] != true || got["done"] != true || got["answered"] != float64(2) || got["first_try_correct"] != float64(1) || got["explain"] != nil {
		t.Fatalf("completing answer: %d %s", code, body)
	}
	ev := s.next("item", 5*time.Second)
	if ev.data != `{"item_id":"foundations/q","kind":"quiz"}` {
		t.Fatalf("event data: %s", ev.data)
	}
	if n := dbCount(t, "item_completions", "foundations/q", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows: %s", n)
	}
	if _, view := getItem(t, tok, "foundations/q"); !strings.Contains(view, `"done":true`) || !strings.Contains(view, `"first_try_correct":1`) {
		t.Fatalf("view after completing: %s", view)
	}

	// A retake changes last_*, never the completion, and sends nothing.
	code, got, body = answer(t, tok, "foundations/q", "second", 1)
	if code != 200 || got["correct"] != false || got["done"] != true || got["first_try_correct"] != float64(1) {
		t.Fatalf("retake: %d %s", code, body)
	}
	if n := dbCount(t, "item_completions", "foundations/q", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rewritten: %s", n)
	}
	s.noItemEvent(500 * time.Millisecond)
}

func TestQuizAnswersRefusalsIntegration(t *testing.T) {
	tok := itemFixture(t)
	for name, tt := range map[string]struct {
		item, body string
		code       int
		err        string
	}{
		"unknown question":    {"foundations/q", `{"question":"nope","choice":0}`, 404, "not_found"},
		"malformed question":  {"foundations/q", `{"question":"Bad_Id","choice":0}`, 404, "not_found"},
		"overlong question":   {"foundations/q", `{"question":"` + strings.Repeat("a", 40) + `","choice":0}`, 404, "not_found"},
		"choice too high":     {"foundations/q", `{"question":"who-last","choice":2}`, 400, "bad_request"},
		"choice above 4":      {"foundations/q", `{"question":"who-last","choice":5}`, 400, "bad_request"},
		"negative choice":     {"foundations/q", `{"question":"who-last","choice":-1}`, 400, "bad_request"},
		"not json":            {"foundations/q", `nope`, 400, "bad_request"},
		"story item":          {"foundations/s", `{"question":"who-last","choice":0}`, 404, "not_found"},
		"ticket item":         {"foundations/a", `{"question":"who-last","choice":0}`, 404, "not_found"},
		"unknown item":        {"foundations/zzz", `{"question":"who-last","choice":0}`, 404, "not_found"},
		"other action":        {"foundations/q/draft", `{}`, 404, "not_found"},
		"bare id, no action":  {"foundations/q", `{}`, 404, "not_found"},
		"answers on an extra": {"foundations/q/answers/x", `{}`, 404, "not_found"},
	} {
		path := "/v1/items/" + tt.item + "/answers"
		switch name {
		case "other action", "bare id, no action", "answers on an extra":
			path = "/v1/items/" + tt.item
		}
		code, body, _, err := postItem(tok, apiURL(t), path, tt.body)
		if err != nil || code != tt.code || strings.TrimSpace(body) != `{"error":"`+tt.err+`"}` {
			t.Errorf("%s: %d %s %v", name, code, body, err)
		}
	}
	if n := asOwner(t, "SELECT count(*) FROM quiz_answers"); strings.TrimSpace(n) != "0" {
		t.Fatalf("a refused answer wrote %s rows", n)
	}

	// A cookie write without the CSRF header is refused before anything runs.
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	if code, body := ada.post("/v1/items/foundations/q/answers", `{"question":"who-last","choice":1}`, false); code != 403 || !strings.Contains(body, "csrf_rejected") {
		t.Fatalf("no csrf: %d %s", code, body)
	}
	if code, body := ada.post("/v1/items/foundations/q/answers", `{"question":"who-last","choice":1}`, true); code != 200 {
		t.Fatalf("cookie with csrf: %d %s", code, body)
	}
	if code, _, _, _ := postItem("", apiURL(t), "/v1/items/foundations/q/answers", `{"question":"who-last","choice":1}`); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}

// Many answers racing on both questions: one completion, one event, one row per question.
func TestQuizAnswersConcurrentIntegration(t *testing.T) {
	tok := itemFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			q, c := "who-last", i%2
			if i%4 >= 2 {
				q = "second"
			}
			code, body, _, err := postItem(tok, apiURL(t), "/v1/items/foundations/q/answers", `{"question":"`+q+`","choice":`+string(rune('0'+c))+`}`)
			if err != nil || code != 200 {
				t.Errorf("racing answer %d: %d %s %v", i, code, body, err)
			}
		}()
	}
	wg.Wait()
	s.next("item", 5*time.Second)
	s.noItemEvent(500 * time.Millisecond)
	if n := dbCount(t, "item_completions", "foundations/q", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows: %s", n)
	}
	if n := dbCount(t, "quiz_answers", "foundations/q", "1001"); strings.TrimSpace(n) != "2" {
		t.Fatalf("answer rows: %s", n)
	}
	// first_correct always agrees with the stored first_choice, whichever request won each question.
	if bad := asOwner(t, "SELECT count(*) FROM quiz_answers WHERE first_correct <> ((question_id = 'who-last' AND first_choice = 1) OR (question_id = 'second' AND first_choice = 0))"); strings.TrimSpace(bad) != "0" {
		t.Fatalf("inconsistent first answers: %s", bad)
	}
}

func TestQuizAnswersAreIsolatedPerLearnerIntegration(t *testing.T) {
	tok := itemFixture(t)
	resetLearners(t, "1002")
	signUp(t, "bob")
	bobTok := cliToken(t, newCLIClient(t), "bob")
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	for _, q := range [][2]any{{"who-last", 1}, {"second", 0}} {
		if code, got, body := answer(t, bobTok, "foundations/q", q[0].(string), q[1].(int)); code != 200 || got["answered"] == nil {
			t.Fatalf("bob: %d %s", code, body)
		}
	}
	if n := dbCount(t, "item_completions", "foundations/q", "1002"); strings.TrimSpace(n) != "1" {
		t.Fatalf("bob completion: %s", n)
	}
	if n := dbCount(t, "item_completions", "foundations/q", "1001"); strings.TrimSpace(n) != "0" {
		t.Fatalf("ada has bob's completion: %s", n)
	}
	s.noItemEvent(500 * time.Millisecond)
	if _, view := getItem(t, tok, "foundations/q"); !strings.Contains(view, `"done":false`) || !strings.Contains(view, `"answered":0`) {
		t.Fatalf("ada's view: %s", view)
	}
}

func TestQuizAnswersBetweenBudgetIntegration(t *testing.T) {
	tok := itemFixture(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	retry := ""
	for i := 0; i < 90; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, h, err := postItem(tok, apiURL(t), "/v1/items/foundations/q/answers", `{"question":"who-last","choice":1}`)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			codes[code]++
			if code == 429 {
				retry = h.Get("Retry-After")
			}
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[200] < 60 || codes[200] > 70 || codes[429] < 20 || codes[200]+codes[429] != 90 || retry == "" {
		t.Fatalf("budget: %v retry=%q", codes, retry)
	}
}
