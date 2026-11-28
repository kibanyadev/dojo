-- name: GetPathBySlug :one
SELECT id, slug, status FROM paths WHERE slug = $1;

-- The learner's chosen path when it still exists and is available; empty otherwise.
-- name: GetLearnerChosenPathSlug :one
SELECT coalesce(p.slug, '')::text AS slug
FROM learners l
LEFT JOIN paths p ON p.id = l.chosen_path_id AND p.status = 'available'
WHERE l.id = $1;

-- name: SetLearnerChosenPath :exec
UPDATE learners SET chosen_path_id = $2, updated_at = now() WHERE id = $1;

-- name: InsertPathWaitlist :exec
INSERT INTO path_waitlist (org_id, learner_id, path_id)
SELECT l.org_id, l.id, $2 FROM learners l WHERE l.id = $1
ON CONFLICT DO NOTHING;

-- name: DeletePathWaitlist :exec
DELETE FROM path_waitlist WHERE learner_id = $1 AND path_id = $2;

-- Applies the choice carried through sign-in (design 3.9) in one statement: the path only while the
-- learner has none and the slug is an available path; the waitlist rows only for planned paths.
-- name: ApplyLearnerChoice :exec
WITH set_path AS (
    UPDATE learners l
    SET chosen_path_id = p.id, updated_at = now()
    FROM paths p
    WHERE l.id = @learner_id AND l.chosen_path_id IS NULL
      AND p.slug = sqlc.narg('path')::text AND p.status = 'available'
)
INSERT INTO path_waitlist (org_id, learner_id, path_id)
SELECT @org_id::uuid, @learner_id::uuid, p.id
FROM paths p
WHERE p.slug = ANY(@waitlist::text[]) AND p.status = 'planned'
ON CONFLICT DO NOTHING;

-- Slugs of the planned paths the learner waits for; a path that became available drops out of the
-- list and keeps its row.
-- name: ListLearnerWaitlistSlugs :many
SELECT p.slug
FROM path_waitlist w
JOIN paths p ON p.id = w.path_id
WHERE w.learner_id = $1 AND p.status = 'planned'
ORDER BY p.slug;

-- What GET /v1/onboarding needs beyond the catalog and doctor rows (design 3.9).
-- name: GetOnboardingFacts :one
SELECT
    EXISTS (SELECT 1 FROM cli_tokens t WHERE t.learner_id = @id AND revoked_at IS NULL) AS cli_connected,
    EXISTS (SELECT 1 FROM ticket_progress p JOIN catalog_items i ON i.id = p.item_id AND i.kind = 'ticket'
               WHERE p.learner_id = @id AND p.state IN ('passed', 'revisit')) AS finished,
    COALESCE((SELECT l.hosted_default FROM learners l WHERE l.id = @id), false)::boolean AS hosted_default;
