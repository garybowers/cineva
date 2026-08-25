-- Runtime settings changed from the admin UI (as opposed to cineva.yaml,
-- which needs a restart). Key/value; values are strings.

CREATE TABLE settings (
    key   TEXT PRIMARY KEY,
    value TEXT NOT NULL
);
