-- Subtitle tracks discovered during scans: embedded streams in the media
-- file, or sidecar files next to it.

CREATE TABLE subtitle_tracks (
    id           TEXT PRIMARY KEY,
    item_id      TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    source       TEXT NOT NULL CHECK (source IN ('embedded', 'sidecar')),
    stream_index INTEGER NOT NULL DEFAULT -1,
    path         TEXT NOT NULL DEFAULT '',
    codec        TEXT NOT NULL DEFAULT '',
    language     TEXT NOT NULL DEFAULT '',
    title        TEXT NOT NULL DEFAULT '',
    forced       INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX idx_subtitles_item ON subtitle_tracks(item_id);
