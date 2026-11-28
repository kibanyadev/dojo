-- The inbox items a first pass unlocks (design 3.10): the ticket's role items in position order, with the
-- fields the item texts need and whether the learner already completed each one.
-- name: ListRoleUnlockItems :many
SELECT i.id, i.kind, i.title, i.est_minutes,
       coalesce(i.payload #>> '{from,name}', '')::text AS from_name,
       (CASE WHEN jsonb_typeof(i.payload -> 'questions') = 'array' THEN jsonb_array_length(i.payload -> 'questions') ELSE 0 END)::int AS questions,
       EXISTS (SELECT 1 FROM item_completions c WHERE c.learner_id = @learner_id AND c.item_id = i.id) AS done
FROM catalog_items i
WHERE i.org_id = @org_id
  AND i.role_id = (SELECT t.role_id FROM catalog_items t WHERE t.id = @ticket_id AND t.org_id = @org_id)
ORDER BY i.position;

-- A story is written with its push pending (design 3.10); the unique key makes a repeat a no-op.
-- name: InsertUnlockInboxItem :execrows
INSERT INTO inbox_items (org_id, learner_id, kind, ref, title, body, url, push_state, push_next_at)
VALUES (@org_id, @learner_id, @kind, @ref, @title, @body, @url, @push_state, @push_next_at)
ON CONFLICT (learner_id, kind, ref) DO NOTHING;
