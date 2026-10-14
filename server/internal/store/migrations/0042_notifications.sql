-- Where notifications go (see docs/notifications.md). owner_id '' is a
-- household destination the admin set up; otherwise it is that user's own.
-- config holds the kind's settings as JSON, secrets included in the clear
-- because they are presented on every send; the API never returns them
-- whole. events is the JSON list of event ids it receives; library_ids, for
-- a household destination's "new in the library" messages, the libraries
-- it may name ([] for those open to everyone).
CREATE TABLE notify_destinations (
    id            TEXT PRIMARY KEY,
    owner_id      TEXT NOT NULL DEFAULT '',
    kind          TEXT NOT NULL CHECK (kind IN ('ntfy', 'gotify', 'discord', 'telegram', 'webhook')),
    name          TEXT NOT NULL,
    config        TEXT NOT NULL DEFAULT '{}',
    events        TEXT NOT NULL DEFAULT '[]',
    library_ids   TEXT NOT NULL DEFAULT '[]',
    enabled       INTEGER NOT NULL DEFAULT 1,
    last_sent_at  TEXT NOT NULL DEFAULT '',
    last_error    TEXT NOT NULL DEFAULT '',
    last_error_at TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at    TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX notify_destinations_owner ON notify_destinations (owner_id);

CREATE TRIGGER notify_destinations_user_deleted AFTER DELETE ON users BEGIN
    DELETE FROM notify_destinations WHERE owner_id = OLD.id;
END;
