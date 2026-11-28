package server

import (
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
	"github.com/kibanyadev/dojo/internal/webpush"
)

type fakePush struct {
	subIn    repo.SubscribePushIn
	subs     int
	subID    pgtype.UUID
	rows     []repo.PushSubscriptionRow
	err      error
	delLearn pgtype.UUID
	delID    pgtype.UUID
}

func (f *fakePush) SubscribePush(_ context.Context, in repo.SubscribePushIn) (pgtype.UUID, error) {
	f.subIn = in
	f.subs++
	return f.subID, f.err
}

func (f *fakePush) ListPushSubscriptions(context.Context, pgtype.UUID) ([]repo.PushSubscriptionRow, error) {
	return f.rows, f.err
}

func (f *fakePush) DeletePushSubscription(_ context.Context, l, id pgtype.UUID) error {
	f.delLearn, f.delID = l, id
	return f.err
}

func pushPolicy(t *testing.T) webpush.Policy {
	t.Helper()
	p, err := webpush.NewPolicy(webpush.DefaultHosts, []string{"http://fake-push:8080"})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func pushAuth(t *testing.T, f *fakePush) Auth {
	t.Helper()
	v, err := webpush.ParseVAPID(webpush.ExampleKey)
	if err != nil {
		t.Fatal(err)
	}
	return Auth{Store: &fakeSessions{row: activeRow(time.Now(), false), cli: activeCLI(time.Now(), false)},
		Push: &Push{Store: f, Policy: pushPolicy(t), VAPID: v}}
}

func pushCall(t *testing.T, a Auth, rl *RateLimits, method, target, body string, bearer bool) *httptest.ResponseRecorder {
	t.Helper()
	var rd *strings.Reader
	if body != "" {
		rd = strings.NewReader(body)
	} else {
		rd = strings.NewReader("")
	}
	req := httptest.NewRequest(method, target, rd)
	if bearer {
		req.Header.Set("Authorization", "Bearer "+goodCLIToken)
	} else {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	}
	if method != http.MethodGet {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	New(fakeReady{}, a, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

func goodKeys(t *testing.T) (p256dh, auth string) {
	t.Helper()
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), base64.RawURLEncoding.EncodeToString([]byte("0123456789abcdef"))
}

type subBody map[string]any

func goodSub(t *testing.T) subBody {
	p, a := goodKeys(t)
	return subBody{"endpoint": "https://fcm.googleapis.com/fcm/send/abc", "keys": map[string]string{"p256dh": p, "auth": a},
		"device_label": "Safari on iPhone", "time_zone": "Europe/Madrid"}
}

func (b subBody) String() string {
	out, _ := json.Marshal(b)
	return string(out)
}

func TestPushKey(t *testing.T) {
	rec := pushCall(t, pushAuth(t, &fakePush{}), NewRateLimits(1), http.MethodGet, pushKeyPath, "", false)
	v, _ := webpush.ParseVAPID(webpush.ExampleKey)
	want := base64.RawURLEncoding.EncodeToString(v.PublicKey())
	if rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" || strings.TrimSpace(rec.Body.String()) != `{"public_key":"`+want+`"}` {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Cache-Control"), rec.Body)
	}
	if len(want) != 87 {
		t.Fatalf("public key is %d characters, want 87 (65 bytes)", len(want))
	}
}

func TestPushRoutesAreCookieOnly(t *testing.T) {
	for _, c := range []struct{ method, target, body string }{
		{http.MethodGet, pushKeyPath, ""},
		{http.MethodGet, pushSubsPath, ""},
		{http.MethodPost, pushSubsPath, "{}"},
		{http.MethodDelete, pushSubsPath + "/" + inboxID, ""},
	} {
		f := &fakePush{}
		rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), c.method, c.target, c.body, true)
		if rec.Code != 403 || !strings.Contains(rec.Body.String(), `"forbidden"`) || f.subs != 0 {
			t.Errorf("%s %s with a bearer: %d %s", c.method, c.target, rec.Code, rec.Body)
		}
	}
}

func TestPushRoutesNeedASession(t *testing.T) {
	a := pushAuth(t, &fakePush{})
	a.Store = &fakeSessions{}
	for _, c := range []struct{ method, target string }{{http.MethodGet, pushKeyPath}, {http.MethodGet, pushSubsPath}} {
		if rec := pushCall(t, a, NewRateLimits(1), c.method, c.target, "", false); rec.Code != 401 {
			t.Errorf("%s %s: %d", c.method, c.target, rec.Code)
		}
	}
	a.Store = &fakeSessions{row: activeRow(time.Now(), true)}
	if rec := pushCall(t, a, NewRateLimits(1), http.MethodGet, pushKeyPath, "", false); rec.Code != 403 || !strings.Contains(rec.Body.String(), "account_suspended") {
		t.Errorf("suspended: %d %s", rec.Code, rec.Body)
	}
}

func TestPushSubscribe(t *testing.T) {
	f := &fakePush{subID: mustUUID(t, inboxID)}
	b := goodSub(t)
	b["expirationTime"] = nil
	b["endpoint"] = "HTTPS://FCM.googleapis.com./fcm/send/abc"
	rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), http.MethodPost, pushSubsPath, b.String(), false)
	if rec.Code != 201 || strings.TrimSpace(rec.Body.String()) != `{"id":"`+inboxID+`"}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if f.subIn.Endpoint != "https://fcm.googleapis.com/fcm/send/abc" || f.subIn.DeviceLabel != "Safari on iPhone" ||
		f.subIn.TimeZone != "Europe/Madrid" || !f.subIn.Learner.Valid {
		t.Fatalf("stored: %+v", f.subIn)
	}
}

func TestPushSubscribeAcceptsTheFakePushOrigin(t *testing.T) {
	f := &fakePush{}
	b := goodSub(t)
	b["endpoint"] = "http://fake-push:8080/push/tok"
	if rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), http.MethodPost, pushSubsPath, b.String(), false); rec.Code != 201 {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestPushSubscribeRefusals(t *testing.T) {
	p, a := goodKeys(t)
	badPoint := base64.RawURLEncoding.EncodeToString(append([]byte{4}, make([]byte, 64)...))
	mut := func(f func(subBody)) string {
		b := goodSub(t)
		f(b)
		return b.String()
	}
	keys := func(p256dh, auth string) map[string]string { return map[string]string{"p256dh": p256dh, "auth": auth} }
	tests := []struct {
		name, body, want string
	}{
		{"not json", "nope", "bad_request"},
		{"unknown field", mut(func(b subBody) { b["extra"] = 1 }), "bad_request"},
		{"no endpoint", mut(func(b subBody) { delete(b, "endpoint") }), "bad_request"},
		{"short p256dh", mut(func(b subBody) { b["keys"] = keys(p[:80], a) }), "bad_request"},
		{"padded p256dh", mut(func(b subBody) { b["keys"] = keys(p+"=", a) }), "bad_request"},
		{"p256dh off the curve", mut(func(b subBody) { b["keys"] = keys(badPoint, a) }), "bad_request"},
		{"standard alphabet p256dh", mut(func(b subBody) { b["keys"] = keys(strings.Repeat("+", 87), a) }), "bad_request"},
		{"short auth", mut(func(b subBody) { b["keys"] = keys(p, a[:10]) }), "bad_request"},
		{"no label", mut(func(b subBody) { b["device_label"] = "" }), "bad_request"},
		{"long label", mut(func(b subBody) { b["device_label"] = strings.Repeat("a", 65) }), "bad_request"},
		{"control in label", mut(func(b subBody) { b["device_label"] = "a\nb" }), "bad_request"},
		{"no zone", mut(func(b subBody) { delete(b, "time_zone") }), "bad_request"},
		{"Local zone", mut(func(b subBody) { b["time_zone"] = "Local" }), "bad_request"},
		{"unknown zone", mut(func(b subBody) { b["time_zone"] = "Mars/Olympus" }), "bad_request"},
		{"long zone", mut(func(b subBody) { b["time_zone"] = strings.Repeat("a", 65) }), "bad_request"},
		{"http endpoint", mut(func(b subBody) { b["endpoint"] = "http://fcm.googleapis.com/x" }), "endpoint_not_allowed"},
		{"other host", mut(func(b subBody) { b["endpoint"] = "https://evil.example/x" }), "endpoint_not_allowed"},
		{"ip literal", mut(func(b subBody) { b["endpoint"] = "https://127.0.0.1/x" }), "endpoint_not_allowed"},
		{"metadata address", mut(func(b subBody) { b["endpoint"] = "https://169.254.169.254/latest" }), "endpoint_not_allowed"},
		{"userinfo", mut(func(b subBody) { b["endpoint"] = "https://u:p@fcm.googleapis.com/x" }), "endpoint_not_allowed"},
		{"port", mut(func(b subBody) { b["endpoint"] = "https://fcm.googleapis.com:8443/x" }), "endpoint_not_allowed"},
		{"too long", mut(func(b subBody) { b["endpoint"] = "https://fcm.googleapis.com/" + strings.Repeat("a", 1024) }), "endpoint_not_allowed"},
		{"suffix without the dot", mut(func(b subBody) { b["endpoint"] = "https://evilnotify.windows.com/x" }), "endpoint_not_allowed"},
		{"body too big", `{"endpoint":"` + strings.Repeat("a", 5000) + `"}`, "bad_request"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := &fakePush{}
			rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), http.MethodPost, pushSubsPath, tt.body, false)
			if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"`+tt.want+`"`) || f.subs != 0 {
				t.Fatalf("got %d %s (stored %d)", rec.Code, rec.Body, f.subs)
			}
		})
	}
}

func TestPushSubscribeStoreErrors(t *testing.T) {
	for name, c := range map[string]struct {
		err  error
		want int
	}{
		"zone postgres refuses": {repo.ErrBadTimeZone, 400},
		"learner deleted":       {repo.ErrLearnerGone, 401},
		"database down":         {errors.New("boom https://secret.example/endpoint"), 500},
	} {
		rec := pushCall(t, pushAuth(t, &fakePush{err: c.err}), NewRateLimits(1), http.MethodPost, pushSubsPath, goodSub(t).String(), false)
		if rec.Code != c.want || strings.Contains(rec.Body.String(), "secret") {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
	}
}

func TestPushSubscribeNeedsJSON(t *testing.T) {
	a := pushAuth(t, &fakePush{})
	req := httptest.NewRequest(http.MethodPost, pushSubsPath, strings.NewReader(goodSub(t).String()))
	req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	rec := httptest.NewRecorder()
	New(fakeReady{}, a, NewRateLimits(1), nil, nil).ServeHTTP(rec, req)
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "csrf_rejected") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestPushList(t *testing.T) {
	seen := time.Date(2026, 12, 3, 9, 0, 0, 0, time.UTC)
	f := &fakePush{rows: []repo.PushSubscriptionRow{
		{ID: mustUUID(t, inboxID), DeviceLabel: "Safari on iPhone", CreatedAt: time.Date(2026, 12, 2, 10, 0, 0, 0, time.UTC), LastSuccessAt: &seen},
		{ID: mustUUID(t, "0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5c"), DeviceLabel: "Chrome on Mac", CreatedAt: time.Date(2026, 12, 1, 10, 0, 0, 0, time.UTC)},
	}}
	rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), http.MethodGet, pushSubsPath, "", false)
	var got struct {
		Subscriptions []map[string]any `json:"subscriptions"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 || rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	a, b := got.Subscriptions[0], got.Subscriptions[1]
	if a["id"] != inboxID || a["device_label"] != "Safari on iPhone" || a["created_at"] != "2026-12-02T10:00:00Z" ||
		a["last_success_at"] != "2026-12-03T09:00:00Z" || len(a) != 4 || b["last_success_at"] != nil {
		t.Fatalf("body: %s", rec.Body)
	}
	empty := pushCall(t, pushAuth(t, &fakePush{}), NewRateLimits(1), http.MethodGet, pushSubsPath, "", false)
	if !strings.Contains(empty.Body.String(), `"subscriptions":[]`) {
		t.Fatalf("empty list: %s", empty.Body)
	}
}

func TestPushDelete(t *testing.T) {
	for name, c := range map[string]struct {
		id   string
		err  error
		want int
	}{
		"deleted":         {inboxID, nil, 204},
		"unknown/foreign": {inboxID, repo.ErrPushNotFound, 404},
		"not a uuid":      {"nope", nil, 404},
		"no dashes":       {strings.ReplaceAll(inboxID, "-", ""), nil, 404},
		"store down":      {inboxID, errors.New("boom"), 500},
	} {
		f := &fakePush{err: c.err}
		rec := pushCall(t, pushAuth(t, f), NewRateLimits(1), http.MethodDelete, pushSubsPath+"/"+c.id, "", false)
		if rec.Code != c.want {
			t.Errorf("%s: %d %s", name, rec.Code, rec.Body)
		}
		if c.want == 204 && (f.delID != mustUUID(t, inboxID) || !f.delLearn.Valid) {
			t.Errorf("%s: deleted %v for %v", name, f.delID, f.delLearn)
		}
	}
}

func TestPushBudgets(t *testing.T) {
	f := &fakePush{}
	a := pushAuth(t, f)
	t.Run("reads", func(t *testing.T) {
		rl := NewRateLimits(1)
		rl.Read = newMemLimiter(2, time.Minute, 100, time.Now)
		var codes []int
		for _, target := range []string{pushKeyPath, pushSubsPath, pushKeyPath} {
			codes = append(codes, pushCall(t, a, rl, http.MethodGet, target, "", false).Code)
		}
		if codes[0] != 200 || codes[1] != 200 || codes[2] != 429 {
			t.Fatalf("codes %v", codes)
		}
	})
	t.Run("writes", func(t *testing.T) {
		rl := NewRateLimits(1)
		rl.Write = newMemLimiter(2, time.Minute, 100, time.Now)
		var codes []int
		for i := 0; i < 3; i++ {
			codes = append(codes, pushCall(t, a, rl, http.MethodDelete, pushSubsPath+"/"+inboxID, "", false).Code)
		}
		if codes[0] != 204 || codes[1] != 204 || codes[2] != 429 {
			t.Fatalf("codes %v", codes)
		}
		if pushCall(t, a, rl, http.MethodPost, pushSubsPath, goodSub(t).String(), false).Code != 429 {
			t.Fatal("POST shares the write budget")
		}
	})
}

func TestPushRoutesAreLeftOutWithoutAStore(t *testing.T) {
	a := pushAuth(t, nil)
	a.Push = nil
	if rec := pushCall(t, a, NewRateLimits(1), http.MethodGet, pushKeyPath, "", false); rec.Code != 404 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestParsePush(t *testing.T) {
	env := func(m map[string]string) func(string) string { return func(k string) string { return m[k] } }
	real := "AQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQEBAQE"
	tests := []struct {
		name string
		env  map[string]string
		ok   bool
	}{
		{"local with the example key", map[string]string{"DOJO_ENV": "local", "DOJO_VAPID_PRIVATE_KEY": webpush.ExampleKey, "DOJO_PUSH_ALLOWED_ORIGINS": "http://fake-push:8080"}, true},
		{"local unset", map[string]string{"DOJO_ENV": "local"}, true},
		{"pro real key", map[string]string{"DOJO_ENV": "pro", "DOJO_VAPID_PRIVATE_KEY": real}, true},
		{"pre missing key", map[string]string{"DOJO_ENV": "pre"}, false},
		{"pro example key", map[string]string{"DOJO_ENV": "pro", "DOJO_VAPID_PRIVATE_KEY": webpush.ExampleKey}, false},
		{"pre origins set", map[string]string{"DOJO_ENV": "pre", "DOJO_VAPID_PRIVATE_KEY": real, "DOJO_PUSH_ALLOWED_ORIGINS": "http://fake-push:8080"}, false},
		{"bad key", map[string]string{"DOJO_ENV": "pro", "DOJO_VAPID_PRIVATE_KEY": "short"}, false},
		{"origin that is not local", map[string]string{"DOJO_ENV": "local", "DOJO_PUSH_ALLOWED_ORIGINS": "http://evil.example"}, false},
		{"custom hosts", map[string]string{"DOJO_ENV": "pro", "DOJO_VAPID_PRIVATE_KEY": real, "DOJO_PUSH_ALLOWED_HOSTS": "push.example, *.cdn.example"}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := ParsePush(env(tt.env))
			if (err == nil) != tt.ok {
				t.Fatalf("err = %v, want ok = %v", err, tt.ok)
			}
			if err != nil && strings.Contains(err.Error(), real) {
				t.Fatal("the error echoes the key")
			}
			if err == nil && tt.name == "custom hosts" {
				if cfg.Policy.Check("https://push.example/x") != nil || cfg.Policy.Check("https://a.cdn.example/x") != nil || cfg.Policy.Check("https://fcm.googleapis.com/x") == nil {
					t.Fatal("custom hosts replace the defaults")
				}
			}
		})
	}
}

func TestEnvExampleHoldsTheExampleKey(t *testing.T) {
	b, err := os.ReadFile("../../.env.example")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), "\nDOJO_VAPID_PRIVATE_KEY="+webpush.ExampleKey+"\n") {
		t.Fatal(".env.example does not carry webpush.ExampleKey")
	}
}
