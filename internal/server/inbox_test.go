package server

import (
	"context"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

const inboxID = "0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"

func mustUUID(t *testing.T, s string) pgtype.UUID {
	t.Helper()
	id, ok := parseCanonicalUUID(s)
	if !ok {
		t.Fatalf("bad uuid %q", s)
	}
	return id
}

func TestParseCanonicalUUID(t *testing.T) {
	for in, want := range map[string]bool{
		inboxID:                                  true,
		strings.ToUpper(inboxID):                 true,
		"":                                       false,
		"0194a1b2c3d47e5f8a9b0c1d2e3f4a5b":       false, // no dashes
		"{" + inboxID + "}":                      false,
		"urn:uuid:" + inboxID:                    false,
		inboxID + "0":                            false,
		inboxID[:35]:                             false,
		"0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5g":   false,
		"0194a1b2-c3d4-7e5f-8a9b0-c1d2e3f4a5b":   false,
		"read-all":                               false,
		"0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b\n": false,
	} {
		if _, ok := parseCanonicalUUID(in); ok != want {
			t.Errorf("%q: got %v, want %v", in, ok, want)
		}
	}
}

func TestInboxCursorRoundTrip(t *testing.T) {
	at := time.Date(2026, 12, 2, 10, 42, 0, 123456000, time.UTC)
	id := mustUUID(t, inboxID)
	c := encodeInboxCursor(at, id)
	if strings.ContainsAny(c, "=+/") {
		t.Fatalf("cursor is not unpadded base64url: %q", c)
	}
	raw, err := base64.RawURLEncoding.DecodeString(c)
	if err != nil || string(raw) != "2026-12-02T10:42:00.123456Z|"+inboxID {
		t.Fatalf("cursor content: %q %v", raw, err)
	}
	got, ok := decodeInboxCursor(c)
	if !ok || !got.At.Equal(at) || got.ID != id {
		t.Fatalf("round trip: %+v %v", got, ok)
	}
}

func TestInboxCursorRejectsMalformed(t *testing.T) {
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }
	for name, in := range map[string]string{
		"empty":            "",
		"not base64":       "!!!",
		"padded":           base64.URLEncoding.EncodeToString([]byte("2026-12-02T10:42:00.123456Z|" + inboxID)),
		"std alphabet":     strings.NewReplacer("-", "+", "_", "/").Replace(enc("\xfb\xff|")),
		"no separator":     enc("2026-12-02T10:42:00.123456Z"),
		"two separators":   enc("2026-12-02T10:42:00.123456Z|" + inboxID + "|x"),
		"bad time":         enc("yesterday|" + inboxID),
		"seconds only":     enc("2026-12-02T10:42:00Z|" + inboxID),
		"offset time":      enc("2026-12-02T10:42:00.123456+01:00|" + inboxID),
		"nanoseconds":      enc("2026-12-02T10:42:00.123456789Z|" + inboxID),
		"bad uuid":         enc("2026-12-02T10:42:00.123456Z|nope"),
		"uuid no dashes":   enc("2026-12-02T10:42:00.123456Z|0194a1b2c3d47e5f8a9b0c1d2e3f4a5b"),
		"empty halves":     enc("|"),
		"huge":             strings.Repeat("A", 5000),
		"year 0":           enc("0000-01-01T00:00:00.000000Z|" + inboxID),
		"sql in the time":  enc("2026-12-02T10:42:00.123456Z' OR 1=1--|" + inboxID),
		"nul byte":         enc("2026-12-02T10:42:00.123456Z\x00|" + inboxID),
		"trailing newline": enc("2026-12-02T10:42:00.123456Z|" + inboxID + "\n"),
	} {
		if _, ok := decodeInboxCursor(in); ok {
			t.Errorf("%s: accepted %q", name, in)
		}
	}
}

func TestParseInboxLimit(t *testing.T) {
	for in, want := range map[string]struct {
		n  int
		ok bool
	}{
		"": {20, true}, "1": {1, true}, "50": {50, true}, "20": {20, true},
	} {
		if n, ok := parseInboxLimit(in, in != ""); n != want.n || ok != want.ok {
			t.Errorf("%q: got %d %v", in, n, ok)
		}
	}
	for _, in := range []string{"0", "51", "-1", "abc", "1.5", "", " 5", "+5", "99999999999999999999", "0x10"} {
		if _, ok := parseInboxLimit(in, true); ok {
			t.Errorf("limit %q accepted", in)
		}
	}
}

type fakeInbox struct {
	page      repo.InboxPage
	item      repo.InboxEntry
	err       error
	gotLimit  int
	gotBefore *repo.InboxCursor
	gotLearn  pgtype.UUID
	gotID     pgtype.UUID
	reads     int
	readAlls  int
}

func (f *fakeInbox) ListInbox(_ context.Context, l pgtype.UUID, limit int, b *repo.InboxCursor) (repo.InboxPage, error) {
	f.gotLearn, f.gotLimit, f.gotBefore = l, limit, b
	return f.page, f.err
}

func (f *fakeInbox) GetInboxItem(_ context.Context, l, id pgtype.UUID) (repo.InboxEntry, error) {
	f.gotLearn, f.gotID = l, id
	return f.item, f.err
}

func (f *fakeInbox) MarkInboxRead(_ context.Context, l, id pgtype.UUID) error {
	f.gotLearn, f.gotID = l, id
	f.reads++
	return f.err
}

func (f *fakeInbox) MarkAllInboxRead(_ context.Context, l pgtype.UUID) error {
	f.gotLearn = l
	f.readAlls++
	return f.err
}

func inboxCall(t *testing.T, f *fakeInbox, method, target string, anon bool, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	return inboxCallWith(t, f, NewRateLimits(1), method, target, anon, csrf)
}

func inboxCallWith(t *testing.T, f *fakeInbox, rl *RateLimits, method, target string, anon, csrf bool) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, target, nil)
	if !anon {
		req.AddCookie(&http.Cookie{Name: SessionCookie, Value: goodToken})
	}
	if csrf {
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set(CSRFHeader, "1")
	}
	rec := httptest.NewRecorder()
	auth := Auth{Store: &fakeSessions{row: activeRow(time.Now(), false)}, Inbox: &Inbox{Store: f}}
	New(fakeReady{}, auth, rl, nil, nil).ServeHTTP(rec, req)
	return rec
}

func sampleRow(t *testing.T) repo.InboxRow {
	return repo.InboxRow{ID: mustUUID(t, inboxID), Kind: "check", Ref: "support-l1/perms-02",
		Title: "Ticket passed · perms-02", Body: "Your fix survived the reboot.", URL: "/t/support-l1/perms-02",
		Read: false, UpdatedAt: time.Date(2026, 12, 2, 10, 42, 0, 123456000, time.UTC)}
}

func TestInboxListShape(t *testing.T) {
	f := &fakeInbox{page: repo.InboxPage{Unread: 2, Items: []repo.InboxRow{sampleRow(t)}}}
	rec := inboxCall(t, f, http.MethodGet, "/v1/inbox", false, false)
	got := decode(t, rec)
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
	it := got["items"].([]any)[0].(map[string]any)
	if got["unread"] != float64(2) || got["next"] != nil || it["id"] != inboxID || it["kind"] != "check" ||
		it["ref"] != "support-l1/perms-02" || it["body"] != "Your fix survived the reboot." ||
		it["url"] != "/t/support-l1/perms-02" || it["read"] != false || it["at"] != "2026-12-02T10:42:00Z" || len(it) != 8 {
		t.Fatalf("body: %s", rec.Body)
	}
	if f.gotLimit != 20 || f.gotBefore != nil || !f.gotLearn.Valid {
		t.Fatalf("defaults: limit %d before %v", f.gotLimit, f.gotBefore)
	}
}

func TestInboxEmptyListIsAnArray(t *testing.T) {
	rec := inboxCall(t, &fakeInbox{}, http.MethodGet, "/v1/inbox", false, false)
	if !strings.Contains(rec.Body.String(), `"items":[]`) || !strings.Contains(rec.Body.String(), `"unread":0`) {
		t.Fatalf("body: %s", rec.Body)
	}
}

func TestInboxNextIsTheLastRowsCursor(t *testing.T) {
	last := sampleRow(t)
	f := &fakeInbox{page: repo.InboxPage{Items: []repo.InboxRow{sampleRow(t), last}, More: true}}
	got := decode(t, inboxCall(t, f, http.MethodGet, "/v1/inbox?limit=2", false, false))
	next, _ := got["next"].(string)
	c, ok := decodeInboxCursor(next)
	if !ok || !c.At.Equal(last.UpdatedAt) || c.ID != last.ID || f.gotLimit != 2 {
		t.Fatalf("next %q limit %d", next, f.gotLimit)
	}
	// And the cursor goes back to the store as the position to start after.
	inboxCall(t, f, http.MethodGet, "/v1/inbox?before="+next, false, false)
	if f.gotBefore == nil || !f.gotBefore.At.Equal(last.UpdatedAt) || f.gotBefore.ID != last.ID {
		t.Fatalf("before: %+v", f.gotBefore)
	}
}

func TestInboxListBadQueryNeverReachesTheStore(t *testing.T) {
	for _, q := range []string{"limit=0", "limit=51", "limit=abc", "limit=", "limit=1&limit=2", "before=", "before=zzz",
		"before=" + base64.RawURLEncoding.EncodeToString([]byte("x|y")), "before=a&before=b"} {
		f := &fakeInbox{}
		rec := inboxCall(t, f, http.MethodGet, "/v1/inbox?"+q, false, false)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"bad_request"`) || f.gotLimit != 0 {
			t.Errorf("%s: %d %s (store called: %v)", q, rec.Code, rec.Body, f.gotLimit != 0)
		}
	}
}

func TestInboxNeedsASession(t *testing.T) {
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/v1/inbox"}, {http.MethodGet, "/v1/inbox/" + inboxID},
		{http.MethodPost, "/v1/inbox/" + inboxID + "/read"}, {http.MethodPost, "/v1/inbox/read-all"},
	} {
		rec := inboxCall(t, &fakeInbox{}, c.method, c.target, true, true)
		if rec.Code != 401 {
			t.Errorf("%s %s: %d", c.method, c.target, rec.Code)
		}
	}
}

func TestInboxItemKinds(t *testing.T) {
	row := sampleRow(t)
	check := &fakeInbox{item: repo.InboxEntry{InboxRow: row}}
	got := decode(t, inboxCall(t, check, http.MethodGet, "/v1/inbox/"+inboxID, false, false))
	if _, has := got["detail"]; has || got["id"] != inboxID || check.gotID != mustUUID(t, inboxID) {
		t.Fatalf("a check item has no detail: %v", got)
	}

	row.Kind, row.Ref = "mark", "written-english"
	mark := &fakeInbox{item: repo.InboxEntry{InboxRow: row, Mark: &repo.InboxMark{Slug: "written-english",
		Title: "Written English for tickets", Summary: "Write a reply", Belt: "white", Total: 4}}}
	d := decode(t, inboxCall(t, mark, http.MethodGet, "/v1/inbox/"+inboxID, false, false))["detail"].(map[string]any)
	if d["slug"] != "written-english" || d["title"] != "Written English for tickets" || d["summary"] != "Write a reply" ||
		d["belt"] != "white" || d["held"] != false || d["total"] != float64(4) || len(d) != 6 {
		t.Fatalf("mark detail: %v", d)
	}

	row.Kind, row.Ref = "mark", "gone"
	gone := &fakeInbox{item: repo.InboxEntry{InboxRow: row}}
	if g := decode(t, inboxCall(t, gone, http.MethodGet, "/v1/inbox/"+inboxID, false, false)); g["detail"] != nil {
		t.Fatalf("a mark missing from the catalog has a null detail: %v", g)
	}

	for ref, want := range map[string][2]any{"white": {"White belt", "yellow"}, "brown": {"Brown belt", "black"}, "black": {"Black belt", nil}} {
		row.Kind, row.Ref = "belt", ref
		b := decode(t, inboxCall(t, &fakeInbox{item: repo.InboxEntry{InboxRow: row}}, http.MethodGet, "/v1/inbox/"+inboxID, false, false))["detail"].(map[string]any)
		if b["belt"] != ref || b["title"] != want[0] || b["next_belt"] != want[1] || len(b) != 3 {
			t.Errorf("belt %s: %v", ref, b)
		}
	}
	row.Ref = "purple"
	if g := decode(t, inboxCall(t, &fakeInbox{item: repo.InboxEntry{InboxRow: row}}, http.MethodGet, "/v1/inbox/"+inboxID, false, false)); g["detail"] != nil {
		t.Fatalf("an unknown belt has a null detail: %v", g)
	}
}

func TestInboxBadIdsAreNotFoundWithoutTheStore(t *testing.T) {
	for _, id := range []string{"nope", "read-all", "0194a1b2c3d47e5f8a9b0c1d2e3f4a5b", "1", "%7B" + inboxID + "%7D", inboxID + "x"} {
		for _, c := range []struct{ method, suffix string }{{http.MethodGet, ""}, {http.MethodPost, "/read"}} {
			f := &fakeInbox{}
			rec := inboxCall(t, f, c.method, "/v1/inbox/"+id+c.suffix, false, true)
			if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) || f.gotID.Valid || f.reads != 0 {
				t.Errorf("%s %s: %d %s", c.method, id, rec.Code, rec.Body)
			}
		}
	}
}

func TestInboxUnknownItemIsNotFound(t *testing.T) {
	f := &fakeInbox{err: repo.ErrInboxNotFound}
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/v1/inbox/" + inboxID}, {http.MethodPost, "/v1/inbox/" + inboxID + "/read"},
	} {
		rec := inboxCall(t, f, c.method, c.target, false, true)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), `"not_found"`) {
			t.Errorf("%s: %d %s", c.target, rec.Code, rec.Body)
		}
	}
}

func TestInboxReadAnswers204(t *testing.T) {
	f := &fakeInbox{}
	rec := inboxCall(t, f, http.MethodPost, "/v1/inbox/"+inboxID+"/read", false, true)
	if rec.Code != 204 || rec.Body.Len() != 0 || f.reads != 1 || f.gotID != mustUUID(t, inboxID) || !f.gotLearn.Valid {
		t.Fatalf("read: %d %q reads=%d", rec.Code, rec.Body, f.reads)
	}
	rec = inboxCall(t, f, http.MethodPost, "/v1/inbox/read-all", false, true)
	if rec.Code != 204 || rec.Body.Len() != 0 || f.readAlls != 1 {
		t.Fatalf("read-all: %d %q", rec.Code, rec.Body)
	}
}

func TestInboxWritesNeedCSRF(t *testing.T) {
	for _, target := range []string{"/v1/inbox/" + inboxID + "/read", "/v1/inbox/read-all"} {
		f := &fakeInbox{}
		rec := inboxCall(t, f, http.MethodPost, target, false, false)
		if rec.Code != http.StatusForbidden || f.reads+f.readAlls != 0 {
			t.Errorf("%s without CSRF: %d", target, rec.Code)
		}
	}
}

func TestInboxStoreFailureIsAGenericServerError(t *testing.T) {
	secret := "boom-" + inboxID
	f := &fakeInbox{err: errors.New(secret)}
	for _, c := range []struct{ method, target string }{
		{http.MethodGet, "/v1/inbox"}, {http.MethodGet, "/v1/inbox/" + inboxID},
		{http.MethodPost, "/v1/inbox/" + inboxID + "/read"}, {http.MethodPost, "/v1/inbox/read-all"},
	} {
		rec := inboxCall(t, f, c.method, c.target, false, true)
		if rec.Code != 500 || strings.Contains(rec.Body.String(), "boom") || !strings.Contains(rec.Body.String(), `"server_error"`) {
			t.Errorf("%s: %d %s", c.target, rec.Code, rec.Body)
		}
	}
}

func TestInboxBudgets(t *testing.T) {
	// Reads share the read budget (burst 120): the list and the item draw from it, the writes do not.
	rl := testLimits(2)
	rl.Between = newMemLimiter(1, time.Minute, 100, time.Now)
	f := &fakeInbox{}
	if c := inboxCallWith(t, f, rl, http.MethodGet, "/v1/inbox", false, false).Code; c != 200 {
		t.Fatalf("first read: %d", c)
	}
	if c := inboxCallWith(t, f, rl, http.MethodGet, "/v1/inbox/"+inboxID, false, false).Code; c != 200 {
		t.Fatalf("second read: %d", c)
	}
	if rec := inboxCallWith(t, f, rl, http.MethodGet, "/v1/inbox", false, false); rec.Code != 429 || rec.Header().Get("Retry-After") == "" {
		t.Fatalf("third read: %d", rec.Code)
	}
	// Both writes draw from the between budget, which is burst 1 here.
	if c := inboxCallWith(t, f, rl, http.MethodPost, "/v1/inbox/read-all", false, true).Code; c != 204 {
		t.Fatalf("first write: %d", c)
	}
	if rec := inboxCallWith(t, f, rl, http.MethodPost, "/v1/inbox/"+inboxID+"/read", false, true); rec.Code != 429 || f.reads != 0 {
		t.Fatalf("second write: %d", rec.Code)
	}
}
