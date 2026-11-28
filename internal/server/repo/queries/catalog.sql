-- name: GetCatalogVersion :one
SELECT catalog_version FROM catalog_state WHERE id = 1;

-- name: ListCatalogPaths :many
SELECT slug, title, status, position FROM paths ORDER BY position;

-- name: ListCatalogRoles :many
SELECT slug, title, summary, kind, has_exam, advanced FROM roles ORDER BY slug;

-- name: ListCatalogPathRoles :many
SELECT p.slug AS path, r.slug AS role, pr.position
FROM path_roles pr
JOIN paths p ON p.id = pr.path_id
JOIN roles r ON r.id = pr.role_id
ORDER BY p.position, pr.position;

-- Items that belong to a role; practice items have no role and are not part of a path.
-- name: ListCatalogItems :many
SELECT i.id, r.slug AS role, i.position, i.kind, i.title, i.est_minutes, i.tags
FROM catalog_items i
JOIN roles r ON r.id = i.role_id
ORDER BY r.slug, i.position;

-- GET /v1/practice (design 3.10): the practice items, by position.
-- name: ListPracticeItems :many
SELECT id, title, est_minutes, tags, difficulty
FROM catalog_items
WHERE kind = 'practice'
ORDER BY position;

-- Marks in belt order (design 3.10), then their place in the belt.
-- name: ListCatalogMarks :many
SELECT slug, belt, title, summary, position
FROM belt_marks
ORDER BY array_position(ARRAY['white', 'yellow', 'orange', 'green', 'blue', 'brown', 'black'], belt), position;

-- name: GetTicketBundle :one
SELECT id, catalog_version, bundle_key, bundle_sha256, bundle_bytes
FROM catalog_items WHERE id = $1 AND bundle_key IS NOT NULL;

-- name: ListLearnerTicketStates :many
SELECT item_id, state, lab_state, last_seen_at, commit_state FROM ticket_progress WHERE learner_id = $1;

-- GET /v1/activity (design 3.8): first passes only, bucketed by the day in the learner's zone.
-- name: ListLearnerActivityDays :many
SELECT (p.first_passed_at AT TIME ZONE @tz::text)::date AS day, count(*)::int AS passes
FROM ticket_progress p
JOIN catalog_items i ON i.id = p.item_id AND i.kind IN ('ticket', 'practice')
WHERE p.learner_id = @learner_id AND p.first_passed_at >= @since::timestamptz
GROUP BY 1
ORDER BY 1;

-- name: CountLearnerPasses :one
SELECT count(*)::int AS total
FROM ticket_progress p
JOIN catalog_items i ON i.id = p.item_id AND i.kind IN ('ticket', 'practice')
WHERE p.learner_id = $1 AND p.first_passed_at IS NOT NULL;

-- name: ListLearnerRecentPasses :many
SELECT p.item_id, i.title, p.first_passed_at, p.passed_commit, p.commit_state
FROM ticket_progress p
JOIN catalog_items i ON i.id = p.item_id AND i.kind IN ('ticket', 'practice')
WHERE p.learner_id = $1 AND p.first_passed_at IS NOT NULL
ORDER BY p.first_passed_at DESC, p.item_id
LIMIT 3;

-- Items the learner finished (design 3.10); done on Path and Today.
-- name: ListLearnerCompletedItems :many
SELECT item_id FROM item_completions WHERE learner_id = $1;
