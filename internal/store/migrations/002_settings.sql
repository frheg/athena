-- settings that hold for all of Athena, such as the one active model
CREATE TABLE settings (
    key         text PRIMARY KEY,
    value       text NOT NULL,
    updated_at  timestamptz NOT NULL DEFAULT now()
);
