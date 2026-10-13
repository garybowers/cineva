package library

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

func TestDatedSeasonCandidates(t *testing.T) {
	premieres := []metadata.SeasonPremiere{
		{Num: 0, AirDate: "2014-01-01"},
		{Num: 1, AirDate: "2014-02-17"},
		{Num: 2, AirDate: "2014-09-22"},
		{Num: 3, AirDate: "2015-09-21"},
		{Num: 4, AirDate: ""}, // announced, not dated
	}
	tests := []struct {
		name      string
		premieres []metadata.SeasonPremiere
		date      string
		want      []int
	}{
		{"mid-season", premieres, "2015-01-27", []int{2, 1}},
		{"on a premiere", premieres, "2015-09-21", []int{3, 2}},
		{"first season", premieres, "2014-03-01", []int{1}},
		{"before the series began", premieres, "2013-12-31", []int{}},
		{"no seasons listed: the year", nil, "2023-01-27", []int{2023}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := datedSeasonCandidates(tc.premieres, tc.date)
			if len(got) == 0 && len(tc.want) == 0 {
				return
			}
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSeasonItemPath(t *testing.T) {
	show := "/tv/Show"
	tests := []struct {
		name, seasonDir string
		season          int
		want            string
	}{
		{"season folder", show + "/Season 2", 2, show + "/Season 2"},
		{"specials folder", show + "/Specials", 0, show + "/Specials"},
		// The case that made a season its own series: no folder of its own.
		{"loose in the show folder", show, 1, show + "/Season 01"},
		{"folder disagrees with the episode", show + "/Season 2", 11, show + "/Season 11"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := seasonItemPath(show, tc.seasonDir, tc.season); got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
		})
	}
}

// Date-stamped episodes of a daily show are placed by their broadcast date:
// the season running then is fetched and the episode aired that day taken.
// One the provider has no record of is numbered by its date instead of
// being skipped. The episodes sit straight in the show's folder, which used
// to make the season row overwrite the series'.
func TestScanDatedEpisodes(t *testing.T) {
	root := t.TempDir()
	show := filepath.Join(root, "The Tonight Show Starring Jimmy Fallon")
	files := []string{
		"The Tonight Show Starring Jimmy Fallon - 2023-01-27 - Margot Robbie.mkv",
		"the.tonight.show.2022.09.29.720p.WEB.h264.mkv",
		"The Tonight Show Starring Jimmy Fallon - 2023-02-04 - Not On TMDB.mkv",
	}
	for _, f := range files {
		writeAt(t, filepath.Join(show, f), time.Now().Add(-time.Hour))
	}
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	video := newFakeVideo()
	video.tv = func(string, int) (metadata.VideoResult, error) {
		return metadata.VideoResult{TmdbID: 59941, Title: "The Tonight Show Starring Jimmy Fallon", Status: "Returning Series",
			SeasonPremieres: []metadata.SeasonPremiere{
				{Num: 0, AirDate: "2014-02-10"},
				{Num: 9, AirDate: "2021-09-13"},
				{Num: 10, AirDate: "2022-09-12"},
				{Num: 11, AirDate: "2023-09-25"},
			}}, nil
	}
	video.season = func(tvID, season int) (metadata.SeasonResult, error) {
		if season != 10 {
			return metadata.SeasonResult{Episodes: []metadata.EpisodeResult{{Num: 1, AirDate: "2021-09-13"}}}, nil
		}
		return metadata.SeasonResult{TmdbID: 1010, Episodes: []metadata.EpisodeResult{
			{TmdbID: 1, Num: 13, Title: "Ben Stiller", AirDate: "2022-09-29"},
			{TmdbID: 2, Num: 80, Title: "Margot Robbie, Diego Calva", AirDate: "2023-01-27"},
		}}, nil
	}
	m := NewManager(st, video, "")
	m.retryPace = 0
	m.Probe = func(string) (media.Info, error) { return media.Info{RuntimeSecs: 2700}, nil }
	scanOnce(t, m, lib, false)

	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	byKind := map[store.ItemKind][]store.Item{}
	byID := map[string]store.Item{}
	for _, it := range items {
		byKind[it.Kind] = append(byKind[it.Kind], it)
		byID[it.ID] = it
	}
	series := byKind[store.KindSeries]
	if len(series) != 1 || series[0].Path != show || series[0].ParentID != "" {
		t.Fatalf("series %+v, want one at the show folder with no parent", series)
	}
	type placed struct {
		season, episode int
		title, aired    string
	}
	want := map[string]placed{
		files[0]: {10, 80, "Margot Robbie", "2023-01-27"},
		files[1]: {10, 13, "Ben Stiller", "2022-09-29"},
		files[2]: {2023, 204, "Not On TMDB", "2023-02-04"},
	}
	eps := byKind[store.KindEpisode]
	if len(eps) != 3 {
		t.Fatalf("%d episodes, want 3", len(eps))
	}
	for _, ep := range eps {
		w, ok := want[filepath.Base(ep.Path)]
		if !ok {
			t.Errorf("unexpected episode %q", ep.Path)
			continue
		}
		if ep.SeasonNum != w.season || ep.EpisodeNum != w.episode || ep.Title != w.title || ep.AirDate != w.aired {
			t.Errorf("%s: S%dE%d %q aired %q, want S%dE%d %q aired %q", filepath.Base(ep.Path),
				ep.SeasonNum, ep.EpisodeNum, ep.Title, ep.AirDate, w.season, w.episode, w.title, w.aired)
		}
		season := byID[ep.ParentID]
		if season.Kind != store.KindSeason || season.SeasonNum != w.season || season.ParentID != series[0].ID {
			t.Errorf("%s: parent %+v, want season %d of the series", filepath.Base(ep.Path), season, w.season)
		}
	}
	if n := len(byKind[store.KindSeason]); n != 2 {
		t.Errorf("%d seasons, want 2 (10 and the fallback 2023)", n)
	}
	// Season 10 is fetched once for all three placements and the fill;
	// season 9 only for the date that was in neither, and the fallback
	// season numbered by the year not at all.
	if !reflect.DeepEqual(video.seasons, []int{10, 9}) {
		t.Errorf("fetched seasons %v, want [10 9]", video.seasons)
	}
}
