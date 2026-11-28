-- GET /v1/items/{id} (design 3.10): quiz, story, writing and feedback items.

-- name: GetItemView :one
SELECT i.id, r.slug AS role, i.kind, i.title, i.est_minutes, i.tags, i.payload,
       i.awards_mark AS awards_mark_slug, m.title AS awards_mark_title
FROM catalog_items i
JOIN roles r ON r.id = i.role_id
LEFT JOIN belt_marks m ON m.slug = i.awards_mark
WHERE i.id = $1 AND i.kind IN ('quiz', 'story', 'writing', 'feedback');

-- name: GetWrittenWork :one
SELECT draft, draft_saved_at, body, submitted_at, submit_count, review_state, review, reviewed_at
FROM written_work
WHERE learner_id = $1 AND item_id = $2;

-- PUT /v1/items/{id}/draft. The learner row is the source of org_id; no row back means the learner is gone.
-- name: UpsertDraft :one
INSERT INTO written_work (org_id, learner_id, item_id, draft, draft_saved_at)
SELECT l.org_id, l.id, @item_id::text, @draft::text, now() FROM learners l WHERE l.id = @learner_id
ON CONFLICT (learner_id, item_id) DO UPDATE
SET draft = EXCLUDED.draft, draft_saved_at = now(), updated_at = now()
RETURNING draft_saved_at;

-- name: GetSystemFlag :one
SELECT value FROM system_flags WHERE key = $1;

-- name: ItemIsDone :one
SELECT EXISTS (SELECT 1 FROM item_completions WHERE learner_id = $1 AND item_id = $2) AS done;

-- name: ListQuizAnswers :many
SELECT question_id, first_choice, first_correct, last_choice, last_correct
FROM quiz_answers
WHERE learner_id = $1 AND item_id = $2;

-- POST /v1/items/{id}/answers: the first answer writes first_*, every answer writes last_* (the grants
-- refuse an update of first_*). Run under the learner's class-1 lock.
-- name: UpsertQuizAnswer :exec
INSERT INTO quiz_answers (org_id, learner_id, item_id, question_id, first_choice, first_correct, last_choice, last_correct)
VALUES (@org_id, @learner_id, @item_id, @question_id, @choice, @correct, @choice, @correct)
ON CONFLICT (learner_id, item_id, question_id) DO UPDATE
SET updated_at = now(), last_choice = EXCLUDED.last_choice, last_correct = EXCLUDED.last_correct, last_at = now();

-- No row back means the item was already complete.
-- name: InsertItemCompletion :one
INSERT INTO item_completions (org_id, learner_id, item_id)
VALUES (@org_id, @learner_id, @item_id)
ON CONFLICT (learner_id, item_id) DO NOTHING
RETURNING item_id;

-- name: GetStoryChoice :one
SELECT choice_id, chosen_at FROM story_choices WHERE learner_id = $1 AND item_id = $2;

-- POST /v1/items/{id}/choice: the choice is final (no UPDATE grant). Run under the learner's class-1 lock.
-- name: InsertStoryChoice :one
INSERT INTO story_choices (org_id, learner_id, item_id, choice_id)
VALUES (@org_id, @learner_id, @item_id, @choice_id)
RETURNING chosen_at;
