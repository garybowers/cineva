-- Cast and crew.
--
-- People are stored once and shared by every film they appear in, keyed on
-- the provider's person id. That is the whole point of a separate table:
-- "other films in this library with this actor" becomes a join on
-- credits.person_id rather than a search by name, which would be both slow
-- and wrong wherever two people share a name.

CREATE TABLE people (
    id      TEXT PRIMARY KEY,
    tmdb_id INTEGER NOT NULL UNIQUE,
    name    TEXT NOT NULL,
    profile TEXT NOT NULL DEFAULT ''   -- /artwork/... headshot, when there is one
);
CREATE INDEX idx_people_name ON people(name);

CREATE TABLE credits (
    item_id   TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    person_id TEXT NOT NULL REFERENCES people(id) ON DELETE CASCADE,
    kind      TEXT NOT NULL CHECK (kind IN ('cast', 'crew')),
    role      TEXT NOT NULL DEFAULT '',  -- character for cast, job for crew
    ord       INTEGER NOT NULL DEFAULT 0, -- billing order; crew sorts by job
    PRIMARY KEY (item_id, person_id, kind, role)
);
CREATE INDEX idx_credits_person ON credits(person_id);
CREATE INDEX idx_credits_item ON credits(item_id, kind, ord);
