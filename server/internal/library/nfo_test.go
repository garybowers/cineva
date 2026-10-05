package library

import (
	"os"
	"path/filepath"
	"testing"
)

const kodiNFO = `<?xml version="1.0" encoding="UTF-8"?>
<movie>
  <title>The Matrix</title>
  <originaltitle>The Matrix</originaltitle>
  <year>1999</year>
  <plot>A hacker learns the truth.</plot>
  <outline>Short version.</outline>
  <tagline>Welcome to the Real World.</tagline>
  <runtime>136</runtime>
  <rating>8.2</rating>
  <genre>Action</genre>
  <genre>Science Fiction</genre>
  <mpaa>Rated R</mpaa>
  <uniqueid type="tmdb">603</uniqueid>
  <uniqueid type="imdb">tt0133093</uniqueid>
</movie>`

func TestReadNFO(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "The Matrix (1999).mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "The Matrix (1999).nfo"), []byte(kodiNFO), 0o644); err != nil {
		t.Fatal(err)
	}
	n, ok, err := ReadNFO(video)
	if err != nil || !ok {
		t.Fatalf("ReadNFO = (%v, %v, %v)", n, ok, err)
	}
	if n.Title != "The Matrix" {
		t.Errorf("Title = %q", n.Title)
	}
	if n.ReleaseYear() != 1999 {
		t.Errorf("ReleaseYear = %d", n.ReleaseYear())
	}
	if n.Synopsis() != "A hacker learns the truth." {
		t.Errorf("Synopsis = %q (should prefer plot over outline)", n.Synopsis())
	}
	tmdbID, imdbID := n.ResolvedIDs()
	if tmdbID != 603 || imdbID != "tt0133093" {
		t.Errorf("ResolvedIDs = (%d, %q), want (603, tt0133093)", tmdbID, imdbID)
	}
	if len(n.Genres) != 2 {
		t.Errorf("Genres = %v", n.Genres)
	}
}

func TestReadNFOVariants(t *testing.T) {
	tests := []struct {
		name       string
		filename   string
		body       string
		wantTmdbID int
		wantYear   int
	}{
		{
			name: "movie.nfo in the film's folder", filename: "movie.nfo",
			body:       `<movie><title>X</title><tmdbid>42</tmdbid><year>2001</year></movie>`,
			wantTmdbID: 42, wantYear: 2001,
		},
		{
			name: "legacy tmdbid element", filename: "movie.nfo",
			body:       `<movie><title>X</title><tmdbid>7</tmdbid><premiered>1994-09-23</premiered></movie>`,
			wantTmdbID: 7, wantYear: 1994,
		},
		{
			name: "premiered supplies the year", filename: "movie.nfo",
			body:     `<movie><title>X</title><premiered>1977-05-25</premiered></movie>`,
			wantYear: 1977,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			video := filepath.Join(dir, "film.mkv")
			if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, tc.filename), []byte(tc.body), 0o644); err != nil {
				t.Fatal(err)
			}
			n, ok, err := ReadNFO(video)
			if err != nil || !ok {
				t.Fatalf("ReadNFO = (%v, %v)", ok, err)
			}
			if got, _ := n.ResolvedIDs(); got != tc.wantTmdbID {
				t.Errorf("tmdbID = %d, want %d", got, tc.wantTmdbID)
			}
			if got := n.ReleaseYear(); got != tc.wantYear {
				t.Errorf("year = %d, want %d", got, tc.wantYear)
			}
		})
	}
}

func TestReadNFOAbsentAndMalformed(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "film.mkv")
	if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Absent is the normal case and must not be an error.
	if _, ok, err := ReadNFO(video); ok || err != nil {
		t.Errorf("missing sidecar: ok=%v err=%v, want (false, nil)", ok, err)
	}
	// Malformed is an error: the operator's own metadata failing to parse
	// should be reported, not silently skipped.
	if err := os.WriteFile(filepath.Join(dir, "movie.nfo"), []byte("<movie><title>oops"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ReadNFO(video); err == nil {
		t.Error("malformed sidecar should return an error")
	}
}

func TestLocalArtworkDiscovery(t *testing.T) {
	tests := []struct {
		name  string
		files []string
		want  string // which file LocalPoster should pick
	}{
		{name: "stem-prefixed poster wins", files: []string{"film-poster.jpg", "folder.jpg"}, want: "film-poster.jpg"},
		{name: "plain poster.jpg", files: []string{"poster.jpg"}, want: "poster.jpg"},
		{name: "folder.jpg alias", files: []string{"folder.jpg"}, want: "folder.jpg"},
		{name: "cover.png alias", files: []string{"cover.png"}, want: "cover.png"},
		{name: "nothing to find", files: []string{"notes.txt"}, want: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			video := filepath.Join(dir, "film.mkv")
			if err := os.WriteFile(video, []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}
			for _, f := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, f), []byte("img"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			got := LocalPoster(video)
			if tc.want == "" {
				if got != "" {
					t.Errorf("LocalPoster = %q, want none", got)
				}
				return
			}
			if filepath.Base(got) != tc.want {
				t.Errorf("LocalPoster = %q, want %q", filepath.Base(got), tc.want)
			}
		})
	}
}

func TestLocalBackdrop(t *testing.T) {
	dir := t.TempDir()
	video := filepath.Join(dir, "film.mkv")
	for _, f := range []string{"film.mkv", "fanart.jpg"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if got := LocalBackdrop(video); filepath.Base(got) != "fanart.jpg" {
		t.Errorf("LocalBackdrop = %q, want fanart.jpg", got)
	}
}

func TestNormaliseMPAA(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Rated R", "R"},
		{"R", "R"},
		{"UK:15", "15"},
		{"US:PG-13", "PG-13"},
		{"  Rated PG-13  ", "PG-13"},
		{"", ""},
	}
	for _, tc := range tests {
		if got := normaliseMPAA(tc.in); got != tc.want {
			t.Errorf("normaliseMPAA(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}
