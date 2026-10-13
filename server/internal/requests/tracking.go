package requests

import (
	"context"
	"slices"
	"time"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/store"
)

// Following a request after it is sent: "downloading" when the instance
// grabs a release (its Grab webhook, or its queue when the webhook was
// registered before Cineva subscribed to grabs), "available" when a scan
// finds it on disk.

var pending = []string{store.RequestRequested, store.RequestApproved, store.RequestDownloading}

// CheckAvailable marks every open request whose title is now in a library
// as available. Called after each completed scan — which is how a webhook
// import reaches it too — and once at startup.
func (s *Service) CheckAvailable(ctx context.Context) error {
	open, err := s.Store.Requests(ctx, store.RequestFilter{Statuses: pending})
	if err != nil {
		return err
	}
	var films []int
	for _, r := range open {
		if r.MediaType == store.RequestMovie {
			films = append(films, r.TmdbID)
		}
	}
	held := map[int]bool{}
	if len(films) > 0 {
		items, err := s.Store.ItemsByTmdbIDs(ctx, store.KindMovie, films)
		if err != nil {
			return err
		}
		for _, it := range items {
			held[it.TmdbID] = true
		}
	}
	changed := false
	for _, r := range open {
		ok := held[r.TmdbID]
		if r.MediaType == store.RequestTV {
			disk, found, err := s.Store.SeriesOnDisk(ctx, r.TmdbID)
			if err != nil {
				return err
			}
			ok = found && seriesSatisfied(r, disk)
		}
		if !ok {
			continue
		}
		r.Status, r.Error = store.RequestAvailable, ""
		if _, err := s.Store.UpdateRequest(ctx, r); err != nil {
			return err
		}
		reqLog.Info("request available", "title", r.Title)
		changed = true
	}
	if changed {
		s.changed()
	}
	return nil
}

// seriesSatisfied reports whether what a series request asked for is on
// disk: the series at all for a whole-series request (Sonarr carries on
// filling it in), every requested season with at least one episode, and
// every requested episode.
func seriesSatisfied(r store.MediaRequest, disk map[int]map[int]bool) bool {
	for _, n := range r.Seasons {
		if len(disk[n]) == 0 {
			return false
		}
	}
	for _, e := range r.Episodes {
		if !disk[e.Season][e.Episode] {
			return false
		}
	}
	return true
}

// Grabbed moves requests for the title a Grab event names from approved to
// downloading.
func (s *Service) Grabbed(ctx context.Context, conn store.ArrConnection, ev arr.Event) error {
	open, err := s.Store.Requests(ctx, store.RequestFilter{Statuses: []string{store.RequestApproved}})
	if err != nil {
		return err
	}
	changed := false
	for _, r := range open {
		if KindFor(r.MediaType) != conn.Kind {
			continue
		}
		same := (r.ConnectionID == conn.ID && r.ArrID > 0 && r.ArrID == ev.ArrID) ||
			(r.MediaType == store.RequestMovie && ev.TmdbID > 0 && ev.TmdbID == r.TmdbID) ||
			(r.MediaType == store.RequestTV && ev.TvdbID > 0 && ev.TvdbID == r.TvdbID)
		if !same {
			continue
		}
		r.Status = store.RequestDownloading
		if _, err := s.Store.UpdateRequest(ctx, r); err != nil {
			return err
		}
		changed = true
	}
	if changed {
		s.changed()
	}
	return nil
}

// PollQueues asks each instance with approved requests what it is
// downloading, and moves those requests to downloading.
func (s *Service) PollQueues(ctx context.Context) error {
	open, err := s.Store.Requests(ctx, store.RequestFilter{Statuses: []string{store.RequestApproved}})
	if err != nil {
		return err
	}
	byConn := map[string][]store.MediaRequest{}
	for _, r := range open {
		if r.ConnectionID != "" && r.ArrID > 0 {
			byConn[r.ConnectionID] = append(byConn[r.ConnectionID], r)
		}
	}
	changed := false
	for id, reqs := range byConn {
		conn, err := s.Store.ArrConnection(ctx, id)
		if err != nil {
			continue // disconnected since; nothing to ask
		}
		queue, err := arr.New(conn.Kind, conn.URL, conn.APIKey).Queue(ctx)
		if err != nil {
			reqLog.Warn("reading the download queue failed", "connection", conn.Name, "err", err)
			continue
		}
		var ids []int
		for _, q := range queue {
			if conn.Kind == "radarr" {
				ids = append(ids, q.MovieID)
			} else {
				ids = append(ids, q.SeriesID)
			}
		}
		for _, r := range reqs {
			if !slices.Contains(ids, r.ArrID) {
				continue
			}
			r.Status = store.RequestDownloading
			if _, err := s.Store.UpdateRequest(ctx, r); err != nil {
				return err
			}
			changed = true
		}
	}
	if changed {
		s.changed()
	}
	return nil
}

// Run follows requests until ctx ends: availability once at the start (a
// scan may have finished while the server was down), then the download
// queues every PollEvery while any request is waiting to start.
func (s *Service) Run(ctx context.Context) {
	if err := s.CheckAvailable(ctx); err != nil {
		reqLog.Warn("checking requests against the library failed", "err", err)
	}
	every := s.PollEvery
	if every <= 0 {
		every = 5 * time.Minute
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if err := s.PollQueues(ctx); err != nil {
				reqLog.Warn("polling download queues failed", "err", err)
			}
		}
	}
}
