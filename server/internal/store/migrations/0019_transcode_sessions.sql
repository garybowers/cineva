-- Transcode sessions, so they survive a server restart.
--
-- A session is cheap to describe and expensive to rebuild: the segments on
-- disk are the expensive part, and they outlive the process that made them.
-- Without this, a restart during playback throws away work that is sitting
-- right there and makes every client start its transcode again.
--
-- Only the description is stored. The transcoder itself is not restarted on
-- startup — a restored session sits idle until a segment is requested, at
-- which point the existing restart-at-offset path starts it exactly where
-- the viewer is. That is the same machinery seeking already uses.
CREATE TABLE transcode_sessions (
    id            TEXT PRIMARY KEY,
    item_id       TEXT NOT NULL,
    title         TEXT NOT NULL,
    input_path    TEXT NOT NULL,
    dir           TEXT NOT NULL,
    segment_secs  INTEGER NOT NULL,
    num_segments  INTEGER NOT NULL,
    runtime_secs  INTEGER NOT NULL,
    quality_label TEXT NOT NULL DEFAULT '',
    quality_height INTEGER NOT NULL DEFAULT 0,
    quality_bitrate INTEGER NOT NULL DEFAULT 0,
    audio_track   INTEGER NOT NULL DEFAULT -1,
    hdr           TEXT NOT NULL DEFAULT '',
    seg_secs_real REAL NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
