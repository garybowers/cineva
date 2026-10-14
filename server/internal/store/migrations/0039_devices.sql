-- A device is a screen, not a session (see docs/devices.md). Until now a
-- device was its browser session (0029), so everything about the screen
-- (its name, its Home Assistant room, its own preferences) went whenever
-- the session did: signing out, choosing another profile, a password
-- change, expiry. The living-room TV kept becoming a stranger.
--
-- A device now outlives its sessions. It is recognised by its own cookie,
-- whose secret is kept only as secret_hash ('' until the browser is given
-- one). A device holds one session at a time, and that session takes the
-- device's id, so every place that already keys on a session's public id
-- (presence, Play on, playbacks, Home Assistant rooms and modes, device
-- preferences) now means the screen without changing.
--
-- The play queue stays on the session: it is the person's, not the
-- screen's.

CREATE TABLE devices (
    id           TEXT PRIMARY KEY,
    secret_hash  TEXT NOT NULL DEFAULT '',
    name         TEXT NOT NULL DEFAULT '',
    client       TEXT NOT NULL DEFAULT '',
    client_ip    TEXT NOT NULL DEFAULT '',
    ha_area      TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL DEFAULT (datetime('now')),
    last_seen_at TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX devices_secret ON devices (secret_hash) WHERE secret_hash != '';

-- Every session becomes a device with the same id, so modes and device
-- preferences that name it keep working untouched.
INSERT INTO devices (id, name, client, client_ip, ha_area, created_at, last_seen_at)
SELECT id, name, client, client_ip, ha_area, created_at, last_seen_at FROM sessions;

ALTER TABLE sessions DROP COLUMN name;
ALTER TABLE sessions DROP COLUMN ha_area;

-- Device preferences belong to the device now, so signing out keeps them.
CREATE TABLE device_prefs_new (
    device_id  TEXT NOT NULL REFERENCES devices(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (device_id, key)
);
INSERT INTO device_prefs_new (device_id, key, value, updated_at)
SELECT device_id, key, value, updated_at FROM device_prefs
WHERE device_id IN (SELECT id FROM devices);
DROP TABLE device_prefs;
ALTER TABLE device_prefs_new RENAME TO device_prefs;

-- Forgetting a device signs it out. A session cannot carry a foreign key to
-- its device (the id column predates devices), so this is done by hand.
CREATE TRIGGER devices_forgotten AFTER DELETE ON devices BEGIN
    DELETE FROM sessions WHERE id = OLD.id;
END;

-- When a session ends, its device remembers when it was last used, which
-- is what forgetting unused devices goes by.
CREATE TRIGGER sessions_ended AFTER DELETE ON sessions BEGIN
    UPDATE devices SET last_seen_at = OLD.last_seen_at WHERE id = OLD.id;
END;
