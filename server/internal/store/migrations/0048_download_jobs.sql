-- Download jobs for native apps (server/docs/offline.md): a track asked
-- for at a profile, prepared on the server (an original is ready at once,
-- a transcode once the audio cache holds it) and fetched afterwards with
-- ranges. device_id is the session the app signed in with, for the record
-- of what each device holds; '' for an API key.
CREATE TABLE download_jobs (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    device_id  TEXT NOT NULL DEFAULT '',
    item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    profile    TEXT NOT NULL,
    status     TEXT NOT NULL CHECK (status IN ('queued', 'preparing', 'ready', 'failed')),
    bytes      INTEGER NOT NULL DEFAULT 0,
    error      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX download_jobs_user ON download_jobs (user_id, created_at);
