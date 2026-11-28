-- ticket_reports (design 3.9): the report form and its email reader.

-- A report is about a ticket or a practice item.
-- name: ReportItemExists :one
SELECT EXISTS (SELECT 1 FROM catalog_items WHERE id = $1 AND kind IN ('ticket', 'practice'))::boolean AS found;

-- One report at a time per learner: the row lock serializes the count and the insert, so two requests cannot
-- both pass the limit. dojo_api may lock a learner row because it can UPDATE some of its columns.
-- name: LockLearnerForReport :one
SELECT id FROM learners WHERE id = $1 FOR NO KEY UPDATE;

-- The reports counted against the limits (5 an hour, 20 a day), read in the database's clock. The oldest of each
-- window says when a slot frees.
-- name: CountRecentReports :one
SELECT count(*) FILTER (WHERE created_at > now() - interval '1 hour')::int AS hour_count,
       count(*)::int AS day_count,
       min(created_at) FILTER (WHERE created_at > now() - interval '1 hour')::timestamptz AS hour_oldest,
       min(created_at)::timestamptz AS day_oldest,
       now()::timestamptz AS db_now
FROM ticket_reports
WHERE learner_id = @learner_id AND created_at > now() - interval '1 day';

-- The org, the device version and the copied texts come from the database, not from the client: the output only
-- when asked for and only the learner's own for that ticket, the doctor summary only for setup help.
-- name: InsertReport :one
INSERT INTO ticket_reports (org_id, learner_id, item_id, kind, cli_version, last_check_output, message, doctor_summary)
SELECT l.org_id, l.id, sqlc.narg('item_id')::text, sqlc.arg('kind')::text,
       COALESCE((SELECT t.cli_version FROM cli_tokens t WHERE t.learner_id = l.id AND t.revoked_at IS NULL
                 ORDER BY t.last_used_at DESC, t.id DESC LIMIT 1), ''),
       CASE WHEN sqlc.arg('include_output')::boolean
            THEN (SELECT p.last_check_output FROM ticket_progress p WHERE p.learner_id = l.id AND p.item_id = sqlc.narg('item_id')::text) END,
       sqlc.arg('message')::text,
       CASE WHEN sqlc.arg('kind')::text = 'setup_help'
            THEN left(COALESCE((SELECT string_agg(d.check_id || ': ' || d.text, E'\n' ORDER BY d.check_id)
                                FROM doctor_results d WHERE d.learner_id = l.id AND d.status <> 'ok'), ''), 2000)
            ELSE '' END
FROM learners l WHERE l.id = sqlc.arg('learner_id')
RETURNING id;

-- The reader claims the oldest unsent report not in skip (reports backing off after a failed send) and keeps the
-- row locked until the transaction ends; a task that loses the race gets the next row, or none.
-- name: ClaimReport :one
SELECT r.id, r.kind, r.item_id, r.cli_version, r.last_check_output, r.message, r.doctor_summary,
       l.github_login, l.email
FROM ticket_reports r
JOIN learners l ON l.id = r.learner_id
WHERE r.emailed_at IS NULL AND r.id <> ALL(COALESCE(sqlc.arg('skip')::uuid[], '{}'))
ORDER BY r.created_at
LIMIT 1
FOR UPDATE OF r SKIP LOCKED;

-- name: MarkReportEmailed :exec
UPDATE ticket_reports SET emailed_at = now(), updated_at = now() WHERE id = $1;
