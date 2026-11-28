package repo

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/catalog"
)

// celebratePlaceholder is the url a mark or belt item is inserted with: the row's id cannot be inserted,
// so the real /celebrate/<id> is set in the same transaction (design 3.10).
const celebratePlaceholder = "/celebrate"

// Awarded is one inbox item a submit created: kind is mark or belt, slug the mark's slug or the belt.
type Awarded struct {
	Kind    string
	InboxID pgtype.UUID
	Slug    string
}

// awardQueries is what awardMark needs; *Queries bound to the submit transaction satisfies it.
type awardQueries interface {
	GetMarkForAward(ctx context.Context, slug string) (GetMarkForAwardRow, error)
	InsertBeltAward(ctx context.Context, arg InsertBeltAwardParams) (int64, error)
	CountBeltHeld(ctx context.Context, arg CountBeltHeldParams) (CountBeltHeldRow, error)
	InsertCelebrateItem(ctx context.Context, arg InsertCelebrateItemParams) (pgtype.UUID, error)
	SetCelebrateURL(ctx context.Context, arg SetCelebrateURLParams) error
}

// awardMark awards the mark once per learner and adds its inbox item; when that award completes the
// belt it adds the belt item too. A mark already held returns nothing. Any error means the caller must
// roll back: no item may commit with the placeholder url.
func awardMark(ctx context.Context, q awardQueries, org, learner pgtype.UUID, slug, itemID string) ([]Awarded, error) {
	m, err := q.GetMarkForAward(ctx, slug)
	if err != nil {
		return nil, fmt.Errorf("load mark: %w", err)
	}
	n, err := q.InsertBeltAward(ctx, InsertBeltAwardParams{OrgID: org, LearnerID: learner, MarkID: m.ID, SourceItemID: itemID})
	if err != nil {
		return nil, fmt.Errorf("insert award: %w", err)
	}
	if n == 0 {
		return nil, nil
	}
	var out []Awarded
	title, body := markText(m.Title, m.Summary)
	if a, ok, err := celebrate(ctx, q, org, learner, "mark", slug, title, body); err != nil {
		return nil, err
	} else if ok {
		out = append(out, a)
	}
	c, err := q.CountBeltHeld(ctx, CountBeltHeldParams{LearnerID: learner, Belt: m.Belt})
	if err != nil {
		return nil, fmt.Errorf("count belt: %w", err)
	}
	if c.Total > 0 && c.Held == c.Total {
		title, body := beltText(m.Belt, c.Total)
		if a, ok, err := celebrate(ctx, q, org, learner, "belt", m.Belt, title, body); err != nil {
			return nil, err
		} else if ok {
			out = append(out, a)
		}
	}
	return out, nil
}

// celebrate inserts one mark or belt item with the placeholder url and points it at its own id. ok is
// false when the learner already has that item.
func celebrate(ctx context.Context, q awardQueries, org, learner pgtype.UUID, kind, ref, title, body string) (Awarded, bool, error) {
	id, err := q.InsertCelebrateItem(ctx, InsertCelebrateItemParams{
		OrgID: org, LearnerID: learner, Kind: kind, Ref: ref, Title: title, Body: body, Url: celebratePlaceholder})
	if errors.Is(err, pgx.ErrNoRows) {
		return Awarded{}, false, nil
	}
	if err != nil {
		return Awarded{}, false, fmt.Errorf("insert %s item: %w", kind, err)
	}
	if err := q.SetCelebrateURL(ctx, SetCelebrateURLParams{ID: id, LearnerID: learner}); err != nil {
		return Awarded{}, false, fmt.Errorf("set %s url: %w", kind, err)
	}
	return Awarded{Kind: kind, InboxID: id, Slug: ref}, true, nil
}

// markText is the title and body of a mark item (design 3.10): the catalog's title and summary.
func markText(title, summary string) (string, string) {
	return "Belt mark: " + title, summary
}

var countWords = []string{"zero", "one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

func capital(s string) string { return strings.ToUpper(s[:1]) + s[1:] }

// beltText is the title and body of a belt item. total is the belt's mark count. The title names the
// next belt; the last belt has none and says it is complete.
func beltText(belt string, total int64) (string, string) {
	count := strconv.FormatInt(total, 10)
	if total >= 0 && int(total) < len(countWords) {
		count = capital(countWords[total])
	}
	noun := "marks"
	if total == 1 {
		noun = "mark"
	}
	tail := capital(belt) + " belt complete."
	if i := slices.Index(catalog.Belts, belt); i >= 0 && i+1 < len(catalog.Belts) {
		tail = capital(catalog.Belts[i+1]) + " belt tied on."
	}
	return count + " " + noun + ". " + tail, fmt.Sprintf("%s belt, %d of %d %s", capital(belt), total, total, noun)
}
