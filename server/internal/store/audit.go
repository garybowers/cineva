package store

import (
	"context"
	"fmt"
)

// AuditEntry is one line of the who-did-what-when trail. Action is a dotted
// verb ("library.create", "scan.start", "play.start", "download") so the UI
// can filter by prefix; Target names the thing acted on.
type AuditEntry struct {
	ID          int64  `json:"id"`
	At          string `json:"at"`
	UserID      string `json:"userId"`
	Action      string `json:"action"`
	TargetKind  string `json:"targetKind,omitempty"`
	TargetID    string `json:"targetId,omitempty"`
	TargetTitle string `json:"targetTitle,omitempty"`
	Detail      string `json:"detail,omitempty"`
	ClientIP    string `json:"clientIp,omitempty"`
	Client      string `json:"client,omitempty"` // "Firefox · Linux"
}

type AuditRepo interface {
	RecordAudit(ctx context.Context, e AuditEntry) error
	AuditLog(ctx context.Context, limit, offset int, action string) ([]AuditEntry, error)
}

var _ AuditRepo = (*Store)(nil)

func (s *Store) RecordAudit(ctx context.Context, e AuditEntry) error {
	if _, err := s.db.ExecContext(ctx, `INSERT INTO audit_log
		(user_id, action, target_kind, target_id, target_title, detail, client_ip, client)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		e.UserID, e.Action, e.TargetKind, e.TargetID, e.TargetTitle, e.Detail, e.ClientIP, e.Client); err != nil {
		return fmt.Errorf("record audit: %w", err)
	}
	return nil
}

// AuditLog returns entries newest first; action filters by prefix ("scan."
// matches scan.start and scan.finish), empty for all.
func (s *Store) AuditLog(ctx context.Context, limit, offset int, action string) ([]AuditEntry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `SELECT id, at, user_id, action, target_kind, target_id,
		target_title, detail, client_ip, client FROM audit_log
		WHERE (? = '' OR action LIKE ? || '%') ORDER BY id DESC LIMIT ? OFFSET ?`,
		action, action, limit, offset)
	if err != nil {
		return nil, fmt.Errorf("audit log: %w", err)
	}
	defer rows.Close()
	out := []AuditEntry{}
	for rows.Next() {
		var e AuditEntry
		if err := rows.Scan(&e.ID, &e.At, &e.UserID, &e.Action, &e.TargetKind, &e.TargetID,
			&e.TargetTitle, &e.Detail, &e.ClientIP, &e.Client); err != nil {
			return nil, fmt.Errorf("audit row: %w", err)
		}
		out = append(out, e)
	}
	return out, rows.Err()
}
