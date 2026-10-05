-- Content rating (certification): "15", "PG-13", "R". Distinct from the
-- provider score already in `rating`, which is an audience average — this
-- is the classification board's age rating, and it is what someone choosing
-- something for a child actually needs.
--
-- Stored as free text because the scale is per-country (BBFC "12A", MPAA
-- "PG-13", FSK "16") and normalising them into one vocabulary would lose
-- information no one asked us to throw away.
ALTER TABLE items ADD COLUMN certification TEXT NOT NULL DEFAULT '';
