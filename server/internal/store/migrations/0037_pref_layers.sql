-- Preferences in layers: the admin's defaults, and values that belong to a
-- device rather than a person.
--
-- user_prefs (0018) stays the user's own layer. Until now it was the only
-- one, so an admin could not say "English audio for the household" or
-- "Kids get subtitles, and cannot turn them off": the server did not know
-- what preferences existed, let alone hold a default for one. The registry
-- of what exists lives in code (internal/prefs); these tables hold the
-- values set at each layer, still as strings, with the registry saying what
-- they mean. See docs/personalisation.md.
--
-- pref_defaults is the admin's layers: the server's default for a key
-- (owner_kind 'server', owner_id '') and each group's. locked stops the
-- user's and the device's own values from applying: a lock is how a
-- setting that really is the admin's to decide is kept that way.
CREATE TABLE pref_defaults (
    owner_kind TEXT NOT NULL CHECK (owner_kind IN ('server', 'group')),
    owner_id   TEXT NOT NULL DEFAULT '',
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    locked     INTEGER NOT NULL DEFAULT 0,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (owner_kind, owner_id, key)
);

-- owner_id cannot carry a foreign key (the server's row has none to point
-- at), so a group's defaults go with it by hand, as library_access does.
CREATE TRIGGER pref_defaults_group_deleted AFTER DELETE ON user_groups BEGIN
    DELETE FROM pref_defaults WHERE owner_kind = 'group' AND owner_id = OLD.id;
END;

-- A device's own values, for the few preferences that belong to the screen
-- rather than the person: video quality on the living-room TV is not the
-- quality on a phone on mobile data. A device is a session (0029); signing
-- it out forgets them.
CREATE TABLE device_prefs (
    device_id  TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (device_id, key)
);

-- Which group's default wins when a user is in several that set the same
-- key: the lowest priority number first, so "Kids" can sit above
-- "Grown-ups" for a child who is in both. Existing groups start in name
-- order, which is how they have been listed until now.
ALTER TABLE user_groups ADD COLUMN priority INTEGER NOT NULL DEFAULT 0;
UPDATE user_groups SET priority = (
    SELECT COUNT(*) FROM user_groups g WHERE g.name < user_groups.name COLLATE NOCASE
);
