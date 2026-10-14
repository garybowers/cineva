// Package importer brings watch state, users, and music playlists and
// favourites from a Plex or Jellyfin server into Cineva (see
// docs/import.md). Each source gives the same model; the matcher finds
// Cineva's titles for it; the runner writes them, never over newer state.
package importer

import (
	"context"
	"net/http"
	"strings"
	"time"
)

// Info is what connecting to a source learned.
type Info struct {
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Version string `json:"version"`
	Users   []User `json:"users"`
	// Caveats are limits of this source worth saying before an import.
	Caveats []string `json:"caveats"`
}

// User is an account on the source.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Admin bool   `json:"admin"`
}

// Watch is one title's watch state on the source.
type Watch struct {
	Movie        bool // else an episode
	Title        string
	Year         int
	TmdbID       int // a film's
	SeriesTitle  string
	SeriesTmdbID int
	Season       int
	Episode      int
	Path         string
	Watched      bool
	PositionSecs int
	At           time.Time
}

// Track is a music track on the source.
type Track struct {
	Artist string
	Album  string
	Title  string
	Path   string
}

// Playlist is a music playlist on the source.
type Playlist struct {
	Name   string
	Tracks []Track
}

// Source is a Plex or Jellyfin server.
type Source interface {
	Check(ctx context.Context) (Info, error)
	Watches(ctx context.Context, u User) ([]Watch, error)
	// Playlists are the user's music playlists; favourites, where the
	// source has them, come as one called "Favourites".
	Playlists(ctx context.Context, u User) ([]Playlist, error)
}

// New makes the source of a kind.
func New(kind, baseURL, token string) (Source, bool) {
	base := strings.TrimRight(baseURL, "/")
	client := &http.Client{Timeout: 60 * time.Second}
	switch kind {
	case "jellyfin":
		return &jellyfin{base: base, token: token, http: client}, true
	case "plex":
		return &plex{base: base, token: token, http: client}, true
	}
	return nil, false
}
