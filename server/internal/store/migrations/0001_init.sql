-- Initial schema. Bakes in the load-bearing decisions from CLAUDE.md:
-- the server's federation identity, typed media domains, and the
-- library-source abstraction (local path vs federated peer).

CREATE TABLE server_identity (
    id          INTEGER PRIMARY KEY CHECK (id = 1),
    public_key  BLOB NOT NULL,
    private_key BLOB NOT NULL,
    created_at  TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE libraries (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE,
    -- One media type per library, chosen at creation, immutable afterwards.
    media_type TEXT NOT NULL CHECK (media_type IN ('movies', 'tv', 'music', 'audiobooks', 'podcasts', 'books')),
    -- Where the library's content comes from: this server's disk, or a
    -- federated peer (milestone 10). peer_id stays NULL for local.
    source     TEXT NOT NULL DEFAULT 'local' CHECK (source IN ('local', 'federated')),
    peer_id    TEXT,
    created_at TEXT NOT NULL DEFAULT (datetime('now')),
    updated_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE library_paths (
    id         INTEGER PRIMARY KEY AUTOINCREMENT,
    library_id TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    path       TEXT NOT NULL,
    UNIQUE (library_id, path)
);
