-- The inbox (design 3.10, N1). Every query is scoped by learner_id; read and read-all never write updated_at.

-- name: ListInboxFirst :many
SELECT id, kind, ref, title, body, url, read_at, updated_at
FROM inbox_items
WHERE learner_id = @learner_id
ORDER BY updated_at DESC, id DESC
LIMIT @row_limit;

-- A page is the rows strictly after the cursor in the (updated_at, id) descending order.
-- name: ListInboxAfter :many
SELECT id, kind, ref, title, body, url, read_at, updated_at
FROM inbox_items
WHERE learner_id = @learner_id AND (updated_at, id) < (@before_at::timestamptz, @before_id::uuid)
ORDER BY updated_at DESC, id DESC
LIMIT @row_limit;

-- name: CountInboxUnread :one
SELECT count(*) FROM inbox_items WHERE learner_id = $1 AND read_at IS NULL;

-- name: GetInboxItem :one
SELECT id, kind, ref, title, body, url, read_at, updated_at
FROM inbox_items
WHERE id = @id AND learner_id = @learner_id;

-- found tells an unknown or foreign id from one already read; changed is 1 when this call read it.
-- name: MarkInboxRead :one
WITH u AS (
    UPDATE inbox_items SET read_at = now()
    WHERE id = @id AND learner_id = @learner_id AND read_at IS NULL
    RETURNING id
)
SELECT EXISTS (SELECT 1 FROM inbox_items i WHERE i.id = @id AND i.learner_id = @learner_id) AS found,
       (SELECT count(*) FROM u) AS changed;

-- name: MarkAllInboxRead :execrows
UPDATE inbox_items SET read_at = now() WHERE learner_id = $1 AND read_at IS NULL;

-- The detail of a mark item (L7): its ref is the mark slug; held is whether this learner holds it.
-- name: GetInboxMarkDetail :one
SELECT m.slug, m.title, m.summary, m.belt,
       EXISTS (SELECT 1 FROM belt_awards a WHERE a.mark_id = m.id AND a.learner_id = @learner_id) AS held,
       (SELECT count(*) FROM belt_marks b WHERE b.belt = m.belt) AS total
FROM belt_marks m
WHERE m.slug = @slug;

-- A check item: one row per learner and ticket. Each newer check upserts it, unread again, and queues a push
-- (design 3.10); updated_at is written only here and by the review upsert.
-- name: UpsertInboxCheck :exec
INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, push_state, push_next_at)
VALUES (@org_id, @learner_id, 'check', @ref, @title, @body, @url, 'pending', @at)
ON CONFLICT (learner_id, kind, ref) DO UPDATE
SET title = EXCLUDED.title, body = EXCLUDED.body, url = EXCLUDED.url, read_at = NULL, updated_at = @at,
    push_state = 'pending', push_tries = 0, push_next_at = @at, pushed_at = NULL;
