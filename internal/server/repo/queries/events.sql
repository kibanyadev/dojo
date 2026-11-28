-- POST /v1/events (design 3.8): one transaction per batch, under the learner's progress lock.

-- Serializes one learner's batches (class 1 = progress, class 2 = the expiry sweeper).
-- name: LockLearnerProgress :exec
SELECT pg_advisory_xact_lock(1, hashtext(sqlc.arg('learner_id')::uuid::text));

-- The transaction clock: every "now" of a batch, so created_at and the pass time agree.
-- name: TransactionNow :one
SELECT now()::timestamptz AS now;

-- No row back means the (learner, client id) pair was already stored: a duplicate.
-- name: InsertEvent :one
INSERT INTO events (org_id, learner_id, client_event_id, type, payload, client_at)
VALUES (@org_id, @learner_id, @client_event_id, @type, @payload, @client_at)
ON CONFLICT (learner_id, client_event_id) DO NOTHING
RETURNING id;

-- name: ListTicketIDs :many
SELECT id FROM catalog_items WHERE org_id = @org_id AND kind IN ('ticket', 'practice') AND id = ANY(@ids::text[]);

-- name: GetTicketProgress :one
SELECT * FROM ticket_progress WHERE learner_id = $1 AND item_id = $2;

-- name: InsertTicketProgress :one
INSERT INTO ticket_progress (org_id, learner_id, item_id, state, lab, lab_state, lab_state_at, first_started_at,
                             last_seen_at, check_count, last_check_at, last_check_ok, last_check_passed,
                             last_check_total, last_check_output, first_passed_at, passed_seconds)
VALUES (@org_id, @learner_id, @item_id, @state, @lab, @lab_state, @lab_state_at, @first_started_at,
        @last_seen_at, @check_count, @last_check_at, @last_check_ok, @last_check_passed,
        @last_check_total, @last_check_output, @first_passed_at, @passed_seconds)
RETURNING *;

-- name: UpdateTicketProgress :exec
UPDATE ticket_progress
SET updated_at = now(), state = @state, lab = @lab, lab_state = @lab_state, lab_state_at = @lab_state_at,
    first_started_at = @first_started_at, last_seen_at = @last_seen_at, check_count = @check_count,
    last_check_at = @last_check_at, last_check_ok = @last_check_ok, last_check_passed = @last_check_passed,
    last_check_total = @last_check_total, last_check_output = @last_check_output,
    first_passed_at = @first_passed_at, passed_seconds = @passed_seconds
WHERE learner_id = @learner_id AND item_id = @item_id;

-- ticket.commit (design 3.9): an accepted sha starts its verification clock. Only the projection writes
-- it, under the class-1 lock.
-- name: AcceptTicketCommit :exec
UPDATE ticket_progress
SET updated_at = now(), passed_commit = @passed_commit, commit_state = 'pending', commit_tries = 0,
    commit_at = @commit_at, commit_next_at = @commit_next_at
WHERE learner_id = @learner_id AND item_id = @item_id;

-- A sha that arrived before its pass; a repeat changes nothing, so the first one wins.
-- name: InsertPendingCommit :exec
INSERT INTO pending_commits (org_id, learner_id, item_id, "commit", client_at)
VALUES (@org_id, @learner_id, @item_id, @commit, @client_at)
ON CONFLICT (learner_id, item_id) DO NOTHING;

-- No row back means no sha was waiting.
-- name: TakePendingCommit :one
DELETE FROM pending_commits WHERE learner_id = $1 AND item_id = $2 RETURNING "commit";

-- The expiry sweeper (design 3.8): class 2 = one sweep at a time, released at commit.
-- name: TryLockExpirySweep :one
SELECT pg_try_advisory_xact_lock(2, 0) AS locked;

-- One batch of live labs unseen for 7 days, after the given key. Read without the learner lock, so
-- ExpireLearnerLabs repeats the test once the lock is held.
-- name: ListStaleLabs :many
SELECT learner_id, item_id FROM ticket_progress
WHERE lab_state IN ('starting', 'running') AND last_seen_at < now() - interval '7 days'
  AND (learner_id, item_id) > (@after_learner::uuid, @after_item::text)
ORDER BY learner_id, item_id
LIMIT 100;

-- lab_state_at is the logical expiry instant, so any event after it wins the ordering rule.
-- name: ExpireLearnerLabs :many
UPDATE ticket_progress
SET updated_at = now(), lab_state = 'expired', lab_state_at = last_seen_at + interval '7 days'
WHERE learner_id = @learner_id AND item_id = ANY(@item_ids::text[])
  AND lab_state IN ('starting', 'running') AND last_seen_at < now() - interval '7 days'
RETURNING *;
