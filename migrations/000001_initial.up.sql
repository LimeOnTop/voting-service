-- Poll configuration and durable anonymous aggregates.

CREATE TABLE polls (
    id          UUID        PRIMARY KEY,
    question    TEXT        NOT NULL,
    type        TEXT        NOT NULL,
    max_choices INTEGER     NOT NULL,
    status      TEXT        NOT NULL,
    created_at  TIMESTAMPTZ NOT NULL,
    starts_at   TIMESTAMPTZ,
    ends_at     TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,

    CONSTRAINT polls_question_not_blank CHECK (length(btrim(question)) > 0),
    CONSTRAINT polls_type_known         CHECK (type IN ('single', 'multiple')),
    CONSTRAINT polls_status_known       CHECK (status IN ('draft', 'active', 'finished')),
    CONSTRAINT polls_max_choices_valid  CHECK (max_choices >= 1),
    CONSTRAINT polls_single_one_choice  CHECK (type <> 'single' OR max_choices = 1),
    CONSTRAINT polls_window_ordered     CHECK (starts_at IS NULL OR ends_at IS NULL OR ends_at > starts_at),
    CONSTRAINT polls_finished_at_matches_status
        CHECK ((status = 'finished') = (finished_at IS NOT NULL))
);

CREATE INDEX polls_created_at_idx ON polls (created_at DESC, id DESC);

CREATE TABLE poll_options (
    id       UUID    PRIMARY KEY,
    poll_id  UUID    NOT NULL REFERENCES polls (id) ON DELETE CASCADE,
    text     TEXT    NOT NULL,
    position INTEGER NOT NULL,

    CONSTRAINT poll_options_text_not_blank CHECK (length(btrim(text)) > 0),
    CONSTRAINT poll_options_position_valid CHECK (position >= 0),
    CONSTRAINT poll_options_position_unique UNIQUE (poll_id, position)
);

CREATE INDEX poll_options_poll_id_idx ON poll_options (poll_id);

CREATE TABLE poll_results (
    poll_id    UUID        NOT NULL REFERENCES polls (id) ON DELETE CASCADE,
    option_id  UUID        NOT NULL REFERENCES poll_options (id) ON DELETE CASCADE,
    votes      BIGINT      NOT NULL DEFAULT 0,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),

    PRIMARY KEY (poll_id, option_id),
    CONSTRAINT poll_results_votes_non_negative CHECK (votes >= 0)
);
