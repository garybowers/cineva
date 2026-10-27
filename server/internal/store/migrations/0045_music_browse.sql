-- Music browsing on the server (server/docs/music.md, part 1): the columns
-- the paged artist, album and track listings sort and filter on, the
-- indexes they need, lyrics, and when an album was last looked up.

-- A track's album title and album artist, so a track stands alone in a
-- search result, a Songs row or the now-playing screen without a lookup
-- through parent_id, and so "appears on" is a column rather than a LIKE
-- over the whole library.
ALTER TABLE items ADD COLUMN album TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN album_artist TEXT NOT NULL DEFAULT '';
-- An album by various artists: tagged so, or found so from its tracks at
-- the end of its scan.
ALTER TABLE items ADD COLUMN compilation INTEGER NOT NULL DEFAULT 0;
-- What the file is, for the information line and the quality badge.
ALTER TABLE items ADD COLUMN bitrate INTEGER NOT NULL DEFAULT 0;     -- kbit/s
ALTER TABLE items ADD COLUMN sample_rate INTEGER NOT NULL DEFAULT 0; -- Hz
ALTER TABLE items ADD COLUMN bit_depth INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN channels INTEGER NOT NULL DEFAULT 0;
-- The MusicBrainz artist, release or recording id, from the tags Picard
-- writes or from a lookup: the music content key for federation.
ALTER TABLE items ADD COLUMN mbid TEXT NOT NULL DEFAULT '';
-- The title as it sorts: folded, lower-cased, a leading English article
-- dropped ("beatles, the"), so a name sort and the letter index agree and
-- neither needs a function call per row. Kept in step by UpsertItem and
-- UpdateItem; see SortKey.
ALTER TABLE items ADD COLUMN sort_title TEXT NOT NULL DEFAULT '';

UPDATE items SET
    album = COALESCE((SELECT a.title FROM items a WHERE a.id = items.parent_id), ''),
    album_artist = COALESCE((SELECT a.artist FROM items a WHERE a.id = items.parent_id), '')
WHERE kind = 'track';

-- The same rule the scanner now applies: a "Various" album artist, or
-- tracks by more than one artist.
UPDATE items SET compilation = 1
WHERE kind = 'album' AND (
    lower(artist) LIKE 'various%'
    OR (SELECT COUNT(DISTINCT lower(t.artist)) FROM items t
        WHERE t.parent_id = items.id AND t.kind = 'track' AND t.artist <> '') > 1
);

UPDATE items SET sort_title = lower(unifold(trim(
    CASE
        WHEN lower(title) LIKE 'the %' THEN substr(title, 5)
        WHEN lower(title) LIKE 'an %' THEN substr(title, 4)
        WHEN lower(title) LIKE 'a %' THEN substr(title, 3)
        ELSE title
    END)));

-- Albums carry the union of their tracks' genres, so the Genres tab and a
-- genre filter list albums without a join through tracks.
INSERT OR IGNORE INTO item_genres (item_id, genre)
SELECT t.parent_id, g.genre
FROM item_genres g JOIN items t ON t.id = g.item_id
WHERE t.kind = 'track' AND t.parent_id IS NOT NULL;

-- The listings' sorts and filters, and the ?since= delta, which scanned
-- the library for its high-water mark.
CREATE INDEX items_library_kind_sort ON items (library_id, kind, sort_title);
CREATE INDEX items_library_kind_added ON items (library_id, kind, added_at);
CREATE INDEX items_library_kind_artist ON items (library_id, kind, artist);
CREATE INDEX items_library_kind_year ON items (library_id, kind, year);
CREATE INDEX items_updated ON items (updated_at);
CREATE INDEX play_events_user_item ON play_events (user_id, item_id);

-- A track's lyrics, from its tags or an .lrc file beside it. synced means
-- the lines are timed ("[01:23.45] ...").
CREATE TABLE lyrics (
    item_id TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    synced  INTEGER NOT NULL DEFAULT 0,
    text    TEXT NOT NULL
);

-- When an album was last looked up on MusicBrainz (Unix seconds), so an
-- album the lookup found nothing for is not asked about on every scan.
-- Artists have artist_info for the same purpose.
CREATE TABLE album_lookups (
    item_id      TEXT PRIMARY KEY REFERENCES items(id) ON DELETE CASCADE,
    looked_up_at INTEGER NOT NULL
);
