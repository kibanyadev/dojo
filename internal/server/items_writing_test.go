package server

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/kibanyadev/dojo/internal/server/repo"
)

const writingPayload = `{"prompt":"Elena asked why the folder broke.","rubric":[{"id":"found","text":"What you found"},{"id":"next","text":"What she does now"}]}`

const feedbackPayload = `{"from":{"name":"Carmen","role":"Team lead"},"scene":"Your last reply was short.","rubric":[{"id":"ack","text":"Acknowledge it"},{"id":"plan","text":"Say what changes"}]}`

const storedReview = `{"criteria":[{"id":"found","text":"What you found","met":true,"evidence":"The team group owns it","note":""},{"id":"next","text":"What she does now","met":false,"evidence":"","note":"Say what she does."}],"next_step":"Add one line."}`

func writingFake() *fakeItems {
	return &fakeItems{view: repo.GetItemViewRow{ID: "support-l1/write-01", Role: "support-l1", Kind: "writing",
		Title: "Tell Elena what happened", EstMinutes: 10, Payload: []byte(writingPayload),
		AwardsMarkSlug: pgtype.Text{String: "written-english", Valid: true}, AwardsMarkTitle: pgtype.Text{String: "Written English for tickets", Valid: true}}}
}

func feedbackFake() *fakeItems {
	return &fakeItems{view: repo.GetItemViewRow{ID: "support-l1/feedback-01", Role: "support-l1", Kind: "feedback",
		Title: "Feedback round", EstMinutes: 8, Payload: []byte(feedbackPayload)}}
}

func ts(s string) pgtype.Timestamptz {
	t, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return pgtype.Timestamptz{Time: t, Valid: true}
}

func reviewedWork() *repo.GetWrittenWorkRow {
	return &repo.GetWrittenWorkRow{Draft: "Hi Elena, v2", DraftSavedAt: ts("2026-12-02T18:09:40Z"),
		Body: pgtype.Text{String: "Hi Elena, v1", Valid: true}, SubmittedAt: ts("2026-12-02T18:10:00Z"), SubmitCount: 1,
		ReviewState: pgtype.Text{String: "done", Valid: true}, Review: []byte(storedReview), ReviewedAt: ts("2026-12-02T18:10:06Z")}
}

func TestWritingFreshView(t *testing.T) {
	rec := itemCall(t, writingFake(), http.MethodGet, "/v1/items/support-l1/write-01", false)
	got := decode(t, rec)
	if got["kind"] != "writing" || got["prompt"] != "Elena asked why the folder broke." || got["work"] != nil || got["done"] != false ||
		got["tags"] == nil || len(got["rubric"].([]any)) != 2 {
		t.Fatalf("body: %s", rec.Body)
	}
	mark := got["awards_mark"].(map[string]any)
	if mark["slug"] != "written-english" || mark["title"] != "Written English for tickets" || mark["held"] != false {
		t.Fatalf("awards_mark: %v", mark)
	}
	if _, ok := got["from"]; ok {
		t.Fatalf("writing has no from: %s", rec.Body)
	}
	if rec.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("cache: %q", rec.Header().Get("Cache-Control"))
	}
}

func TestWritingWithoutMarkHasNullAwardsMark(t *testing.T) {
	f := writingFake()
	f.view.AwardsMarkSlug, f.view.AwardsMarkTitle = pgtype.Text{}, pgtype.Text{}
	if got := decode(t, itemCall(t, f, http.MethodGet, "/v1/items/support-l1/write-01", false)); got["awards_mark"] != nil {
		t.Fatalf("awards_mark: %v", got["awards_mark"])
	}
}

func TestWritingWorkShapes(t *testing.T) {
	tests := []struct {
		name  string
		work  *repo.GetWrittenWorkRow
		flag  string
		check func(t *testing.T, w map[string]any)
	}{
		{"draft only", &repo.GetWrittenWorkRow{Draft: "wip", DraftSavedAt: ts("2026-12-02T18:09:40Z")}, "", func(t *testing.T, w map[string]any) {
			if w["draft"] != "wip" || w["draft_saved_at"] != "2026-12-02T18:09:40Z" || w["body"] != nil || w["submitted_at"] != nil ||
				w["submit_count"] != float64(0) || w["review_state"] != nil || w["reviewed_at"] != nil || w["review"] != nil {
				t.Fatalf("work: %v", w)
			}
		}},
		{"reviewed", reviewedWork(), "", func(t *testing.T, w map[string]any) {
			rv := w["review"].(map[string]any)
			if w["review_state"] != "done" || w["body"] != "Hi Elena, v1" || w["submit_count"] != float64(1) ||
				w["reviewed_at"] != "2026-12-02T18:10:06Z" || w["submitted_at"] != "2026-12-02T18:10:00Z" ||
				rv["next_step"] != "Add one line." || len(rv["criteria"].([]any)) != 2 {
				t.Fatalf("work: %v", w)
			}
		}},
		{"pending", &repo.GetWrittenWorkRow{Draft: "x", DraftSavedAt: ts("2026-12-02T18:09:40Z"), SubmitCount: 2,
			ReviewState: pgtype.Text{String: "pending", Valid: true}, Review: []byte(storedReview)}, "", func(t *testing.T, w map[string]any) {
			// The last finished review stays visible while a new one is pending.
			if w["review_state"] != "pending" || w["review"] == nil {
				t.Fatalf("work: %v", w)
			}
		}},
		{"pending but paused", &repo.GetWrittenWorkRow{Draft: "x", DraftSavedAt: ts("2026-12-02T18:09:40Z"), SubmitCount: 1,
			ReviewState: pgtype.Text{String: "pending", Valid: true}}, "false", func(t *testing.T, w map[string]any) {
			if w["review_state"] != "paused" {
				t.Fatalf("work: %v", w)
			}
		}},
		{"done stays done while paused", reviewedWork(), "false", func(t *testing.T, w map[string]any) {
			if w["review_state"] != "done" {
				t.Fatalf("work: %v", w)
			}
		}},
		{"capped", &repo.GetWrittenWorkRow{Draft: "x", DraftSavedAt: ts("2026-12-02T18:09:40Z"), SubmitCount: 1,
			ReviewState: pgtype.Text{String: "capped", Valid: true}}, "false", func(t *testing.T, w map[string]any) {
			if w["review_state"] != "capped" || w["review"] != nil {
				t.Fatalf("work: %v", w)
			}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			f := writingFake()
			f.work, f.flag = tt.work, tt.flag
			rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/write-01", false)
			tt.check(t, decode(t, rec)["work"].(map[string]any))
		})
	}
}

func TestFeedbackView(t *testing.T) {
	f := feedbackFake()
	f.work = reviewedWork()
	f.done = true
	got := decode(t, itemCall(t, f, http.MethodGet, "/v1/items/support-l1/feedback-01", false))
	from := got["from"].(map[string]any)
	if got["kind"] != "feedback" || got["scene"] != "Your last reply was short." || from["name"] != "Carmen" || from["role"] != "Team lead" ||
		got["done"] != true || got["awards_mark"] != nil || got["prompt"] != nil || len(got["rubric"].([]any)) != 2 ||
		got["work"].(map[string]any)["review_state"] != "done" {
		t.Fatalf("body: %v", got)
	}
}

func TestWritingFlagReadOnlyWhenPending(t *testing.T) {
	f := writingFake()
	f.work = reviewedWork()
	f.flag = "not json"
	// A done review never reads the flag, so a corrupt flag cannot break the view.
	if rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/write-01", false); rec.Code != 200 {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	f.work = &repo.GetWrittenWorkRow{DraftSavedAt: ts("2026-12-02T18:09:40Z"), SubmitCount: 1, ReviewState: pgtype.Text{String: "pending", Valid: true}}
	if rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/write-01", false); rec.Code != 500 || strings.Contains(rec.Body.String(), "json") {
		t.Fatalf("corrupt flag while pending: %d %s", rec.Code, rec.Body)
	}
}

func TestWritingCorruptPayloadOrReviewIs500(t *testing.T) {
	f := writingFake()
	f.view.Payload = []byte(`{`)
	if rec := itemCall(t, f, http.MethodGet, "/v1/items/support-l1/write-01", false); rec.Code != 500 {
		t.Fatalf("payload: %d", rec.Code)
	}
}
