-- Scanning in passes (server/docs/scanning.md): a big scan registers files
-- first and fetches metadata afterwards in a background job. The scan row
-- says which phase it is in and how far the fetching has got.
--   phase: registering (the walk and the rows), enriching (the lookups),
--          done
ALTER TABLE scans ADD COLUMN phase TEXT NOT NULL DEFAULT 'done';
ALTER TABLE scans ADD COLUMN enriched INTEGER NOT NULL DEFAULT 0;
ALTER TABLE scans ADD COLUMN to_enrich INTEGER NOT NULL DEFAULT 0;
