package requests

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/store"
)

// What a series held in the library is missing, by Sonarr's account: the
// episodes it lists that are not on disk, and those still to air.

// MissingEpisode is an episode Sonarr knows of that is not on disk.
type MissingEpisode struct {
	Season  int    `json:"season"`
	Episode int    `json:"episode"`
	Title   string `json:"title"`
	AirDate string `json:"airDate,omitempty"`
	// Status is "missing" (aired, not here) or "upcoming" (not aired yet).
	Status    string `json:"status"`
	Monitored bool   `json:"monitored"`
}

// MissingSeason is one season's gaps.
type MissingSeason struct {
	SeasonNumber int `json:"seasonNumber"`
	// Held counts its episodes on disk, Total those Sonarr lists.
	Held     int              `json:"held"`
	Total    int              `json:"total"`
	Episodes []MissingEpisode `json:"episodes"`
}

// Missing is a series' gaps. Managed is false when no connected Sonarr has
// the series, in which case nothing else is filled in.
type Missing struct {
	// TmdbID is the series', which a request for its gaps names.
	TmdbID     int             `json:"tmdbId"`
	Managed    bool            `json:"managed"`
	Connection string          `json:"connection,omitempty"`
	Seasons    []MissingSeason `json:"seasons"`
	// Requests are the open requests for the series, so its pages can say
	// "Requested" beside what they cover.
	Requests []store.MediaRequest `json:"requests"`
}

// missingTTL is how long Sonarr's episode list is trusted. Short: the
// point is to save a burst of page views, not to show stale gaps.
const missingTTL = 2 * time.Minute

type missingEntry struct {
	at       time.Time
	conn     store.ArrConnection
	seriesID int // 0: no Sonarr has it
	episodes []arr.Episode
}

// ErrNotManaged is a series no connected Sonarr has.
var ErrNotManaged = errors.New("no connected Sonarr has this series")

// sonarrSeries finds the series in a connected Sonarr, with its episodes.
func (s *Service) sonarrSeries(ctx context.Context, tmdbID int) (missingEntry, error) {
	s.missingMu.Lock()
	e, ok := s.missing[tmdbID]
	s.missingMu.Unlock()
	if ok && s.Now().Sub(e.at) < missingTTL {
		return e, nil
	}
	e = missingEntry{at: s.Now()}
	if s.Discover == nil {
		return e, ErrNoTMDB
	}
	d, err := s.Discover.TVDetail(ctx, tmdbID)
	if err != nil {
		return e, err
	}
	if d.TvdbID == 0 {
		return e, nil
	}
	conns, err := s.Store.ArrConnections(ctx)
	if err != nil {
		return e, err
	}
	for _, conn := range conns {
		if conn.Kind != "sonarr" {
			continue
		}
		client := arr.New(conn.Kind, conn.URL, conn.APIKey)
		series, err := client.SeriesByTVDB(ctx, d.TvdbID)
		if err != nil {
			reqLog.Warn("looking up a series in Sonarr failed", "connection", conn.Name, "err", err)
			continue
		}
		if series == nil {
			continue
		}
		eps, err := client.Episodes(ctx, series.ID())
		if err != nil {
			return e, err
		}
		e.conn, e.seriesID, e.episodes = conn, series.ID(), eps
		break
	}
	s.missingMu.Lock()
	s.missing[tmdbID] = e
	s.missingMu.Unlock()
	return e, nil
}

// MissingFor reports a series' gaps. Specials (season 0) are left out:
// they are rarely wanted and would fill the page.
func (s *Service) MissingFor(ctx context.Context, series store.Item) (Missing, error) {
	out := Missing{TmdbID: series.TmdbID, Seasons: []MissingSeason{}, Requests: []store.MediaRequest{}}
	if series.Kind != store.KindSeries || series.TmdbID == 0 {
		return out, nil
	}
	e, err := s.sonarrSeries(ctx, series.TmdbID)
	if err != nil {
		return out, err
	}
	reqs, err := s.Store.Requests(ctx, store.RequestFilter{MediaType: store.RequestTV, TmdbID: series.TmdbID, Statuses: pending})
	if err != nil {
		return out, err
	}
	out.Requests = reqs
	if e.seriesID == 0 {
		return out, nil
	}
	out.Managed, out.Connection = true, e.conn.Name
	disk, _, err := s.Store.SeriesOnDisk(ctx, series.TmdbID)
	if err != nil {
		return out, err
	}
	now := s.Now()
	bySeason := map[int]*MissingSeason{}
	for _, ep := range e.episodes {
		if ep.SeasonNumber == 0 {
			continue
		}
		ms := bySeason[ep.SeasonNumber]
		if ms == nil {
			ms = &MissingSeason{SeasonNumber: ep.SeasonNumber, Held: len(disk[ep.SeasonNumber]), Episodes: []MissingEpisode{}}
			bySeason[ep.SeasonNumber] = ms
		}
		ms.Total++
		if disk[ep.SeasonNumber][ep.EpisodeNumber] {
			continue
		}
		status := "missing"
		aired, err := time.Parse(time.RFC3339, ep.AirDateUTC)
		if err != nil {
			if ep.AirDate == "" {
				continue // no date at all: a placeholder, not worth a row
			}
			status = "upcoming"
		} else if aired.After(now) {
			status = "upcoming"
		}
		ms.Episodes = append(ms.Episodes, MissingEpisode{
			Season: ep.SeasonNumber, Episode: ep.EpisodeNumber, Title: ep.Title,
			AirDate: ep.AirDate, Status: status, Monitored: ep.Monitored,
		})
	}
	for _, ms := range bySeason {
		slices.SortFunc(ms.Episodes, func(a, b MissingEpisode) int { return a.Episode - b.Episode })
		out.Seasons = append(out.Seasons, *ms)
	}
	slices.SortFunc(out.Seasons, func(a, b MissingSeason) int { return a.SeasonNumber - b.SeasonNumber })
	return out, nil
}

// SearchMissing asks Sonarr to search now for seasons or episodes of a
// series it has, monitoring them first. For admins and users whose
// requests go straight through; everyone else requests instead.
func (s *Service) SearchMissing(ctx context.Context, u store.User, series store.Item, seasons []int, episodes []store.EpisodeRef) error {
	if u.EffectiveRequestLevel() != store.RequestAuto {
		return ErrNotAllowed
	}
	if len(seasons) == 0 && len(episodes) == 0 {
		return fmt.Errorf("%w: name the seasons or episodes to search for", ErrInvalid)
	}
	e, err := s.sonarrSeries(ctx, series.TmdbID)
	if err != nil {
		return err
	}
	if e.seriesID == 0 {
		return ErrNotManaged
	}
	client := arr.New(e.conn.Kind, e.conn.URL, e.conn.APIKey)
	if len(seasons) > 0 {
		if err := client.SearchSeasons(ctx, e.seriesID, seasons); err != nil {
			return err
		}
	}
	if len(episodes) > 0 {
		if err := client.SearchEpisodes(ctx, e.seriesID, episodes); err != nil {
			return err
		}
	}
	// Monitoring changed; the next look should say so.
	s.missingMu.Lock()
	delete(s.missing, series.TmdbID)
	s.missingMu.Unlock()
	return nil
}
