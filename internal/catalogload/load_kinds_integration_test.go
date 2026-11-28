//go:build integration

package catalogload_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const marksYAML = `
marks:
  - {slug: written-english, belt: white, title: Written English for tickets, summary: Write a reply}
  - {slug: feedback-round, belt: white, title: Feedback round with Carmen, summary: Take feedback}
  - {slug: tidy-shell, belt: yellow, title: Tidy shell, summary: Clean up}
`

const writingYAML = `id: support-l1/write-01
title: Tell Elena what happened
role: support-l1
order: 3
est_minutes: 10
tags: [writing]
prompt: Elena asked why the folder broke.
rubric:
  - {id: found, text: What you found}
  - {id: next, text: What she does now}
awards_mark: written-english
`

const feedbackYAML = `id: support-l1/feedback-01
title: Carmen's feedback
role: support-l1
order: 4
est_minutes: 10
from: {name: Carmen, role: Team lead}
scene: Your last reply was too short.
rubric:
  - {id: owns, text: Owns the miss}
  - {id: plan, text: Names the next step}
awards_mark: feedback-round
`

const quizYAML = `id: support-l1/quiz-01
title: Users and logs
role: support-l1
order: 5
est_minutes: 5
questions:
  - id: who-last
    text: Who logged in last night?
    choices: [{text: a, note: n}, {text: b, note: n}]
    answer: 1
    explain: last reads wtmp.
  - id: q2
    text: Second?
    choices: [{text: a, note: n}, {text: b, note: n}]
    answer: 0
  - id: q3
    text: Third?
    choices: [{text: a, note: n}, {text: b, note: n}]
    answer: 0
`

const storyYAML = `id: support-l1/quiz-01
title: Elena asks
role: support-l1
order: 5
est_minutes: 3
from: {name: Elena, role: Finance}
scene: The folder is broken again.
choices:
  - {id: fix, text: I will fix it now, verdict: good, reaction: She relaxes.}
  - {id: later, text: Later, verdict: poor, reaction: She waits.}
`

func practiceYAML(slug, extra string) string {
	return "id: practice/" + slug + "\ntitle: Practice " + slug + "\nest_minutes: 20\ntags: [storage]\n" + extra + "brief: Fix it.\nhints: [one]\n"
}

func put(t *testing.T, dir, rel, body string) {
	t.Helper()
	p := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// richFixture is the shared catalog plus four marks-bearing and kind items, two practice
// scenarios and three marks, each practice bundle unique to the test.
func richFixture(t *testing.T) string {
	t.Helper()
	dir := fixture(t)
	b, err := os.ReadFile(filepath.Join(dir, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, "catalog.yaml", string(b)+marksYAML)
	put(t, dir, "support-l1/write-01/writing.yaml", writingYAML)
	put(t, dir, "support-l1/feedback-01/feedback.yaml", feedbackYAML)
	put(t, dir, "support-l1/quiz-01/quiz.yaml", quizYAML)
	for slug, extra := range map[string]string{"disk-full": "difficulty: hard\n", "no-dns": ""} {
		put(t, dir, "practice/"+slug+"/ticket.yaml", practiceYAML(slug, extra))
		put(t, dir, "practice/"+slug+"/setup.sh", "#!/bin/sh\n# "+t.Name()+"\n")
		put(t, dir, "practice/"+slug+"/check.sh", "#!/bin/sh\n# "+t.Name()+"\n")
	}
	// Three marks need an award each: the yellow one rides on the writing item's sibling.
	put(t, dir, "support-l1/write-02/writing.yaml", strings.NewReplacer("write-01", "write-02", "order: 3", "order: 6", "written-english", "tidy-shell").Replace(writingYAML))
	return dir
}

func TestLoadEveryKind(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "catalog "+v1+" loaded: 4 tickets, 2 practice, 6 bundles uploaded, 0 reused")

	if got := psql(t, "SELECT string_agg(kind || ':' || count, ',' ORDER BY kind) FROM (SELECT kind, count(*) FROM catalog_items GROUP BY kind) x"); got != "feedback:1,practice:2,quiz:1,ticket:4,writing:2" {
		t.Fatalf("kinds: %s", got)
	}
	if got := psql(t, "SELECT string_agg(id || '|' || coalesce(role_id::text, 'null') || '|' || position || '|' || coalesce(difficulty, '-'), ',' ORDER BY position) FROM catalog_items WHERE kind = 'practice'"); !strings.HasPrefix(got, "practice/disk-full|null|1|hard,practice/no-dns|null|2|-") {
		t.Fatalf("practice: %s", got)
	}
	if got := psql(t, "SELECT count(*) FROM catalog_items WHERE kind = 'practice' AND bundle_key LIKE 'bundles/practice/%' AND brief IS NOT NULL AND hints IS NOT NULL AND payload IS NULL"); got != "2" {
		t.Fatalf("practice columns: %s", got)
	}
	if got := psql(t, "SELECT count(*) FROM catalog_items WHERE kind IN ('writing','quiz','feedback') AND brief IS NULL AND hints IS NULL AND bundle_key IS NULL AND difficulty IS NULL AND payload IS NOT NULL AND role_id IS NOT NULL"); got != "4" {
		t.Fatalf("item columns: %s", got)
	}
	if got := psql(t, "SELECT payload->>'prompt' || '|' || jsonb_array_length(payload->'rubric') || '|' || awards_mark FROM catalog_items WHERE id = 'support-l1/write-01'"); got != "Elena asked why the folder broke.|2|written-english" {
		t.Fatalf("writing: %s", got)
	}
	if got := psql(t, "SELECT jsonb_array_length(payload->'questions') || '|' || (payload->'questions'->0->>'answer') FROM catalog_items WHERE id = 'support-l1/quiz-01'"); got != "3|1" {
		t.Fatalf("quiz: %s", got)
	}
	if got := psql(t, "SELECT string_agg(slug || ':' || belt || ':' || position, ',' ORDER BY belt, position) FROM belt_marks"); got != "written-english:white:1,feedback-round:white:2,tidy-shell:yellow:1" {
		t.Fatalf("marks: %s", got)
	}

	mustLoad(t, dir, v2, "6 reused")
	if got := psql(t, "SELECT count(*) FROM catalog_items WHERE catalog_version = '"+v2+"'"); got != "10" {
		t.Fatalf("reload: %s", got)
	}
}

func TestKindChangeWithoutLearnerRowsIsAnUpsert(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "4 tickets")
	if err := os.Remove(filepath.Join(dir, "support-l1", "quiz-01", "quiz.yaml")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "support-l1/quiz-01/story.yaml", storyYAML)
	mustLoad(t, dir, v2, "4 tickets")
	if got := psql(t, "SELECT kind || '|' || (payload ? 'scene') || '|' || (payload ? 'questions') FROM catalog_items WHERE id = 'support-l1/quiz-01'"); got != "story|true|false" {
		t.Fatalf("after kind change: %s", got)
	}
}

func TestKindChangeRefusedWithLearnerRows(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "4 tickets")
	psql(t, seedLearner+"; INSERT INTO item_completions (org_id, learner_id, item_id) "+
		"SELECT org_id, id, 'support-l1/quiz-01' FROM learners WHERE github_id = 9101")
	if err := os.Remove(filepath.Join(dir, "support-l1", "quiz-01", "quiz.yaml")); err != nil {
		t.Fatal(err)
	}
	put(t, dir, "support-l1/quiz-01/story.yaml", storyYAML)
	out, err := load(t, dir, v2)
	if err == nil || !strings.Contains(out, "items with learner progress would change kind: support-l1/quiz-01") {
		t.Fatalf("want a refusal naming the item, got %v\n%s", err, out)
	}
	if got := psql(t, "SELECT kind || '|' || catalog_version FROM catalog_items WHERE id = 'support-l1/quiz-01'"); got != "quiz|"+v1 {
		t.Fatalf("after refusal: %s", got)
	}
}

func TestKindChangeRefusedWithAnswerRows(t *testing.T) {
	for name, insert := range map[string]string{
		"quiz_answers": "INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, last_choice, last_correct) " +
			"SELECT org_id, id, 'support-l1/quiz-01', 'q1', 0, true, 0, true FROM learners WHERE github_id = 9101",
		"story_choices": "INSERT INTO story_choices (org_id, learner_id, item_id, choice_id) " +
			"SELECT org_id, id, 'support-l1/quiz-01', 'c1' FROM learners WHERE github_id = 9101",
	} {
		t.Run(name, func(t *testing.T) {
			wipe(t)
			dir := richFixture(t)
			mustLoad(t, dir, v1, "4 tickets")
			psql(t, seedLearner+"; "+insert)
			if err := os.Remove(filepath.Join(dir, "support-l1", "quiz-01", "quiz.yaml")); err != nil {
				t.Fatal(err)
			}
			put(t, dir, "support-l1/quiz-01/story.yaml", storyYAML)
			out, err := load(t, dir, v2)
			if err == nil || !strings.Contains(out, "would change kind: support-l1/quiz-01") {
				t.Fatalf("want a refusal naming the item, got %v\n%s", err, out)
			}
		})
	}
}

func TestDropRefusedForCompletionOfAnyKind(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "4 tickets")
	psql(t, seedLearner+"; INSERT INTO item_completions (org_id, learner_id, item_id) "+
		"SELECT org_id, id, 'support-l1/feedback-01' FROM learners WHERE github_id = 9101")
	if err := os.RemoveAll(filepath.Join(dir, "support-l1", "feedback-01")); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(dir, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	put(t, dir, "catalog.yaml", strings.Replace(string(b), "  - {slug: feedback-round, belt: white, title: Feedback round with Carmen, summary: Take feedback}\n", "", 1))
	out, lerr := load(t, dir, v2)
	if lerr == nil || !strings.Contains(out, "items with learner progress would disappear: support-l1/feedback-01") {
		t.Fatalf("want a refusal naming the item, got %v\n%s", lerr, out)
	}
	if got := psql(t, "SELECT count(*) FROM belt_marks"); got != "3" {
		t.Fatalf("marks after refusal: %s", got)
	}

	// Without the row, the item and the mark only it awarded retire in the same load.
	psql(t, "DELETE FROM item_completions")
	mustLoad(t, dir, v2, "4 tickets")
	if got := psql(t, "SELECT (SELECT count(*) FROM catalog_items WHERE id = 'support-l1/feedback-01') || ',' || (SELECT count(*) FROM belt_marks)"); got != "0,2" {
		t.Fatalf("after retire: %s", got)
	}
}

func TestPracticeDropRefusedWhenStarted(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "4 tickets")
	psql(t, seedLearner+"; INSERT INTO ticket_progress (org_id, learner_id, item_id, state, first_started_at) "+
		"SELECT org_id, id, 'practice/no-dns', 'started', now() FROM learners WHERE github_id = 9101")
	if err := os.RemoveAll(filepath.Join(dir, "practice", "no-dns")); err != nil {
		t.Fatal(err)
	}
	out, err := load(t, dir, v2)
	if err == nil || !strings.Contains(out, "would disappear: practice/no-dns") {
		t.Fatalf("want a refusal, got %v\n%s", err, out)
	}
}

// Reordering marks swaps positions inside a belt and moves one to another belt in one load: the
// deferrable UNIQUE (belt, position) holds only at commit.
func TestMarkReorderAndBeltMove(t *testing.T) {
	wipe(t)
	dir := richFixture(t)
	mustLoad(t, dir, v1, "4 tickets")

	b, err := os.ReadFile(filepath.Join(dir, "catalog.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	head, _, _ := strings.Cut(string(b), "\nmarks:")
	const (
		we = "  - {slug: written-english, belt: white, title: Written English for tickets, summary: Write a reply}\n"
		fr = "  - {slug: feedback-round, belt: %s, title: Feedback round with Carmen, summary: Take feedback}\n"
		ts = "  - {slug: tidy-shell, belt: yellow, title: Tidy shell, summary: Clean up}\n"
	)
	// Swap: feedback-round before written-english in white, then move tidy-shell into white at the end.
	swapped := head + "\nmarks:\n" + strings.Replace(fr, "%s", "white", 1) + we + strings.Replace(ts, "yellow", "white", 1)
	put(t, dir, "catalog.yaml", swapped)
	mustLoad(t, dir, v2, "4 tickets")
	if got := psql(t, "SELECT string_agg(slug || ':' || belt || ':' || position, ',' ORDER BY belt, position) FROM belt_marks"); got != "feedback-round:white:1,written-english:white:2,tidy-shell:white:3" {
		t.Fatalf("after swap and move: %s", got)
	}

	// Back to the first order, tidy-shell in yellow again.
	put(t, dir, "catalog.yaml", head+"\nmarks:\n"+we+strings.Replace(fr, "%s", "white", 1)+ts)
	mustLoad(t, dir, v1, "4 tickets")
	if got := psql(t, "SELECT string_agg(slug || ':' || belt || ':' || position, ',' ORDER BY belt, position) FROM belt_marks"); got != "written-english:white:1,feedback-round:white:2,tidy-shell:yellow:1" {
		t.Fatalf("after restore: %s", got)
	}
}
