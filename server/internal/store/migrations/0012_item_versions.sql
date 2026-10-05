-- Multi-version movies: the same film held twice at different qualities, or
-- as a remux beside an encode. Each file is still its own row — it has its
-- own path, codecs and size — but only the primary appears in the library
-- grid, with the others hanging off it as alternative versions.
--
-- Deliberately not a foreign key: version_of is re-derived from scratch on
-- every scan (see GroupVersions), so a stale value cannot outlive a scan,
-- and a plain column avoids an ON DELETE rule that would have to be kept in
-- step with pruning.

ALTER TABLE items ADD COLUMN version_of TEXT NOT NULL DEFAULT '';
CREATE INDEX idx_items_version_of ON items(version_of);
