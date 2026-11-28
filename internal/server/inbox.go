package server

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/catalog"
	"github.com/kibanyadev/dojo/internal/server/repo"
)

const (
	inboxDefaultLimit = 20
	inboxMaxLimit     = 50
	// inboxCursorLayout is RFC 3339 in UTC with microseconds, the precision of a Postgres timestamptz.
	inboxCursorLayout = "2006-01-02T15:04:05.000000Z"
	// maxInboxCursor bounds the text before anything is decoded: a real cursor is 63 characters.
	maxInboxCursor = 128
)

// InboxStore reads and updates the learner's inbox (design 3.10, N1).
type InboxStore interface {
	ListInbox(ctx context.Context, learner pgtype.UUID, limit int, before *repo.InboxCursor) (repo.InboxPage, error)
	GetInboxItem(ctx context.Context, learner, id pgtype.UUID) (repo.InboxEntry, error)
	MarkInboxRead(ctx context.Context, learner, id pgtype.UUID) error
	MarkAllInboxRead(ctx context.Context, learner pgtype.UUID) error
}

// Inbox serves /v1/inbox (design 3.10).
type Inbox struct {
	Store InboxStore
}

// parseCanonicalUUID accepts only the 36-character hyphenated form: pgtype would also take other
// spellings, and an id that is not canonical is the same not found as an unknown one.
func parseCanonicalUUID(s string) (pgtype.UUID, bool) {
	if len(s) != 36 || s[8] != '-' || s[13] != '-' || s[18] != '-' || s[23] != '-' {
		return pgtype.UUID{}, false
	}
	var u pgtype.UUID
	b, err := hex.DecodeString(s[:8] + s[9:13] + s[14:18] + s[19:23] + s[24:])
	if err != nil || len(b) != 16 {
		return pgtype.UUID{}, false
	}
	copy(u.Bytes[:], b)
	u.Valid = true
	return u, true
}

// encodeInboxCursor is the opaque cursor: the unpadded base64url of "<updated_at>|<id>".
func encodeInboxCursor(at time.Time, id pgtype.UUID) string {
	return base64.RawURLEncoding.EncodeToString([]byte(at.UTC().Format(inboxCursorLayout) + "|" + uuidText(id)))
}

func uuidText(id pgtype.UUID) string {
	h := hex.EncodeToString(id.Bytes[:])
	return h[:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:]
}

// decodeInboxCursor never trusts its input: anything that is not exactly what encodeInboxCursor
// writes is refused, so the store only ever sees a real timestamp and a real id.
func decodeInboxCursor(s string) (repo.InboxCursor, bool) {
	if s == "" || len(s) > maxInboxCursor {
		return repo.InboxCursor{}, false
	}
	raw, err := base64.RawURLEncoding.DecodeString(s)
	if err != nil {
		return repo.InboxCursor{}, false
	}
	ts, idText, ok := strings.Cut(string(raw), "|")
	if !ok {
		return repo.InboxCursor{}, false
	}
	at, err := time.Parse(inboxCursorLayout, ts)
	if err != nil || at.Year() < 1970 || at.Format(inboxCursorLayout) != ts {
		return repo.InboxCursor{}, false
	}
	id, ok := parseCanonicalUUID(idText)
	if !ok || uuidText(id) != idText {
		return repo.InboxCursor{}, false
	}
	return repo.InboxCursor{At: at, ID: id}, true
}

// parseInboxLimit reads ?limit=: absent is 20, present must be 1 to 50 in plain digits.
func parseInboxLimit(v string, present bool) (int, bool) {
	if !present {
		return inboxDefaultLimit, true
	}
	if v == "" || len(v) > 2 {
		return 0, false
	}
	for _, c := range v {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 1 || n > inboxMaxLimit {
		return 0, false
	}
	return n, true
}

type inboxItemView struct {
	ID    string `json:"id"`
	Kind  string `json:"kind"`
	Ref   string `json:"ref"`
	Title string `json:"title"`
	Body  string `json:"body"`
	URL   string `json:"url"`
	Read  bool   `json:"read"`
	At    string `json:"at"`
}

func viewInboxRow(r repo.InboxRow) inboxItemView {
	return inboxItemView{ID: uuidText(r.ID), Kind: r.Kind, Ref: r.Ref, Title: r.Title, Body: r.Body, URL: r.URL,
		Read: r.Read, At: r.UpdatedAt.UTC().Format(time.RFC3339)}
}

type inboxList struct {
	Unread int64           `json:"unread"`
	Next   *string         `json:"next"`
	Items  []inboxItemView `json:"items"`
}

type inboxMarkDetail struct {
	Slug    string `json:"slug"`
	Title   string `json:"title"`
	Summary string `json:"summary"`
	Belt    string `json:"belt"`
	Held    bool   `json:"held"`
	Total   int64  `json:"total"`
}

type inboxBeltDetail struct {
	Belt     string  `json:"belt"`
	Title    string  `json:"title"`
	NextBelt *string `json:"next_belt"`
}

type inboxItemDetail struct {
	inboxItemView
	Detail any `json:"detail,omitempty"`
}

// inboxFailed answers a store failure; the route pattern is the only thing logged, never request values.
func inboxFailed(w http.ResponseWriter, r *http.Request, err error) {
	if r.Context().Err() != nil {
		return
	}
	slog.Error("inbox request failed", "route", r.Pattern, "error", err)
	writeError(w, http.StatusInternalServerError, "server_error")
}

// list answers GET /v1/inbox?limit=&before=.
func (in *Inbox) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	lv, hasLimit := q["limit"]
	bv, hasBefore := q["before"]
	if len(lv) > 1 || len(bv) > 1 {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	limit, ok := parseInboxLimit(q.Get("limit"), hasLimit)
	if !ok {
		writeError(w, http.StatusBadRequest, "bad_request")
		return
	}
	var before *repo.InboxCursor
	if hasBefore {
		c, ok := decodeInboxCursor(q.Get("before"))
		if !ok {
			writeError(w, http.StatusBadRequest, "bad_request")
			return
		}
		before = &c
	}
	p, _ := principalOf(r.Context())
	page, err := in.Store.ListInbox(r.Context(), p.learner, limit, before)
	if err != nil {
		inboxFailed(w, r, err)
		return
	}
	out := inboxList{Unread: page.Unread, Items: make([]inboxItemView, 0, len(page.Items))}
	for _, row := range page.Items {
		out.Items = append(out.Items, viewInboxRow(row))
	}
	if page.More && len(page.Items) > 0 {
		last := page.Items[len(page.Items)-1]
		next := encodeInboxCursor(last.UpdatedAt, last.ID)
		out.Next = &next
	}
	writeNoStore(w, out)
}

// get answers GET /v1/inbox/{id}: the item and, for a mark or a belt, what L7 and L8 draw.
func (in *Inbox) get(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCanonicalUUID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, _ := principalOf(r.Context())
	item, err := in.Store.GetInboxItem(r.Context(), p.learner, id)
	if errors.Is(err, repo.ErrInboxNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		inboxFailed(w, r, err)
		return
	}
	out := inboxItemDetail{inboxItemView: viewInboxRow(item.InboxRow)}
	switch item.Kind {
	case "mark":
		if m := item.Mark; m != nil {
			out.Detail = inboxMarkDetail{Slug: m.Slug, Title: m.Title, Summary: m.Summary, Belt: m.Belt, Held: m.Held, Total: m.Total}
		}
	case "belt":
		out.Detail = beltDetail(item.Ref)
	}
	writeNoStore(w, out)
}

// beltDetail is the fixed-order belt and the one after it; an unknown ref has no detail.
func beltDetail(belt string) any {
	for i, b := range catalog.Belts {
		if b != belt {
			continue
		}
		d := inboxBeltDetail{Belt: b, Title: strings.ToUpper(b[:1]) + b[1:] + " belt"}
		if i+1 < len(catalog.Belts) {
			next := catalog.Belts[i+1]
			d.NextBelt = &next
		}
		return d
	}
	return nil
}

// read answers POST /v1/inbox/{id}/read.
func (in *Inbox) read(w http.ResponseWriter, r *http.Request) {
	id, ok := parseCanonicalUUID(r.PathValue("id"))
	if !ok {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	p, _ := principalOf(r.Context())
	err := in.Store.MarkInboxRead(context.WithoutCancel(r.Context()), p.learner, id)
	if errors.Is(err, repo.ErrInboxNotFound) {
		writeError(w, http.StatusNotFound, "not_found")
		return
	}
	if err != nil {
		inboxFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// readAll answers POST /v1/inbox/read-all.
func (in *Inbox) readAll(w http.ResponseWriter, r *http.Request) {
	p, _ := principalOf(r.Context())
	if err := in.Store.MarkAllInboxRead(context.WithoutCancel(r.Context()), p.learner); err != nil {
		inboxFailed(w, r, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
