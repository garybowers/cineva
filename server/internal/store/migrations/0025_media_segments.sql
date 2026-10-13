-- Parts of an item that are not the item itself — an episode's title
-- sequence, and later its end credits — found by comparing episodes'
-- audio (see internal/segments). One row per item and kind: the player
-- offers "Skip intro" while playback is inside it.
CREATE TABLE media_segments (
    item_id    TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind       TEXT NOT NULL CHECK (kind IN ('intro', 'credits')),
    start_secs REAL NOT NULL,
    end_secs   REAL NOT NULL,
    PRIMARY KEY (item_id, kind)
);

-- Which items have been analysed, and the file as it was when they were:
-- an episode with no intro has no segment row, and without this it would
-- be decoded again on every pass. A changed file (file_mtime no longer
-- matching the item's) is analysed afresh.
CREATE TABLE segment_analysis (
    item_id     TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    kind        TEXT NOT NULL,
    file_mtime  INTEGER NOT NULL,
    analysed_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (item_id, kind)
);
