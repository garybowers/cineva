-- Intro and credits set by hand. An administrator can correct a detection,
-- mark a segment detection missed, or say an episode has none; any of those
-- locks the episode's analysis for that kind, so a re-detection of its
-- season — or a replaced file — leaves the decision alone until the
-- administrator clears it, as locked metadata fields survive a rescan.
--
-- The lock lives on segment_analysis rather than media_segments because
-- "this episode has no intro" is a decision too, and has no segment row.
ALTER TABLE segment_analysis ADD COLUMN locked INTEGER NOT NULL DEFAULT 0;
