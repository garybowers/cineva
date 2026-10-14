package arr

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// Event is a webhook delivery, reduced to what decides a rescan and what
// moves a request along.
type Event struct {
	Type string
	// Paths are the folders and files the event names, as the app sees
	// them — which may not be how Cineva sees them, when the app runs in a
	// container with its own mount points.
	Paths []string
	// Title is the film or series the event is about, Year a film's year.
	Title string
	Year  int
	// ArrID is the film's or series' id inside the app; TmdbID and TvdbID
	// its ids elsewhere, where the payload carries them.
	ArrID  int
	TmdbID int
	TvdbID int
	// Episodes are the episodes a Sonarr event names.
	Episodes []store.EpisodeRef
	// Albums are the albums a Lidarr event names; Title is then the artist.
	Albums []string
}

// Grab is a release sent to the download client: the title is downloading.
func (e Event) Grab() bool { return e.Type == "Grab" }

// Describe names what the event is about, for a connection's status line:
// "Show S02E04", "Show S02E01-E03", "Film (2020)".
func (e Event) Describe() string {
	if e.Title == "" {
		return ""
	}
	if len(e.Albums) > 0 {
		if len(e.Albums) == 1 {
			return e.Title + " · " + e.Albums[0]
		}
		return fmt.Sprintf("%s · %d albums", e.Title, len(e.Albums))
	}
	if len(e.Episodes) == 0 {
		if e.Year > 0 {
			return fmt.Sprintf("%s (%d)", e.Title, e.Year)
		}
		return e.Title
	}
	first, last := e.Episodes[0], e.Episodes[len(e.Episodes)-1]
	code := fmt.Sprintf("S%02dE%02d", first.Season, first.Episode)
	if len(e.Episodes) > 1 {
		if last.Season == first.Season {
			code += fmt.Sprintf("-E%02d", last.Episode)
		} else {
			code += fmt.Sprintf("-S%02dE%02d", last.Season, last.Episode)
		}
	}
	return strings.TrimSpace(e.Title + " " + code)
}

// Test is the event an app sends when a webhook is saved or its Test button
// pressed.
func (e Event) Test() bool { return e.Type == "Test" }

// ChangesFiles reports whether the event added, replaced, renamed or
// removed media files — the ones worth a rescan.
func (e Event) ChangesFiles() bool {
	switch e.Type {
	case "Download", "ImportComplete", "Rename",
		"SeriesDelete", "EpisodeFileDelete",
		"MovieDelete", "MovieFileDelete",
		"Retag", "ArtistDelete", "AlbumDelete": // Lidarr's
		return true
	}
	return false
}

type pathed struct {
	Path       string `json:"path"`
	FolderPath string `json:"folderPath"`
	ID         int    `json:"id"`
	Title      string `json:"title"`
	Name       string `json:"name"` // a Lidarr artist's
	Year       int    `json:"year"`
	TmdbID     int    `json:"tmdbId"`
	TvdbID     int    `json:"tvdbId"`
}

// ParseEvent reads a Sonarr, Radarr or Lidarr webhook body.
func ParseEvent(body []byte) (Event, error) {
	var raw struct {
		EventType    string   `json:"eventType"`
		Series       *pathed  `json:"series"`
		Movie        *pathed  `json:"movie"`
		EpisodeFile  *pathed  `json:"episodeFile"`
		EpisodeFiles []pathed `json:"episodeFiles"`
		MovieFile    *pathed  `json:"movieFile"`
		Artist       *pathed  `json:"artist"`
		Albums       []pathed `json:"albums"`
		TrackFiles   []pathed `json:"trackFiles"`
		Episodes     []struct {
			SeasonNumber  int `json:"seasonNumber"`
			EpisodeNumber int `json:"episodeNumber"`
		} `json:"episodes"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return Event{}, fmt.Errorf("webhook body: %w", err)
	}
	ev := Event{Type: raw.EventType}
	add := func(p *pathed) {
		if p == nil {
			return
		}
		for _, s := range []string{p.Path, p.FolderPath} {
			if s != "" {
				ev.Paths = append(ev.Paths, s)
			}
		}
	}
	add(raw.EpisodeFile)
	add(raw.MovieFile)
	for i := range raw.EpisodeFiles {
		add(&raw.EpisodeFiles[i])
	}
	for i := range raw.TrackFiles {
		add(&raw.TrackFiles[i])
	}
	if a := raw.Artist; a != nil {
		add(a)
		ev.Title, ev.ArrID = a.Name, a.ID
		for _, al := range raw.Albums {
			if al.Title != "" {
				ev.Albums = append(ev.Albums, al.Title)
			}
		}
	}
	add(raw.Series)
	add(raw.Movie)
	for _, t := range []*pathed{raw.Series, raw.Movie} {
		if t != nil {
			ev.Title, ev.Year, ev.ArrID, ev.TmdbID, ev.TvdbID = t.Title, t.Year, t.ID, t.TmdbID, t.TvdbID
		}
	}
	if raw.Series != nil {
		ev.Year = 0 // a series' year says nothing about the episode
	}
	for _, e := range raw.Episodes {
		ev.Episodes = append(ev.Episodes, store.EpisodeRef{Season: e.SeasonNumber, Episode: e.EpisodeNumber})
	}
	return ev, nil
}
