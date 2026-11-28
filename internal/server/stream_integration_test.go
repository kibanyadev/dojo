//go:build integration

package server_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kibanyadev/dojo/internal/server"
)

type sseEvent struct{ name, data string }

type sseStream struct {
	t      *testing.T
	resp   *http.Response
	events chan sseEvent
}

// openStream connects with the learner's session cookie; base is the API or the edge.
func openStream(t *testing.T, base string, cookie *http.Cookie) *sseStream {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, base+"/v1/stream", nil)
	req.AddCookie(cookie)
	resp, err := (&http.Client{}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	s := &sseStream{t: t, resp: resp, events: make(chan sseEvent, 64)}
	if resp.StatusCode != http.StatusOK {
		return s
	}
	go func() {
		defer close(s.events)
		sc := bufio.NewScanner(resp.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case line == "":
				if ev.name != "" {
					s.events <- ev
				}
				ev = sseEvent{}
			case strings.HasPrefix(line, "event: "):
				ev.name = line[len("event: "):]
			case strings.HasPrefix(line, "data: "):
				ev.data = line[len("data: "):]
			}
		}
	}()
	return s
}

// next returns the next event of the given name, skipping heartbeats unless asked for them.
func (s *sseStream) next(want string, within time.Duration) sseEvent {
	s.t.Helper()
	deadline := time.After(within)
	for {
		select {
		case ev, ok := <-s.events:
			if !ok {
				s.t.Fatalf("stream closed while waiting for %q", want)
			}
			if ev.name == want {
				return ev
			}
			if ev.name != "heartbeat" && ev.name != "ready" {
				s.t.Fatalf("waiting for %q, got %q %s", want, ev.name, ev.data)
			}
		case <-deadline:
			s.t.Fatalf("no %q within %s", want, within)
		}
	}
}

// quiet fails on any progress event within d.
func (s *sseStream) quiet(d time.Duration) {
	s.t.Helper()
	end := time.After(d)
	for {
		select {
		case ev, ok := <-s.events:
			if ok && ev.name == "progress" {
				s.t.Fatalf("unexpected progress %s", ev.data)
			}
		case <-end:
			return
		}
	}
}

func (s *sseStream) closed(within time.Duration) bool {
	end := time.After(within)
	for {
		select {
		case _, ok := <-s.events:
			if !ok {
				return true
			}
		case <-end:
			return false
		}
	}
}

func cookieOf(t *testing.T, b *browser) *http.Cookie {
	t.Helper()
	u, _ := url.Parse(apiURL(t))
	for _, c := range b.c.Jar.Cookies(u) {
		if c.Name == server.SessionCookie {
			return c
		}
	}
	t.Fatal("no session cookie")
	return nil
}

// streamFixture is ada with events, a CLI token and a signed-in browser.
func streamFixture(t *testing.T) (c *cliClient, tok string, ada *browser) {
	t.Helper()
	c, tok = eventsFixture(t)
	ada = newBrowser(t)
	if resp := ada.signIn("ada", "", "age=on&terms=on", false); resp.Header.Get("Location") != "/app" {
		t.Fatalf("ada sign-in: %q", resp.Header.Get("Location"))
	}
	return c, tok, ada
}

// waitStreamsServe returns once /v1/stream accepts a subscriber. After the database restarts the
// API's listener reconnects on its own schedule; the next stream test must not race it.
func waitStreamsServe(t *testing.T) {
	t.Helper()
	asOwner(t, "DELETE FROM learners WHERE github_id = 1001")
	defer asOwner(t, "DELETE FROM learners WHERE github_id = 1001")
	b := newBrowser(t)
	if resp := b.signIn("ada", "", "age=on&terms=on", false); resp.Header.Get("Location") != "/app" {
		t.Errorf("stream readiness sign-in: %q", resp.Header.Get("Location"))
		return
	}
	cookie := cookieOf(t, b)
	deadline := time.Now().Add(15 * time.Second)
	last := "no attempt"
	for time.Now().Before(deadline) {
		ctx, cancel := context.WithCancel(context.Background())
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, apiURL(t)+"/v1/stream", nil)
		req.AddCookie(cookie)
		resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
		if err == nil {
			code := resp.StatusCode
			_ = resp.Body.Close()
			cancel()
			if code == http.StatusOK {
				return
			}
			last = http.StatusText(code)
		} else {
			cancel()
			last = err.Error()
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Errorf("/v1/stream still refuses subscribers 15s after the database restart (last: %s)", last)
}

func killListener(t *testing.T) {
	t.Helper()
	if n := asOwner(t, "SELECT count(pg_terminate_backend(pid)) FROM pg_stat_activity WHERE application_name = 'dojo-api-listener'"); n == "0" {
		t.Fatal("no listener connection to kill")
	}
}

func TestStreamReadyProgressHeartbeat(t *testing.T) {
	c, tok, ada := streamFixture(t)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	if ct := s.resp.Header.Get("Content-Type"); ct != "text/event-stream" || s.resp.Header.Get("Cache-Control") != "no-cache, no-transform" || s.resp.Header.Get("X-Accel-Buffering") != "no" {
		t.Fatalf("headers: %v", s.resp.Header)
	}
	if ev := s.next("ready", 5*time.Second); ev.data != "{}" {
		t.Fatalf("ready: %q", ev.data)
	}
	if code, body := post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z")); code != 200 {
		t.Fatalf("%d %q", code, body)
	}
	ev := s.next("progress", 5*time.Second)
	want := `{"item_id":"ev-role/one","state":"started","lab_state":"starting","lab":"local","check_count":0,"last_check_ok":null,"last_check_at":null,"passed_commit_state":null}`
	if ev.data != want {
		t.Fatalf("progress: %s", ev.data)
	}
	post(t, c, tok, evCheck(evID(), "2026-11-23T10:05:00Z", "fail", "SECRET-OUTPUT"))
	ev = s.next("progress", 5*time.Second)
	var m map[string]any
	if json.Unmarshal([]byte(ev.data), &m) != nil || m["check_count"] != float64(1) || m["last_check_ok"] != false || strings.Contains(ev.data, "SECRET") {
		t.Fatalf("progress after check: %s", ev.data)
	}
	if ib := s.next("inbox", 5*time.Second); ib.data != "{}" {
		t.Fatalf("inbox after check: %q", ib.data)
	}
	if hb := s.next("heartbeat", 5*time.Second); !strings.HasPrefix(hb.data, `{"at":"20`) {
		t.Fatalf("heartbeat: %q", hb.data)
	}
}

func TestStreamNeverCarriesAnotherLearnersEvents(t *testing.T) {
	c, tok, ada := streamFixture(t)
	resetLearners(t, "1002")
	bob := newBrowser(t)
	if resp := bob.signIn("bob", "", "age=on&terms=on", false); resp.Header.Get("Location") != "/app" {
		t.Fatalf("bob sign-in: %q", resp.Header.Get("Location"))
	}
	t.Cleanup(func() { asOwner(t, "DELETE FROM learners WHERE github_id = 1002") })
	sb := openStream(t, apiURL(t), cookieOf(t, bob))
	sa := openStream(t, apiURL(t), cookieOf(t, ada))
	sb.next("ready", 5*time.Second)
	sa.next("ready", 5*time.Second)
	post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z"))
	sa.next("progress", 5*time.Second)
	sb.quiet(2500 * time.Millisecond)
}

func TestStreamSixthIsRefusedAndNothingEvicted(t *testing.T) {
	c, tok, ada := streamFixture(t)
	cookie := cookieOf(t, ada)
	var open []*sseStream
	for i := 0; i < 5; i++ {
		s := openStream(t, apiURL(t), cookie)
		s.next("ready", 5*time.Second)
		open = append(open, s)
	}
	sixth := openStream(t, apiURL(t), cookie)
	if sixth.resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("sixth: %d", sixth.resp.StatusCode)
	}
	var b bytes.Buffer
	_, _ = b.ReadFrom(sixth.resp.Body)
	if strings.TrimSpace(b.String()) != `{"error":"too_many_streams"}` {
		t.Fatalf("body %q", b.String())
	}
	post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z"))
	for i, s := range open {
		s.next("progress", 5*time.Second)
		_ = i
	}
}

func TestStreamAuthRefusals(t *testing.T) {
	c, tok, _ := streamFixture(t)
	if code, _ := c.call(http.MethodGet, "/v1/stream", tok, nil); code != http.StatusForbidden {
		t.Fatalf("bearer: %d", code)
	}
	if code, _ := c.call(http.MethodGet, "/v1/stream", "", nil); code != http.StatusUnauthorized {
		t.Fatalf("anonymous: %d", code)
	}
}

func TestStreamClosesAtHeartbeatWhenLearnerIsSuspended(t *testing.T) {
	_, _, ada := streamFixture(t)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)
	asOwner(t, "UPDATE learners SET suspended_at = now() WHERE github_id = 1001")
	if !s.closed(8 * time.Second) {
		t.Fatal("stream still open after suspension")
	}
}

func TestStreamClosesAtHeartbeatWhenSessionIsRevoked(t *testing.T) {
	_, _, ada := streamFixture(t)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)
	asOwner(t, "UPDATE sessions SET revoked_at = now() WHERE learner_id IN (SELECT id FROM learners WHERE github_id = 1001)")
	if !s.closed(8 * time.Second) {
		t.Fatal("stream still open after revoke")
	}
}

func TestStreamResyncsAfterListenerDrop(t *testing.T) {
	c, tok, ada := streamFixture(t)
	s := openStream(t, apiURL(t), cookieOf(t, ada))
	s.next("ready", 5*time.Second)
	killListener(t)
	s.next("resync", 15*time.Second)
	// The task re-LISTENed: a new event still arrives.
	post(t, c, tok, evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z"))
	s.next("progress", 5*time.Second)
}

func TestEdgeStreamFlushesReadyProgressHeartbeatAndResync(t *testing.T) {
	_, tok, ada := streamFixture(t)
	s := openStream(t, edgeURL(t), cookieOf(t, ada))
	if s.resp.StatusCode != http.StatusOK {
		t.Fatalf("edge stream: %d", s.resp.StatusCode)
	}
	s.next("ready", 5*time.Second)

	body, _ := json.Marshal(map[string]any{"events": []any{evStarted(evID(), "ev-role/one", "2026-11-23T10:00:00Z")}})
	req, _ := http.NewRequest(http.MethodPost, edgeURL(t)+"/v1/events", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("post through the edge: %d", resp.StatusCode)
	}
	s.next("progress", 5*time.Second)
	s.next("heartbeat", 5*time.Second)
	killListener(t)
	s.next("resync", 15*time.Second)
}
