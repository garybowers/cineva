-- Listening history that lasts, and playlists.
--
-- play_counts is each user's count of finished plays of an item and when
-- the last one was. play_events cannot answer "how often have I played
-- this?" for long: it is pruned after libraries.history_retention_days,
-- and it records every start, skips included. A play counts here when the
-- playback ends far enough in (for a track, half of it or four minutes,
-- the rule scrobblers use; for video, the watched threshold), so the
-- numbers mean what a listener expects them to. No foreign key to users,
-- like play_state: rows can belong to the pre-auth placeholder user.
--
-- Backfilled from the history still held: plays that completed, and track
-- plays that stayed open long enough by the same rule (wall-clock time is
-- all a row recorded, which is close enough for a starting count).

CREATE TABLE play_counts (
    user_id        TEXT NOT NULL,
    item_id        TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    plays          INTEGER NOT NULL DEFAULT 0,
    last_played_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX play_counts_recent ON play_counts (user_id, last_played_at);
CREATE INDEX play_counts_item ON play_counts (item_id);

INSERT INTO play_counts (user_id, item_id, plays, last_played_at)
SELECT p.user_id, p.item_id, COUNT(*), MAX(p.started_at)
FROM play_events p JOIN items i ON i.id = p.item_id
WHERE p.completed = 1
   OR (p.kind = 'track' AND p.secs >= 30 AND (p.secs >= 240 OR p.secs * 2 >= i.runtime_secs))
GROUP BY p.user_id, p.item_id;

-- Playlists belong to one user. A manual playlist lists its tracks in
-- playlist_items, in position order (a track may appear more than once);
-- a smart one has its rules as JSON in smart and no rows there, since it
-- is evaluated afresh each time it is opened. smart is '' for a manual
-- playlist.

CREATE TABLE playlists (
    id         TEXT PRIMARY KEY,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name       TEXT NOT NULL,
    smart      TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX playlists_user ON playlists (user_id);

CREATE TABLE playlist_items (
    playlist_id TEXT NOT NULL REFERENCES playlists(id) ON DELETE CASCADE,
    position    INTEGER NOT NULL,
    item_id     TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    added_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (playlist_id, position)
);
CREATE INDEX playlist_items_item ON playlist_items (item_id);

-- What a lookup found about an artist beyond the files. The picture and
-- biography go on the artist's own row (poster, overview), where every
-- listing already reads them; this keeps where the biography came from,
-- for the credit its licence asks for, the MusicBrainz id it was found
-- by, and when the lookup ran (Unix seconds), so an artist nobody knows
-- of is asked about again only after a long wait.

CREATE TABLE artist_info (
    item_id      TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    mbid         TEXT NOT NULL DEFAULT '',
    bio_url      TEXT NOT NULL DEFAULT '',
    looked_up_at INTEGER NOT NULL DEFAULT 0
);
