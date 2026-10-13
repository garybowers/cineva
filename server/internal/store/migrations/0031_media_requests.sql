-- Requests: a user asks for a film or series that is not in the library, and
-- Cineva hands it to Radarr or Sonarr — straight away for users trusted to
-- ("auto"), after an admin approves it for the rest ("approval"), or not at
-- all ("none", which also hides the request screens). Admins are always
-- treated as "auto" whatever their row says. New accounts take the server's
-- default (setting requests.default_level); existing ones start at
-- "approval", which sends nothing anywhere without an admin's say-so.

ALTER TABLE users ADD COLUMN request_level TEXT NOT NULL DEFAULT 'approval'
    CHECK (request_level IN ('none', 'approval', 'auto'));

-- What a request to an instance is made with: the quality profile and root
-- folder chosen from the instance's own lists (0 and '' until an admin picks,
-- when the instance's first is used). path_maps translates the instance's
-- paths into Cineva's, as JSON [{"from": "/tv", "to": "/mnt/media/TV"}], so a
-- webhook event can rescan the one library that holds the file. last_event*
-- is the most recent webhook delivery, for the connection's status line.
ALTER TABLE arr_connections ADD COLUMN quality_profile_id INTEGER NOT NULL DEFAULT 0;
ALTER TABLE arr_connections ADD COLUMN root_folder TEXT NOT NULL DEFAULT '';
ALTER TABLE arr_connections ADD COLUMN path_maps TEXT NOT NULL DEFAULT '[]';
ALTER TABLE arr_connections ADD COLUMN last_event TEXT NOT NULL DEFAULT '';
ALTER TABLE arr_connections ADD COLUMN last_event_title TEXT NOT NULL DEFAULT '';
ALTER TABLE arr_connections ADD COLUMN last_event_at TEXT NOT NULL DEFAULT '';

-- One request: a film, a whole series, some of its seasons, or single
-- episodes of a series already held. Title, year and artwork are copied from
-- TMDB when it is made, so the queue reads without a TMDB call per row.
--
-- status runs requested (waiting for an admin, or for an instance that could
-- not be reached) -> approved (sent) -> downloading (the instance grabbed a
-- release) -> available (a scan found it in a library), or declined. seasons
-- and episodes are JSON: [] seasons and [] episodes is the whole series.
-- arr_id is the film's or series' id inside the instance once sent; tvdb_id
-- is what Sonarr knows a series by.
CREATE TABLE media_requests (
    id                 TEXT PRIMARY KEY,
    user_id            TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_type         TEXT NOT NULL CHECK (media_type IN ('movie', 'tv')),
    tmdb_id            INTEGER NOT NULL,
    tvdb_id            INTEGER NOT NULL DEFAULT 0,
    title              TEXT NOT NULL,
    year               INTEGER NOT NULL DEFAULT 0,
    poster             TEXT NOT NULL DEFAULT '',
    backdrop           TEXT NOT NULL DEFAULT '',
    seasons            TEXT NOT NULL DEFAULT '[]',
    episodes           TEXT NOT NULL DEFAULT '[]',
    status             TEXT NOT NULL DEFAULT 'requested'
        CHECK (status IN ('requested', 'approved', 'downloading', 'available', 'declined')),
    connection_id      TEXT NOT NULL DEFAULT '',
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    root_folder        TEXT NOT NULL DEFAULT '',
    arr_id             INTEGER NOT NULL DEFAULT 0,
    note               TEXT NOT NULL DEFAULT '',
    error              TEXT NOT NULL DEFAULT '',
    decided_by         TEXT NOT NULL DEFAULT '',
    decided_at         TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE INDEX media_requests_status ON media_requests (status, created_at);
CREATE INDEX media_requests_user ON media_requests (user_id, created_at);
CREATE INDEX media_requests_title ON media_requests (media_type, tmdb_id);
