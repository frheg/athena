-- the files attached to a user message (what the files service said about each: id, name, type, size, ...)
ALTER TABLE messages ADD COLUMN files jsonb;
