-- play_events recorded only that a play started, which answers "how many
-- plays yesterday?" but not "did anyone actually watch it?". A start and an
-- abandoned start looked identical on the dashboard.
--
-- secs is wall-clock time the playback was open, not playhead movement: a
-- viewer who seeks about has still spent that long watching, and it is the
-- honest answer to "how much was this server used". completed records
-- whether the session crossed the watched threshold.
--
-- Existing rows keep secs = 0 and completed = 0 and ended_at NULL: that is
-- truthful, since nothing was measured for them.

ALTER TABLE play_events ADD COLUMN ended_at  TEXT;
ALTER TABLE play_events ADD COLUMN secs      INTEGER NOT NULL DEFAULT 0;
ALTER TABLE play_events ADD COLUMN completed INTEGER NOT NULL DEFAULT 0;
