-- Home Assistant (see docs/homeassistant.md): one connection per server,
-- the entities an admin has exposed and who may use them, the room each
-- device is in, and modes ("Movie mode") that users run.
--
-- The token is the instance's long-lived access token, kept in the clear
-- because Cineva presents it on every call; it is never returned by the
-- API. Home Assistant's own state, areas and registries are never stored:
-- they are held in memory while the websocket is open.

CREATE TABLE ha_connection (
    id              INTEGER PRIMARY KEY CHECK (id = 1),
    name            TEXT NOT NULL,
    url             TEXT NOT NULL,
    token           TEXT NOT NULL,
    playback_events INTEGER NOT NULL DEFAULT 0,
    version         TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at      TEXT NOT NULL DEFAULT (datetime('now'))
);

-- An exposed entity, open to everyone unless restricted. A row exists only
-- while the entity is exposed; hiding it deletes the row and its access.
CREATE TABLE ha_entities (
    entity_id  TEXT PRIMARY KEY,
    restricted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- A mode's steps are JSON (store.HAModeStep), validated by the API.
CREATE TABLE ha_modes (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    steps      TEXT NOT NULL DEFAULT '[]',
    restricted INTEGER NOT NULL DEFAULT 0,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

-- Who may use a restricted entity or run a restricted mode, as
-- library_access does for libraries. target_kind says which table
-- target_id names; server_id is '' for this server's users and groups.
CREATE TABLE ha_access (
    target_kind  TEXT NOT NULL CHECK (target_kind IN ('entity', 'mode')),
    target_id    TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('user', 'group')),
    principal_id TEXT NOT NULL,
    server_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (target_kind, target_id, kind, principal_id, server_id)
);

CREATE TRIGGER ha_access_user_deleted AFTER DELETE ON users BEGIN
    DELETE FROM ha_access WHERE kind = 'user' AND server_id = '' AND principal_id = OLD.id;
END;
CREATE TRIGGER ha_access_group_deleted AFTER DELETE ON user_groups BEGIN
    DELETE FROM ha_access WHERE kind = 'group' AND server_id = '' AND principal_id = OLD.id;
END;
CREATE TRIGGER ha_access_entity_deleted AFTER DELETE ON ha_entities BEGIN
    DELETE FROM ha_access WHERE target_kind = 'entity' AND target_id = OLD.entity_id;
END;
CREATE TRIGGER ha_access_mode_deleted AFTER DELETE ON ha_modes BEGIN
    DELETE FROM ha_access WHERE target_kind = 'mode' AND target_id = OLD.id;
END;

-- The Home Assistant area a device is in: its room.
ALTER TABLE sessions ADD COLUMN ha_area TEXT NOT NULL DEFAULT '';
