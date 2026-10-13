package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/google/uuid"
)

// Playlist is one of a user's playlists. A manual playlist holds tracks in
// the order they were arranged; a smart one (Smart set) is a set of rules
// evaluated each time it is read, so it follows the library and the
// listener's history without being edited.
type Playlist struct {
	ID    string      `json:"id"`
	Name  string      `json:"name"`
	Smart *SmartRules `json:"smart,omitempty"`
	// TrackCount and DurationSecs describe the tracks the reader can play
	// now, and Covers holds up to four distinct album covers among them
	// for a mosaic.
	TrackCount   int      `json:"trackCount"`
	DurationSecs int      `json:"durationSecs"`
	Covers       []string `json:"covers"`
	CreatedAt    string   `json:"createdAt"`
	UpdatedAt    string   `json:"updatedAt"`
}

// SmartRules define a smart playlist: tracks matching all (or any) of the
// rules, sorted and cut to a limit.
type SmartRules struct {
	Match string      `json:"match"` // "all" or "any"
	Rules []SmartRule `json:"rules"`
	Sort  string      `json:"sort"`
	Limit int         `json:"limit"`
}

// SmartRule is one condition. Value is text for the text fields and a
// number, as text, for the rest.
type SmartRule struct {
	Field string `json:"field"`
	Op    string `json:"op"`
	Value string `json:"value"`
}

// Smart playlist limits: a default that suits a listening session, and a
// ceiling that keeps one request bounded.
const (
	SmartDefaultLimit = 100
	SmartMaxLimit     = 1000
)

// ErrInvalidPlaylist is returned for a change that would leave a playlist
// unusable — no name, or smart rules that cannot be evaluated; the message
// says which rule and why.
var ErrInvalidPlaylist = errors.New("invalid playlist")

// smartFields maps each rule field to the operators it accepts.
var smartFields = map[string][]string{
	"artist":     {"is", "isNot", "contains"},
	"album":      {"is", "contains"},
	"title":      {"contains"},
	"genre":      {"is", "isNot"},
	"year":       {"is", "gt", "lt"},
	"format":     {"is"},
	"added":      {"inLast", "notInLast"},
	"plays":      {"is", "gt", "lt"},
	"lastPlayed": {"inLast", "notInLast"},
}

// smartSorts maps each sort to its ORDER BY. "random" reshuffles on every
// read, which is the point of it.
var smartSorts = map[string]string{
	"random":      "random()",
	"title":       "t.title COLLATE NOCASE, t.id",
	"artist":      "t.artist COLLATE NOCASE, a.year, a.title COLLATE NOCASE, t.disc_num, t.track_num",
	"album":       "a.title COLLATE NOCASE, t.disc_num, t.track_num",
	"year":        "t.year DESC, a.title COLLATE NOCASE, t.disc_num, t.track_num",
	"added":       "t.added_at DESC, t.disc_num, t.track_num",
	"plays":       "COALESCE(pc.plays, 0) DESC, pc.last_played_at DESC",
	"lastPlayed":  "COALESCE(pc.last_played_at, '') DESC",
	"leastRecent": "COALESCE(pc.last_played_at, '') ASC, random()",
}

// Normalise fills the defaults and checks every rule, so what is stored is
// always something Evaluate can run.
func (r *SmartRules) Normalise() error {
	if r.Match == "" {
		r.Match = "all"
	}
	if r.Match != "all" && r.Match != "any" {
		return fmt.Errorf("%w: match must be all or any", ErrInvalidPlaylist)
	}
	if r.Sort == "" {
		r.Sort = "random"
	}
	if _, ok := smartSorts[r.Sort]; !ok {
		return fmt.Errorf("%w: unknown sort %q", ErrInvalidPlaylist, r.Sort)
	}
	if r.Limit <= 0 {
		r.Limit = SmartDefaultLimit
	}
	r.Limit = min(r.Limit, SmartMaxLimit)
	if r.Rules == nil {
		r.Rules = []SmartRule{}
	}
	for i, rule := range r.Rules {
		ops, ok := smartFields[rule.Field]
		if !ok {
			return fmt.Errorf("%w: rule %d: unknown field %q", ErrInvalidPlaylist, i+1, rule.Field)
		}
		known := false
		for _, op := range ops {
			known = known || op == rule.Op
		}
		if !known {
			return fmt.Errorf("%w: rule %d: %s cannot use %q", ErrInvalidPlaylist, i+1, rule.Field, rule.Op)
		}
		r.Rules[i].Value = strings.TrimSpace(rule.Value)
		if smartNumeric(rule.Field) {
			if n, err := strconv.Atoi(r.Rules[i].Value); err != nil || n < 0 {
				return fmt.Errorf("%w: rule %d: %s needs a whole number", ErrInvalidPlaylist, i+1, rule.Field)
			}
		} else if r.Rules[i].Value == "" {
			return fmt.Errorf("%w: rule %d: %s needs a value", ErrInvalidPlaylist, i+1, rule.Field)
		}
	}
	return nil
}

func smartNumeric(field string) bool {
	switch field {
	case "year", "added", "plays", "lastPlayed":
		return true
	}
	return false
}

// condition is one rule as SQL over tracks t, their albums a and the
// viewer's play counts pc. Rules are checked by Normalise first.
func (r SmartRule) condition() (string, []any) {
	n, _ := strconv.Atoi(r.Value)
	days := fmt.Sprintf("-%d days", n)
	switch r.Field + "." + r.Op {
	case "artist.is":
		return `(t.artist = ? COLLATE NOCASE OR a.artist = ? COLLATE NOCASE)`, []any{r.Value, r.Value}
	case "artist.isNot":
		return `(t.artist <> ? COLLATE NOCASE AND a.artist <> ? COLLATE NOCASE)`, []any{r.Value, r.Value}
	case "artist.contains":
		p := likeContains(r.Value)
		return `(` + foldedCol("t.artist", r.Value) + ` LIKE ? ESCAPE '\' OR ` +
			foldedCol("a.artist", r.Value) + ` LIKE ? ESCAPE '\')`, []any{p, p}
	case "album.is":
		return `a.title = ? COLLATE NOCASE`, []any{r.Value}
	case "album.contains":
		return foldedCol("a.title", r.Value) + ` LIKE ? ESCAPE '\'`, []any{likeContains(r.Value)}
	case "title.contains":
		return foldedCol("t.title", r.Value) + ` LIKE ? ESCAPE '\'`, []any{likeContains(r.Value)}
	case "genre.is":
		return `EXISTS (SELECT 1 FROM item_genres g WHERE g.item_id IN (t.id, a.id) AND g.genre = ? COLLATE NOCASE)`, []any{r.Value}
	case "genre.isNot":
		return `NOT EXISTS (SELECT 1 FROM item_genres g WHERE g.item_id IN (t.id, a.id) AND g.genre = ? COLLATE NOCASE)`, []any{r.Value}
	case "year.is":
		return `t.year = ?`, []any{n}
	case "year.gt":
		return `t.year > ?`, []any{n}
	case "year.lt":
		return `t.year > 0 AND t.year < ?`, []any{n}
	case "format.is":
		return `(t.container = ? COLLATE NOCASE OR t.audio_codec = ? COLLATE NOCASE)`, []any{r.Value, r.Value}
	case "added.inLast":
		return `t.added_at >= datetime('now', ?)`, []any{days}
	case "added.notInLast":
		return `t.added_at < datetime('now', ?)`, []any{days}
	case "plays.is":
		return `COALESCE(pc.plays, 0) = ?`, []any{n}
	case "plays.gt":
		return `COALESCE(pc.plays, 0) > ?`, []any{n}
	case "plays.lt":
		return `COALESCE(pc.plays, 0) < ?`, []any{n}
	case "lastPlayed.inLast":
		return `pc.last_played_at >= datetime('now', ?)`, []any{days}
	case "lastPlayed.notInLast":
		// Never played is not played in the last N days too.
		return `(pc.last_played_at IS NULL OR pc.last_played_at < datetime('now', ?))`, []any{days}
	}
	return "0", nil
}

// trackQuery is the shared SELECT for playlist tracks: track columns, then
// the viewer's play count and last play.
func trackQuery() string {
	return `SELECT ` + itemColumns("t") + `, COALESCE(pc.plays, 0), COALESCE(pc.last_played_at, '')
		FROM items t
		JOIN items a ON a.id = t.parent_id
		LEFT JOIN play_counts pc ON pc.item_id = t.id AND pc.user_id = ?`
}

// libraryScope limits a query to libraries (column col); nil means no limit
// and an empty list means nothing at all.
func libraryScope(col string, libraryIDs []string) (string, []any) {
	if libraryIDs == nil {
		return "1", nil
	}
	if len(libraryIDs) == 0 {
		return "0", nil
	}
	args := make([]any, len(libraryIDs))
	for i, id := range libraryIDs {
		args[i] = id
	}
	return col + ` IN (?` + strings.Repeat(", ?", len(libraryIDs)-1) + `)`, args
}

// EvaluateSmart runs smart rules for a viewer over the libraries they may
// see (nil for all).
func (s *Store) EvaluateSmart(ctx context.Context, userID string, rules SmartRules, libraryIDs []string) ([]Item, error) {
	if err := rules.Normalise(); err != nil {
		return nil, err
	}
	scope, args := libraryScope("t.library_id", libraryIDs)
	args = append([]any{userID}, args...)
	where := "t.kind = 'track' AND " + scope
	if len(rules.Rules) > 0 {
		joiner := " AND "
		if rules.Match == "any" {
			joiner = " OR "
		}
		conds := make([]string, 0, len(rules.Rules))
		for _, r := range rules.Rules {
			c, a := r.condition()
			conds = append(conds, c)
			args = append(args, a...)
		}
		where += " AND (" + strings.Join(conds, joiner) + ")"
	}
	args = append(args, rules.Limit)
	return s.queryItemsPlayed(ctx,
		trackQuery()+` WHERE `+where+` ORDER BY `+smartSorts[rules.Sort]+` LIMIT ?`, args...)
}

// CreatePlaylist makes a playlist for a user: a manual one when smart is
// nil.
func (s *Store) CreatePlaylist(ctx context.Context, userID, name string, smart *SmartRules) (Playlist, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return Playlist{}, fmt.Errorf("%w: a playlist needs a name", ErrInvalidPlaylist)
	}
	raw, err := encodeSmart(smart)
	if err != nil {
		return Playlist{}, err
	}
	id := uuid.NewString()
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO playlists (id, user_id, name, smart) VALUES (?, ?, ?, ?)`,
		id, userID, name, raw); err != nil {
		return Playlist{}, fmt.Errorf("create playlist: %w", err)
	}
	return s.playlistRow(ctx, userID, id)
}

func encodeSmart(smart *SmartRules) (string, error) {
	if smart == nil {
		return "", nil
	}
	if err := smart.Normalise(); err != nil {
		return "", err
	}
	raw, err := json.Marshal(smart)
	if err != nil {
		return "", fmt.Errorf("encode smart rules: %w", err)
	}
	return string(raw), nil
}

// playlistRow reads a playlist's own columns, scoped to its owner:
// another user's playlist is ErrNotFound, not forbidden, so ids do not
// leak whose they are.
func (s *Store) playlistRow(ctx context.Context, userID, id string) (Playlist, error) {
	var p Playlist
	var raw string
	err := s.db.QueryRowContext(ctx,
		`SELECT id, name, smart, created_at, updated_at FROM playlists WHERE id = ? AND user_id = ?`,
		id, userID).Scan(&p.ID, &p.Name, &raw, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Playlist{}, ErrNotFound
	}
	if err != nil {
		return Playlist{}, fmt.Errorf("playlist %s: %w", id, err)
	}
	if raw != "" {
		var rules SmartRules
		if err := json.Unmarshal([]byte(raw), &rules); err != nil {
			return Playlist{}, fmt.Errorf("playlist %s rules: %w", id, err)
		}
		p.Smart = &rules
	}
	p.Covers = []string{}
	return p, nil
}

// summarise fills a playlist's track count, duration and covers.
func summarise(p *Playlist, tracks []Item) {
	seen := map[string]bool{}
	p.TrackCount = len(tracks)
	p.DurationSecs = 0
	for _, t := range tracks {
		p.DurationSecs += t.RuntimeSecs
		if t.Poster != "" && !seen[t.Poster] && len(p.Covers) < 4 {
			seen[t.Poster] = true
			p.Covers = append(p.Covers, t.Poster)
		}
	}
}

// Playlists lists a user's playlists by name, each summarised over the
// tracks they can play in the given libraries (nil for all).
func (s *Store) Playlists(ctx context.Context, userID string, libraryIDs []string) ([]Playlist, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id FROM playlists WHERE user_id = ? ORDER BY name COLLATE NOCASE, created_at`, userID)
	if err != nil {
		return nil, fmt.Errorf("playlists: %w", err)
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, fmt.Errorf("playlists: %w", err)
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("playlists: %w", err)
	}
	out := make([]Playlist, 0, len(ids))
	for _, id := range ids {
		p, _, err := s.PlaylistWithTracks(ctx, userID, id, libraryIDs)
		if err != nil {
			return nil, err
		}
		out = append(out, p)
	}
	return out, nil
}

// PlaylistWithTracks returns one of a user's playlists and its tracks, in
// order, limited to the libraries given (nil for all): a track in a
// library the user has since lost is skipped rather than shown.
func (s *Store) PlaylistWithTracks(ctx context.Context, userID, id string, libraryIDs []string) (Playlist, []Item, error) {
	p, err := s.playlistRow(ctx, userID, id)
	if err != nil {
		return Playlist{}, nil, err
	}
	var tracks []Item
	if p.Smart != nil {
		tracks, err = s.EvaluateSmart(ctx, userID, *p.Smart, libraryIDs)
	} else {
		scope, args := libraryScope("t.library_id", libraryIDs)
		args = append([]any{userID, id}, args...)
		tracks, err = s.queryItemsPlayed(ctx,
			strings.Replace(trackQuery(), "FROM items t",
				"FROM playlist_items pi JOIN items t ON t.id = pi.item_id", 1)+
				` WHERE pi.playlist_id = ? AND `+scope+` ORDER BY pi.position`, args...)
	}
	if err != nil {
		return Playlist{}, nil, fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	summarise(&p, tracks)
	return p, tracks, nil
}

// UpdatePlaylist renames a playlist and, for a smart one, replaces its
// rules. A nil argument leaves that part alone; a manual playlist cannot
// be given rules, nor a smart one have them taken away.
func (s *Store) UpdatePlaylist(ctx context.Context, userID, id string, name *string, smart *SmartRules) error {
	p, err := s.playlistRow(ctx, userID, id)
	if err != nil {
		return err
	}
	if name != nil {
		p.Name = strings.TrimSpace(*name)
		if p.Name == "" {
			return fmt.Errorf("%w: a playlist needs a name", ErrInvalidPlaylist)
		}
	}
	if smart != nil {
		if p.Smart == nil {
			return fmt.Errorf("%w: a manual playlist has no rules", ErrInvalidPlaylist)
		}
		p.Smart = smart
	}
	raw, err := encodeSmart(p.Smart)
	if err != nil {
		return err
	}
	if _, err := s.db.ExecContext(ctx,
		`UPDATE playlists SET name = ?, smart = ?, updated_at = datetime('now') WHERE id = ? AND user_id = ?`,
		p.Name, raw, id, userID); err != nil {
		return fmt.Errorf("update playlist %s: %w", id, err)
	}
	return nil
}

// DeletePlaylist removes one of a user's playlists.
func (s *Store) DeletePlaylist(ctx context.Context, userID, id string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM playlists WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return fmt.Errorf("delete playlist %s: %w", id, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// ExpandTracks turns items into the tracks they stand for, in listening
// order: a track is itself, an album its tracks by disc and number, an
// artist their albums oldest first. Anything else, or an id that no longer
// exists, adds nothing.
func (s *Store) ExpandTracks(ctx context.Context, ids []string) ([]Item, error) {
	var out []Item
	for _, id := range ids {
		var tracks []Item
		it, err := s.Item(ctx, id)
		if errors.Is(err, sql.ErrNoRows) {
			continue
		}
		if err != nil {
			return nil, err
		}
		switch it.Kind {
		case KindTrack:
			tracks = []Item{it}
		case KindAlbum:
			tracks, err = s.ItemsByParent(ctx, it.ID)
		case KindArtist:
			tracks, err = s.queryItems(ctx,
				`SELECT `+itemColumns("t")+` FROM items t JOIN items a ON a.id = t.parent_id
				 WHERE a.parent_id = ? AND t.kind = 'track'
				 ORDER BY a.year, a.title COLLATE NOCASE, a.id, t.disc_num, t.track_num`, it.ID)
		}
		if err != nil {
			return nil, fmt.Errorf("expand %s: %w", id, err)
		}
		for _, t := range tracks {
			if t.Kind == KindTrack {
				out = append(out, t)
			}
		}
	}
	return out, nil
}

// AppendPlaylistTracks adds tracks to the end of a manual playlist.
func (s *Store) AppendPlaylistTracks(ctx context.Context, userID, id string, trackIDs []string) error {
	return s.writePlaylistTracks(ctx, userID, id, trackIDs, false)
}

// SetPlaylistTracks replaces a manual playlist's tracks with these, in this
// order — how a client reorders and removes.
func (s *Store) SetPlaylistTracks(ctx context.Context, userID, id string, trackIDs []string) error {
	return s.writePlaylistTracks(ctx, userID, id, trackIDs, true)
}

func (s *Store) writePlaylistTracks(ctx context.Context, userID, id string, trackIDs []string, replace bool) error {
	p, err := s.playlistRow(ctx, userID, id)
	if err != nil {
		return err
	}
	if p.Smart != nil {
		return fmt.Errorf("%w: a smart playlist's tracks come from its rules", ErrInvalidPlaylist)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	defer tx.Rollback()
	next := 0
	if replace {
		if _, err := tx.ExecContext(ctx, `DELETE FROM playlist_items WHERE playlist_id = ?`, id); err != nil {
			return fmt.Errorf("playlist %s tracks: %w", id, err)
		}
	} else if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(position) + 1, 0) FROM playlist_items WHERE playlist_id = ?`, id).Scan(&next); err != nil {
		return fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	// Only tracks: the insert selects from items, so an id that is not a
	// track (or no longer exists) is dropped instead of failing the lot.
	stmt, err := tx.PrepareContext(ctx,
		`INSERT INTO playlist_items (playlist_id, position, item_id)
		 SELECT ?, ?, id FROM items WHERE id = ? AND kind = 'track'`)
	if err != nil {
		return fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	defer stmt.Close()
	for _, tid := range trackIDs {
		res, err := stmt.ExecContext(ctx, id, next, tid)
		if err != nil {
			return fmt.Errorf("playlist %s add %s: %w", id, tid, err)
		}
		if n, _ := res.RowsAffected(); n > 0 {
			next++
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE playlists SET updated_at = datetime('now') WHERE id = ?`, id); err != nil {
		return fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("playlist %s tracks: %w", id, err)
	}
	return nil
}
