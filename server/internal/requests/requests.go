// Package requests is asking for what the library does not have: finding
// films and series on TMDB, recording a user's request, sending it to Radarr
// or Sonarr (at once for users trusted to, after an admin approves it for
// the rest), and following it until a scan finds it on disk.
//
// It also answers what a series held in the library is missing — the
// episodes Sonarr knows of that are not on disk, and those still to air.
package requests

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/arr"
	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

var reqLog = logging.For("requests")

// SettingDefaultLevel is the request level new accounts are created with.
const SettingDefaultLevel = "requests.default_level"

// DefaultLevel is the server default when the setting is unset: requests
// wait for an admin.
const DefaultLevel = store.RequestApproval

var (
	ErrNotAllowed   = errors.New("requests are not enabled for this account")
	ErrNoTMDB       = errors.New("no TMDB key is configured, so titles outside the library cannot be looked up")
	ErrNoConnection = errors.New("no Sonarr or Radarr is connected for this kind of title")
	ErrInLibrary    = errors.New("that is already in the library")
	ErrState        = errors.New("the request cannot change from its current status")
	// ErrInvalid marks a request that does not make sense, as opposed to
	// one Sonarr, Radarr or TMDB failed.
	ErrInvalid = errors.New("invalid request")
)

// Service holds what requests need. Discover is nil when there is no TMDB
// key; the rest of the service still works on requests already made.
type Service struct {
	Store    *store.Store
	Discover metadata.Discoverer
	Events   *events.Bus
	// Now is the clock, swapped in tests.
	Now func() time.Time
	// PollEvery is how often Run asks the instances what they are
	// downloading, while any request is waiting to start. Zero is five
	// minutes.
	PollEvery time.Duration
	// Notify, when set, hears of each change worth telling someone about
	// (see docs/notifications.md): "request.waiting", "request.failed",
	// "request.approved", "request.declined" and "request.available".
	Notify func(event string, r store.MediaRequest)

	missingMu sync.Mutex
	missing   map[int]missingEntry // by series TMDB id
}

func New(st *store.Store, discover metadata.Discoverer, bus *events.Bus) *Service {
	return &Service{Store: st, Discover: discover, Events: bus, Now: time.Now, missing: map[int]missingEntry{}}
}

// KindFor is the instance kind that handles a request's media type.
func KindFor(mediaType string) string {
	if mediaType == store.RequestMovie {
		return "radarr"
	}
	return "sonarr"
}

func (s *Service) changed() { s.Events.Publish(events.Event{Type: "requests"}) }

func (s *Service) stamp() string { return s.Now().UTC().Format("2006-01-02 15:04:05") }

// DefaultLevelFor reads the level new accounts start at.
func (s *Service) DefaultLevelFor(ctx context.Context) string {
	v, err := s.Store.Setting(ctx, SettingDefaultLevel, DefaultLevel)
	if err != nil || !slices.Contains(store.RequestLevels, v) {
		return DefaultLevel
	}
	return v
}

// Connection is the instance requests of a media type go to: the first of
// its kind, by name, unless id names another.
func (s *Service) Connection(ctx context.Context, mediaType, id string) (store.ArrConnection, error) {
	kind := KindFor(mediaType)
	if id != "" {
		c, err := s.Store.ArrConnection(ctx, id)
		if err != nil {
			return store.ArrConnection{}, fmt.Errorf("connection %s: %w", id, err)
		}
		if c.Kind != kind {
			return store.ArrConnection{}, fmt.Errorf("%s is not a %s connection", c.Name, kind)
		}
		return c, nil
	}
	conns, err := s.Store.ArrConnections(ctx)
	if err != nil {
		return store.ArrConnection{}, err
	}
	for _, c := range conns {
		if c.Kind == kind {
			return c, nil
		}
	}
	return store.ArrConnection{}, ErrNoConnection
}

// Status is what the client needs to decide whether to show requests at
// all: the viewer's level, and which kinds can be requested.
type Status struct {
	Level  string `json:"level"`
	Movies bool   `json:"movies"`
	TV     bool   `json:"tv"`
	TMDB   bool   `json:"tmdb"`
	// Enabled is the sum of it: the request screens are worth showing.
	Enabled bool `json:"enabled"`
}

func (s *Service) StatusFor(ctx context.Context, u store.User) (Status, error) {
	st := Status{Level: u.EffectiveRequestLevel(), TMDB: s.Discover != nil}
	conns, err := s.Store.ArrConnections(ctx)
	if err != nil {
		return st, err
	}
	for _, c := range conns {
		switch c.Kind {
		case "radarr":
			st.Movies = true
		case "sonarr":
			st.TV = true
		}
	}
	st.Enabled = st.Level != store.RequestNone && st.TMDB && (st.Movies || st.TV)
	return st, nil
}

// NewRequest is what a user asks for.
type NewRequest struct {
	MediaType string             `json:"mediaType"`
	TmdbID    int                `json:"tmdbId"`
	Seasons   []int              `json:"seasons"`
	Episodes  []store.EpisodeRef `json:"episodes"`
}

// covers reports whether an open request already asks for everything in
// the new one.
func covers(have store.MediaRequest, want NewRequest) bool {
	if have.MediaType == store.RequestMovie || have.WholeSeries() {
		return true
	}
	if len(want.Seasons) == 0 && len(want.Episodes) == 0 {
		return false
	}
	for _, n := range want.Seasons {
		if !slices.Contains(have.Seasons, n) {
			return false
		}
	}
	for _, e := range want.Episodes {
		if !slices.Contains(have.Episodes, e) && !slices.Contains(have.Seasons, e.Season) {
			return false
		}
	}
	return true
}

// Create records a request, and sends it at once when the user's level is
// "auto". A request someone already made that covers this one is returned
// instead, with created false. A request that could not be sent is still
// recorded — it waits in the admin's queue with the reason — and the error
// is returned alongside it.
func (s *Service) Create(ctx context.Context, u store.User, in NewRequest) (store.MediaRequest, bool, error) {
	level := u.EffectiveRequestLevel()
	if level == store.RequestNone {
		return store.MediaRequest{}, false, ErrNotAllowed
	}
	if in.MediaType != store.RequestMovie && in.MediaType != store.RequestTV {
		return store.MediaRequest{}, false, fmt.Errorf("%w: mediaType must be movie or tv", ErrInvalid)
	}
	if in.TmdbID <= 0 {
		return store.MediaRequest{}, false, fmt.Errorf("%w: tmdbId is required", ErrInvalid)
	}
	if in.MediaType == store.RequestMovie {
		in.Seasons, in.Episodes = nil, nil
	}
	if s.Discover == nil {
		return store.MediaRequest{}, false, ErrNoTMDB
	}
	if _, err := s.Connection(ctx, in.MediaType, ""); err != nil {
		return store.MediaRequest{}, false, err
	}
	open, err := s.Store.Requests(ctx, store.RequestFilter{
		MediaType: in.MediaType, TmdbID: in.TmdbID,
		Statuses: []string{store.RequestRequested, store.RequestApproved, store.RequestDownloading},
	})
	if err != nil {
		return store.MediaRequest{}, false, err
	}
	for _, r := range open {
		if covers(r, in) {
			return r, false, nil
		}
	}

	req := store.MediaRequest{
		UserID: u.ID, MediaType: in.MediaType, TmdbID: in.TmdbID,
		Seasons: in.Seasons, Episodes: in.Episodes, Status: store.RequestRequested,
	}
	var detail metadata.TitleDetail
	if in.MediaType == store.RequestMovie {
		held, err := s.Store.ItemsByTmdbIDs(ctx, store.KindMovie, []int{in.TmdbID})
		if err != nil {
			return store.MediaRequest{}, false, err
		}
		if len(held) > 0 {
			return store.MediaRequest{}, false, ErrInLibrary
		}
		detail, err = s.Discover.MovieDetail(ctx, in.TmdbID)
		if err != nil {
			return store.MediaRequest{}, false, fmt.Errorf("look up the film: %w", err)
		}
	} else {
		detail, err = s.Discover.TVDetail(ctx, in.TmdbID)
		if err != nil {
			return store.MediaRequest{}, false, fmt.Errorf("look up the series: %w", err)
		}
		req.TvdbID = detail.TvdbID
	}
	req.Title, req.Year = detail.Title, detail.Year
	req.Poster, req.Backdrop = detail.PosterURL, detail.BackdropURL

	saved, err := s.Store.CreateRequest(ctx, req)
	if err != nil {
		return store.MediaRequest{}, false, err
	}
	defer s.changed()
	if level != store.RequestAuto {
		s.notify("request.waiting", saved)
		return saved, true, nil
	}
	sent, err := s.send(ctx, saved, Override{}, u.ID)
	return sent, true, err
}

// Override is what an admin approving a request may change.
type Override struct {
	ConnectionID     string `json:"connectionId"`
	QualityProfileID int    `json:"qualityProfileId"`
	RootFolder       string `json:"rootFolder"`
	Note             string `json:"note"`
}

// send hands a request to its instance. On success it is approved; on
// failure it stays requested with the reason, for an admin to retry.
func (s *Service) send(ctx context.Context, r store.MediaRequest, ov Override, by string) (store.MediaRequest, error) {
	sendErr := s.deliver(ctx, &r, ov)
	r.DecidedBy, r.DecidedAt = by, s.stamp()
	if ov.Note != "" {
		r.Note = ov.Note
	}
	if sendErr != nil {
		r.Status, r.Error = store.RequestRequested, sendErr.Error()
		reqLog.Warn("sending request failed", "title", r.Title, "err", sendErr)
	} else {
		r.Status, r.Error = store.RequestApproved, ""
		reqLog.Info("request sent", "title", r.Title, "type", r.MediaType)
	}
	saved, err := s.Store.UpdateRequest(context.WithoutCancel(ctx), r)
	if err != nil {
		return r, err
	}
	switch {
	case sendErr != nil:
		s.notify("request.failed", saved)
	case by != saved.UserID:
		// Approved by an admin. One sent straight away on its owner's own
		// say-so needs no news: they have just asked.
		s.notify("request.approved", saved)
	}
	return saved, sendErr
}

func (s *Service) notify(event string, r store.MediaRequest) {
	if s.Notify != nil {
		s.Notify(event, r)
	}
}

func (s *Service) deliver(ctx context.Context, r *store.MediaRequest, ov Override) error {
	connID := ov.ConnectionID
	if connID == "" {
		connID = r.ConnectionID
	}
	conn, err := s.Connection(ctx, r.MediaType, connID)
	if errors.Is(err, store.ErrNotFound) {
		// The connection it was meant for is gone: use whichever there is.
		conn, err = s.Connection(ctx, r.MediaType, "")
	}
	if err != nil {
		return err
	}
	opts := arr.AddOptions{QualityProfileID: conn.QualityProfileID, RootFolder: conn.RootFolder}
	if ov.QualityProfileID > 0 {
		opts.QualityProfileID = ov.QualityProfileID
	}
	if ov.RootFolder != "" {
		opts.RootFolder = ov.RootFolder
	}
	client := arr.New(conn.Kind, conn.URL, conn.APIKey)
	// Resolved here rather than left to the add, so the request records
	// what it was really sent with.
	if opts, err = client.Defaults(ctx, opts); err != nil {
		return err
	}
	var id int
	if r.MediaType == store.RequestMovie {
		id, err = client.RequestMovie(ctx, r.TmdbID, opts)
	} else {
		if r.TvdbID == 0 && s.Discover != nil {
			if d, derr := s.Discover.TVDetail(ctx, r.TmdbID); derr == nil {
				r.TvdbID = d.TvdbID
			}
		}
		if r.TvdbID == 0 {
			return errors.New("TMDB has no TVDB id for this series, which Sonarr needs to find it")
		}
		id, err = client.RequestSeries(ctx, r.TvdbID, opts, arr.SeriesScope{Seasons: r.Seasons, Episodes: r.Episodes})
	}
	if err != nil {
		return err
	}
	r.ConnectionID, r.ArrID = conn.ID, id
	r.QualityProfileID, r.RootFolder = opts.QualityProfileID, opts.RootFolder
	return nil
}

// Approve sends a waiting (or declined) request on an admin's say-so.
func (s *Service) Approve(ctx context.Context, admin store.User, id string, ov Override) (store.MediaRequest, error) {
	r, err := s.Store.Request(ctx, id)
	if err != nil {
		return store.MediaRequest{}, err
	}
	if r.Status != store.RequestRequested && r.Status != store.RequestDeclined {
		return r, ErrState
	}
	defer s.changed()
	return s.send(ctx, r, ov, admin.ID)
}

// Decline turns a waiting request down, with an optional note for the
// person who asked.
func (s *Service) Decline(ctx context.Context, admin store.User, id, note string) (store.MediaRequest, error) {
	r, err := s.Store.Request(ctx, id)
	if err != nil {
		return store.MediaRequest{}, err
	}
	if r.Status != store.RequestRequested {
		return r, ErrState
	}
	r.Status, r.Note, r.Error = store.RequestDeclined, note, ""
	r.DecidedBy, r.DecidedAt = admin.ID, s.stamp()
	saved, err := s.Store.UpdateRequest(ctx, r)
	if err != nil {
		return store.MediaRequest{}, err
	}
	s.notify("request.declined", saved)
	s.changed()
	return saved, nil
}

// Withdraw removes a request: its owner may while it still waits for an
// admin; an admin may remove any. Nothing is undone in Sonarr or Radarr.
func (s *Service) Withdraw(ctx context.Context, u store.User, id string) (store.MediaRequest, error) {
	r, err := s.Store.Request(ctx, id)
	if err != nil {
		return store.MediaRequest{}, err
	}
	if !u.IsAdmin() {
		if r.UserID != u.ID {
			return r, store.ErrNotFound
		}
		if r.Status != store.RequestRequested {
			return r, ErrState
		}
	}
	if err := s.Store.DeleteRequest(ctx, id); err != nil {
		return r, err
	}
	s.changed()
	return r, nil
}

// List is the user's own requests, or everyone's for an admin asking for
// all, newest first.
func (s *Service) List(ctx context.Context, u store.User, all bool, statuses []string) ([]store.MediaRequest, error) {
	f := store.RequestFilter{Statuses: statuses, Limit: 500}
	if !all || !u.IsAdmin() {
		f.UserID = u.ID
	}
	return s.Store.Requests(ctx, f)
}
