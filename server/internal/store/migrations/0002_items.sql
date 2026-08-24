-- Media items and scan runs. Items are one table across kinds: movies stand
-- alone; TV nests series -> season -> episode via parent_id. Series/seasons
-- use their directory as path, so UNIQUE(library_id, path) holds for all kinds.

CREATE TABLE items (
    id           TEXT PRIMARY KEY,
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('movie', 'series', 'season', 'episode')),
    parent_id    TEXT REFERENCES items(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    year         INTEGER NOT NULL DEFAULT 0,
    season_num   INTEGER NOT NULL DEFAULT 0,
    episode_num  INTEGER NOT NULL DEFAULT 0,
    path         TEXT NOT NULL,
    size_bytes   INTEGER NOT NULL DEFAULT 0,
    runtime_secs INTEGER NOT NULL DEFAULT 0,
    container    TEXT NOT NULL DEFAULT '',
    video_codec  TEXT NOT NULL DEFAULT '',
    audio_codec  TEXT NOT NULL DEFAULT '',
    width        INTEGER NOT NULL DEFAULT 0,
    height       INTEGER NOT NULL DEFAULT 0,
    tmdb_id      INTEGER NOT NULL DEFAULT 0,
    overview     TEXT NOT NULL DEFAULT '',
    poster       TEXT NOT NULL DEFAULT '',
    added_at     TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at   TEXT NOT NULL DEFAULT (datetime('now')),
    UNIQUE (library_id, path)
);
CREATE INDEX idx_items_library_kind ON items(library_id, kind);
CREATE INDEX idx_items_parent ON items(parent_id);

CREATE TABLE scans (
    id            TEXT PRIMARY KEY,
    library_id    TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    status        TEXT NOT NULL DEFAULT 'running' CHECK (status IN ('running', 'completed', 'failed')),
    files_seen    INTEGER NOT NULL DEFAULT 0,
    items_created INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    started_at    TEXT NOT NULL DEFAULT (datetime('now')),
    finished_at   TEXT
);
