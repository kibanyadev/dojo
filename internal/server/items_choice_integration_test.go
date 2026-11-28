//go:build integration

package server_test

import (
	"strings"
	"sync"
	"testing"
	"time"
)

const storyChoicePath = "/v1/items/foundations/s/choice"

func TestStoryChoiceFlowIntegration(t *testing.T) {
	tok := itemFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	code, body, h, err := postItem(tok, apiURL(t), storyChoicePath, `{"choice":"calm"}`)
	if err != nil || code != 200 || h.Get("Cache-Control") != "no-store" ||
		!strings.Contains(body, `"id":"calm"`) || !strings.Contains(body, `"verdict":"good"`) ||
		!strings.Contains(body, `"reaction":"reaction-calm"`) || !strings.Contains(body, `"chosen_at":"`) ||
		strings.Contains(body, "rude") || strings.Contains(body, "reaction-rude") {
		t.Fatalf("first choice: %d %s %v", code, body, err)
	}
	if ev := s.next("item", 5*time.Second); ev.data != `{"item_id":"foundations/s","kind":"story"}` {
		t.Fatalf("event data: %s", ev.data)
	}
	if n := dbCount(t, "item_completions", "foundations/s", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows: %s", n)
	}

	// The same choice again answers with the stored pick and changes nothing.
	code2, body2, _, _ := postItem(tok, apiURL(t), storyChoicePath, `{"choice":"calm"}`)
	if code2 != 200 || body2 != body {
		t.Fatalf("repeat: %d %s, first %s", code2, body2, body)
	}
	// A different one is refused and nothing is stored.
	code, body, _, _ = postItem(tok, apiURL(t), storyChoicePath, `{"choice":"rude"}`)
	if code != 409 || strings.TrimSpace(body) != `{"error":"already_chosen"}` {
		t.Fatalf("second choice: %d %s", code, body)
	}
	if row := asOwner(t, "SELECT choice_id FROM story_choices"); strings.TrimSpace(row) != "calm" {
		t.Fatalf("stored: %q", row)
	}
	s.noItemEvent(500 * time.Millisecond)
	if _, view := getItem(t, tok, "foundations/s"); !strings.Contains(view, `"done":true`) || !strings.Contains(view, "reaction-calm") {
		t.Fatalf("view: %s", view)
	}
}

func TestStoryChoiceRefusalsIntegration(t *testing.T) {
	tok := itemFixture(t)
	for name, tt := range map[string]struct {
		path, body string
		code       int
		err        string
	}{
		"unknown choice":   {storyChoicePath, `{"choice":"nope"}`, 404, "not_found"},
		"malformed choice": {storyChoicePath, `{"choice":"Bad_Id"}`, 404, "not_found"},
		"overlong choice":  {storyChoicePath, `{"choice":"` + strings.Repeat("a", 40) + `"}`, 404, "not_found"},
		"not json":         {storyChoicePath, `nope`, 400, "bad_request"},
		"missing choice":   {storyChoicePath, `{}`, 400, "bad_request"},
		"number choice":    {storyChoicePath, `{"choice":1}`, 400, "bad_request"},
		"quiz item":        {"/v1/items/foundations/q/choice", `{"choice":"calm"}`, 404, "not_found"},
		"ticket item":      {"/v1/items/foundations/a/choice", `{"choice":"calm"}`, 404, "not_found"},
		"unknown item":     {"/v1/items/foundations/zzz/choice", `{"choice":"calm"}`, 404, "not_found"},
	} {
		code, body, _, err := postItem(tok, apiURL(t), tt.path, tt.body)
		if err != nil || code != tt.code || strings.TrimSpace(body) != `{"error":"`+tt.err+`"}` {
			t.Errorf("%s: %d %s %v", name, code, body, err)
		}
	}
	if n := asOwner(t, "SELECT count(*) FROM story_choices"); strings.TrimSpace(n) != "0" {
		t.Fatalf("a refused choice wrote %s rows", n)
	}

	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	if code, body := ada.post(storyChoicePath, `{"choice":"calm"}`, false); code != 403 || !strings.Contains(body, "csrf_rejected") {
		t.Fatalf("no csrf: %d %s", code, body)
	}
	if code, body := ada.post(storyChoicePath, `{"choice":"calm"}`, true); code != 200 {
		t.Fatalf("cookie with csrf: %d %s", code, body)
	}
	if code, _, _, _ := postItem("", apiURL(t), storyChoicePath, `{"choice":"calm"}`); code != 401 {
		t.Fatalf("anonymous: %d", code)
	}
}

// Racing picks, some of each choice: one row, one completion, one event; every response is 200 or 409
// and agrees with the stored pick.
func TestStoryChoiceConcurrentIntegration(t *testing.T) {
	tok := itemFixture(t)
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[string]map[int]int{"calm": {}, "rude": {}}
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := []string{"calm", "rude"}[i%2]
			code, body, _, err := postItem(tok, apiURL(t), storyChoicePath, `{"choice":"`+c+`"}`)
			if err != nil || (code != 200 && code != 409) {
				t.Errorf("racing choice %d: %d %s %v", i, code, body, err)
			}
			mu.Lock()
			codes[c][code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	s.next("item", 5*time.Second)
	s.noItemEvent(500 * time.Millisecond)
	if n := dbCount(t, "item_completions", "foundations/s", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("completion rows: %s", n)
	}
	if n := dbCount(t, "story_choices", "foundations/s", "1001"); strings.TrimSpace(n) != "1" {
		t.Fatalf("choice rows: %s", n)
	}
	won := strings.TrimSpace(asOwner(t, "SELECT choice_id FROM story_choices"))
	lost := map[string]string{"calm": "rude", "rude": "calm"}[won]
	if codes[won][409] != 0 || codes[won][200] != 6 || codes[lost][200] != 0 || codes[lost][409] != 6 {
		t.Fatalf("won %s: %v", won, codes)
	}
}

func TestStoryChoiceIsolatedPerLearnerIntegration(t *testing.T) {
	tok := itemFixture(t)
	resetLearners(t, "1002")
	signUp(t, "bob")
	bobTok := cliToken(t, newCLIClient(t), "bob")
	ada := newBrowser(t)
	ada.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)

	if code, body, _, _ := postItem(bobTok, apiURL(t), storyChoicePath, `{"choice":"rude"}`); code != 200 {
		t.Fatalf("bob: %d %s", code, body)
	}
	s.noItemEvent(500 * time.Millisecond)
	if n := dbCount(t, "item_completions", "foundations/s", "1001"); strings.TrimSpace(n) != "0" {
		t.Fatalf("ada has bob's completion: %s", n)
	}
	if _, view := getItem(t, tok, "foundations/s"); !strings.Contains(view, `"chosen":null`) || !strings.Contains(view, `"done":false`) {
		t.Fatalf("ada's view: %s", view)
	}
	// Ada can still pick, with her own answer.
	if code, body, _, _ := postItem(tok, apiURL(t), storyChoicePath, `{"choice":"calm"}`); code != 200 || !strings.Contains(body, "reaction-calm") {
		t.Fatalf("ada: %d %s", code, body)
	}
}

func TestStoryChoiceBetweenBudgetIntegration(t *testing.T) {
	tok := itemFixture(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	codes := map[int]int{}
	for i := 0; i < 90; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			code, _, _, err := postItem(tok, apiURL(t), storyChoicePath, `{"choice":"calm"}`)
			if err != nil {
				t.Error(err)
				return
			}
			mu.Lock()
			codes[code]++
			mu.Unlock()
		}()
	}
	wg.Wait()
	if codes[200] < 60 || codes[200] > 70 || codes[429] < 20 || codes[200]+codes[429] != 90 {
		t.Fatalf("budget: %v", codes)
	}
}
