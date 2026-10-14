-- Viewers telling the administrator that an intro or credits skip is in the
-- wrong place. A report names the episode or film, which kind is wrong, and
-- where playback was when it was made (often just past the bad skip), with
-- an optional note. One user has at most one open report per item and kind:
-- reporting again updates it, so one person cannot make a season look
-- three times as wrong. resolved_at stays '' while the report is open.
CREATE TABLE segment_reports (
    id            TEXT PRIMARY KEY,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    kind          TEXT NOT NULL CHECK (kind IN ('intro', 'credits')),
    position_secs REAL NOT NULL DEFAULT 0,
    note          TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    resolved_at   TEXT NOT NULL DEFAULT '',
    resolved_by   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX segment_reports_open ON segment_reports (resolved_at, created_at);
CREATE INDEX segment_reports_item ON segment_reports (item_id);

-- Trusted editors: users who may correct intro and credits times and start
-- detection on the titles they can see, without being administrators.
-- Detection across a whole library stays an administrator's decision.
ALTER TABLE users ADD COLUMN segment_editor INTEGER NOT NULL DEFAULT 0;
