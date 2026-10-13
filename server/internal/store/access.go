package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"
)

// LibraryAccess is who may see a library. An open library is visible to
// every user and its lists are ignored; a restricted one only to the users
// and groups named (and to admins, who see everything).
type LibraryAccess struct {
	Restricted bool     `json:"restricted"`
	UserIDs    []string `json:"userIds"`
	GroupIDs   []string `json:"groupIds"`
}

// UserGroup is a named set of this server's users ("Kids", "Grown-ups"),
// for restricting libraries without listing every account.
type UserGroup struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	MemberIDs []string `json:"memberIds"`
	CreatedAt string   `json:"createdAt"`
}

var ErrGroupNameUsed = errors.New("a group with that name already exists")

type AccessRepo interface {
	LibraryAccess(ctx context.Context, libraryID string) (LibraryAccess, error)
	SetLibraryAccess(ctx context.Context, libraryID string, acc LibraryAccess) error
	Groups(ctx context.Context) ([]UserGroup, error)
	CreateGroup(ctx context.Context, name string, memberIDs []string) (UserGroup, error)
	UpdateGroup(ctx context.Context, id, name string, memberIDs []string) (UserGroup, error)
	DeleteGroup(ctx context.Context, id string) error
}

var _ AccessRepo = (*Store)(nil)

// visibleLibraries lists the libraries a user can see. Only this server's
// own users and groups are matched for now (server_id = ”); federation adds
// visiting users of other servers.
func (s *Store) visibleLibraries(ctx context.Context, u User) ([]string, error) {
	query := `SELECT id FROM libraries`
	args := []any{}
	if !u.IsAdmin() {
		query += ` WHERE restricted = 0 OR EXISTS (
			SELECT 1 FROM library_access a
			WHERE a.library_id = libraries.id AND a.server_id = ''
			  AND ((a.kind = 'user' AND a.principal_id = ?)
			    OR (a.kind = 'group' AND a.principal_id IN
			        (SELECT group_id FROM user_group_members WHERE user_id = ?))))`
		args = append(args, u.ID, u.ID)
	}
	rows, err := s.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("visible libraries: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("visible libraries: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) LibraryAccess(ctx context.Context, libraryID string) (LibraryAccess, error) {
	acc := LibraryAccess{UserIDs: []string{}, GroupIDs: []string{}}
	err := s.db.QueryRowContext(ctx, `SELECT restricted FROM libraries WHERE id = ?`, libraryID).Scan(&acc.Restricted)
	if errors.Is(err, sql.ErrNoRows) {
		return LibraryAccess{}, ErrNotFound
	}
	if err != nil {
		return LibraryAccess{}, fmt.Errorf("library access %s: %w", libraryID, err)
	}
	rows, err := s.db.QueryContext(ctx,
		`SELECT kind, principal_id FROM library_access WHERE library_id = ? AND server_id = '' ORDER BY kind, principal_id`, libraryID)
	if err != nil {
		return LibraryAccess{}, fmt.Errorf("library access %s: %w", libraryID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var kind, id string
		if err := rows.Scan(&kind, &id); err != nil {
			return LibraryAccess{}, fmt.Errorf("library access %s: %w", libraryID, err)
		}
		if kind == "user" {
			acc.UserIDs = append(acc.UserIDs, id)
		} else {
			acc.GroupIDs = append(acc.GroupIDs, id)
		}
	}
	return acc, rows.Err()
}

// SetLibraryAccess replaces who may see a library. The lists are kept when
// a library is opened, so restricting it again restores them.
func (s *Store) SetLibraryAccess(ctx context.Context, libraryID string, acc LibraryAccess) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("set library access: %w", err)
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, `UPDATE libraries SET restricted = ? WHERE id = ?`, acc.Restricted, libraryID)
	if err != nil {
		return fmt.Errorf("set library access: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM library_access WHERE library_id = ? AND server_id = ''`, libraryID); err != nil {
		return fmt.Errorf("clear library access: %w", err)
	}
	add := func(kind, table string, ids []string) error {
		for _, id := range ids {
			// Checked here rather than by a foreign key, which principal_id
			// cannot have: an unknown id would otherwise sit in the list
			// granting nothing and confusing everyone.
			var ok int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE id = ?`, id).Scan(&ok); err != nil {
				return fmt.Errorf("check %s %s: %w", kind, id, err)
			}
			if ok == 0 {
				return fmt.Errorf("%w: no %s %s", ErrNotFound, kind, id)
			}
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO library_access (library_id, kind, principal_id) VALUES (?, ?, ?)`,
				libraryID, kind, id); err != nil {
				return fmt.Errorf("add %s %s: %w", kind, id, err)
			}
		}
		return nil
	}
	if err := add("user", "users", acc.UserIDs); err != nil {
		return err
	}
	if err := add("group", "user_groups", acc.GroupIDs); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *Store) Groups(ctx context.Context) ([]UserGroup, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, name, created_at FROM user_groups ORDER BY name COLLATE NOCASE`)
	if err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	out := []UserGroup{}
	for rows.Next() {
		g := UserGroup{MemberIDs: []string{}}
		if err := rows.Scan(&g.ID, &g.Name, &g.CreatedAt); err != nil {
			rows.Close()
			return nil, fmt.Errorf("groups: %w", err)
		}
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("groups: %w", err)
	}
	for i := range out {
		if out[i].MemberIDs, err = s.groupMembers(ctx, out[i].ID); err != nil {
			return nil, err
		}
	}
	return out, nil
}

func (s *Store) groupMembers(ctx context.Context, groupID string) ([]string, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT user_id FROM user_group_members WHERE group_id = ? ORDER BY user_id`, groupID)
	if err != nil {
		return nil, fmt.Errorf("group members: %w", err)
	}
	defer rows.Close()
	out := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("group members: %w", err)
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

func (s *Store) group(ctx context.Context, id string) (UserGroup, error) {
	g := UserGroup{}
	err := s.db.QueryRowContext(ctx, `SELECT id, name, created_at FROM user_groups WHERE id = ?`, id).
		Scan(&g.ID, &g.Name, &g.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return UserGroup{}, ErrNotFound
	}
	if err != nil {
		return UserGroup{}, fmt.Errorf("group %s: %w", id, err)
	}
	g.MemberIDs, err = s.groupMembers(ctx, id)
	return g, err
}

func (s *Store) CreateGroup(ctx context.Context, name string, memberIDs []string) (UserGroup, error) {
	return s.saveGroup(ctx, uuid.NewString(), name, memberIDs, true)
}

func (s *Store) UpdateGroup(ctx context.Context, id, name string, memberIDs []string) (UserGroup, error) {
	return s.saveGroup(ctx, id, name, memberIDs, false)
}

// saveGroup writes a group's name and replaces its members.
func (s *Store) saveGroup(ctx context.Context, id, name string, memberIDs []string, create bool) (UserGroup, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return UserGroup{}, errors.New("a group needs a name")
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return UserGroup{}, fmt.Errorf("save group: %w", err)
	}
	defer tx.Rollback()
	var res interface{ RowsAffected() (int64, error) }
	if create {
		res, err = tx.ExecContext(ctx, `INSERT INTO user_groups (id, name) VALUES (?, ?)`, id, name)
	} else {
		res, err = tx.ExecContext(ctx, `UPDATE user_groups SET name = ? WHERE id = ?`, name, id)
	}
	if err != nil {
		if strings.Contains(err.Error(), "UNIQUE") {
			return UserGroup{}, ErrGroupNameUsed
		}
		return UserGroup{}, fmt.Errorf("save group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return UserGroup{}, ErrNotFound
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM user_group_members WHERE group_id = ?`, id); err != nil {
		return UserGroup{}, fmt.Errorf("clear group members: %w", err)
	}
	for _, uid := range memberIDs {
		if _, err := tx.ExecContext(ctx,
			`INSERT OR IGNORE INTO user_group_members (group_id, user_id) VALUES (?, ?)`, id, uid); err != nil {
			return UserGroup{}, fmt.Errorf("add member %s: %w", uid, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return UserGroup{}, fmt.Errorf("save group: %w", err)
	}
	return s.group(ctx, id)
}

func (s *Store) DeleteGroup(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM user_groups WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete group: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}
