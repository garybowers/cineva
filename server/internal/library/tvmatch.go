package library

import (
	"context"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"

	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// seasonCache holds the provider's season data for one scan, so a season is
// fetched once however many of its episodes changed — and a season looked
// at to place a date-stamped episode is not fetched again to fill it in.
type seasonCache struct {
	video metadata.VideoProvider
	data  map[[2]int]*metadata.SeasonResult // (series tmdb id, season) -> nil when not found
}

func newSeasonCache(video metadata.VideoProvider) *seasonCache {
	return &seasonCache{video: video, data: map[[2]int]*metadata.SeasonResult{}}
}

// get returns a season of a matched series, or false when there is no
// provider, the series is unmatched, or the provider has no such season.
func (c *seasonCache) get(ctx context.Context, seriesTmdb, season int) (metadata.SeasonResult, bool) {
	if c.video == nil || seriesTmdb == 0 {
		return metadata.SeasonResult{}, false
	}
	key := [2]int{seriesTmdb, season}
	sr, seen := c.data[key]
	if !seen {
		if r, err := c.video.TVSeason(ctx, seriesTmdb, season); err == nil {
			sr = &r
		} else if ctx.Err() != nil {
			// A cancelled scan is not the provider saying no; do not
			// remember it as an answer.
			return metadata.SeasonResult{}, false
		}
		c.data[key] = sr
	}
	if sr == nil {
		return metadata.SeasonResult{}, false
	}
	return *sr, true
}

// seasonItemPath is the path a season's row is stored under. A season folder
// is its own path. Episodes sitting straight in the show's folder have none,
// and taking the show's folder for the season as well gave the series and
// the season one path between them — the season's row overwrote the
// series' and became its own parent. Those seasons, and one whose episodes
// say they belong to a different season than the folder they are in, get a
// path of their own beneath the show instead. Nothing reads a season's path
// from disk.
func seasonItemPath(showDir, seasonDir string, season int) string {
	if seasonDir != showDir {
		if n, ok := seasonFromDir(filepath.Base(seasonDir)); ok && n == season {
			return seasonDir
		}
	}
	return filepath.Join(showDir, fmt.Sprintf("Season %02d", season))
}

// applySeasonData fills a season's row from the provider's season.
func (m *Manager) applySeasonData(ctx context.Context, season *store.Item, seriesTmdb int, sr metadata.SeasonResult) {
	season.Overview = sr.Overview
	// A season could not be re-matched or refreshed on its own without its
	// own id, the way a film can.
	season.TmdbID = sr.TmdbID
	season.AirDate = sr.AirDate
	if len(sr.AirDate) >= 4 {
		if y, err := strconv.Atoi(sr.AirDate[:4]); err == nil {
			season.Year = y
		}
	}
	if sr.PosterURL != "" && m.ArtworkDir != "" {
		name := fmt.Sprintf("tmdb-s%d-season%d", seriesTmdb, season.SeasonNum)
		if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, sr.PosterURL, m.ArtworkDir, name); err == nil {
			season.Poster = "/artwork/" + file
		}
	}
}

// applyEpisodeData fills whatever tags and the filename left of an episode
// from its season's provider data, and returns its guest stars. A title
// still at its "Episode N" placeholder takes the provider's.
func (m *Manager) applyEpisodeData(ctx context.Context, item *store.Item, seriesTmdb, season, episode int, sr metadata.SeasonResult) []metadata.Person {
	for _, te := range sr.Episodes {
		if te.Num != episode {
			continue
		}
		if item.Overview == "" {
			item.Overview = te.Overview
		}
		if item.Title == fmt.Sprintf("Episode %d", episode) && te.Title != "" {
			item.Title = te.Title
		}
		if te.StillURL != "" && m.ArtworkDir != "" {
			name := fmt.Sprintf("tmdb-s%d-e%dx%d", seriesTmdb, season, episode)
			if file, err := metadata.DownloadArtwork(ctx, http.DefaultClient, te.StillURL, m.ArtworkDir, name); err == nil {
				item.Poster = "/artwork/" + file
			}
		}
		item.TmdbID = te.TmdbID
		item.Rating, item.VoteCount = te.Rating, te.VoteCount
		// The broadcast date is the only date an episode has. Two thirds of
		// this library's episodes had none at all, which also left nothing
		// correct to sort them by within a year.
		if te.AirDate != "" {
			item.AirDate = te.AirDate
		}
		if item.Year == 0 && len(te.AirDate) >= 4 {
			if y, err := strconv.Atoi(te.AirDate[:4]); err == nil {
				item.Year = y
			}
		}
		// Guest stars are the people in this episode and no other.
		return te.GuestStars
	}
	return nil
}

// datedSeasonCandidates are the seasons worth fetching to find an episode
// by its broadcast date: the season that began most recently on or before
// the date, then the one before it, for a season that began early or an
// episode TMDB filed late. Specials are not searched — a daily show's
// specials are seldom dated like its episodes.
//
// When the provider listed no seasons, a season numbered by the year is
// tried, which is how TMDB numbers some daily shows.
func datedSeasonCandidates(premieres []metadata.SeasonPremiere, date string) []int {
	var started []metadata.SeasonPremiere
	for _, p := range premieres {
		if p.Num > 0 && p.AirDate != "" && p.AirDate <= date {
			started = append(started, p)
		}
	}
	if len(premieres) == 0 {
		if y, err := strconv.Atoi(date[:4]); err == nil {
			return []int{y}
		}
		return nil
	}
	sort.SliceStable(started, func(i, j int) bool { return started[i].AirDate > started[j].AirDate })
	out := make([]int, 0, 2)
	for _, p := range started[:min(2, len(started))] {
		out = append(out, p.Num)
	}
	return out
}

// placeDatedEpisode gives a date-stamped episode its season and number:
// the episode the provider lists as broadcast that day, in the season
// running then. Where the series is unmatched or the provider has no
// episode on that date, it falls back to the year as the season and the
// month and day as the number (27 January 2023 is 2023x0127) — the
// convention other media servers use for daily shows, which keeps the
// episodes together, in order, and stable from one scan to the next.
// Reports whether the provider placed it.
func (m *Manager) placeDatedEpisode(ctx context.Context, ep *EpisodeInfo, seriesTmdb int, premieres []metadata.SeasonPremiere, seasons *seasonCache) bool {
	if seriesTmdb != 0 {
		for _, n := range datedSeasonCandidates(premieres, ep.AirDate) {
			sr, ok := seasons.get(ctx, seriesTmdb, n)
			if !ok {
				continue
			}
			for _, te := range sr.Episodes {
				if te.AirDate == ep.AirDate && te.Num > 0 {
					ep.Season, ep.Episode = n, te.Num
					return true
				}
			}
		}
	}
	y, _ := strconv.Atoi(ep.AirDate[:4])
	mo, _ := strconv.Atoi(ep.AirDate[5:7])
	d, _ := strconv.Atoi(ep.AirDate[8:10])
	ep.Season, ep.Episode = y, mo*100+d
	logger.Debug("dated episode not found at the provider, numbered by its date",
		"show", ep.Show, "air_date", ep.AirDate, "season", ep.Season, "episode", ep.Episode)
	return false
}
