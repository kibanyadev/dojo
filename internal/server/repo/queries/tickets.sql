-- GET /v1/tickets/{id} and POST /v1/tickets/{id}/hints/{n} (design 3.8).

-- name: GetTicketView :one
SELECT i.id, r.slug AS role, i.title, i.est_minutes, i.tags, i.brief, i.hints
FROM catalog_items i
LEFT JOIN roles r ON r.id = i.role_id
WHERE i.id = $1 AND i.kind IN ('ticket', 'practice');

-- name: ListHintOpens :many
SELECT n FROM hint_opens WHERE learner_id = $1 AND item_id = $2 ORDER BY n;

-- name: HintIsOpen :one
SELECT EXISTS (SELECT 1 FROM hint_opens WHERE learner_id = $1 AND item_id = $2 AND n = $3) AS open;

-- name: TicketIsPassed :one
SELECT EXISTS (
    SELECT 1 FROM ticket_progress WHERE learner_id = $1 AND item_id = $2 AND state IN ('passed', 'revisit')
) AS passed;

-- No row back means the hint was already open: a repeat changes nothing.
-- name: InsertHintOpen :one
INSERT INTO hint_opens (org_id, learner_id, item_id, n)
VALUES (@org_id, @learner_id, @item_id, @n)
ON CONFLICT (learner_id, item_id, n) DO NOTHING
RETURNING n;

-- The event id is made here: hint.opened never comes from a client.
-- name: InsertHintOpenedEvent :exec
INSERT INTO events (org_id, learner_id, client_event_id, type, payload, client_at)
VALUES (@org_id, @learner_id, gen_random_uuid(), 'hint.opened',
        jsonb_build_object('item_id', @item_id::text, 'n', @n::smallint), now());
