-- A user's picture on the profile picker and in the top bar: an uploaded
-- photo (a path under /artwork/), or else their initial on a colour they
-- chose. The colour is a theme token name ("accent", "chart-2", ...) rather
-- than a colour value, so every theme draws it in its own palette; '' is
-- the default gradient.

ALTER TABLE users ADD COLUMN avatar TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN avatar_colour TEXT NOT NULL DEFAULT '';
