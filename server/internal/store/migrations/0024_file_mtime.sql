-- When each item's file was last modified, as Unix seconds, so a rescan can
-- tell an unchanged file from a changed one without reading it. Together
-- with size_bytes it is the whole change test: both equal means the file is
-- skipped, and re-reading and re-matching thousands of untouched files is
-- what made every scan as slow as the first.
--
-- 0 means not yet recorded (every row, until its first scan after this
-- migration) and for items that are not files at all — a series, a season,
-- an artist.

ALTER TABLE items ADD COLUMN file_mtime INTEGER NOT NULL DEFAULT 0;
