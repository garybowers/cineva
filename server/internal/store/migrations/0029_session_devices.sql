-- A browser session is a device: the thing a user sees in "Signed-in
-- devices", revokes, renames ("Living-room TV"), and hands a play queue to
-- or takes one from.
--
-- id is the session's public handle; the token hash is a credential and
-- never leaves the server. queue is the device's video play queue as JSON
-- ({"itemIds": [...], "index": n}), kept current by the device so another
-- one can take it over. Positions are not stored here: resume positions are
-- per user and item already, so whichever device plays next resumes where
-- the last one stopped.

ALTER TABLE sessions ADD COLUMN id TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN name TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN queue TEXT NOT NULL DEFAULT '';
ALTER TABLE sessions ADD COLUMN queue_updated_at TEXT NOT NULL DEFAULT '';

UPDATE sessions SET id = lower(hex(randomblob(8)));
CREATE UNIQUE INDEX sessions_id ON sessions (id);
