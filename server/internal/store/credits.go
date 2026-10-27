package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/google/uuid"
)

// Person is someone credited on an item. Role carries the character for
// cast and the job for crew.
type Person struct {
	ID      string `json:"id"`
	TmdbID  int    `json:"tmdbId"`
	Name    string `json:"name"`
	Profile string `json:"profile,omitempty"`
	Role    string `json:"role,omitempty"`
	Kind    string `json:"kind,omitempty"` // "cast" | "crew"
	Order   int    `json:"-"`
}

type CreditRepo interface {
	ReplaceCredits(ctx context.Context, itemID string, cast, crew []Person) error
	CreditsForItem(ctx context.Context, itemID string) (cast, crew []Person, err error)
	Person(ctx context.Context, id string) (Person, error)
	ItemsForPerson(ctx context.Context, personID string) ([]Item, error)
}

var _ CreditRepo = (*Store)(nil)

// upsertPerson returns the row id for a provider person, inserting them the
// first time they are seen and refreshing the name and headshot after that.
func upsertPerson(ctx context.Context, tx dbtx, p Person) (string, error) {
	var id string
	err := tx.QueryRowContext(ctx, `SELECT id FROM people WHERE tmdb_id = ?`, p.TmdbID).Scan(&id)
	switch {
	case errors.Is(err, sql.ErrNoRows):
		id = uuid.NewString()
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO people (id, tmdb_id, name, profile) VALUES (?, ?, ?, ?)`,
			id, p.TmdbID, p.Name, p.Profile); err != nil {
			return "", fmt.Errorf("insert person %q: %w", p.Name, err)
		}
	case err != nil:
		return "", fmt.Errorf("find person %q: %w", p.Name, err)
	default:
		// A later film may carry a headshot the first one lacked.
		if _, err := tx.ExecContext(ctx,
			`UPDATE people SET name = ?, profile = CASE WHEN ? != '' THEN ? ELSE profile END WHERE id = ?`,
			p.Name, p.Profile, p.Profile, id); err != nil {
			return "", fmt.Errorf("update person %q: %w", p.Name, err)
		}
	}
	return id, nil
}

// ReplaceCredits sets an item's cast and crew, replacing what it had.
// People themselves are never deleted: another film may still credit them.
func (s *Store) ReplaceCredits(ctx context.Context, itemID string, cast, crew []Person) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("replace credits: %w", err)
	}
	defer tx.Rollback()
	if err := replaceCredits(ctx, tx, itemID, cast, crew); err != nil {
		return err
	}
	return tx.Commit()
}

// replaceCredits is ReplaceCredits inside a transaction the caller holds.
func replaceCredits(ctx context.Context, tx dbtx, itemID string, cast, crew []Person) error {
	if _, err := tx.ExecContext(ctx, `DELETE FROM credits WHERE item_id = ?`, itemID); err != nil {
		return fmt.Errorf("clear credits: %w", err)
	}
	add := func(people []Person, kind string) error {
		for i, p := range people {
			if p.TmdbID == 0 || p.Name == "" {
				continue
			}
			pid, err := upsertPerson(ctx, tx, p)
			if err != nil {
				return err
			}
			// Order is meaningful for both: billing for cast, job rank
			// for crew. The slice index only breaks ties.
			ord := p.Order*100 + i
			if _, err := tx.ExecContext(ctx,
				`INSERT OR IGNORE INTO credits (item_id, person_id, kind, role, ord)
				 VALUES (?, ?, ?, ?, ?)`, itemID, pid, kind, p.Role, ord); err != nil {
				return fmt.Errorf("insert credit: %w", err)
			}
		}
		return nil
	}
	if err := add(cast, "cast"); err != nil {
		return err
	}
	return add(crew, "crew")
}

func (s *Store) creditsOfKind(ctx context.Context, itemID, kind string) ([]Person, error) {
	rows, err := s.db.QueryContext(ctx, `
		SELECT p.id, p.tmdb_id, p.name, p.profile, c.role, c.ord
		FROM credits c JOIN people p ON p.id = c.person_id
		WHERE c.item_id = ? AND c.kind = ?
		ORDER BY c.ord, p.name`, itemID, kind)
	if err != nil {
		return nil, fmt.Errorf("credits for %s: %w", itemID, err)
	}
	defer rows.Close()
	out := []Person{}
	for rows.Next() {
		var p Person
		if err := rows.Scan(&p.ID, &p.TmdbID, &p.Name, &p.Profile, &p.Role, &p.Order); err != nil {
			return nil, fmt.Errorf("scan credit: %w", err)
		}
		p.Kind = kind
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *Store) CreditsForItem(ctx context.Context, itemID string) ([]Person, []Person, error) {
	cast, err := s.creditsOfKind(ctx, itemID, "cast")
	if err != nil {
		return nil, nil, err
	}
	crew, err := s.creditsOfKind(ctx, itemID, "crew")
	if err != nil {
		return nil, nil, err
	}
	return cast, crew, nil
}

func (s *Store) Person(ctx context.Context, id string) (Person, error) {
	var p Person
	err := s.db.QueryRowContext(ctx,
		`SELECT id, tmdb_id, name, profile FROM people WHERE id = ?`, id).
		Scan(&p.ID, &p.TmdbID, &p.Name, &p.Profile)
	if err != nil {
		return Person{}, fmt.Errorf("person %s: %w", id, err)
	}
	return p, nil
}

// ItemsForPerson lists the films in the library this person is credited on
// — the "more with this actor" row. Alternative versions are excluded so a
// film held twice appears once, and a person credited both as cast and crew
// on the same film yields one row.
func (s *Store) ItemsForPerson(ctx context.Context, personID string) ([]Item, error) {
	return s.queryItems(ctx,
		`SELECT DISTINCT `+itemColumns("i")+` FROM items i
		 JOIN credits c ON c.item_id = i.id
		 WHERE c.person_id = ? AND i.version_of = ''
		 ORDER BY i.year DESC, i.title`, personID)
}

// PersonCredit is one film in a person's filmography, with what they did on
// it. A person can be both cast and crew on the same film (acting in one
// they directed), which is two credits — and worth showing as two, since
// that is the interesting fact.
type PersonCredit struct {
	Item
	// CreditKind is "cast" or "crew". Named apart from Item.Kind, which is
	// the media kind (movie, episode).
	CreditKind string `json:"creditKind"`
	// Role is the character for cast, the job for crew. Several roles on one
	// film are joined ("Writer · Director").
	Role string `json:"role,omitempty"`
	// Billing is the cast order, so a lead sorts above a bit part when two
	// films share a year. Crew rows carry the job's rank.
	Billing int `json:"-"`
}

// PersonProfile is a person plus the shape of their filmography in this
// library — enough for a page rather than a list.
type PersonProfile struct {
	Person
	Credits []PersonCredit `json:"credits"`
	// Counts for the summary line: how many films they act in, how many
	// they worked on behind the camera, and the span of years held.
	CastCount int `json:"castCount"`
	CrewCount int `json:"crewCount"`
	FirstYear int `json:"firstYear,omitempty"`
	LastYear  int `json:"lastYear,omitempty"`
	// Jobs are the distinct crew jobs they are credited with, most frequent
	// first — "Director, Writer" is how you describe someone.
	Jobs []string `json:"jobs,omitempty"`
}

// CreditsForPerson returns a person's filmography, newest first, with what
// they did on each film. Alternative versions are excluded so a film held
// twice appears once.
//
// Several roles of the same kind on one film collapse into one row with the
// roles joined, because "Writer" and "Director" on the same film is one line
// in any filmography, not two.
func (s *Store) CreditsForPerson(ctx context.Context, personID string) ([]PersonCredit, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemColumns("i")+`, c.kind,
		        GROUP_CONCAT(DISTINCT c.role) AS roles,
		        MIN(c.ord) AS billing
		 FROM items i
		 JOIN credits c ON c.item_id = i.id
		 WHERE c.person_id = ? AND i.version_of = ''
		 GROUP BY i.id, c.kind
		 ORDER BY i.year DESC, billing, i.title`, personID)
	if err != nil {
		return nil, fmt.Errorf("credits for person: %w", err)
	}
	defer rows.Close()
	out := []PersonCredit{}
	for rows.Next() {
		var pc PersonCredit
		var roles sql.NullString
		targets := append(itemScanTargets(&pc.Item), &pc.CreditKind, &roles, &pc.Billing)
		if err := rows.Scan(targets...); err != nil {
			return nil, fmt.Errorf("scan person credit: %w", err)
		}
		// GROUP_CONCAT separates with a bare comma; a middot reads better
		// for a list of jobs and cannot be mistaken for part of a title.
		pc.Role = strings.ReplaceAll(roles.String, ",", " · ")
		out = append(out, pc)
	}
	return out, rows.Err()
}

// PersonPage assembles a person's profile: who they are, everything they are
// credited on here, and the summary a page needs.
func (s *Store) PersonPage(ctx context.Context, personID string) (PersonProfile, error) {
	p, err := s.Person(ctx, personID)
	if err != nil {
		return PersonProfile{}, err
	}
	credits, err := s.CreditsForPerson(ctx, personID)
	if err != nil {
		return PersonProfile{}, err
	}
	prof := PersonProfile{Person: p, Credits: credits}
	jobCount := map[string]int{}
	for _, c := range credits {
		if c.CreditKind == "cast" {
			prof.CastCount++
		} else {
			prof.CrewCount++
			for _, j := range strings.Split(c.Role, " · ") {
				if j = strings.TrimSpace(j); j != "" {
					jobCount[j]++
				}
			}
		}
		if y := c.Year; y > 0 {
			if prof.FirstYear == 0 || y < prof.FirstYear {
				prof.FirstYear = y
			}
			if y > prof.LastYear {
				prof.LastYear = y
			}
		}
	}
	for j := range jobCount {
		prof.Jobs = append(prof.Jobs, j)
	}
	// Most frequent first, then alphabetically so the order is stable.
	sort.Slice(prof.Jobs, func(a, b int) bool {
		ja, jb := prof.Jobs[a], prof.Jobs[b]
		if jobCount[ja] != jobCount[jb] {
			return jobCount[ja] > jobCount[jb]
		}
		return ja < jb
	})
	return prof, nil
}
