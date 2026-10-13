-- Television fields TMDB has always returned and we never stored.
--
-- status ("Ended", "Returning Series", "Canceled") is kept as TMDB words it
-- rather than reduced to a boolean: "Canceled" and "Ended" mean different
-- things to someone deciding whether to start a series.
--
-- air_date matters most. An episode's only date is the day it was broadcast,
-- and without it two thirds of this library's episodes had no date at all
-- and nothing correct to sort by. It is a full date where `year` is only a
-- year, and applies to seasons as well.
--
-- season_count and episode_count are what TMDB says exists, not what is on
-- disk. The difference is the point: it lets a series page say "3 of 5
-- seasons" instead of implying the library is complete.

ALTER TABLE items ADD COLUMN status        TEXT    NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN network       TEXT    NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN air_date      TEXT    NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN season_count  INTEGER NOT NULL DEFAULT 0;
ALTER TABLE items ADD COLUMN episode_count INTEGER NOT NULL DEFAULT 0;

-- Episodes sort by broadcast date in a "recently aired" view, and the date
-- is also how a season's span is worked out.
CREATE INDEX items_air_date ON items (air_date) WHERE air_date != '';
