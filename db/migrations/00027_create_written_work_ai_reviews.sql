-- +goose Up
-- Reviews (design 3.10): system_flags, the writing and feedback work, and the review calls.
-- system_flags is platform level (no org, like operators). Only ai_reviews_enabled is seeded here; each
-- later key is seeded by the migration of the milestone that first reads it.
CREATE TABLE system_flags (
    key        text PRIMARY KEY CHECK (char_length(key) BETWEEN 1 AND 64),
    value      jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO system_flags (key, value) VALUES ('ai_reviews_enabled', 'true'::jsonb);

-- written_work: one row per learner and item, from the first draft or submit on. item_id has no cascade:
-- a catalog load cannot drop an item with learner rows. review_state null means never submitted.
CREATE TABLE written_work (
    org_id             uuid NOT NULL REFERENCES orgs (id),
    learner_id         uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    item_id            text NOT NULL REFERENCES catalog_items (id),
    created_at         timestamptz NOT NULL DEFAULT now(),
    updated_at         timestamptz NOT NULL DEFAULT now(),
    draft              text NOT NULL DEFAULT '' CHECK (char_length(draft) <= 4000),
    draft_saved_at     timestamptz NOT NULL DEFAULT now(),
    body               text CHECK (char_length(body) <= 4000),
    first_submitted_at timestamptz,
    submitted_at       timestamptz,
    submit_count       integer NOT NULL DEFAULT 0 CHECK (submit_count >= 0),
    review_state       text CHECK (review_state IN ('pending', 'done', 'capped', 'failed')),
    review             jsonb CHECK (jsonb_typeof(review) = 'object'),
    review_tries       smallint NOT NULL DEFAULT 0 CHECK (review_tries BETWEEN 0 AND 5),
    review_next_at     timestamptz,
    reviewed_at        timestamptz,
    PRIMARY KEY (learner_id, item_id)
);

-- The worker claims the pending rows that are due.
CREATE INDEX written_work_review_due_idx ON written_work (review_next_at) WHERE review_state = 'pending';

-- ai_reviews: one row per review call, holding no learner text. learner_id is nulled when the learner is
-- deleted; item_id has no foreign key (like ticket_reports), so retiring an item never blocks a load.
CREATE TABLE ai_reviews (
    id           uuid PRIMARY KEY DEFAULT uuidv7(),
    org_id       uuid NOT NULL REFERENCES orgs (id),
    learner_id   uuid REFERENCES learners (id) ON DELETE SET NULL,
    item_id      text NOT NULL,
    created_at   timestamptz NOT NULL DEFAULT now(),
    updated_at   timestamptz NOT NULL DEFAULT now(),
    submit_count integer NOT NULL CHECK (submit_count >= 1),
    model        text NOT NULL CHECK (char_length(model) BETWEEN 1 AND 100),
    tokens_in    integer NOT NULL CHECK (tokens_in >= 0),
    tokens_out   integer NOT NULL CHECK (tokens_out >= 0),
    outcome      text NOT NULL CHECK (outcome IN ('ok', 'invalid', 'error'))
);

-- The hidden daily cap counts a learner's calls of today.
CREATE INDEX ai_reviews_learner_created_idx ON ai_reviews (learner_id, created_at);

-- prune_ai_reviews(): SECURITY INVOKER, run by the nightly job as the owner, EXECUTE revoked from everyone.
-- Keeps 12 months, the window of the spend estimate.
-- +goose StatementBegin
CREATE FUNCTION prune_ai_reviews() RETURNS bigint
    LANGUAGE sql
    SET search_path = pg_catalog, public
AS $$
    WITH deleted AS (
        DELETE FROM ai_reviews WHERE created_at < now() - interval '12 months' RETURNING 1
    )
    SELECT count(*) FROM deleted
$$;
-- +goose StatementEnd

REVOKE EXECUTE ON FUNCTION prune_ai_reviews() FROM PUBLIC;

-- Grants (design 3.7, 3.10). system_flags is read-only for both services until A3 (M5b). The API writes
-- and updates written_work and inserts ai_reviews; the review worker needs no more. dojo_admin has no
-- grant on the other two. The draft route needs no more than the INSERT and UPDATE of its own columns.
GRANT SELECT ON system_flags TO dojo_api, dojo_admin;
GRANT SELECT ON written_work, ai_reviews TO dojo_api;
GRANT INSERT (org_id, learner_id, item_id, draft, draft_saved_at, body, first_submitted_at, submitted_at, submit_count,
              review_state, review_next_at)
    ON written_work TO dojo_api;
GRANT UPDATE (updated_at, draft, draft_saved_at, body, first_submitted_at, submitted_at, submit_count, review_state,
              review, review_tries, review_next_at, reviewed_at)
    ON written_work TO dojo_api;
GRANT INSERT (org_id, learner_id, item_id, submit_count, model, tokens_in, tokens_out, outcome)
    ON ai_reviews TO dojo_api;

-- +goose Down
DROP FUNCTION prune_ai_reviews();
DROP TABLE ai_reviews;
DROP TABLE written_work;
DROP TABLE system_flags;
