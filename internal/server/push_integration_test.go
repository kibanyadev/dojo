//go:build integration

package server_test

import (
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/kibanyadev/dojo/internal/webpush"
)

const pushFakeOrigin = "http://fake-push:8080"

func pushBody(t *testing.T, endpoint, label, zone string) string {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := json.Marshal(map[string]any{
		"endpoint": endpoint,
		"keys": map[string]string{"p256dh": base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()),
			"auth": base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))},
		"device_label": label, "time_zone": zone})
	return string(b)
}

func subscribeAs(t *testing.T, b *browser, endpoint, label string) string {
	t.Helper()
	code, out := b.send(http.MethodPost, "/v1/push/subscriptions", pushBody(t, endpoint, label, "Europe/Madrid"), true)
	if code != http.StatusCreated {
		t.Fatalf("subscribe %s: %d %s", label, code, out)
	}
	var r struct{ ID string }
	if err := json.Unmarshal([]byte(out), &r); err != nil || len(r.ID) != 36 {
		t.Fatalf("subscribe body: %s", out)
	}
	return r.ID
}

func listPush(t *testing.T, b *browser) []map[string]any {
	t.Helper()
	code, out := b.getJSON("/v1/push/subscriptions")
	if code != http.StatusOK {
		t.Fatalf("list: %d %s", code, out)
	}
	var r struct{ Subscriptions []map[string]any }
	if err := json.Unmarshal([]byte(out), &r); err != nil {
		t.Fatal(err)
	}
	return r.Subscriptions
}

func pushFixture(t *testing.T) (ada, bob *browser) {
	t.Helper()
	resetLearners(t, "1001", "1002")
	ada, bob = newBrowser(t), newBrowser(t)
	for b, login := range map[*browser]string{ada: "ada", bob: "bob"} {
		if resp := b.signIn(login, "", "age=on&terms=on", false); resp.Header.Get("Location") != "/app" {
			t.Fatalf("%s sign-in: %q", login, resp.Header.Get("Location"))
		}
	}
	return ada, bob
}

func TestPushKeyIntegration(t *testing.T) {
	ada, _ := pushFixture(t)
	v, _ := webpush.ParseVAPID(envExample(t, "DOJO_VAPID_PRIVATE_KEY"))
	resp, err := ada.c.Get(apiURL(t) + "/v1/push/key")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var got struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil || resp.StatusCode != 200 ||
		got.PublicKey != base64.RawURLEncoding.EncodeToString(v.PublicKey()) || resp.Header.Get("Cache-Control") != "no-store" {
		t.Fatalf("key: %d %+v %q", resp.StatusCode, got, resp.Header.Get("Cache-Control"))
	}
}

func TestPushSubscriptionLifecycleIntegration(t *testing.T) {
	ada, bob := pushFixture(t)
	endpoint := pushFakeOrigin + "/push/lifecycle"

	id := subscribeAs(t, ada, endpoint, "Safari on iPhone")
	if got := asOwner(t, "SELECT time_zone FROM learners WHERE github_id = 1001"); got != "Europe/Madrid" {
		t.Fatalf("time_zone: %q", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM push_subscriptions WHERE endpoint = '"+endpoint+"'"); got != "1" {
		t.Fatalf("rows: %s", got)
	}
	list := listPush(t, ada)
	if len(list) != 1 || list[0]["id"] != id || list[0]["device_label"] != "Safari on iPhone" || list[0]["last_success_at"] != nil || len(list[0]) != 4 {
		t.Fatalf("list: %v", list)
	}

	// The same browser, subscribed by bob: it belongs to him now.
	bobID := subscribeAs(t, bob, endpoint, "Shared browser")
	if l := listPush(t, ada); len(l) != 0 {
		t.Fatalf("ada keeps the endpoint: %v", l)
	}
	if l := listPush(t, bob); len(l) != 1 || l[0]["id"] != bobID {
		t.Fatalf("bob's list: %v", l)
	}

	// Another learner's id is a 404 and deletes nothing, like an unknown or malformed one.
	for _, target := range []string{bobID, "0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", "nope"} {
		if code, _ := ada.send(http.MethodDelete, "/v1/push/subscriptions/"+target, "", true); code != http.StatusNotFound {
			t.Fatalf("ada deleting %s: %d", target, code)
		}
	}
	if l := listPush(t, bob); len(l) != 1 {
		t.Fatalf("bob lost his row: %v", l)
	}
	if code, _ := bob.send(http.MethodDelete, "/v1/push/subscriptions/"+bobID, "", true); code != http.StatusNoContent {
		t.Fatalf("bob deleting his own: %d", code)
	}
	if code, _ := bob.send(http.MethodDelete, "/v1/push/subscriptions/"+bobID, "", true); code != http.StatusNotFound {
		t.Fatalf("second delete: %d", code)
	}
}

func TestPushSubscribeKeepsTheNewestTenIntegration(t *testing.T) {
	ada, _ := pushFixture(t)
	for i := 1; i <= 10; i++ {
		asOwner(t, fmt.Sprintf("INSERT INTO push_subscriptions (org_id, learner_id, endpoint, p256dh, auth, device_label, created_at) "+
			"SELECT org_id, id, 'http://fake-push:8080/push/cap-%d', repeat('A', 87), repeat('B', 22), 'Device %d', now() - interval '%d minutes' FROM learners WHERE github_id = 1001", i, i, 100-i))
	}
	subscribeAs(t, ada, pushFakeOrigin+"/push/cap-new", "Newest")
	list := listPush(t, ada)
	if len(list) != 10 || list[0]["device_label"] != "Newest" {
		t.Fatalf("list: %v", list)
	}
	if got := asOwner(t, "SELECT count(*) FROM push_subscriptions WHERE endpoint = 'http://fake-push:8080/push/cap-1'"); got != "0" {
		t.Fatalf("the oldest stays: %s", got)
	}
	// Subscribing the same endpoint again replaces its row, it does not evict another.
	subscribeAs(t, ada, pushFakeOrigin+"/push/cap-new", "Newest again")
	if l := listPush(t, ada); len(l) != 10 {
		t.Fatalf("after a resubscribe: %d rows", len(l))
	}
}

func TestPushRefusalsIntegration(t *testing.T) {
	ada, _ := pushFixture(t)
	for name, c := range map[string]struct{ endpoint, want string }{
		"plain http on a real host": {"http://fcm.googleapis.com/fcm/send/x", "endpoint_not_allowed"},
		"unlisted host":             {"https://push.evil.example/x", "endpoint_not_allowed"},
		"cloud metadata":            {"https://169.254.169.254/latest/meta-data", "endpoint_not_allowed"},
		"loopback":                  {"https://127.0.0.1/x", "endpoint_not_allowed"},
		"loopback name":             {"https://localhost/x", "endpoint_not_allowed"},
		"other port on the stub":    {"http://fake-push:9090/push/x", "endpoint_not_allowed"},
		"userinfo":                  {"https://u:p@fcm.googleapis.com/x", "endpoint_not_allowed"},
	} {
		code, out := ada.send(http.MethodPost, "/v1/push/subscriptions", pushBody(t, c.endpoint, "Phone", "Europe/Madrid"), true)
		if code != http.StatusBadRequest || !strings.Contains(out, c.want) {
			t.Errorf("%s: %d %s", name, code, out)
		}
	}
	if code, out := ada.send(http.MethodPost, "/v1/push/subscriptions", pushBody(t, pushFakeOrigin+"/push/z", "Phone", "Mars/Olympus"), true); code != http.StatusBadRequest || !strings.Contains(out, "bad_request") {
		t.Errorf("zone: %d %s", code, out)
	}
	if code, _ := ada.send(http.MethodPost, "/v1/push/subscriptions", pushBody(t, pushFakeOrigin+"/push/z", "Phone", "Europe/Madrid"), false); code != http.StatusForbidden {
		t.Errorf("no CSRF header: %d", code)
	}
	if got := asOwner(t, "SELECT count(*) FROM push_subscriptions"); got != "0" {
		t.Fatalf("a refused request left %s rows", got)
	}
	if got := asOwner(t, "SELECT coalesce(time_zone, 'null') FROM learners WHERE github_id = 1001"); got != "null" {
		t.Fatalf("a refused request set the zone: %q", got)
	}
}

func TestPushIsCookieOnlyIntegration(t *testing.T) {
	pushFixture(t)
	tok := cliToken(t, newCLIClient(t), "ada")
	for _, path := range []string{"/v1/push/key", "/v1/push/subscriptions"} {
		if code, body, _ := getAs(t, tok, path); code != http.StatusForbidden || !strings.Contains(body, `"forbidden"`) {
			t.Errorf("GET %s with a bearer: %d %s", path, code, body)
		}
	}
	code, body, _, err := postItem(tok, apiURL(t), "/v1/push/subscriptions", pushBody(t, pushFakeOrigin+"/push/b", "Phone", "Europe/Madrid"))
	if err != nil || code != http.StatusForbidden || !strings.Contains(body, `"forbidden"`) {
		t.Errorf("POST with a bearer: %d %s %v", code, body, err)
	}
	req, _ := http.NewRequest(http.MethodDelete, apiURL(t)+"/v1/push/subscriptions/0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b", nil)
	req.Header.Set("Authorization", "Bearer "+tok)
	resp, err := newBrowser(t).c.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Errorf("DELETE with a bearer: %d", resp.StatusCode)
	}
	if got := asOwner(t, "SELECT count(*) FROM push_subscriptions"); got != "0" {
		t.Fatalf("a bearer left %s rows", got)
	}
}

func TestPushTableChecksIntegration(t *testing.T) {
	pushFixture(t)
	ins := func(endpoint, p, a, label string) string {
		return "INSERT INTO push_subscriptions (org_id, learner_id, endpoint, p256dh, auth, device_label) SELECT org_id, id, '" + endpoint +
			"', " + p + ", " + a + ", '" + label + "' FROM learners WHERE github_id = 1001"
	}
	A87, B22 := "repeat('A', 87)", "repeat('B', 22)"
	for name, sql := range map[string]string{
		"ftp endpoint":    ins("ftp://x.example/a", A87, B22, "d"),
		"short p256dh":    ins("https://x.example/a", "repeat('A', 86)", B22, "d"),
		"padded p256dh":   ins("https://x.example/a", "repeat('A', 86) || '='", B22, "d"),
		"short auth":      ins("https://x.example/a", A87, "repeat('B', 21)", "d"),
		"empty label":     ins("https://x.example/a", A87, B22, ""),
		"long label":      ins("https://x.example/a", A87, B22, strings.Repeat("d", 65)),
		"long endpoint":   ins("https://x.example/"+strings.Repeat("a", 1010), A87, B22, "d"),
		"push on weekly":  "INSERT INTO notification_prefs (org_id, learner_id, kind, push) SELECT org_id, id, 'weekly', true FROM learners WHERE github_id = 1001",
		"push on news":    "INSERT INTO notification_prefs (org_id, learner_id, kind, push) SELECT org_id, id, 'news', false FROM learners WHERE github_id = 1001",
		"email on check":  "INSERT INTO notification_prefs (org_id, learner_id, kind, email) SELECT org_id, id, 'check', true FROM learners WHERE github_id = 1001",
		"email on story":  "INSERT INTO notification_prefs (org_id, learner_id, kind, email) SELECT org_id, id, 'story', true FROM learners WHERE github_id = 1001",
		"email on exam":   "INSERT INTO notification_prefs (org_id, learner_id, kind, email) SELECT org_id, id, 'exam', true FROM learners WHERE github_id = 1001",
		"unknown kind":    "INSERT INTO notification_prefs (org_id, learner_id, kind, push) SELECT org_id, id, 'quiz', true FROM learners WHERE github_id = 1001",
		"empty time zone": "UPDATE learners SET time_zone = '' WHERE github_id = 1001",
		"long time zone":  "UPDATE learners SET time_zone = repeat('a', 65) WHERE github_id = 1001",
	} {
		out, err := psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"), sql)
		if err == nil || !strings.Contains(out, "violates check constraint") {
			t.Errorf("%s: %v %s", name, err, out)
		}
	}
	asOwner(t, ins("https://x.example/ok", A87, B22, "d"))
	asOwner(t, "INSERT INTO notification_prefs (org_id, learner_id, kind, push, email) SELECT org_id, id, 'role_passed', true, false FROM learners WHERE github_id = 1001")
	if out, err := psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"), ins("https://x.example/ok", A87, B22, "d")); err == nil || !strings.Contains(out, "duplicate key") {
		t.Errorf("duplicate endpoint: %v %s", err, out)
	}
}
