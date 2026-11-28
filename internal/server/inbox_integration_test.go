//go:build integration

package server_test

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"testing"
	"time"
)

type inboxPage struct {
	Unread int64
	Next   *string
	Items  []struct {
		ID, Kind, Ref, Title, Body, URL, At string
		Read                                bool
	}
}

// inboxFixture signs ada (github 1001) up, gives her a CLI token and inserts bob (1002) with no
// credential: the tests seed rows for both straight into the table.
func inboxFixture(t *testing.T) (tok, ada, bob string) {
	t.Helper()
	resetLearners(t, "1001", "1002")
	signUp(t, "ada")
	tok = cliToken(t, newCLIClient(t), "ada")
	ada = asOwner(t, "SELECT id FROM learners WHERE github_id = 1001")
	bob = strings.Fields(asOwner(t, "INSERT INTO learners (org_id, github_id, github_login, username, email, age_confirmed_at, terms_accepted_at, terms_version) "+
		"SELECT id, 1002, 'bob', 'bob', 'bob@example.test', now(), now(), '2026-11-20' FROM orgs WHERE slug = 'public' RETURNING id"))[0]
	return tok, ada, bob
}

// seedInboxRow inserts one unread item with the given updated_at and returns its id.
func seedInboxRow(t *testing.T, learner, kind, ref, updatedAt string) string {
	t.Helper()
	return strings.Fields(asOwner(t, "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, updated_at) "+
		"SELECT org_id, id, '"+kind+"', '"+ref+"', 'Title "+ref+"', 'Body "+ref+"', '/t/"+ref+"', '"+updatedAt+"' FROM learners WHERE id = '"+learner+"' RETURNING id"))[0]
}

func getInbox(t *testing.T, tok, query string) (int, inboxPage) {
	t.Helper()
	code, body, h := getAs(t, tok, "/v1/inbox"+query)
	var p inboxPage
	if code == 200 {
		if h.Get("Cache-Control") != "no-store" {
			t.Fatalf("cache: %q", h.Get("Cache-Control"))
		}
		if err := json.Unmarshal([]byte(body), &p); err != nil {
			t.Fatalf("decode %q: %v", body, err)
		}
	}
	return code, p
}

func postInbox(t *testing.T, tok, path string) int {
	t.Helper()
	code, _, _, err := postItem(tok, apiURL(t), path, "")
	if err != nil {
		t.Fatal(err)
	}
	return code
}

func (s *sseStream) noInboxEvent(d time.Duration) {
	s.t.Helper()
	end := time.After(d)
	for {
		select {
		case ev, ok := <-s.events:
			if ok && ev.name == "inbox" {
				s.t.Fatalf("unexpected inbox event %s", ev.data)
			}
		case <-end:
			return
		}
	}
}

func TestInboxPagingAcrossTiesIntegration(t *testing.T) {
	tok, ada, bob := inboxFixture(t)
	tie := "2026-12-02 10:42:00.123456+00"
	seedInboxRow(t, ada, "check", "newest", "2026-12-02 11:00:00+00")
	for _, ref := range []string{"tie-a", "tie-b", "tie-c", "tie-d"} {
		seedInboxRow(t, ada, "check", ref, tie)
	}
	seedInboxRow(t, ada, "quiz", "old-a", "2026-12-01 09:00:00+00")
	seedInboxRow(t, ada, "story", "old-b", "2026-12-01 09:00:00+00")
	seedInboxRow(t, bob, "check", "bobs", "2026-12-02 12:00:00+00")

	want := strings.Split(asOwner(t, "SELECT string_agg(ref, ',' ORDER BY updated_at DESC, id DESC) FROM inbox_items WHERE learner_id = '"+ada+"'"), ",")
	if len(want) != 7 || want[0] != "newest" {
		t.Fatalf("seed: %v", want)
	}
	for _, limit := range []int{1, 2, 3, 6, 7, 50} {
		var got []string
		seen := map[string]bool{}
		query := "?limit=" + strconv.Itoa(limit)
		for pages := 0; ; pages++ {
			if pages > 10 {
				t.Fatalf("limit %d: the walk does not end", limit)
			}
			code, p := getInbox(t, tok, query)
			if code != 200 || p.Unread != 7 {
				t.Fatalf("limit %d: %d unread %d", limit, code, p.Unread)
			}
			for _, it := range p.Items {
				if seen[it.ID] {
					t.Fatalf("limit %d: %s repeated", limit, it.Ref)
				}
				seen[it.ID] = true
				got = append(got, it.Ref)
			}
			if p.Next == nil {
				break
			}
			if len(p.Items) != limit {
				t.Fatalf("limit %d: a page with a next holds %d rows", limit, len(p.Items))
			}
			query = "?limit=" + strconv.Itoa(limit) + "&before=" + *p.Next
		}
		if strings.Join(got, ",") != strings.Join(want, ",") {
			t.Fatalf("limit %d: walk %v, want %v", limit, got, want)
		}
	}

	// Another learner's row never shows, and the first page carries the shape of the design.
	code, p := getInbox(t, tok, "")
	if code != 200 || len(p.Items) != 7 || p.Next != nil {
		t.Fatalf("default page: %d %+v", code, p)
	}
	first := p.Items[0]
	if first.Kind != "check" || first.Title != "Title newest" || first.Body != "Body newest" || first.URL != "/t/newest" ||
		first.Read || first.At != "2026-12-02T11:00:00Z" {
		t.Fatalf("first item: %+v", first)
	}
}

func TestInboxRefusalsIntegration(t *testing.T) {
	tok, ada, bob := inboxFixture(t)
	mine := seedInboxRow(t, ada, "check", "mine", "2026-12-02 10:00:00+00")
	theirs := seedInboxRow(t, bob, "check", "theirs", "2026-12-02 10:00:00+00")
	enc := func(s string) string { return base64.RawURLEncoding.EncodeToString([]byte(s)) }

	for name, q := range map[string]string{
		"limit 0":          "?limit=0",
		"limit 51":         "?limit=51",
		"limit word":       "?limit=many",
		"limit empty":      "?limit=",
		"two limits":       "?limit=1&limit=2",
		"empty cursor":     "?before=",
		"garbage cursor":   "?before=%00%ff",
		"not a cursor":     "?before=" + enc("hello"),
		"bad time":         "?before=" + enc("yesterday|"+mine),
		"bad id":           "?before=" + enc("2026-12-02T10:00:00.000000Z|nope"),
		"sql in cursor":    "?before=" + enc("2026-12-02T10:00:00.000000Z'; DROP TABLE inbox_items;--|"+mine),
		"overlong cursor":  "?before=" + strings.Repeat("A", 4000),
		"standard base64":  "?before=" + base64.StdEncoding.EncodeToString([]byte("2026-12-02T10:00:00.000000Z|"+mine)) + "%3D",
		"two cursors":      "?before=a&before=b",
		"cursor with nuls": "?before=" + enc("2026-12-02T10:00:00.000000Z\x00|"+mine),
	} {
		code, body, _ := getAs(t, tok, "/v1/inbox"+q)
		if code != 400 || !strings.Contains(body, `"bad_request"`) {
			t.Errorf("%s: %d %s", name, code, body)
		}
	}

	notFound := func(code int, what string) {
		t.Helper()
		if code != 404 {
			t.Errorf("%s: %d, want 404", what, code)
		}
	}
	for _, id := range []string{"nope", "read-all", strings.ReplaceAll(mine, "-", ""), "00000000-0000-0000-0000-000000000000", theirs} {
		code, _, _ := getAs(t, tok, "/v1/inbox/"+id)
		notFound(code, "GET "+id)
		notFound(postInbox(t, tok, "/v1/inbox/"+id+"/read"), "POST read "+id)
	}
	// Bob's row is untouched by ada's attempts; her own opens.
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE id = '"+theirs+"' AND read_at IS NULL"); got != "1" {
		t.Fatalf("a foreign read changed bob's row: %s", got)
	}
	if code, body, _ := getAs(t, tok, "/v1/inbox/"+mine); code != 200 || !strings.Contains(body, `"ref":"mine"`) || strings.Contains(body, "detail") {
		t.Fatalf("own item: %d %s", code, body)
	}
}

func TestInboxReadDoesNotReorderAndSendsOnChangeIntegration(t *testing.T) {
	tok, ada, _ := inboxFixture(t)
	ada1 := newBrowser(t)
	ada1.signIn("ada", "", "age=on&terms=on", false)
	s := openStream(t, apiURL(t), cookieOf(t, ada1))
	s.next("ready", 5*time.Second)

	a := seedInboxRow(t, ada, "check", "a", "2026-12-02 10:03:00+00")
	b := seedInboxRow(t, ada, "story", "b", "2026-12-02 10:02:00+00")
	seedInboxRow(t, ada, "quiz", "c", "2026-12-02 10:01:00+00")
	stamps := func() string {
		return asOwner(t, "SELECT string_agg(ref || '=' || updated_at::text, ';' ORDER BY ref) FROM inbox_items WHERE learner_id = '"+ada+"'")
	}
	before := stamps()

	if code := postInbox(t, tok, "/v1/inbox/"+b+"/read"); code != 204 {
		t.Fatalf("read: %d", code)
	}
	if ev := s.next("inbox", 5*time.Second); ev.data != "{}" {
		t.Fatalf("event data: %q", ev.data)
	}
	code, p := getInbox(t, tok, "")
	order := p.Items[0].Ref + p.Items[1].Ref + p.Items[2].Ref
	if code != 200 || order != "abc" || p.Unread != 2 || !p.Items[1].Read || p.Items[0].Read || p.Items[2].Read {
		t.Fatalf("after read: %d %s unread %d %+v", code, order, p.Unread, p.Items)
	}
	if stamps() != before {
		t.Fatalf("read changed updated_at: %s -> %s", before, stamps())
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE id = '"+b+"' AND read_at IS NOT NULL"); got != "1" {
		t.Fatalf("read_at not set: %s", got)
	}

	// Reading it again is a 204 that changes nothing and sends nothing.
	readAt := asOwner(t, "SELECT read_at::text FROM inbox_items WHERE id = '"+b+"'")
	if code := postInbox(t, tok, "/v1/inbox/"+b+"/read"); code != 204 {
		t.Fatalf("second read: %d", code)
	}
	s.noInboxEvent(500 * time.Millisecond)
	if got := asOwner(t, "SELECT read_at::text FROM inbox_items WHERE id = '"+b+"'"); got != readAt {
		t.Fatalf("a second read moved read_at: %s -> %s", readAt, got)
	}

	// Read all: the two unread go, the order and the stamps stay, one event.
	if code := postInbox(t, tok, "/v1/inbox/read-all"); code != 204 {
		t.Fatalf("read-all: %d", code)
	}
	s.next("inbox", 5*time.Second)
	code, p = getInbox(t, tok, "")
	if code != 200 || p.Unread != 0 || p.Items[0].ID != a || !p.Items[0].Read || !p.Items[1].Read || !p.Items[2].Read {
		t.Fatalf("after read-all: %d %+v", code, p)
	}
	if stamps() != before {
		t.Fatalf("read-all changed updated_at")
	}
	if got := asOwner(t, "SELECT read_at::text FROM inbox_items WHERE id = '"+b+"'"); got != readAt {
		t.Fatalf("read-all re-stamped an item already read: %s -> %s", readAt, got)
	}
	if code := postInbox(t, tok, "/v1/inbox/read-all"); code != 204 {
		t.Fatalf("empty read-all: %d", code)
	}
	s.noInboxEvent(500 * time.Millisecond)
}

func TestInboxReadAllStaysWithinTheLearnerIntegration(t *testing.T) {
	tok, ada, bob := inboxFixture(t)
	seedInboxRow(t, ada, "check", "a", "2026-12-02 10:00:00+00")
	seedInboxRow(t, bob, "check", "b1", "2026-12-02 10:00:00+00")
	seedInboxRow(t, bob, "quiz", "b2", "2026-12-02 10:00:00+00")
	if code := postInbox(t, tok, "/v1/inbox/read-all"); code != 204 {
		t.Fatalf("read-all: %d", code)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE learner_id = '"+bob+"' AND read_at IS NOT NULL"); got != "0" {
		t.Fatalf("ada's read-all read %s of bob's rows", got)
	}
	if got := asOwner(t, "SELECT count(*) FROM inbox_items WHERE learner_id = '"+ada+"' AND read_at IS NULL"); got != "0" {
		t.Fatalf("ada's own rows left unread: %s", got)
	}
}

func TestInboxDetailIntegration(t *testing.T) {
	tok, ada, _ := inboxFixture(t)
	freshCatalog(t)
	org := "(SELECT id FROM orgs WHERE slug = 'public')"
	asOwner(t, "INSERT INTO belt_marks (org_id, slug, belt, title, summary, position) VALUES "+
		"("+org+", 'written-english', 'white', 'Written English for tickets', 'Write a reply', 1), "+
		"("+org+", 'clear-handoff', 'white', 'A clear handoff', 'Hand over a shift', 2), "+
		"("+org+", 'first-yellow', 'yellow', 'Yellow one', 'Something', 1)")
	mark := seedInboxRow(t, ada, "mark", "written-english", "2026-12-02 10:00:00+00")
	belt := seedInboxRow(t, ada, "belt", "white", "2026-12-02 10:01:00+00")
	gone := seedInboxRow(t, ada, "mark", "removed-mark", "2026-12-02 10:02:00+00")

	get := func(id string) map[string]any {
		t.Helper()
		code, body, h := getAs(t, tok, "/v1/inbox/"+id)
		var m map[string]any
		if code != 200 || h.Get("Cache-Control") != "no-store" || json.Unmarshal([]byte(body), &m) != nil {
			t.Fatalf("GET %s: %d %s", id, code, body)
		}
		return m
	}
	d, _ := get(mark)["detail"].(map[string]any)
	if d["slug"] != "written-english" || d["title"] != "Written English for tickets" || d["summary"] != "Write a reply" ||
		d["belt"] != "white" || d["held"] != false || d["total"] != float64(2) {
		t.Fatalf("mark detail: %v", d)
	}
	d, _ = get(belt)["detail"].(map[string]any)
	if d["belt"] != "white" || d["title"] != "White belt" || d["next_belt"] != "yellow" {
		t.Fatalf("belt detail: %v", d)
	}
	if m := get(gone); m["detail"] != nil || m["ref"] != "removed-mark" {
		t.Fatalf("a mark no longer in the catalog: %v", m)
	}
}

func TestInboxTableRulesIntegration(t *testing.T) {
	_, ada, _ := inboxFixture(t)
	seedInboxRow(t, ada, "check", "dup", "2026-12-02 10:00:00+00")
	insert := func(kind, ref, title, body, url, extra string) string {
		cols, vals := "", ""
		if extra != "" {
			cols, vals = ", push_state", ", '"+extra+"'"
		}
		out, err := psqlAs(t, "dojo", envExample(t, "POSTGRES_PASSWORD"),
			"INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url"+cols+") SELECT org_id, id, '"+kind+"', '"+ref+"', '"+title+"', '"+body+"', '"+url+"'"+vals+" FROM learners WHERE id = '"+ada+"'")
		if err == nil {
			t.Errorf("accepted kind=%q ref=%q url=%q push=%q", kind, ref, url, extra)
		}
		return out
	}
	for name, out := range map[string]string{
		"kind exam":             insert("exam", "r", "t", "b", "/x", ""),
		"empty ref":             insert("check", "", "t", "b", "/x", ""),
		"long ref":              insert("check", strings.Repeat("r", 129), "t", "b", "/x", ""),
		"empty title":           insert("check", "r", "", "b", "/x", ""),
		"long title":            insert("check", "r", strings.Repeat("t", 201), "b", "/x", ""),
		"empty body":            insert("check", "r", "t", "", "/x", ""),
		"long body":             insert("check", "r", "t", strings.Repeat("b", 501), "/x", ""),
		"url absolute":          insert("check", "r", "t", "b", "https://example.test/x", ""),
		"url relative":          insert("check", "r", "t", "b", "t/x", ""),
		"url just slash":        insert("check", "r", "t", "b", "/", ""),
		"bad push state":        insert("check", "r", "t", "b", "/x", "later"),
		"url protocol-relative": insert("check", "r", "t", "b", "//evil.example", ""),
		"url backslash":         insert("check", "r", "t", "b", "/\\evil.example", ""),
		"url control char":      insert("check", "r", "t", "b", "/x\ty", ""),
	} {
		if !strings.Contains(out, "violates check constraint") {
			t.Errorf("%s: %s", name, out)
		}
	}
	if out := insert("check", "dup", "t", "b", "/x", ""); !strings.Contains(out, "duplicate key") {
		t.Errorf("a second (learner, kind, ref): %s", out)
	}
	// Same ref under another kind is a different item.
	asOwner(t, "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url) SELECT org_id, id, 'story', 'dup', 't', 'b', '/x' FROM learners WHERE id = '"+ada+"'")
	for _, kind := range []string{"mark", "belt", "review_ready", "story", "quiz", "check"} {
		asOwner(t, "INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url) SELECT org_id, id, '"+kind+"', 'six', 't', 'b', '/x' FROM learners WHERE id = '"+ada+"'")
	}
	if got := asOwner(t, "SELECT push_state || push_tries FROM inbox_items WHERE ref = 'six' LIMIT 1"); got != "none0" {
		t.Fatalf("defaults: %q", got)
	}
}

func TestInboxNeedsASessionIntegration(t *testing.T) {
	inboxFixture(t)
	for _, target := range []string{"/v1/inbox", "/v1/inbox/0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b"} {
		if code, body, _ := getAs(t, "not-a-token", target); code != http.StatusUnauthorized {
			t.Errorf("GET %s: %d %s", target, code, body)
		}
	}
	for _, target := range []string{"/v1/inbox/0194a1b2-c3d4-7e5f-8a9b-0c1d2e3f4a5b/read", "/v1/inbox/read-all"} {
		if code := postInbox(t, "not-a-token", target); code != http.StatusUnauthorized {
			t.Errorf("POST %s: %d", target, code)
		}
	}
}
