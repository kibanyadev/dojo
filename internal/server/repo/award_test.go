package repo

import (
	"context"
	"errors"
	"testing"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
)

func TestAwardTexts(t *testing.T) {
	title, body := markText("Written English for tickets", "Write a reply")
	if title != "Belt mark: Written English for tickets" || body != "Write a reply" {
		t.Errorf("mark: %q %q", title, body)
	}
	tests := []struct {
		belt        string
		total       int64
		title, body string
	}{
		{"white", 4, "Four marks. Yellow belt tied on.", "White belt, 4 of 4 marks"},
		{"white", 1, "One mark. Yellow belt tied on.", "White belt, 1 of 1 mark"},
		{"brown", 12, "12 marks. Black belt tied on.", "Brown belt, 12 of 12 marks"},
		{"black", 3, "Three marks. Black belt complete.", "Black belt, 3 of 3 marks"},
	}
	for _, tt := range tests {
		if g, b := beltText(tt.belt, tt.total); g != tt.title || b != tt.body {
			t.Errorf("belt %s/%d: %q %q", tt.belt, tt.total, g, b)
		}
	}
}

type fakeAward struct {
	mark      GetMarkForAwardRow
	inserted  int64
	held      CountBeltHeldRow
	inboxNone map[string]bool // kind with a conflicting row
	items     []InsertCelebrateItemParams
	urlErr    error
	urlSet    []SetCelebrateURLParams
}

func (f *fakeAward) GetMarkForAward(context.Context, string) (GetMarkForAwardRow, error) {
	return f.mark, nil
}

func (f *fakeAward) InsertBeltAward(context.Context, InsertBeltAwardParams) (int64, error) {
	return f.inserted, nil
}

func (f *fakeAward) CountBeltHeld(context.Context, CountBeltHeldParams) (CountBeltHeldRow, error) {
	return f.held, nil
}

func (f *fakeAward) InsertCelebrateItem(_ context.Context, p InsertCelebrateItemParams) (pgtype.UUID, error) {
	f.items = append(f.items, p)
	if f.inboxNone[p.Kind] {
		return pgtype.UUID{}, errNoRowsForTest
	}
	id := pgtype.UUID{Bytes: [16]byte{15: byte(len(f.items))}, Valid: true}
	return id, nil
}

func (f *fakeAward) SetCelebrateURL(_ context.Context, p SetCelebrateURLParams) error {
	f.urlSet = append(f.urlSet, p)
	return f.urlErr
}

var errNoRowsForTest = pgx.ErrNoRows

func newFakeAward() *fakeAward {
	return &fakeAward{
		mark:     GetMarkForAwardRow{ID: pgtype.UUID{Valid: true}, Belt: "white", Title: "T", Summary: "S"},
		inserted: 1, held: CountBeltHeldRow{Total: 4, Held: 2},
	}
}

func TestAwardMarkNewMarkAddsOneItem(t *testing.T) {
	f := newFakeAward()
	got, err := awardMark(context.Background(), f, pgtype.UUID{}, pgtype.UUID{}, "written-english", "r/w")
	if err != nil || len(got) != 1 || got[0].Kind != "mark" || got[0].Slug != "written-english" || !got[0].InboxID.Valid {
		t.Fatalf("got %+v %v", got, err)
	}
	if len(f.items) != 1 || f.items[0].Ref != "written-english" || f.items[0].Url != celebratePlaceholder || len(f.urlSet) != 1 {
		t.Fatalf("items %+v urls %+v", f.items, f.urlSet)
	}
}

func TestAwardMarkRepeatAwardsNothing(t *testing.T) {
	f := newFakeAward()
	f.inserted = 0
	got, err := awardMark(context.Background(), f, pgtype.UUID{}, pgtype.UUID{}, "written-english", "r/w")
	if err != nil || len(got) != 0 || len(f.items) != 0 {
		t.Fatalf("got %+v %v items %+v", got, err, f.items)
	}
}

func TestAwardMarkLastMarkAddsBeltItem(t *testing.T) {
	f := newFakeAward()
	f.held = CountBeltHeldRow{Total: 4, Held: 4}
	got, err := awardMark(context.Background(), f, pgtype.UUID{}, pgtype.UUID{}, "feedback-round", "r/f")
	if err != nil || len(got) != 2 || got[0].Kind != "mark" || got[1].Kind != "belt" || got[1].Slug != "white" {
		t.Fatalf("got %+v %v", got, err)
	}
	if len(f.urlSet) != 2 {
		t.Fatalf("every celebrate item gets its real url: %+v", f.urlSet)
	}
}

func TestAwardMarkBeltItemAlreadyThereIsNotListed(t *testing.T) {
	f := newFakeAward()
	f.held = CountBeltHeldRow{Total: 4, Held: 4}
	f.inboxNone = map[string]bool{"belt": true}
	got, err := awardMark(context.Background(), f, pgtype.UUID{}, pgtype.UUID{}, "feedback-round", "r/f")
	if err != nil || len(got) != 1 || got[0].Kind != "mark" {
		t.Fatalf("got %+v %v", got, err)
	}
}

// A failure between the placeholder insert and the url update must surface so the caller rolls back.
func TestAwardMarkUrlFailureIsAnError(t *testing.T) {
	f := newFakeAward()
	f.urlErr = errors.New("boom")
	got, err := awardMark(context.Background(), f, pgtype.UUID{}, pgtype.UUID{}, "written-english", "r/w")
	if err == nil || got != nil {
		t.Fatalf("want an error and no result, got %+v %v", got, err)
	}
}
