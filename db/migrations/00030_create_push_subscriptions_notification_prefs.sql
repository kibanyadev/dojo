-- +goose Up
-- Push and notification settings (design 3.10). learners.time_zone is an IANA name the API validates (Go and
-- Postgres both accept it before it is stored); null means UTC.
ALTER TABLE learners ADD COLUMN time_zone text CHECK (char_length(time_zone) BETWEEN 1 AND 64);

-- One row per browser subscription. The endpoint is unique: a browser belongs to whoever subscribed last on it.
-- The CHECK on endpoint is only a floor (http for the local fake-push); the API's allowlist is the real rule.
-- p256dh and auth are unpadded base64url of 65 and 16 bytes.
CREATE TABLE push_subscriptions (
    id              uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id          uuid NOT NULL REFERENCES orgs (id),
    learner_id      uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    endpoint        text NOT NULL UNIQUE CHECK (char_length(endpoint) BETWEEN 1 AND 1024 AND (endpoint LIKE 'https://%' OR endpoint LIKE 'http://%')),
    p256dh          text NOT NULL CHECK (char_length(p256dh) = 87 AND p256dh ~ '^[A-Za-z0-9_-]+$'),
    auth            text NOT NULL CHECK (char_length(auth) = 22 AND auth ~ '^[A-Za-z0-9_-]+$'),
    device_label    text NOT NULL CHECK (char_length(device_label) BETWEEN 1 AND 64),
    last_success_at timestamptz
);

CREATE INDEX push_subscriptions_learner_idx ON push_subscriptions (learner_id, created_at DESC, id DESC);

-- One row per learner and kind the learner changed; a missing row is the default. A channel the kind does
-- not have is null: push for weekly and news, email for check, story and exam.
CREATE TABLE notification_prefs (
    org_id     uuid NOT NULL REFERENCES orgs (id),
    learner_id uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    kind       text NOT NULL CHECK (kind IN ('check', 'story', 'role_passed', 'exam', 'weekly', 'news')),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    push       boolean,
    email      boolean,
    PRIMARY KEY (learner_id, kind),
    CONSTRAINT notification_prefs_push_channel CHECK (push IS NULL OR kind NOT IN ('weekly', 'news')),
    CONSTRAINT notification_prefs_email_channel CHECK (email IS NULL OR kind NOT IN ('check', 'story', 'exam'))
);

-- Grants (design 3.7, 3.10). The API changes only the zone of a learner, never a subscription's keys or
-- endpoint (a new subscribe replaces the row), and removes a subscription itself. dojo_admin has none.
GRANT UPDATE (time_zone) ON learners TO dojo_api;
GRANT SELECT ON push_subscriptions TO dojo_api;
GRANT INSERT (org_id, learner_id, endpoint, p256dh, auth, device_label) ON push_subscriptions TO dojo_api;
GRANT UPDATE (updated_at, last_success_at) ON push_subscriptions TO dojo_api;
GRANT DELETE ON push_subscriptions TO dojo_api;
GRANT SELECT ON notification_prefs TO dojo_api;
GRANT INSERT (org_id, learner_id, kind, push, email) ON notification_prefs TO dojo_api;
GRANT UPDATE (updated_at, push, email) ON notification_prefs TO dojo_api;

-- +goose Down
DROP TABLE notification_prefs;
DROP TABLE push_subscriptions;
ALTER TABLE learners DROP COLUMN time_zone;
