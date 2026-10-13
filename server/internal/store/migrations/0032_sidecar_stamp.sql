-- A fingerprint of the sidecar files a scan reads beside each video — the
-- .nfo, local poster and fanart, external subtitles — so an incremental scan
-- notices one being edited, added or removed next to a video that has not
-- changed. Without it the video's own size and mtime were the whole change
-- test, and a corrected .nfo waited for a full rescan.
--
-- The value is a hash of each sidecar's name, size and modification time,
-- taken from the directory walk the scan does anyway. 0 means not recorded:
-- every row until its first scan after this migration, which adopts the
-- current fingerprint rather than rereading every file that has sidecars,
-- and items that are not video files at all.

ALTER TABLE items ADD COLUMN sidecar_stamp INTEGER NOT NULL DEFAULT 0;
