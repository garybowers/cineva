-- Per-user playback state. Until auth lands (milestone 4) everything uses the
-- 'local' user; the schema is per-user from day one so accounts bolt on.

CREATE TABLE play_state (
    user_id       TEXT NOT NULL DEFAULT 'local',
    item_id       TEXT NOT NULL REFERENCES items(id) ON DELETE CASCADE,
    position_secs INTEGER NOT NULL DEFAULT 0,
    watched       INTEGER NOT NULL DEFAULT 0,
    updated_at    TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, item_id)
);
CREATE INDEX idx_play_state_recent ON play_state(user_id, updated_at);
