package repo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

// ErrInboxNotFound means no inbox item of that learner has that id.
var ErrInboxNotFound = errors.New("inbox item not found")

// InboxRow is one inbox item as GET /v1/inbox lists it.
type InboxRow struct {
	ID        pgtype.UUID
	Kind      string
	Ref       string
	Title     string
	Body      string
	URL       string
	Read      bool
	UpdatedAt time.Time
}

// InboxCursor is the position after which the next page starts: the last row's (updated_at, id).
type InboxCursor struct {
	At time.Time
	ID pgtype.UUID
}

// InboxPage is one page of the list and the learner's unread count.
type InboxPage struct {
	Unread int64
	Items  []InboxRow
	// More is true when a row exists after the last one in Items.
	More bool
}

// InboxMark is the detail of a mark item: the mark, whether the learner holds it, and the size of its belt.
type InboxMark struct {
	Slug    string
	Title   string
	Summary string
	Belt    string
	Held    bool
	Total   int64
}

// InboxEntry is one item and, for a mark, its detail (nil when the mark is no longer in the catalog).
type InboxEntry struct {
	InboxRow
	Mark *InboxMark
}

// ListInbox returns up to limit rows after the cursor (the first page when nil) in (updated_at, id)
// descending order, so rows with the same updated_at never repeat or skip across pages.
func (s *Store) ListInbox(ctx context.Context, learner pgtype.UUID, limit int, before *InboxCursor) (InboxPage, error) {
	var page InboxPage
	n := int32(limit) + 1 //nolint:gosec // limit is 1 to 50, checked by the handler
	if before == nil {
		rows, err := s.q.ListInboxFirst(ctx, ListInboxFirstParams{LearnerID: learner, RowLimit: n})
		if err != nil {
			return page, fmt.Errorf("list inbox: %w", err)
		}
		for _, r := range rows {
			page.Items = append(page.Items, InboxRow{r.ID, r.Kind, r.Ref, r.Title, r.Body, r.Url, r.ReadAt.Valid, r.UpdatedAt.Time})
		}
	} else {
		rows, err := s.q.ListInboxAfter(ctx, ListInboxAfterParams{LearnerID: learner, RowLimit: n,
			BeforeAt: pgtype.Timestamptz{Time: before.At, Valid: true}, BeforeID: before.ID})
		if err != nil {
			return page, fmt.Errorf("list inbox: %w", err)
		}
		for _, r := range rows {
			page.Items = append(page.Items, InboxRow{r.ID, r.Kind, r.Ref, r.Title, r.Body, r.Url, r.ReadAt.Valid, r.UpdatedAt.Time})
		}
	}
	if len(page.Items) > limit {
		page.Items, page.More = page.Items[:limit], true
	}
	unread, err := s.q.CountInboxUnread(ctx, learner)
	if err != nil {
		return page, fmt.Errorf("count unread: %w", err)
	}
	page.Unread = unread
	return page, nil
}

// GetInboxItem returns one of the learner's items; another learner's id is ErrInboxNotFound.
func (s *Store) GetInboxItem(ctx context.Context, learner, id pgtype.UUID) (InboxEntry, error) {
	r, err := s.q.GetInboxItem(ctx, GetInboxItemParams{ID: id, LearnerID: learner})
	if errors.Is(err, pgx.ErrNoRows) {
		return InboxEntry{}, ErrInboxNotFound
	}
	if err != nil {
		return InboxEntry{}, fmt.Errorf("get inbox item: %w", err)
	}
	out := InboxEntry{InboxRow: InboxRow{r.ID, r.Kind, r.Ref, r.Title, r.Body, r.Url, r.ReadAt.Valid, r.UpdatedAt.Time}}
	if r.Kind == "mark" {
		m, err := s.q.GetInboxMarkDetail(ctx, GetInboxMarkDetailParams{LearnerID: learner, Slug: r.Ref})
		if err != nil && !errors.Is(err, pgx.ErrNoRows) {
			return InboxEntry{}, fmt.Errorf("get mark detail: %w", err)
		}
		if err == nil {
			out.Mark = &InboxMark{Slug: m.Slug, Title: m.Title, Summary: m.Summary, Belt: m.Belt, Held: m.Held, Total: m.Total}
		}
	}
	return out, nil
}

// notifyInbox sends `event: inbox` inside tx, so it reaches the stream only at commit. It carries no
// count: the page refetches the unread count (design 3.10).
func notifyInbox(ctx context.Context, tx pgx.Tx, learner pgtype.UUID) error {
	msg, _ := json.Marshal(map[string]string{"event": "inbox"})
	return notifyLearner(ctx, tx, learner, msg)
}

// MarkInboxRead sets read_at on one item, never updated_at. Reading an item already read is a no-op that
// sends nothing; an unknown or foreign id is ErrInboxNotFound.
func (s *Store) MarkInboxRead(ctx context.Context, learner, id pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	res, err := s.q.WithTx(tx).MarkInboxRead(ctx, MarkInboxReadParams{ID: id, LearnerID: learner})
	if err != nil {
		return fmt.Errorf("mark read: %w", err)
	}
	if !res.Found {
		return ErrInboxNotFound
	}
	if res.Changed > 0 {
		if err := notifyInbox(ctx, tx, learner); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}

// MarkAllInboxRead sets read_at on every unread item of the learner and sends `event: inbox` when it
// changed any.
func (s *Store) MarkAllInboxRead(ctx context.Context, learner pgtype.UUID) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()
	n, err := s.q.WithTx(tx).MarkAllInboxRead(ctx, learner)
	if err != nil {
		return fmt.Errorf("mark all read: %w", err)
	}
	if n > 0 {
		if err := notifyInbox(ctx, tx, learner); err != nil {
			return fmt.Errorf("notify: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit: %w", err)
	}
	return nil
}
