-- notes: short notes and reminders, per tenant (the calling service) and subject (the user).
CREATE TABLE notes (
    id          uuid PRIMARY KEY,
    tenant      text NOT NULL,
    subject     text NOT NULL,
    title       text NOT NULL,
    body        text NOT NULL DEFAULT '',
    tags        text[] NOT NULL DEFAULT '{}',
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX notes_owner ON notes (tenant, subject, updated_at DESC);
CREATE INDEX notes_text ON notes USING gin (to_tsvector('simple', title || ' ' || body));

CREATE TABLE reminders (
    id            uuid PRIMARY KEY,
    tenant        text NOT NULL,
    subject       text NOT NULL,
    text          text NOT NULL,
    due_at        timestamptz NOT NULL,
    cancelled     boolean NOT NULL DEFAULT false,
    delivered_at  timestamptz,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX reminders_due ON reminders (tenant, due_at) WHERE delivered_at IS NULL AND NOT cancelled;
