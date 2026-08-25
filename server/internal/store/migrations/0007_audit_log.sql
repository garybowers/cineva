-- Audit trail for the admin Logs page: who did what, when, to which item,
-- from which client. Append-only.

CREATE TABLE audit_log (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    at           TEXT NOT NULL DEFAULT (datetime('now')),
    user_id      TEXT NOT NULL,
    action       TEXT NOT NULL,
    target_kind  TEXT NOT NULL DEFAULT '',
    target_id    TEXT NOT NULL DEFAULT '',
    target_title TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    client_ip    TEXT NOT NULL DEFAULT '',
    client       TEXT NOT NULL DEFAULT ''
);

CREATE INDEX audit_log_at ON audit_log (at);
