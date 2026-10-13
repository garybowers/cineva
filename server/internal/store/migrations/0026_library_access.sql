-- Libraries are open to every user by default, and an admin can restrict one
-- to chosen users and groups. This replaces the per-user grants of 0008,
-- which were closed by default: a new user saw nothing until someone ticked
-- every library for them, and "the kids see these, everyone sees the rest"
-- meant ticking boxes on every account.
--
-- Groups belong to this server ("Kids", "Grown-ups"). With federation (see
-- docs/federation.md) a restricted library's list can also name users and
-- groups of other servers in the nebula; server_id says whose they are,
-- and '' is this server.

ALTER TABLE libraries ADD COLUMN restricted INTEGER NOT NULL DEFAULT 0;

CREATE TABLE user_groups (
    id         TEXT PRIMARY KEY,
    name       TEXT NOT NULL UNIQUE COLLATE NOCASE,
    created_at TEXT NOT NULL DEFAULT (datetime('now'))
);

CREATE TABLE user_group_members (
    group_id TEXT NOT NULL REFERENCES user_groups(id) ON DELETE CASCADE,
    user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY (group_id, user_id)
);
CREATE INDEX user_group_members_user ON user_group_members (user_id);

-- Who may see a restricted library. Ignored while the library is open. A
-- restricted library with no rows is visible to admins only.
CREATE TABLE library_access (
    library_id   TEXT NOT NULL REFERENCES libraries(id) ON DELETE CASCADE,
    kind         TEXT NOT NULL CHECK (kind IN ('user', 'group')),
    principal_id TEXT NOT NULL,
    server_id    TEXT NOT NULL DEFAULT '',
    PRIMARY KEY (library_id, kind, principal_id, server_id)
);

-- principal_id cannot carry a foreign key (it names a user or a group, and
-- later ones on other servers), so local deletions are cascaded by hand.
CREATE TRIGGER library_access_user_deleted AFTER DELETE ON users BEGIN
    DELETE FROM library_access WHERE kind = 'user' AND server_id = '' AND principal_id = OLD.id;
END;
CREATE TRIGGER library_access_group_deleted AFTER DELETE ON user_groups BEGIN
    DELETE FROM library_access WHERE kind = 'group' AND server_id = '' AND principal_id = OLD.id;
END;

-- Carry the old grants over without changing what anyone can see: a library
-- every regular user was granted stays open, and any other is restricted to
-- exactly the users who had it.
UPDATE libraries SET restricted = 1
WHERE EXISTS (
    SELECT 1 FROM users u
    WHERE u.role = 'user'
      AND NOT EXISTS (SELECT 1 FROM library_grants g WHERE g.user_id = u.id AND g.library_id = libraries.id)
);
INSERT INTO library_access (library_id, kind, principal_id)
SELECT g.library_id, 'user', g.user_id
FROM library_grants g JOIN libraries l ON l.id = g.library_id
WHERE l.restricted = 1;

DROP TABLE library_grants;
