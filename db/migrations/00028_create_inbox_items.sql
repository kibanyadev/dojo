-- +goose Up
-- The inbox (design 3.10, N1): one row per learner, kind and ref, so every kind is bounded. The title, body
-- and in-app URL are written with the row. updated_at moves only on an upsert (check, review_ready), never
-- on a read: it is the keyset order of GET /v1/inbox.
CREATE TABLE inbox_items (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id       uuid NOT NULL REFERENCES orgs (id),
    learner_id   uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    kind         text NOT NULL CHECK (kind IN ('mark', 'belt', 'review_ready', 'story', 'quiz', 'check')),
    ref          text NOT NULL CHECK (char_length(ref) BETWEEN 1 AND 128),
    title        text NOT NULL CHECK (char_length(title) BETWEEN 1 AND 200),
    body         text NOT NULL CHECK (char_length(body) BETWEEN 1 AND 500),
    url          text NOT NULL CHECK (char_length(url) BETWEEN 2 AND 256 AND left(url, 1) = '/' AND left(url, 2) <> '//' AND position(E'\\' in url) = 0 AND url !~ '[[:cntrl:]]'),
    read_at      timestamptz,
    push_state   text NOT NULL DEFAULT 'none' CHECK (push_state IN ('none', 'pending', 'sent', 'skipped', 'failed')),
    push_tries   integer NOT NULL DEFAULT 0 CHECK (push_tries >= 0),
    push_next_at timestamptz,
    pushed_at    timestamptz,
    CONSTRAINT inbox_items_learner_kind_ref_key UNIQUE (learner_id, kind, ref)
);

-- The keyset order of GET /v1/inbox, the push worker's claim, and the unread count.
CREATE INDEX inbox_items_order_idx ON inbox_items (learner_id, updated_at DESC, id DESC);
CREATE INDEX inbox_items_push_idx ON inbox_items (push_next_at) WHERE push_state = 'pending';
CREATE INDEX inbox_items_unread_idx ON inbox_items (learner_id) WHERE read_at IS NULL;

-- Grants (design 3.7, 3.10). The API inserts and updates only the named columns and never deletes (a
-- learner's delete cascades): id, created_at, learner_id, kind and ref never change. dojo_admin has none.
GRANT SELECT ON inbox_items TO dojo_api;
GRANT INSERT (org_id, learner_id, kind, ref, title, body, url, push_state, push_next_at) ON inbox_items TO dojo_api;
GRANT UPDATE (updated_at, title, body, url, read_at, push_state, push_tries, push_next_at, pushed_at)
    ON inbox_items TO dojo_api;

-- +goose Down
DROP TABLE inbox_items;
