-- Sonarr and Radarr instances an admin has connected. Cineva registers a
-- webhook in each, so an import there triggers a rescan here within seconds
-- rather than at the next scheduled one.
--
-- api_key is the instance's own key, kept in the clear because Cineva has to
-- present it on every call; it is never returned by the API. The webhook's
-- secret is ours, and only its hash is kept. notification_id is the webhook's
-- id inside Sonarr/Radarr, so it can be updated or removed there; 0 until it
-- has been registered.

CREATE TABLE arr_connections (
    id              TEXT PRIMARY KEY,
    kind            TEXT NOT NULL CHECK (kind IN ('sonarr', 'radarr')),
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    api_key         TEXT NOT NULL,
    callback_url    TEXT NOT NULL,
    secret_hash     TEXT NOT NULL DEFAULT '',
    notification_id INTEGER NOT NULL DEFAULT 0,
    version         TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);
