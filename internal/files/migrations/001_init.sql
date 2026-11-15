-- files: what a user attaches to a conversation (and, later, what Athena makes). Per tenant (the calling service)
-- and subject (the user). A file never changes after upload; text and preview are made once, at upload.
CREATE TABLE files (
    id            uuid PRIMARY KEY,
    tenant        text NOT NULL,
    subject       text NOT NULL,
    name          text NOT NULL,
    mime          text NOT NULL,
    kind          text NOT NULL,             -- image, text, document, binary
    size          bigint NOT NULL,
    sha256        text NOT NULL,
    data          bytea NOT NULL,
    preview       bytea,                     -- image: a JPEG at most 1536 px on the long side, for models and the UI
    text          text,                      -- text, code and documents: the readable text
    chars         integer NOT NULL DEFAULT 0,
    pages         integer NOT NULL DEFAULT 0, -- PDF pages, slides or sheets
    width         integer NOT NULL DEFAULT 0,
    height        integer NOT NULL DEFAULT 0,
    note          text NOT NULL DEFAULT '',  -- what could not be read, for the model and the user
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX files_owner ON files (tenant, subject, created_at DESC);
