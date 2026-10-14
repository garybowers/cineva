-- A user's star rating of an item, 1 to 5; no row means not rated. Per-user
-- state like play_counts. The web client rates tracks; the table does not
-- mind which kind of item it is.
CREATE TABLE ratings (
    user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    item_id  TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    stars    INTEGER NOT NULL CHECK (stars BETWEEN 1 AND 5),
    rated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX ratings_item ON ratings (item_id);
