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
		{"/movies/10 Cloverfield Lane/10 Cloverfield Lane 2016 Remux-2160p.mkv", "10 Cloverfield Lane", 2016},
		{"/movies/Project Hail Mary (2026)/Project Hail Mary 2026 WEBDL-2160p.mkv", "Project Hail Mary", 2026},
		{"/movies/Batteries Not Included/-batteries not included 1987 Bluray-1080p.mkv", "batteries not included", 1987},
		{"/movies/13 Hours/13 Hours The Secret Soldiers of Benghazi 2016.mkv", "13 Hours The Secret Soldiers of Benghazi", 2016},
		{"/movies/007_ Moonraker/007_ Moonraker.m4v", "007_ Moonraker", 0},
		// Two years: the last is the release year, the first belongs to the
		// title. Taking the first gave "Blade Runner" for Blade Runner 2049.
		{"/movies/Blade Runner/Blade Runner 2049 2017 Remux-1080p.mkv", "Blade Runner 2049", 2017},
		{"/movies/Apollo 13/Apollo 13 1995 Bluray-1080p.mkv", "Apollo 13", 1995},
		// Dots stand in for spaces when the name has none of its own.
		{"/movies/American Siege/American.Siege.2021.1080p.WEB-DL.H264-CMRG.mkv", "American Siege", 2021},
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

func TestPreferTagTitle(t *testing.T) {
	tests := []struct {
		name   string
		tag    string
		parsed string
		want   bool
	}{
		// Punctuation the filename flattens — what tags are for.
		{"restores apostrophe", "Bram Stoker's Dracula", "Bram Stokers Dracula", true},
		{"restores colon", "Dune: Part Two", "Dune Part Two", true},
		{"identical", "Dredd", "Dredd", true},
		{"case only", "dr. no", "Dr. No", true},
		// Release names encoders stamp into the title tag, beside clean paths.
		{"dotted release", "Almost.Famous.2000.1080p.BluRay.x264-nikt0", "Almost Famous", false},
		{"spaced release", " Doctor Strange 2016 IMAX BluRay 1080p DTS AC3 x264-MgB", "Doctor Strange", false},
		{"group only", "EVO", "Dunkirk", false},
		{"remux", "300.Rise.of.an.Empire.2014.BluRay.Remux.1080p.AVC.DTS-HD.MA.7.1-HiFi", "300 Rise of an Empire", false},
		{"junk site name", "Downloaded From HDHub4u", "Wicked", false},
		// Nothing parsed from the path: any non-release tag beats no title.
		{"empty parsed takes clean tag", "The Shawshank Redemption", "", true},
		{"empty parsed rejects release", "Arctic.2018.1080p.BluRay.x265-RARBG", "", false},
		{"empty tag", "", "Dredd", false},
		// Better punctuation behind a trailing year/edition: keep the colon,
		// drop the tail.
		{"year tail", "American Pie 2 (2001)", "American Pie 2", true},
		{"colon plus year and edition", "American Pie Presents: Beta House (2007) Unrated", "American Pie Presents Beta House", true},
		// A bare trailing year is ambiguous with a title that ends in one
		// ("Blade Runner 2049"), so it is not stripped: the tag is rejected
		// and the title parsed from the path stands, which is already right.
		{"bare year tail rejected", "Aladdin 2019", "Aladdin", false},
		{"title ending in a year is not truncated", "Blade Runner 2049", "Blade Runner", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, got := PreferTagTitle(tt.tag, tt.parsed)
			if got != tt.want {
				t.Errorf("PreferTagTitle(%q, %q) ok = %v, want %v", tt.tag, tt.parsed, got, tt.want)
			}
		})
	}
}

func TestPreferTagTitleKeepsPunctuation(t *testing.T) {
	tests := []struct{ tag, parsed, want string }{
		{"American Pie Presents: Beta House (2007) Unrated", "American Pie Presents Beta House", "American Pie Presents: Beta House"},
		{"Bram Stoker's Dracula", "Bram Stokers Dracula", "Bram Stoker's Dracula"},
		{"Dune: Part Two", "Dune Part Two", "Dune: Part Two"},
	}
	for _, tt := range tests {
		t.Run(tt.tag, func(t *testing.T) {
			got, ok := PreferTagTitle(tt.tag, tt.parsed)
			if !ok || got != tt.want {
				t.Errorf("PreferTagTitle(%q, %q) = (%q, %v), want (%q, true)", tt.tag, tt.parsed, got, ok, tt.want)
			}
		})
	}
}

func TestIsExtra(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		// Bonus material, by folder…
		{"/movies/Dune (2021)/Featurettes/Making Of.mkv", true},
		{"/movies/Dune (2021)/Extras/Deleted Scenes.mkv", true},
		{"/movies/Dune (2021)/Behind The Scenes/bts.mp4", true},
		{"/movies/Dune (2021)/Trailers/teaser.mp4", true},
		// …and by filename suffix.
		{"/movies/Dune (2021)/Dune-trailer.mkv", true},
		{"/movies/Dune (2021)/Dune.sample.mkv", true},
		{"/movies/Dune (2021)/Dune-featurette.mkv", true},
		// The feature itself, including names that merely contain a word
		// like "scene" or "short" without the separator convention.
		{"/movies/Dune (2021)/Dune 2021 Bluray-1080p.mkv", false},
		{"/movies/Deleted Scenes The Movie (2019)/Deleted Scenes The Movie.mkv", false},
		{"/movies/A Short Film (2020)/A Short Film.mkv", false},
		{"/movies/Project Hail Mary (2026)/Project Hail Mary 2026 WEBDL-2160p.mkv", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			if got := IsExtra(tt.path); got != tt.want {
				t.Errorf("IsExtra(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestParseMovieIDHints(t *testing.T) {
	tests := []struct {
		name       string
		path       string
		wantTitle  string
		wantYear   int
		wantTmdbID int
		wantImdbID string
	}{
		{
			name:      "jellyfin bracket form",
			path:      "/m/The Matrix (1999) [tmdbid=603].mkv",
			wantTitle: "The Matrix", wantYear: 1999, wantTmdbID: 603,
		},
		{
			name:      "radarr brace form",
			path:      "/m/The Matrix (1999) {tmdb-603}.mkv",
			wantTitle: "The Matrix", wantYear: 1999, wantTmdbID: 603,
		},
		{
			name:      "imdb id",
			path:      "/m/The Matrix (1999) [imdbid=tt0133093].mkv",
			wantTitle: "The Matrix", wantYear: 1999, wantImdbID: "tt0133093",
		},
		{
			name:       "hint on the parent directory",
			path:       "/m/The Matrix (1999) [tmdbid=603]/movie.mkv",
			wantTmdbID: 603,
		},
		{
			name:      "both ids at once",
			path:      "/m/Heat (1995) [tmdbid=949][imdbid=tt0113277].mkv",
			wantTitle: "Heat", wantYear: 1995, wantTmdbID: 949, wantImdbID: "tt0113277",
		},
		{
			name:      "no hint leaves the ids empty",
			path:      "/m/The Matrix (1999).mkv",
			wantTitle: "The Matrix", wantYear: 1999,
		},
		{
			// A bare [1080p] tag must not be read as an id.
			name:      "quality tags are not ids",
			path:      "/m/The Matrix (1999) [1080p][x265].mkv",
			wantTitle: "The Matrix", wantYear: 1999,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ParseMovie(tc.path)
			if tc.wantTitle != "" && got.Title != tc.wantTitle {
				t.Errorf("Title = %q, want %q", got.Title, tc.wantTitle)
			}
			if tc.wantYear != 0 && got.Year != tc.wantYear {
				t.Errorf("Year = %d, want %d", got.Year, tc.wantYear)
			}
			if got.TmdbID != tc.wantTmdbID {
				t.Errorf("TmdbID = %d, want %d", got.TmdbID, tc.wantTmdbID)
			}
			if got.ImdbID != tc.wantImdbID {
				t.Errorf("ImdbID = %q, want %q", got.ImdbID, tc.wantImdbID)
			}
		})
	}
}
