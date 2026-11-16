-- live mode: a message spoken to Athena (the answer is read aloud: short, no markdown) and what the camera saw then
ALTER TABLE messages ADD COLUMN voice boolean NOT NULL DEFAULT false;
ALTER TABLE messages ADD COLUMN seen text NOT NULL DEFAULT '';
