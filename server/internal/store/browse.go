package store

import (
	"context"
	"fmt"
	"strings"
)

// The paged music listings (server/docs/music.md, part 1): artists, albums
// and tracks of one library, filtered, sorted and decorated with the
// viewer's plays and ratings on the server, so a library of thirty thousand
// tracks costs a page per request rather than the whole library per visit.

// MusicQuery narrows and orders a music listing. The zero value lists
// everything by name.
type MusicQuery struct {
	// Query is words to match: each must be a substring of the title, the
	// artist or (tracks) the album, folded like Fold, so "arc mon" finds
	// Arctic Monkeys as it is typed.
	Query string
	// Artist narrows albums to one artist's own (their item id).
	Artist string
	// Genre narrows to titles carrying the genre.
	Genre string
	// Year and Decade narrow albums and tracks by their year; Decade is
	// its first year (1990).
	Year, Decade int
	// Compilations keeps only albums by various artists.
	Compilations bool
	// Sort is one of the listing's sorts (see artistSorts, albumSorts and
	// trackSorts); unknown or empty means by name.
	Sort string
	// Letter keeps titles whose sort key starts with it, or "#" for those
	// starting with anything but a letter.
	Letter string
	// Page is 1-based; Size is the page size. The caller clamps both.
	Page, Size int
}

// MusicPage is one page of a listing with the counts around it.
type MusicPage struct {
	Items []Item `json:"items"`
	// Total is how many match the filters, across every page.
	Total int `json:"total"`
	Page  int `json:"page"`
	Size  int `json:"size"`
	// Index is how many match under each letter of the alphabet (and "#"),
	// with the Letter filter not applied, for drawing a letter strip. Only
	// on the name sort.
	Index map[string]int `json:"index,omitempty"`
}

// Sorts by listing, as the API names them, mapped to their ORDER BY. The
// id breaks every tie so paging is stable.
var (
	artistSorts = map[string]string{
		"name":   "i.sort_title, i.id",
		"added":  "i.added_at DESC, i.sort_title, i.id",
		"plays":  "COALESCE(pl.plays, 0) DESC, i.sort_title, i.id",
		"played": "COALESCE(pl.last, '') DESC, i.sort_title, i.id",
		"random": "random()",
	}
	albumSorts = map[string]string{
		"name":   "i.sort_title, i.id",
		"artist": "i.artist COLLATE NOCASE, i.year, i.sort_title, i.id",
		"year":   "i.year DESC, i.sort_title, i.id",
		"added":  "i.added_at DESC, i.sort_title, i.id",
		"plays":  "COALESCE(pl.plays, 0) DESC, i.sort_title, i.id",
		"played": "COALESCE(pl.last, '') DESC, i.sort_title, i.id",
		"rating": "COALESCE(rt.stars, 0) DESC, i.sort_title, i.id",
		"random": "random()",
	}
	trackSorts = map[string]string{
		"title":  "i.sort_title, i.id",
		"artist": "i.artist COLLATE NOCASE, i.sort_title, i.id",
		"album":  "a.sort_title, a.id, i.disc_num, i.track_num, i.id",
		"added":  "i.added_at DESC, i.sort_title, i.id",
		"plays":  "COALESCE(pl.plays, 0) DESC, i.sort_title, i.id",
		"played": "COALESCE(pl.last, '') DESC, i.sort_title, i.id",
		"rating": "COALESCE(rt.stars, 0) DESC, i.sort_title, i.id",
		"random": "random()",
	}
)

// The viewer's plays summed up the tree: per album from its tracks, per
// artist from the tracks on their own albums. Proportional to the viewer's
// own play_counts rows, however big the library.
const (
	albumPlays = `(SELECT t.parent_id AS id, SUM(pc.plays) AS plays, MAX(pc.last_played_at) AS last
	               FROM play_counts pc JOIN items t ON t.id = pc.item_id AND t.kind = 'track'
	               WHERE pc.user_id = ? AND t.library_id = ? GROUP BY t.parent_id)`
	artistPlays = `(SELECT a.parent_id AS id, SUM(pc.plays) AS plays, MAX(pc.last_played_at) AS last
	                FROM play_counts pc JOIN items t ON t.id = pc.item_id AND t.kind = 'track'
	                JOIN items a ON a.id = t.parent_id
	                WHERE pc.user_id = ? AND t.library_id = ? GROUP BY a.parent_id)`
	trackPlays = `(SELECT item_id AS id, plays, last_played_at AS last FROM play_counts WHERE user_id = ?)`
)

// letterOf files a sort key under its first letter, or "#".
const letterOf = `CASE WHEN substr(%s, 1, 1) BETWEEN 'a' AND 'z' THEN upper(substr(%s, 1, 1)) ELSE '#' END`

// wordsWhere is the condition that every word of q is a substring of one
// of the columns, folded like Fold; "1" when q is blank.
func wordsWhere(q string, cols ...string) (string, []any) {
	var conds []string
	var args []any
	for _, w := range strings.Fields(q) {
		pat := likeContains(w)
		var alts []string
		for _, c := range cols {
			alts = append(alts, foldedCol(c)+` LIKE ? ESCAPE '\'`)
			args = append(args, pat)
		}
		conds = append(conds, "("+strings.Join(alts, " OR ")+")")
	}
	if len(conds) == 0 {
		return "1", nil
	}
	return strings.Join(conds, " AND "), args
}

// letterWhere keeps rows whose sort key files under the letter.
func letterWhere(col, letter string) (string, []any) {
	letter = strings.ToLower(strings.TrimSpace(letter))
	switch letter {
	case "":
		return "1", nil
	case "#":
		return `substr(` + col + `, 1, 1) NOT BETWEEN 'a' AND 'z'`, nil
	default:
		return col + ` LIKE ? ESCAPE '\'`, []any{likeEscaper.Replace(letter[:1]) + "%"}
	}
}

// listing is one listing's query, assembled by Artists, Albums and Tracks.
type listing struct {
	// from is the FROM clause with its joins; i is the listed item.
	from string
	// fromArgs are the joins' arguments (the viewer's id for play counts).
	fromArgs []any
	// where and args are the filters, without the letter.
	where []string
	args  []any
	// letter is the letter filter, applied to the page but not the index.
	letter     string
	letterArgs []any
	order      string
	// rank, when set, orders a name sort of a word query with the titles
	// that start with the query first; rankArgs are its arguments.
	rank     string
	rankArgs []any
	// child counts the item's children for ChildCount.
	child string
}

func (l *listing) filter(cond string, args []any) {
	if cond == "" || cond == "1" {
		return
	}
	l.where = append(l.where, cond)
	l.args = append(l.args, args...)
}

// run fetches the page, its total and (on the name sort) the letter index.
func (s *Store) runListing(ctx context.Context, l listing, q MusicQuery, nameSort bool) (MusicPage, error) {
	page := MusicPage{Items: []Item{}, Page: q.Page, Size: q.Size}
	where := strings.Join(append([]string{"1"}, l.where...), " AND ")

	countArgs := append(append([]any{}, l.args...), l.letterArgs...)
	if err := s.db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM items i WHERE `+where+` AND `+l.letter, countArgs...).Scan(&page.Total); err != nil {
		return MusicPage{}, fmt.Errorf("count listing: %w", err)
	}

	if nameSort {
		page.Index = map[string]int{}
		expr := fmt.Sprintf(letterOf, "i.sort_title", "i.sort_title")
		rows, err := s.db.QueryContext(ctx,
			`SELECT `+expr+` AS l, COUNT(*) FROM items i WHERE `+where+` GROUP BY l`, l.args...)
		if err != nil {
			return MusicPage{}, fmt.Errorf("letter index: %w", err)
		}
		for rows.Next() {
			var letter string
			var n int
			if err := rows.Scan(&letter, &n); err != nil {
				rows.Close()
				return MusicPage{}, fmt.Errorf("letter index: %w", err)
			}
			page.Index[letter] = n
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return MusicPage{}, fmt.Errorf("letter index: %w", err)
		}
	}

	// Placeholders in the order they appear: the joins', the filters', the
	// ranking's, then the page's.
	order := l.order
	args := append(append([]any{}, l.fromArgs...), countArgs...)
	if l.rank != "" {
		order = l.rank + ", " + order
		args = append(args, l.rankArgs...)
	}
	args = append(args, q.Size, (q.Page-1)*q.Size)
	rows, err := s.db.QueryContext(ctx,
		`SELECT `+itemColumns("i")+`, COALESCE(pl.plays, 0), COALESCE(pl.last, ''), COALESCE(rt.stars, 0), `+l.child+`
		 FROM `+l.from+`
		 WHERE `+where+` AND `+l.letter+`
		 ORDER BY `+order+` LIMIT ? OFFSET ?`, args...)
	if err != nil {
		return MusicPage{}, fmt.Errorf("query listing: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var it Item
		if err := rows.Scan(append(itemScanTargets(&it), &it.PlayCount, &it.LastPlayedAt, &it.UserRating, &it.ChildCount)...); err != nil {
			return MusicPage{}, fmt.Errorf("scan listing: %w", err)
		}
		page.Items = append(page.Items, it)
	}
	return page, rows.Err()
}

// rankFor puts titles starting with the query first on a name sort of a
// word query, so "arc" lists Arctic Monkeys before Jon Arcturus.
func rankFor(q MusicQuery, nameSort bool) (string, []any) {
	w := strings.Fields(q.Query)
	if !nameSort || len(w) == 0 {
		return "", nil
	}
	return `CASE WHEN i.sort_title LIKE ? ESCAPE '\' THEN 0 ELSE 1 END`, []any{likePrefix(strings.Join(w, " "))}
}

// Artists is one page of a music library's artists for the viewer, each
// with their album count and the viewer's plays of them.
func (s *Store) Artists(ctx context.Context, userID, libraryID string, q MusicQuery) (MusicPage, error) {
	order, ok := artistSorts[q.Sort]
	if !ok {
		order, q.Sort = artistSorts["name"], "name"
	}
	l := listing{
		from: `items i
		       LEFT JOIN ` + artistPlays + ` pl ON pl.id = i.id
		       LEFT JOIN ratings rt ON rt.item_id = i.id AND rt.user_id = ?`,
		fromArgs: []any{userID, libraryID, userID},
		order:    order,
		child:    `(SELECT COUNT(*) FROM items c WHERE c.parent_id = i.id AND c.kind = 'album')`,
	}
	l.filter(`i.library_id = ? AND i.kind = 'artist'`, []any{libraryID})
	l.filter(wordsWhere(q.Query, "i.title"))
	if q.Genre != "" {
		l.filter(`EXISTS (SELECT 1 FROM items al JOIN item_genres g ON g.item_id = al.id
		                  WHERE al.parent_id = i.id AND al.kind = 'album' AND g.genre = ?)`, []any{q.Genre})
	}
	l.letter, l.letterArgs = letterWhere("i.sort_title", q.Letter)
	nameSort := q.Sort == "name"
	l.rank, l.rankArgs = rankFor(q, nameSort)
	return s.runListing(ctx, l, q, nameSort)
}

// Albums is one page of a music library's albums for the viewer, each with
// its track count, the viewer's plays of it and their rating of it.
func (s *Store) Albums(ctx context.Context, userID, libraryID string, q MusicQuery) (MusicPage, error) {
	order, ok := albumSorts[q.Sort]
	if !ok {
		order, q.Sort = albumSorts["name"], "name"
	}
	l := listing{
		from: `items i
		       LEFT JOIN ` + albumPlays + ` pl ON pl.id = i.id
		       LEFT JOIN ratings rt ON rt.item_id = i.id AND rt.user_id = ?`,
		fromArgs: []any{userID, libraryID, userID},
		order:    order,
		child:    `(SELECT COUNT(*) FROM items c WHERE c.parent_id = i.id AND c.kind = 'track')`,
	}
	l.filter(`i.library_id = ? AND i.kind = 'album'`, []any{libraryID})
	l.filter(wordsWhere(q.Query, "i.title", "i.artist"))
	if q.Artist != "" {
		l.filter(`i.parent_id = ?`, []any{q.Artist})
	}
	if q.Genre != "" {
		l.filter(`EXISTS (SELECT 1 FROM item_genres g WHERE g.item_id = i.id AND g.genre = ?)`, []any{q.Genre})
	}
	if q.Year != 0 {
		l.filter(`i.year = ?`, []any{q.Year})
	}
	if q.Decade != 0 {
		l.filter(`i.year BETWEEN ? AND ?`, []any{q.Decade, q.Decade + 9})
	}
	if q.Compilations {
		l.filter(`i.compilation = 1`, nil)
	}
	l.letter, l.letterArgs = letterWhere("i.sort_title", q.Letter)
	nameSort := q.Sort == "name"
	l.rank, l.rankArgs = rankFor(q, nameSort)
	return s.runListing(ctx, l, q, nameSort)
}

// Tracks is one page of a music library's tracks for the viewer, each with
// the viewer's plays and rating.
func (s *Store) Tracks(ctx context.Context, userID, libraryID string, q MusicQuery) (MusicPage, error) {
	order, ok := trackSorts[q.Sort]
	if !ok {
		order, q.Sort = trackSorts["title"], "title"
	}
	l := listing{
		from: `items i
		       JOIN items a ON a.id = i.parent_id
		       LEFT JOIN ` + trackPlays + ` pl ON pl.id = i.id
		       LEFT JOIN ratings rt ON rt.item_id = i.id AND rt.user_id = ?`,
		fromArgs: []any{userID, userID},
		order:    order,
		child:    `0`,
	}
	l.filter(`i.library_id = ? AND i.kind = 'track'`, []any{libraryID})
	l.filter(wordsWhere(q.Query, "i.title", "i.artist", "i.album"))
	if q.Genre != "" {
		l.filter(`EXISTS (SELECT 1 FROM item_genres g WHERE g.item_id = i.id AND g.genre = ?)`, []any{q.Genre})
	}
	if q.Year != 0 {
		l.filter(`i.year = ?`, []any{q.Year})
	}
	if q.Decade != 0 {
		l.filter(`i.year BETWEEN ? AND ?`, []any{q.Decade, q.Decade + 9})
	}
	l.letter, l.letterArgs = letterWhere("i.sort_title", q.Letter)
	nameSort := q.Sort == "title"
	l.rank, l.rankArgs = rankFor(q, nameSort)
	return s.runListing(ctx, l, q, nameSort)
}

// TracksUnder is every track under an artist (on their own albums, album
// by album in release order) or an album (disc and track order), in play
// order, with the viewer's plays and rating: what Play, Shuffle and Add to
// queue send to the queue in one request.
func (s *Store) TracksUnder(ctx context.Context, userID string, item Item) ([]Item, error) {
	switch item.Kind {
	case KindAlbum:
		return s.queryTracks(ctx, trackQuery()+`
			WHERE t.kind = 'track' AND t.parent_id = ?
			ORDER BY t.disc_num, t.track_num, t.title`, userID, userID, item.ID)
	case KindArtist:
		return s.queryTracks(ctx, trackQuery()+`
			WHERE t.kind = 'track' AND a.parent_id = ?
			ORDER BY a.year, a.sort_title, a.id, t.disc_num, t.track_num, t.title`, userID, userID, item.ID)
	}
	return nil, fmt.Errorf("tracks under %s: not an artist or album", item.Kind)
}

// LibrarySearch is what a music library's search box shows as it is typed:
// the artists, albums and tracks matching every word of the query, a few of
// each, titles starting with the query first.
type LibrarySearch struct {
	Artists []Item `json:"artists"`
	Albums  []Item `json:"albums"`
	Tracks  []Item `json:"tracks"`
}

func (s *Store) SearchLibrary(ctx context.Context, userID, libraryID, query string, few, many int) (LibrarySearch, error) {
	var out LibrarySearch
	artists, err := s.Artists(ctx, userID, libraryID, MusicQuery{Query: query, Page: 1, Size: few})
	if err != nil {
		return out, err
	}
	albums, err := s.Albums(ctx, userID, libraryID, MusicQuery{Query: query, Page: 1, Size: few})
	if err != nil {
		return out, err
	}
	tracks, err := s.Tracks(ctx, userID, libraryID, MusicQuery{Query: query, Page: 1, Size: many})
	if err != nil {
		return out, err
	}
	out.Artists, out.Albums, out.Tracks = artists.Items, albums.Items, tracks.Items
	return out, nil
}
