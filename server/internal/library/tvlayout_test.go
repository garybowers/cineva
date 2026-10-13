package library

import (
	"path/filepath"
	"testing"
)

const tvRoot = "/mnt/media/TV Shows"

// The series folder is the first directory under the library root, whatever
// the depth below it. Assuming the grandparent was the series turned season
// folders into series of their own.
func TestSeriesDirFor(t *testing.T) {
	roots := []string{"/mnt/other", tvRoot}
	tests := []struct {
		name, path, want string
	}{
		{
			"show/season/file",
			tvRoot + "/Alone/Season 5/alone.s05e01.mkv",
			tvRoot + "/Alone",
		},
		{
			"episodes loose in the show folder",
			tvRoot + "/Alone/alone.s03e01.mkv",
			tvRoot + "/Alone",
		},
		{
			"release folder per episode — the layout that broke it",
			tvRoot + "/Alone/Season 3/Alone.S03E01.1080p.WEB.H264-GROUP/alone.s03e01.mkv",
			tvRoot + "/Alone",
		},
		{
			"deeper still",
			tvRoot + "/Archer (2009)/Season 1/Disc 1/VIDEO_TS/archer.s01e01.mkv",
			tvRoot + "/Archer (2009)",
		},
		{
			"file loose in the library root has no series folder",
			tvRoot + "/stray.s01e01.mkv",
			"",
		},
		{
			"path outside every root",
			"/somewhere/else/Show/Season 1/x.s01e01.mkv",
			"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := seriesDirFor(roots, tc.path); got != tc.want {
				t.Errorf("seriesDirFor(%q)\n  got  %q\n  want %q", tc.path, got, tc.want)
			}
		})
	}
}

// The season folder is the nearest "Season N" ancestor, not simply the
// parent — which is wrong as soon as a release folder sits in between.
func TestSeasonDirFor(t *testing.T) {
	series := tvRoot + "/Alone"
	tests := []struct {
		name, path, want string
	}{
		{"direct child", series + "/Season 5/alone.s05e01.mkv", series + "/Season 5"},
		{"below a release folder", series + "/Season 3/Alone.S03E01-GROUP/alone.s03e01.mkv", series + "/Season 3"},
		{"no season folder falls back to the series", series + "/alone.s03e01.mkv", series},
		{"season spelled without a space", series + "/Season05/alone.s05e01.mkv", series + "/Season05"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonDirFor(series, tc.path); got != tc.want {
				t.Errorf("seasonDirFor(%q)\n  got  %q\n  want %q", tc.path, got, tc.want)
			}
		})
	}
}

// Series names come from the folder, with the year kept separately so a
// match can be disambiguated rather than guessed.
func TestShowName(t *testing.T) {
	tests := []struct {
		in    string
		title string
		year  int
	}{
		{"Alone", "Alone", 0},
		{"Alone (UK)", "Alone (UK)", 0},
		{"Archer (2009)", "Archer", 2009},
		{"Animal Kingdom (2016)", "Animal Kingdom", 2016},
		{"Animal.Kingdom", "Animal Kingdom", 0},
		{"Big.Sky.2020", "Big Sky", 2020},
		// A title that is only a year is a title.
		{"1883", "1883", 0},
		{"Babylon 5", "Babylon 5", 0},
		{"99", "99", 0},
		// A season pack at the top level carries a marker the series does not.
		{"Ted.Lasso.S01.1080p.WEBRip.x265", "Ted Lasso", 0},
		{"The Wire Season 2", "The Wire", 0},
		{"Fargo S03 1080p", "Fargo", 0},
		// ...but a number that is part of the name stays.
		{"Babylon 5", "Babylon 5", 0},
		{"Stranger Things", "Stranger Things", 0},
		{"Mr. Robot", "Mr. Robot", 0},
		{"Dr. Death", "Dr. Death", 0},
	}
	for _, tc := range tests {
		title, year := showName(tc.in)
		if title != tc.title || year != tc.year {
			t.Errorf("showName(%q) = (%q, %d), want (%q, %d)", tc.in, title, year, tc.title, tc.year)
		}
	}
}

// End to end on the paths that produced the duplicate series.
func TestEpisodeLayoutsFromRealLibrary(t *testing.T) {
	roots := []string{tvRoot}
	tests := []struct {
		path     string
		series   string
		seasonNo int
		epNo     int
	}{
		{tvRoot + "/Alone/Season 5/alone.s05e01.1080p.web.h264.mkv", "Alone", 5, 1},
		{tvRoot + "/Animal Kingdom (2016)/Season 5/Animal.Kingdom.S05E01.1080p.mkv", "Animal Kingdom", 5, 1},
		{tvRoot + "/Archer (2009)/Season 1/Archer.2009.S01E01.720p.mkv", "Archer", 1, 1},
		{tvRoot + "/Babylon 5/Season 1/Babylon 5 - S01E01 - Midnight on the Firing Line.mkv", "Babylon 5", 1, 1},
	}
	for _, tc := range tests {
		ep, ok := ParseEpisode(tc.path)
		if !ok {
			t.Errorf("ParseEpisode(%q) failed", tc.path)
			continue
		}
		dir := seriesDirFor(roots, tc.path)
		title, _ := showName(filepath.Base(dir))
		if title != tc.series {
			t.Errorf("%q -> series %q, want %q", tc.path, title, tc.series)
		}
		if ep.Season != tc.seasonNo || ep.Episode != tc.epNo {
			t.Errorf("%q -> S%02dE%02d, want S%02dE%02d", tc.path, ep.Season, ep.Episode, tc.seasonNo, tc.epNo)
		}
	}
}

// Episodes numbered in the filename with the season in the folder —
// "Season 1/02 Part Two.mp4". This layout is as common as SxxEyy and was
// silently dropping 176 files of the test library, 47 of House of Cards.
func TestNumberedEpisodeLayout(t *testing.T) {
	const show = tvRoot + "/House of Cards"
	tests := []struct {
		name, path string
		ok         bool
		season, ep int
		title      string
	}{
		{"number and title", show + "/Season 1/05 Chapter 5.m4v", true, 1, 5, "Chapter 5"},
		{"two-digit episode", show + "/Season 3/11 Birth.mp4", true, 3, 11, "Birth"},
		{"title that is itself numeric", show + "/Season 1/01 1_23_45.m4v", true, 1, 1, "1_23_45"},
		{"apostrophes and underscores", show + "/Season 2/18 Love's Labor Lost_ Part 1.mp4", true, 2, 18, "Love's Labor Lost_ Part 1"},
		{"dash separator", show + "/Season 1/01 - Pilot.mkv", true, 1, 1, "Pilot"},
		{"E prefix", show + "/Season 4/E03 Hide and Seek.mkv", true, 4, 3, "Hide and Seek"},
		{"bare number", show + "/Season 2/07.mp4", true, 2, 7, ""},

		// Not episodes, and inventing numbers for them would scatter bonus
		// material through the season.
		{"outtakes reel", show + "/Season 1/Season 1 - Outtakes.mkv", false, 0, 0, ""},
		{"named special", show + "/Season 1/Pilot - The Services.mp4", false, 0, 0, ""},
		{"a year is not an episode", show + "/Season 1/1917.mkv", false, 0, 0, ""},
		{"zero is not an episode", show + "/Season 1/00 Prologue.mkv", false, 0, 0, ""},
		// Episodes numbered straight inside the series folder are one
		// season's worth by the only reading the layout offers.
		{"no season folder", show + "/05 Chapter 5.m4v", true, 1, 5, "Chapter 5"},
		{"underscore separator", show + "/01_Pilot.mkv", true, 1, 1, "Pilot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := ParseEpisode(tc.path)
			if ok != tc.ok {
				t.Fatalf("ParseEpisode(%q) ok=%v, want %v (got %+v)", tc.path, ok, tc.ok, got)
			}
			if !tc.ok {
				return
			}
			if got.Season != tc.season || got.Episode != tc.ep {
				t.Errorf("%q -> S%02dE%02d, want S%02dE%02d", tc.path, got.Season, got.Episode, tc.season, tc.ep)
			}
			if got.Title != tc.title {
				t.Errorf("%q -> title %q, want %q", tc.path, got.Title, tc.title)
			}
			if got.Show != "House of Cards" {
				t.Errorf("%q -> show %q, want \"House of Cards\"", tc.path, got.Show)
			}
		})
	}
}

// A "Season 3 Extras" folder is bonus material, like a bare "Extras".
func TestExtrasDirIncludesSeasonPrefixed(t *testing.T) {
	for _, dir := range []string{"Extras", "extras", "Season 3 Extras", "Season 1 Deleted Scenes", "Featurettes"} {
		if !extrasDirRe.MatchString(dir) {
			t.Errorf("%q should be treated as an extras folder", dir)
		}
	}
	for _, dir := range []string{"Season 1", "Season 3", "House of Cards"} {
		if extrasDirRe.MatchString(dir) {
			t.Errorf("%q must not be treated as an extras folder", dir)
		}
	}
}

// Specials are season 0 — where TMDB, Plex and Jellyfin all put Christmas
// episodes and one-offs. Treating the folder as bonus material dropped 17
// real episodes of the test library, every one properly marked S00Exx.
func TestSpecialsAreSeasonZeroNotExtras(t *testing.T) {
	if extrasDirRe.MatchString("Specials") {
		t.Error(`"Specials" is being treated as an extras folder; its episodes never reach the scanner`)
	}
	for _, name := range []string{"Specials", "specials", "Special"} {
		n, ok := seasonFromDir(name)
		if !ok || n != 0 {
			t.Errorf("seasonFromDir(%q) = (%d, %v), want (0, true)", name, n, ok)
		}
	}
	// Real paths from the library.
	cases := []struct {
		path       string
		show       string
		season, ep int
	}{
		{tvRoot + "/Two Doors Down/Specials/Two Doors Down - S00E02 - 2017 Christmas Special SDTV.mp4", "Two Doors Down", 0, 2},
		{tvRoot + "/The IT Crowd/Specials/The IT Crowd - S00E04 - The Internet Is Coming WEBRip-1080p.mkv", "The IT Crowd", 0, 4},
		{tvRoot + "/Alone/Specials/Alone - S00E07 - Tales from the Island HDTV-720p.mkv", "Alone", 0, 7},
	}
	for _, c := range cases {
		got, ok := ParseEpisode(c.path)
		if !ok {
			t.Errorf("ParseEpisode(%q) refused a special", c.path)
			continue
		}
		if got.Season != c.season || got.Episode != c.ep || got.Show != c.show {
			t.Errorf("%q -> %s S%02dE%02d, want %s S%02dE%02d",
				c.path, got.Show, got.Season, got.Episode, c.show, c.season, c.ep)
		}
	}
	// And the season folder is found, so specials do not land on the series.
	series := tvRoot + "/Alone"
	if got := seasonDirFor(series, series+"/Specials/Alone - S00E07 - x.mkv"); got != series+"/Specials" {
		t.Errorf("seasonDirFor gave %q, want the Specials folder", got)
	}
	// Genuine bonus material in there still has no marker, so it is refused.
	if _, ok := ParseEpisode(series + "/Specials/Behind the scenes.mkv"); ok {
		t.Error("an unmarked file in Specials was taken as an episode")
	}
}

// Episodes numbered inside the series folder, with no season folder at all.
func TestFlatNumberedLayout(t *testing.T) {
	const show = tvRoot + "/Some Show"
	cases := []struct {
		path       string
		season, ep int
		title      string
	}{
		{show + "/01_Pilot.mkv", 1, 1, "Pilot"},
		{show + "/02_The Second One.mp4", 1, 2, "The Second One"},
		{show + "/10 Finale.mkv", 1, 10, "Finale"},
	}
	for _, c := range cases {
		got, ok := ParseEpisode(c.path)
		if !ok {
			t.Errorf("ParseEpisode(%q) refused", c.path)
			continue
		}
		if got.Season != c.season || got.Episode != c.ep || got.Title != c.title || got.Show != "Some Show" {
			t.Errorf("%q -> show=%q S%02dE%02d %q", c.path, got.Show, got.Season, got.Episode, got.Title)
		}
	}
	// A marked filename still wins over the assumed season 1.
	if got, _ := ParseEpisode(show + "/Some.Show.S04E02.mkv"); got.Season != 4 {
		t.Errorf("a marked filename gave season %d, want 4", got.Season)
	}
}

// A library pointed straight at one show's folder: its children are season
// folders, so the root itself is the series. Taking the first child produced
// a library of series called "Season 01", with no show at all and season
// lookups against an id that was never found.
func TestLibraryRootThatIsItselfAShow(t *testing.T) {
	root := "/mnt/media/The IT Crowd"
	roots := []string{root}
	for _, p := range []string{
		root + "/Season 01/The IT Crowd - S01E01 - Yesterday's Jam.mkv",
		root + "/Specials/The IT Crowd - S00E04 - The Internet Is Coming.mkv",
		root + "/Season 4/Disc 1/it.crowd.s04e01.mkv",
	} {
		if got := seriesDirFor(roots, p); got != root {
			t.Errorf("seriesDirFor(%q)\n  got  %q\n  want %q (the root is the series)", p, got, root)
		}
	}
	// The ordinary arrangement is unaffected: a root holding show folders.
	tv := []string{tvRoot}
	if got := seriesDirFor(tv, tvRoot+"/The IT Crowd/Season 01/x.s01e01.mkv"); got != tvRoot+"/The IT Crowd" {
		t.Errorf("a normal library root gave %q", got)
	}
}
