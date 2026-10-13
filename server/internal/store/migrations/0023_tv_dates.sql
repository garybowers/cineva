-- The last of what TMDB returns for television.
--
-- next_air_date is the one with a shelf life: for a series still running it
-- is the only thing on a detail page that answers "when is the next one?",
-- and it is wrong the moment the episode airs — so it is refreshed by a
-- rescan like everything else, never trusted as a fact about the past.
--
-- original_title is stored only when it differs from the title. "Dark" is
-- German and "Money Heist" is "La Casa de Papel"; an English-language series
-- would otherwise carry its own name twice and show it on screen.

ALTER TABLE items ADD COLUMN last_air_date  TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN next_air_date  TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN original_title TEXT NOT NULL DEFAULT '';
ALTER TABLE items ADD COLUMN country        TEXT NOT NULL DEFAULT '';
