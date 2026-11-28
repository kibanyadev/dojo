-- Push subscriptions (design 3.10). Every query is scoped by learner_id except the endpoint takeover.

-- name: SetLearnerTimeZone :execrows
UPDATE learners SET time_zone = @time_zone, updated_at = now() WHERE id = @learner_id;

-- A browser belongs to whoever subscribed last on it, so the old owner's row goes.
-- name: DeletePushSubscriptionByEndpoint :exec
DELETE FROM push_subscriptions WHERE endpoint = @endpoint;

-- No row back means the learner no longer exists.
-- name: InsertPushSubscription :one
INSERT INTO push_subscriptions (org_id, learner_id, endpoint, p256dh, auth, device_label)
SELECT l.org_id, l.id, @endpoint, @p256dh, @auth, @device_label FROM learners l WHERE l.id = @learner_id
RETURNING id;

-- Keeps the newest @keep rows of the learner; the oldest go.
-- name: TrimPushSubscriptions :exec
DELETE FROM push_subscriptions d
WHERE d.learner_id = @learner_id
  AND d.id NOT IN (SELECT s.id FROM push_subscriptions s WHERE s.learner_id = @learner_id
                 ORDER BY s.created_at DESC, s.id DESC LIMIT @keep::int);

-- name: ListPushSubscriptions :many
SELECT id, device_label, created_at, last_success_at
FROM push_subscriptions
WHERE learner_id = @learner_id
ORDER BY created_at DESC, id DESC;

-- name: DeletePushSubscription :execrows
DELETE FROM push_subscriptions WHERE id = @id AND learner_id = @learner_id;
