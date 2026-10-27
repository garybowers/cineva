-- Mixes (server/docs/mix.md): what ListenBrainz knows about an artist,
-- cached so a mix costs no requests after the first, and when a playlist
-- was last added to, for the pickers that order playlists by use.

-- Artists similar to one of ours, by MusicBrainz id, with ListenBrainz's
-- score. A lookup that found nothing leaves one row with an empty
-- similar_mbid, so the miss is remembered and not asked again for 90 days.
CREATE TABLE artist_similar (
    artist_mbid  TEXT NOT NULL,
    similar_mbid TEXT NOT NULL,
    name         TEXT NOT NULL DEFAULT '',
    score        INTEGER NOT NULL DEFAULT 0,
    fetched_at   INTEGER NOT NULL,
    PRIMARY KEY (artist_mbid, similar_mbid)
);

-- An artist's best-known recordings in ListenBrainz's listening data, by
-- rank; a miss is a row with an empty recording_mbid likewise.
CREATE TABLE artist_top (
    artist_mbid    TEXT NOT NULL,
    recording_mbid TEXT NOT NULL,
    name           TEXT NOT NULL DEFAULT '',
    rank           INTEGER NOT NULL DEFAULT 0,
    fetched_at     INTEGER NOT NULL,
    PRIMARY KEY (artist_mbid, recording_mbid)
);

-- When tracks were last added to a playlist, so a picker can offer the
-- playlists in use first. NULL until the first add after this migration.
ALTER TABLE playlists ADD COLUMN last_added_at TEXT;
