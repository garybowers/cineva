package library

import "testing"

func TestParseEpisode(t *testing.T) {
	tests := []struct {
		path    string
		ok      bool
		show    string
		season  int
		episode int
		title   string
	}{
		{"/tv/Succession/Season 04/Succession - S04E02 - Rehearsal HDTV-1080p.mkv", true, "Succession", 4, 2, "Rehearsal"},
		{"/tv/The Wire/Season 01/The Wire - S01E01 - The Target.mkv", true, "The Wire", 1, 1, "The Target"},
		{"/tv/Show/Season 02/s02e10.mkv", true, "Show", 2, 10, ""},
		{"/tv/Loose/S01E05 720p.mkv", true, "Loose", 1, 5, ""},
		{"/tv/Movies/Heat (1995).mkv", false, "", 0, 0, ""},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got, ok := ParseEpisode(tt.path)
			if ok != tt.ok {
				t.Fatalf("ok = %v, want %v", ok, tt.ok)
			}
			if !ok {
				return
			}
			if got.Show != tt.show || got.Season != tt.season || got.Episode != tt.episode || got.Title != tt.title {
				t.Errorf("got %+v, want show=%q s=%d e=%d title=%q", got, tt.show, tt.season, tt.episode, tt.title)
			}
		})
	}
}

func TestParseMovie(t *testing.T) {
	tests := []struct {
		path  string
		title string
		year  int
	}{
		{"/movies/Heat (1995).mkv", "Heat", 1995},
		{"/movies/Heat (1995)/Heat (1995) Remastered BluRay-1080p.mkv", "Heat", 1995},
		{"/movies/Alien (1979)/movie.mkv", "Alien", 1979},
		{"/movies/Unknown Film.mkv", "Unknown Film", 0},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := ParseMovie(tt.path)
			if got.Title != tt.title || got.Year != tt.year {
				t.Errorf("got %+v, want title=%q year=%d", got, tt.title, tt.year)
			}
		})
	}
}
