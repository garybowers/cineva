package importer

import (
	"fmt"
	"path"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// Matcher finds Cineva's titles for a source's: by TMDB id, then by the
// end of the file path, then (music) by artist, album and title.
type Matcher struct {
	films    map[int]string    // tmdb id -> film
	series   map[int]string    // tmdb id -> series
	episodes map[string]string // "series/season/episode" -> episode
	byPath   map[string]string // path tail -> film, episode or track
	tracks   map[string]string // "artist|album|title" -> track
	library  map[string]string // item -> its library
}

// NewMatcher indexes Cineva's library.
func NewMatcher(items []store.IndexItem) *Matcher {
	m := &Matcher{
		films: map[int]string{}, series: map[int]string{}, episodes: map[string]string{},
		byPath: map[string]string{}, tracks: map[string]string{}, library: map[string]string{},
	}
	seasonOf := map[string]store.IndexItem{}
	for _, it := range items {
		if it.Kind == store.KindSeason {
			seasonOf[it.ID] = it
		}
	}
	for _, it := range items {
		m.library[it.ID] = it.LibraryID
		switch it.Kind {
		case store.KindMovie:
			if it.TmdbID > 0 {
				m.films[it.TmdbID] = it.ID
			}
		case store.KindSeries:
			if it.TmdbID > 0 {
				m.series[it.TmdbID] = it.ID
			}
		case store.KindEpisode:
			if season, ok := seasonOf[it.ParentID]; ok {
				m.episodes[episodeKey(season.ParentID, it.SeasonNum, it.EpisodeNum)] = it.ID
			}
		case store.KindTrack:
			m.tracks[trackKey(it.Artist, it.Album, it.Title)] = it.ID
		}
		if it.Kind != store.KindSeries && it.Kind != store.KindSeason && it.Path != "" {
			m.byPath[pathTail(it.Path)] = it.ID
		}
	}
	return m
}

func episodeKey(seriesID string, season, episode int) string {
	return fmt.Sprintf("%s/%d/%d", seriesID, season, episode)
}

func trackKey(artist, album, title string) string {
	return strings.ToLower(strings.TrimSpace(artist)) + "|" + strings.ToLower(strings.TrimSpace(album)) + "|" +
		strings.ToLower(strings.TrimSpace(title))
}

// pathTail is a file's name with its folder, lower case, whichever slashes
// the server used: what stays the same when two servers mount the same
// files at different places.
func pathTail(p string) string {
	p = strings.ToLower(strings.ReplaceAll(p, `\`, "/"))
	dir, file := path.Split(p)
	return path.Base(strings.TrimSuffix(dir, "/")) + "/" + file
}

// Watch finds a watch state's title, or "".
func (m *Matcher) Watch(w Watch) string {
	if w.Movie && w.TmdbID > 0 {
		if id, ok := m.films[w.TmdbID]; ok {
			return id
		}
	}
	if !w.Movie && w.SeriesTmdbID > 0 {
		if s, ok := m.series[w.SeriesTmdbID]; ok {
			if id, ok := m.episodes[episodeKey(s, w.Season, w.Episode)]; ok {
				return id
			}
		}
	}
	if w.Path != "" {
		return m.byPath[pathTail(w.Path)]
	}
	return ""
}

// Track finds a track, or "".
func (m *Matcher) Track(t Track) string {
	if t.Path != "" {
		if id, ok := m.byPath[pathTail(t.Path)]; ok {
			return id
		}
	}
	return m.tracks[trackKey(t.Artist, t.Album, t.Title)]
}

// Library is the library an item is in.
func (m *Matcher) Library(itemID string) string { return m.library[itemID] }
