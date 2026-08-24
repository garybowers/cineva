-- Music domain: artist/album/track item kinds plus track ordering and a
-- display-artist column. SQLite can't alter a CHECK constraint, so the items
-- table is rebuilt (the migrator runs with foreign keys off; child tables
-- reference items by name and are valid again after the rename).

CREATE TABLE items_new (
    id           TEXT PRIMARY KEY,
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('movie', 'series', 'season', 'episode', 'artist', 'album', 'track')),
    parent_id    TEXT REFERENCES items(id) ON DELETE CASCADE,
    title        TEXT NOT NULL,
    year         INTEGER NOT NULL DEFAULT 0,
    season_num   INTEGER NOT NULL DEFAULT 0,
    episode_num  INTEGER NOT NULL DEFAULT 0,
    disc_num     INTEGER NOT NULL DEFAULT 0,
    track_num    INTEGER NOT NULL DEFAULT 0,
    artist       TEXT NOT NULL DEFAULT '',
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

INSERT INTO items_new (id, library_id, kind, parent_id, title, year, season_num,
    episode_num, path, size_bytes, runtime_secs, container, video_codec,
    audio_codec, width, height, tmdb_id, overview, poster, added_at, updated_at)
SELECT id, library_id, kind, parent_id, title, year, season_num,
    episode_num, path, size_bytes, runtime_secs, container, video_codec,
    audio_codec, width, height, tmdb_id, overview, poster, added_at, updated_at
FROM items;

DROP TABLE items;
ALTER TABLE items_new RENAME TO items;

CREATE INDEX idx_items_library_kind ON items(library_id, kind);
CREATE INDEX idx_items_parent ON items(parent_id);
