package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/google/uuid"
)

// Request levels: what happens when a user asks for something the library
// does not have.
const (
	RequestNone     = "none"     // no request screens at all
	RequestApproval = "approval" // requests wait in the admin's queue
	RequestAuto     = "auto"     // requests go straight to Sonarr or Radarr
)

// RequestLevels lists them in the order the admin UI offers them.
var RequestLevels = []string{RequestNone, RequestApproval, RequestAuto}

// Request statuses, in the order a request moves through them.
const (
	RequestRequested   = "requested"   // waiting for an admin (or for an instance that was unreachable)
	RequestApproved    = "approved"    // sent to Sonarr or Radarr
	RequestDownloading = "downloading" // the instance grabbed a release
	RequestAvailable   = "available"   // a scan found it in a library
	RequestDeclined    = "declined"
)

// Media types a request can be for.
const (
	RequestMovie = "movie"
	RequestTV    = "tv"
)

// EpisodeRef names one episode of a series.
type EpisodeRef struct {
	Season  int `json:"season"`
	Episode int `json:"episode"`
}

// MediaRequest is one request for a film, a series, some of its seasons, or
// single episodes of a series already held.
type MediaRequest struct {
	ID     string `json:"id"`
	UserID string `json:"userId"`
	// UserName is the requester's display name, joined in on load.
	UserName  string `json:"userName"`
	MediaType string `json:"mediaType"` // RequestMovie | RequestTV
	TmdbID    int    `json:"tmdbId"`
	TvdbID    int    `json:"tvdbId,omitempty"`
	Title     string `json:"title"`
	Year      int    `json:"year,omitempty"`
	Poster    string `json:"poster,omitempty"`
	Backdrop  string `json:"backdrop,omitempty"`
	// Seasons and Episodes narrow a series request; both empty is the
	// whole series.
	Seasons  []int        `json:"seasons"`
	Episodes []EpisodeRef `json:"episodes"`
	Status   string       `json:"status"`
	// ConnectionID, QualityProfileID and RootFolder are what it was (or
	// will be) sent with; ArrID is the title's id inside that instance.
	ConnectionID     string `json:"connectionId,omitempty"`
	QualityProfileID int    `json:"qualityProfileId,omitempty"`
	RootFolder       string `json:"rootFolder,omitempty"`
	ArrID            int    `json:"arrId,omitempty"`
	// Note is the admin's word on approving or declining; Error why the
	// last attempt to send it failed.
	Note      string `json:"note,omitempty"`
	Error     string `json:"error,omitempty"`
	DecidedBy string `json:"decidedBy,omitempty"`
	DecidedAt string `json:"decidedAt,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}

// Open reports whether the request still has somewhere to go: not declined
// and not yet in the library.
func (r MediaRequest) Open() bool {
	return r.Status != RequestDeclined && r.Status != RequestAvailable
}

// WholeSeries reports whether a series request is for everything.
func (r MediaRequest) WholeSeries() bool {
	return r.MediaType == RequestTV && len(r.Seasons) == 0 && len(r.Episodes) == 0
}

// RequestFilter narrows a request listing. Zero values do not filter.
type RequestFilter struct {
	UserID    string
	Statuses  []string
	MediaType string
	TmdbID    int
	Limit     int
}

type RequestRepo interface {
	CreateRequest(ctx context.Context, r MediaRequest) (MediaRequest, error)
	Request(ctx context.Context, id string) (MediaRequest, error)
	Requests(ctx context.Context, f RequestFilter) ([]MediaRequest, error)
	UpdateRequest(ctx context.Context, r MediaRequest) (MediaRequest, error)
	DeleteRequest(ctx context.Context, id string) error
	ItemsByTmdbIDs(ctx context.Context, kind ItemKind, ids []int) ([]Item, error)
	SeriesOnDisk(ctx context.Context, tmdbID int) (map[int]map[int]bool, bool, error)
}

var _ RequestRepo = (*Store)(nil)

const requestCols = `r.id, r.user_id, COALESCE(NULLIF(u.display_name, ''), u.username, ''), r.media_type,
	r.tmdb_id, r.tvdb_id, r.title, r.year, r.poster, r.backdrop, r.seasons, r.episodes, r.status,
	r.connection_id, r.quality_profile_id, r.root_folder, r.arr_id, r.note, r.error,
	r.decided_by, r.decided_at, r.created_at, r.updated_at`

const requestFrom = ` FROM media_requests r LEFT JOIN users u ON u.id = r.user_id`

func scanRequest(row interface{ Scan(...any) error }) (MediaRequest, error) {
	var r MediaRequest
	var seasons, episodes string
	err := row.Scan(&r.ID, &r.UserID, &r.UserName, &r.MediaType,
		&r.TmdbID, &r.TvdbID, &r.Title, &r.Year, &r.Poster, &r.Backdrop, &seasons, &episodes, &r.Status,
		&r.ConnectionID, &r.QualityProfileID, &r.RootFolder, &r.ArrID, &r.Note, &r.Error,
		&r.DecidedBy, &r.DecidedAt, &r.CreatedAt, &r.UpdatedAt)
	if err != nil {
		return r, err
	}
	r.Seasons, r.Episodes = []int{}, []EpisodeRef{}
	if err := json.Unmarshal([]byte(seasons), &r.Seasons); err != nil {
		return r, fmt.Errorf("request %s seasons: %w", r.ID, err)
	}
	if err := json.Unmarshal([]byte(episodes), &r.Episodes); err != nil {
		return r, fmt.Errorf("request %s episodes: %w", r.ID, err)
	}
	return r, nil
}

// encodeScope turns a request's seasons and episodes into their stored JSON,
// sorted and without duplicates so two requests for the same thing compare
// equal.
func encodeScope(r MediaRequest) (string, string, error) {
	seasons := slices.Clone(r.Seasons)
	slices.Sort(seasons)
	seasons = slices.Compact(seasons)
	if seasons == nil {
		seasons = []int{}
	}
	eps := slices.Clone(r.Episodes)
	slices.SortFunc(eps, func(a, b EpisodeRef) int {
		if a.Season != b.Season {
			return a.Season - b.Season
		}
		return a.Episode - b.Episode
	})
	eps = slices.Compact(eps)
	if eps == nil {
		eps = []EpisodeRef{}
	}
	s, err := json.Marshal(seasons)
	if err != nil {
		return "", "", fmt.Errorf("encode seasons: %w", err)
	}
	e, err := json.Marshal(eps)
	if err != nil {
		return "", "", fmt.Errorf("encode episodes: %w", err)
	}
	return string(s), string(e), nil
}

func validRequest(r MediaRequest) error {
	if r.MediaType != RequestMovie && r.MediaType != RequestTV {
		return fmt.Errorf("unknown media type %q: want movie or tv", r.MediaType)
	}
	if r.TmdbID <= 0 {
		return errors.New("tmdbId is required")
	}
	if r.MediaType == RequestMovie && (len(r.Seasons) > 0 || len(r.Episodes) > 0) {
		return errors.New("a film has no seasons or episodes")
	}
	switch r.Status {
	case RequestRequested, RequestApproved, RequestDownloading, RequestAvailable, RequestDeclined:
	default:
		return fmt.Errorf("unknown request status %q", r.Status)
	}
	return nil
}

func (s *Store) CreateRequest(ctx context.Context, r MediaRequest) (MediaRequest, error) {
	if r.Status == "" {
		r.Status = RequestRequested
	}
	if err := validRequest(r); err != nil {
		return MediaRequest{}, err
	}
	seasons, episodes, err := encodeScope(r)
	if err != nil {
		return MediaRequest{}, err
	}
	r.ID = uuid.NewString()
	if _, err := s.db.ExecContext(ctx, `INSERT INTO media_requests
		(id, user_id, media_type, tmdb_id, tvdb_id, title, year, poster, backdrop, seasons, episodes,
		 status, connection_id, quality_profile_id, root_folder, arr_id, note, error)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		r.ID, r.UserID, r.MediaType, r.TmdbID, r.TvdbID, r.Title, r.Year, r.Poster, r.Backdrop, seasons, episodes,
		r.Status, r.ConnectionID, r.QualityProfileID, r.RootFolder, r.ArrID, r.Note, r.Error); err != nil {
		return MediaRequest{}, fmt.Errorf("create request: %w", err)
	}
	return s.Request(ctx, r.ID)
}

func (s *Store) Request(ctx context.Context, id string) (MediaRequest, error) {
	r, err := scanRequest(s.db.QueryRowContext(ctx, `SELECT `+requestCols+requestFrom+` WHERE r.id = ?`, id))
	if errors.Is(err, sql.ErrNoRows) {
		return MediaRequest{}, ErrNotFound
	}
	if err != nil {
		return MediaRequest{}, fmt.Errorf("request %s: %w", id, err)
	}
	return r, nil
}

// Requests lists requests newest first.
func (s *Store) Requests(ctx context.Context, f RequestFilter) ([]MediaRequest, error) {
	where := []string{"1 = 1"}
	args := []any{}
	if f.UserID != "" {
		where = append(where, "r.user_id = ?")
		args = append(args, f.UserID)
	}
	if len(f.Statuses) > 0 {
		where = append(where, "r.status IN (?"+strings.Repeat(", ?", len(f.Statuses)-1)+")")
		for _, st := range f.Statuses {
			args = append(args, st)
		}
	}
	if f.MediaType != "" {
		where = append(where, "r.media_type = ?")
		args = append(args, f.MediaType)
	}
	if f.TmdbID > 0 {
		where = append(where, "r.tmdb_id = ?")
		args = append(args, f.TmdbID)
	}
	q := `SELECT ` + requestCols + requestFrom + ` WHERE ` + strings.Join(where, " AND ") +
		` ORDER BY r.created_at DESC, r.rowid DESC`
	if f.Limit > 0 {
		q += ` LIMIT ?`
		args = append(args, f.Limit)
	}
	rows, err := s.db.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("requests: %w", err)
	}
	defer rows.Close()
	out := []MediaRequest{}
	for rows.Next() {
		r, err := scanRequest(rows)
		if err != nil {
			return nil, fmt.Errorf("request row: %w", err)
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// UpdateRequest rewrites everything the request's life changes: its status,
// what it was sent with, and the admin's decision. Who asked and for what
// stay as created, apart from the series' TVDB id, learnt on sending.
func (s *Store) UpdateRequest(ctx context.Context, r MediaRequest) (MediaRequest, error) {
	if err := validRequest(r); err != nil {
		return MediaRequest{}, err
	}
	res, err := s.db.ExecContext(ctx, `UPDATE media_requests SET
		tvdb_id = ?, status = ?, connection_id = ?, quality_profile_id = ?, root_folder = ?, arr_id = ?,
		note = ?, error = ?, decided_by = ?, decided_at = ?, updated_at = datetime('now')
		WHERE id = ?`,
		r.TvdbID, r.Status, r.ConnectionID, r.QualityProfileID, r.RootFolder, r.ArrID,
		r.Note, r.Error, r.DecidedBy, r.DecidedAt, r.ID)
	if err != nil {
		return MediaRequest{}, fmt.Errorf("update request: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return MediaRequest{}, fmt.Errorf("update request: %w", err)
	} else if n == 0 {
		return MediaRequest{}, ErrNotFound
	}
	return s.Request(ctx, r.ID)
}

func (s *Store) DeleteRequest(ctx context.Context, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM media_requests WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete request: %w", err)
	}
	if n, err := res.RowsAffected(); err != nil {
		return fmt.Errorf("delete request: %w", err)
	} else if n == 0 {
		return ErrNotFound
	}
	return nil
}

// ItemsByTmdbIDs finds the films or series (primaries only, not alternative
// versions) carrying any of the given TMDB ids, in every library; callers
// filter by what the viewer may see.
func (s *Store) ItemsByTmdbIDs(ctx context.Context, kind ItemKind, ids []int) ([]Item, error) {
	if len(ids) == 0 {
		return []Item{}, nil
	}
	args := []any{kind}
	for _, id := range ids {
		args = append(args, id)
	}
	return s.queryItems(ctx, `SELECT `+itemCols+` FROM items i
		WHERE i.kind = ? AND i.version_of = '' AND i.tmdb_id IN (?`+strings.Repeat(", ?", len(ids)-1)+`)`,
		args...)
}

// SeriesOnDisk reports which episodes of a series (by its TMDB id) are in
// any library, as season -> episode -> true, and whether the series is
// there at all. A season held with no episodes appears with an empty map.
func (s *Store) SeriesOnDisk(ctx context.Context, tmdbID int) (map[int]map[int]bool, bool, error) {
	out := map[int]map[int]bool{}
	var found bool
	if err := s.db.QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM items WHERE kind = 'series' AND tmdb_id = ?)`, tmdbID).Scan(&found); err != nil {
		return nil, false, fmt.Errorf("series %d on disk: %w", tmdbID, err)
	}
	if !found {
		return out, false, nil
	}
	rows, err := s.db.QueryContext(ctx, `SELECT se.season_num, COALESCE(e.episode_num, -1)
		FROM items sr
		JOIN items se ON se.parent_id = sr.id AND se.kind = 'season'
		LEFT JOIN items e ON e.parent_id = se.id AND e.kind = 'episode'
		WHERE sr.kind = 'series' AND sr.tmdb_id = ?`, tmdbID)
	if err != nil {
		return nil, false, fmt.Errorf("series %d episodes: %w", tmdbID, err)
	}
	defer rows.Close()
	for rows.Next() {
		var season, ep int
		if err := rows.Scan(&season, &ep); err != nil {
			return nil, false, fmt.Errorf("series %d episode row: %w", tmdbID, err)
		}
		if out[season] == nil {
			out[season] = map[int]bool{}
		}
		if ep >= 0 {
			out[season][ep] = true
		}
	}
	return out, true, rows.Err()
}
