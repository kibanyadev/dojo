-- Belt awards (design 3.10): written in the submit transaction, under the learner's class-1 lock.

-- name: GetMarkForAward :one
SELECT id, belt, title, summary FROM belt_marks WHERE slug = $1;

-- A new row means a new award; zero rows means the learner already held the mark.
-- name: InsertBeltAward :execrows
INSERT INTO belt_awards (org_id, learner_id, mark_id, source_item_id)
VALUES (@org_id, @learner_id, @mark_id, @source_item_id)
ON CONFLICT (learner_id, mark_id) DO NOTHING;

-- total is the marks the catalog lists for the belt, held the ones this learner holds.
-- name: CountBeltHeld :one
SELECT count(*) AS total, count(a.mark_id) AS held
FROM belt_marks m
LEFT JOIN belt_awards a ON a.mark_id = m.id AND a.learner_id = @learner_id
WHERE m.belt = @belt;

-- A mark or belt item. The url must name the row's own id, which cannot be inserted, so it starts as a
-- valid placeholder and SetCelebrateURL replaces it in the same transaction. No row back means the learner
-- already has the item.
-- name: InsertCelebrateItem :one
INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url)
VALUES (@org_id, @learner_id, @kind, @ref, @title, @body, @url)
ON CONFLICT (learner_id, kind, ref) DO NOTHING
RETURNING id;

-- name: SetCelebrateURL :exec
UPDATE inbox_items SET url = '/celebrate/' || id::text WHERE id = @id AND learner_id = @learner_id;

-- name: IsMarkHeld :one
SELECT EXISTS (
    SELECT 1 FROM belt_awards a JOIN belt_marks m ON m.id = a.mark_id
    WHERE a.learner_id = @learner_id AND m.slug = @slug
) AS held;

-- Every mark in belt order then place, with the time this learner earned it (null when not held).
-- name: ListLearnerBeltMarks :many
SELECT m.slug, m.belt, m.title, m.summary, a.awarded_at
FROM belt_marks m
LEFT JOIN belt_awards a ON a.mark_id = m.id AND a.learner_id = @learner_id
ORDER BY array_position(ARRAY['white', 'yellow', 'orange', 'green', 'blue', 'brown', 'black'], m.belt), m.position;
