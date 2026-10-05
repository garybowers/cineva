-- Operator-managed collections.
--
-- Provider grouping (TMDB's belongs_to_collection) is a good default and a
-- poor final answer: it splits series the operator thinks of as one, misses
-- films it does not consider part of a set, and orders strictly by release
-- date when a viewer may want chronological or an opinionated order.
--
-- A managed collection either *overrides* a provider one (tmdb_id > 0) or is
-- wholly the operator's own (tmdb_id = 0). Overriding is materialising: the
-- provider's membership is copied in on first edit, after which the managed
-- rows are the truth and the provider's grouping for that id is ignored.
-- That keeps the common case zero-effort while making every part of it
-- editable, and means a later rescan cannot silently undo a manual change.
CREATE TABLE collections (
    id         TEXT PRIMARY KEY,
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    -- The provider collection this replaces, or 0 for a custom one.
    tmdb_id    INTEGER NOT NULL DEFAULT 0,
    name       TEXT NOT NULL,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- One override per provider collection per library; custom collections
-- (tmdb_id = 0) are exempt, since several may coexist.
CREATE UNIQUE INDEX idx_collections_override
    ON collections(library_id, tmdb_id) WHERE tmdb_id > 0;

-- Membership and the operator's chosen order. position is a plain integer
-- rewritten wholesale on reorder: these lists are a handful of films, so
-- there is nothing to gain from fractional ranks.
CREATE TABLE collection_items (
    collection_id TEXT NOT NULL REFERENCES collections(id) ON DELETE CASCADE,
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position      INTEGER NOT NULL,
    PRIMARY KEY (collection_id, item_id)
);
CREATE INDEX idx_collection_items_order ON collection_items(collection_id, position);
