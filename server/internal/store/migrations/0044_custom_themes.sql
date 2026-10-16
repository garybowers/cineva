-- Themes people make in Settings (see docs/themes.md). Built-in themes are
-- folders in the binary; these sit beside them with ids of the form
-- "c-<uuid>". tokens is a JSON object holding the full design-token set by
-- name without the dashes ("bg", "on-accent"). background is an /artwork/
-- path, "" for none; dim is how strongly the theme's background colour
-- covers it, in per cent. household 1 is an admin's share with everyone.
CREATE TABLE custom_themes (
    id          TEXT PRIMARY KEY,
    owner_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name        TEXT NOT NULL,
    appearance  TEXT NOT NULL CHECK (appearance IN ('dark', 'light')),
    tokens      TEXT NOT NULL,
    background  TEXT NOT NULL DEFAULT '',
    dim         INTEGER NOT NULL DEFAULT 70 CHECK (dim BETWEEN 0 AND 95),
    effect      TEXT NOT NULL DEFAULT '' CHECK (effect IN ('', 'snow', 'leaves', 'blossom')),
    household   INTEGER NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at  TEXT NOT NULL DEFAULT (datetime('now'))
);
CREATE INDEX custom_themes_owner ON custom_themes (owner_id);
