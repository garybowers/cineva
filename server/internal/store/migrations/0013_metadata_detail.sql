-- Richer metadata from the provider's detail endpoint: ratings, runtime,
-- tagline, backdrop artwork, and the collection a film belongs to.

ALTER TABLE items ADD COLUMN rating REAL NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN vote_count INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN tagline TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN backdrop TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN collection_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN collection_name TEXT NOT NULL DEFAULT '';

-- Genres get their own table rather than a delimited column. The library's
-- genre list is then whatever its films actually carry — a GROUP BY, not a
-- fixed vocabulary — and narrowing the grid to one genre is an index hit
-- rather than a LIKE over every row.
CREATE TABLE item_genres (
    item_id TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    genre   TEXT NOT NULL,
    PRIMARY KEY (item_id, genre)
);
CREATE INDEX idx_item_genres_genre ON item_genres(genre);
