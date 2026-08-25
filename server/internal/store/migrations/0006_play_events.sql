-- Play history: one row per playback start, for the admin dashboard's
-- plays-over-time chart. play_state stays the per-user resume/watched
-- record; this table is append-only history.

CREATE TABLE play_events (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    user_id    TEXT NOT NULL,
    item_id    TEXT NOT NULL,
    kind       TEXT NOT NULL,
    started_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX play_events_started ON play_events (started_at);
