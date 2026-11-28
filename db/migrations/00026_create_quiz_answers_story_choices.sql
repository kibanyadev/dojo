-- +goose Up
-- Quiz answers and story choices (design 3.10). item_id has no cascade: a catalog load cannot drop an
-- item with learner rows. question_id and choice_id have no foreign key: a quiz may lose a question and
-- the answer is kept and ignored.
CREATE TABLE quiz_answers (
    org_id        uuid NOT NULL REFERENCES orgs (id),
    learner_id    uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    item_id       text NOT NULL REFERENCES catalog_items (id),
    question_id   text NOT NULL CHECK (char_length(question_id) <= 32 AND question_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    created_at    timestamptz NOT NULL DEFAULT now(),
    updated_at    timestamptz NOT NULL DEFAULT now(),
    first_choice  smallint NOT NULL CHECK (first_choice BETWEEN 0 AND 4),
    first_correct boolean NOT NULL,
    first_at      timestamptz NOT NULL DEFAULT now(),
    last_choice   smallint NOT NULL CHECK (last_choice BETWEEN 0 AND 4),
    last_correct  boolean NOT NULL,
    last_at       timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (learner_id, item_id, question_id)
);

CREATE TABLE story_choices (
    org_id     uuid NOT NULL REFERENCES orgs (id),
    learner_id uuid NOT NULL REFERENCES learners (id) ON DELETE CASCADE,
    item_id    text NOT NULL REFERENCES catalog_items (id),
    choice_id  text NOT NULL CHECK (char_length(choice_id) <= 32 AND choice_id ~ '^[a-z0-9]+(-[a-z0-9]+)*$'),
    created_at timestamptz NOT NULL DEFAULT now(),
    updated_at timestamptz NOT NULL DEFAULT now(),
    chosen_at  timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (learner_id, item_id)
);

-- Grants (design 3.7, 3.10). The first answer is written once and never changes: only the last_* columns
-- and updated_at can be updated. A story choice is final, so it has no UPDATE. dojo_admin has none.
GRANT SELECT ON quiz_answers, story_choices TO dojo_api;
GRANT INSERT (org_id, learner_id, item_id, question_id, first_choice, first_correct, first_at, last_choice, last_correct, last_at)
    ON quiz_answers TO dojo_api;
GRANT UPDATE (updated_at, last_choice, last_correct, last_at) ON quiz_answers TO dojo_api;
GRANT INSERT (org_id, learner_id, item_id, choice_id) ON story_choices TO dojo_api;

-- +goose Down
DROP TABLE story_choices;
DROP TABLE quiz_answers;
