-- Films and series the metadata provider could not match are retried on a
-- slow cadence, since an incremental scan never looks at them again while
-- their files are unchanged. match_attempts counts the lookups that found
-- nothing and match_tried_at (Unix seconds) is when the last one ran; the
-- wait before the next grows with the count, from a day to about a month,
-- so a title the provider will never know stops costing a lookup a day.
--
-- Both stay 0 for anything matched, and for rows from before this migration
-- until their next failed lookup — which makes an unmatched title due for
-- a retry straight away.

ALTER TABLE items ADD COLUMN match_attempts INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN match_tried_at INTEGER NOT NULL DEFAULT 0;
