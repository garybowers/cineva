-- Lidarr joins Sonarr and Radarr: what it imports is scanned into the music
-- libraries within seconds, as for films and series. The kind's CHECK has
-- to widen, and SQLite cannot alter one, so the table is rebuilt with the
-- same columns. Nothing references it.

CREATE TABLE arr_connections_new (
    id                 TEXT PRIMARY KEY,
    kind               TEXT NOT NULL CHECK (kind IN ('sonarr', 'radarr', 'lidarr')),
    name               TEXT NOT NULL,
    url                TEXT NOT NULL,
    api_key            TEXT NOT NULL,
    callback_url       TEXT NOT NULL,
    secret_hash        TEXT NOT NULL DEFAULT '',
    notification_id    INTEGER NOT NULL DEFAULT 0,
    version            TEXT NOT NULL DEFAULT '',
    created_at         TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at         TEXT NOT NULL DEFAULT (datetime('now')),
    quality_profile_id INTEGER NOT NULL DEFAULT 0,
    root_folder        TEXT NOT NULL DEFAULT '',
    path_maps          TEXT NOT NULL DEFAULT '[]',
    last_event         TEXT NOT NULL DEFAULT '',
    last_event_title   TEXT NOT NULL DEFAULT '',
    last_event_at      TEXT NOT NULL DEFAULT ''
);
INSERT INTO arr_connections_new (id, kind, name, url, api_key, callback_url, secret_hash, notification_id,
    version, created_at, updated_at, quality_profile_id, root_folder, path_maps, last_event, last_event_title, last_event_at)
SELECT id, kind, name, url, api_key, callback_url, secret_hash, notification_id,
    version, created_at, updated_at, quality_profile_id, root_folder, path_maps, last_event, last_event_title, last_event_at
FROM arr_connections;
DROP TABLE arr_connections;
ALTER TABLE arr_connections_new RENAME TO arr_connections;
