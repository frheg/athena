-- memory: what an assistant remembers about a user. Postgres holds the items (the source of truth); Qdrant holds
-- their vectors for search and can be rebuilt from here at any time.
CREATE TABLE memory_items (
    id          uuid PRIMARY KEY,
    tenant      text NOT NULL,             -- the calling service (from its token)
    subject     text NOT NULL,             -- the user the item is about
    kind        text NOT NULL,             -- fact, preference, person, place, event, other
    text        text NOT NULL,
    source      text NOT NULL DEFAULT '',  -- where it came from (a conversation, the user in the UI)
    embedded    boolean NOT NULL DEFAULT false, -- its vector is in Qdrant
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX memory_items_owner ON memory_items (tenant, subject, updated_at DESC);
CREATE INDEX memory_items_unembedded ON memory_items (embedded) WHERE NOT embedded;
CREATE INDEX memory_items_text ON memory_items USING gin (to_tsvector('simple', text));
