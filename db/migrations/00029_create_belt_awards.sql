-- +goose Up
-- Belt awards (design 3.10): one row per learner and mark, written in the submit transaction. mark_id has no
-- cascade, so a catalog load cannot drop a mark a learner holds (the load names it first). source_item_id
-- has no foreign key: it records which item earned the mark and never blocks a retired item.
CREATE TABLE belt_awards (
    org_id         uuid NOT NULL REFERENCES orgs (id),
    learner_id     uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    mark_id        uuid NOT NULL REFERENCES belt_marks (id),
    source_item_id text NOT NULL CHECK (char_length(source_item_id) BETWEEN 1 AND 128),
    created_at     timestamptz NOT NULL DEFAULT now(),
    updated_at     timestamptz NOT NULL DEFAULT now(),
    awarded_at     timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (learner_id, mark_id)
);

-- Grants (design 3.7, 3.10). The API reads and inserts an award once and never changes or removes it
-- (a learner's delete cascades); dojo_admin reads it for the progress view.
GRANT SELECT ON belt_awards TO dojo_api, dojo_admin;
GRANT INSERT (org_id, learner_id, mark_id, source_item_id) ON belt_awards TO dojo_api;

-- +goose Down
DROP TABLE belt_awards;
