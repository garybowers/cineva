-- Per-user preferences, kept server-side.
--
-- Theme, playback quality, next-episode autoplay and music quality have all
-- lived in localStorage. That makes them per-browser rather than per-person:
-- the same account on a phone and a laptop disagrees, a cleared cache loses
-- them, and a native client could never see them at all.
--
-- A key/value table rather than columns: these are small, open-ended UI
-- preferences, and each new one should not need a migration. Values are
-- opaque strings — the client owns their meaning, the server only stores
-- them per user.
CREATE TABLE user_prefs (
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    key        TEXT NOT NULL,
    value      TEXT NOT NULL,
    updated_at TEXT NOT NULL DEFAULT (datetime('now')),
    PRIMARY KEY (user_id, key)
);
