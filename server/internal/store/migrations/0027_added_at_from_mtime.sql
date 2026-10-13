-- An item now counts as added when its file arrived (its modification time)
-- rather than when a scan first noticed it. Rows from before that change
-- carry the scan time, so a back catalogue imported in one long scan ranks
-- in the order the scanner happened to walk it — Z to A in Recently added.
-- Re-date them from the file, never later than the date already recorded.

UPDATE items SET added_at = datetime(file_mtime, 'unixepoch')
WHERE file_mtime > 0
  AND file_mtime < CAST(strftime('%s', added_at) AS INTEGER);
