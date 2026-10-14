package importer

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/garybowers/cineva/server/internal/auth"
	"github.com/garybowers/cineva/server/internal/logging"
	"github.com/garybowers/cineva/server/internal/store"
)

var logger = logging.For("import")

// Store is what an import needs from the database.
type Store interface {
	ImportIndex(ctx context.Context) ([]store.IndexItem, error)
	ImportPlayState(ctx context.Context, userID, itemID string, watched bool, positionSecs int, at string) (bool, error)
	UserByID(ctx context.Context, id string) (store.User, error)
	UserByUsername(ctx context.Context, username string) (store.User, error)
	CreateUser(ctx context.Context, username, displayName, passwordHash, role string) (store.User, error)
	Playlists(ctx context.Context, userID string, libraryIDs []string) ([]store.Playlist, error)
	CreatePlaylist(ctx context.Context, userID, name string, smart *store.SmartRules) (store.Playlist, error)
	SetPlaylistTracks(ctx context.Context, userID, id string, trackIDs []string) error
}

// Mapping is what one source user becomes: an existing Cineva user, a
// new one (Create), or nothing (neither set).
type Mapping struct {
	SourceID   string `json:"sourceId"`
	SourceName string `json:"sourceName"`
	UserID     string `json:"userId"`
	Create     bool   `json:"create"`
}

// Options are what to bring.
type Options struct {
	WatchState bool `json:"watchState"`
	Playlists  bool `json:"playlists"`
}

// Request is one dry run or import.
type Request struct {
	Kind    string    `json:"kind"`
	URL     string    `json:"url"`
	Token   string    `json:"token"`
	Users   []Mapping `json:"users"`
	Options Options   `json:"options"`
	DryRun  bool      `json:"dryRun"`
}

// UserResult is what came across for one source user.
type UserResult struct {
	Source     string `json:"source"`
	User       string `json:"user"`
	Watched    int    `json:"watched"`
	InProgress int    `json:"inProgress"`
	// Written is how many states were written: fewer than matched where
	// Cineva already had newer ones. Zero on a dry run.
	Written   int `json:"written"`
	Unmatched int `json:"unmatched"`
	// UnmatchedTitles names some of them, for the admin to check.
	UnmatchedTitles []string `json:"unmatchedTitles"`
	// NotVisible are matched titles in libraries the Cineva user cannot
	// see, left alone.
	NotVisible int `json:"notVisible"`
	Playlists  int `json:"playlists"`
	Tracks     int `json:"tracks"`
	// Created is the account made for this user, with the temporary
	// password the admin passes on; shown once.
	Created *Created `json:"created,omitempty"`
	Error   string   `json:"error,omitempty"`
}

// Created is a new account and its temporary password.
type Created struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// Run is an import's progress and outcome.
type Run struct {
	ID         string       `json:"id"`
	Kind       string       `json:"kind"`
	Source     string       `json:"source"`
	DryRun     bool         `json:"dryRun"`
	Status     string       `json:"status"` // "running" | "done" | "failed"
	Step       string       `json:"step"`
	Done       int          `json:"done"`
	Total      int          `json:"total"`
	Users      []UserResult `json:"users"`
	Error      string       `json:"error,omitempty"`
	StartedAt  time.Time    `json:"startedAt"`
	FinishedAt *time.Time   `json:"finishedAt,omitempty"`
}

// unmatchedShown is how many unmatched titles a result names.
const unmatchedShown = 100

// undated is when a state the source gave no date for counts as played:
// long ago, so anything Cineva has wins.
const undated = "2000-01-01 00:00:00"

// ErrBusy is a second import while one runs.
var ErrBusy = errors.New("an import is already running")

// Runner runs one import at a time and keeps the last one's result.
type Runner struct {
	Store Store
	// OnProgress, when set, hears each change to the run.
	OnProgress func(Run)
	// NewSource makes a source; swapped in tests.
	NewSource func(kind, url, token string) (Source, bool)

	mu   sync.Mutex
	last *Run
}

// Current is the running or last run.
func (r *Runner) Current() (Run, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.last == nil {
		return Run{}, false
	}
	return copyRun(*r.last), true
}

func copyRun(run Run) Run {
	run.Users = slices.Clone(run.Users)
	return run
}

func (r *Runner) update(fn func(*Run)) {
	r.mu.Lock()
	fn(r.last)
	snap := copyRun(*r.last)
	r.mu.Unlock()
	if r.OnProgress != nil {
		r.OnProgress(snap)
	}
}

// Check connects to a source and lists its users.
func (r *Runner) Check(ctx context.Context, kind, url, token string) (Info, error) {
	src, ok := r.source(kind, url, token)
	if !ok {
		return Info{}, fmt.Errorf("unknown source %q: want plex or jellyfin", kind)
	}
	return src.Check(ctx)
}

func (r *Runner) source(kind, url, token string) (Source, bool) {
	if r.NewSource != nil {
		return r.NewSource(kind, url, token)
	}
	return New(kind, url, token)
}

// Start begins a run in the background and returns at once.
func (r *Runner) Start(req Request) (Run, error) {
	src, ok := r.source(req.Kind, req.URL, req.Token)
	if !ok {
		return Run{}, fmt.Errorf("unknown source %q: want plex or jellyfin", req.Kind)
	}
	r.mu.Lock()
	if r.last != nil && r.last.Status == "running" {
		r.mu.Unlock()
		return Run{}, ErrBusy
	}
	run := &Run{ID: uuid.NewString(), Kind: req.Kind, DryRun: req.DryRun, Status: "running", Step: "Connecting",
		Users: []UserResult{}, StartedAt: time.Now().UTC()}
	r.last = run
	snap := copyRun(*run)
	r.mu.Unlock()
	go r.run(src, req)
	return snap, nil
}

func (r *Runner) run(src Source, req Request) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
	defer cancel()
	err := r.work(ctx, src, req)
	r.update(func(run *Run) {
		now := time.Now().UTC()
		run.FinishedAt, run.Step = &now, ""
		if err != nil {
			run.Status, run.Error = "failed", err.Error()
		} else {
			run.Status = "done"
		}
	})
	if err != nil {
		logger.Warn("import failed", "source", req.Kind, "err", err)
	}
}

func (r *Runner) work(ctx context.Context, src Source, req Request) error {
	info, err := src.Check(ctx)
	if err != nil {
		return err
	}
	var todo []Mapping
	for _, m := range req.Users {
		if m.UserID != "" || m.Create {
			todo = append(todo, m)
		}
	}
	r.update(func(run *Run) { run.Source, run.Total, run.Step = info.Name, len(todo), "Reading Cineva's library" })
	index, err := r.Store.ImportIndex(ctx)
	if err != nil {
		return err
	}
	matcher := NewMatcher(index)
	for i, m := range todo {
		name := m.SourceName
		r.update(func(run *Run) { run.Step = "Importing " + name })
		res := r.user(ctx, src, matcher, req, m)
		r.update(func(run *Run) {
			run.Users = append(run.Users, res)
			run.Done = i + 1
		})
		logger.Info("import user done", "source", m.SourceName, "watched", res.Watched, "in_progress", res.InProgress,
			"written", res.Written, "unmatched", res.Unmatched, "playlists", res.Playlists, "dry_run", req.DryRun)
	}
	return nil
}

func (r *Runner) user(ctx context.Context, src Source, matcher *Matcher, req Request, m Mapping) UserResult {
	res := UserResult{Source: m.SourceName, UnmatchedTitles: []string{}}
	var u store.User
	switch {
	case m.UserID != "":
		var err error
		if u, err = r.Store.UserByID(ctx, m.UserID); err != nil {
			res.Error = "no such Cineva user"
			return res
		}
	case req.DryRun:
		u = store.User{Username: suggestUsername(m.SourceName), Role: store.RoleUser}
	default:
		created, pw, err := r.createUser(ctx, m.SourceName)
		if err != nil {
			res.Error = "creating the user failed: " + err.Error()
			return res
		}
		u = created
		res.Created = &Created{Username: created.Username, Password: pw}
	}
	res.User = u.Username
	if u.DisplayName != "" {
		res.User = u.DisplayName
	}
	canSee := func(itemID string) bool {
		return u.ID == "" || u.IsAdmin() || slices.Contains(u.LibraryIDs, matcher.Library(itemID))
	}
	srcUser := User{ID: m.SourceID, Name: m.SourceName}

	if req.Options.WatchState {
		watches, err := src.Watches(ctx, srcUser)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		for _, w := range watches {
			id := matcher.Watch(w)
			if id == "" {
				res.Unmatched++
				if len(res.UnmatchedTitles) < unmatchedShown {
					res.UnmatchedTitles = append(res.UnmatchedTitles, describe(w))
				}
				continue
			}
			if !canSee(id) {
				res.NotVisible++
				continue
			}
			if w.Watched {
				res.Watched++
			} else {
				res.InProgress++
			}
			if req.DryRun {
				continue
			}
			at := undated
			if !w.At.IsZero() {
				at = w.At.UTC().Format("2006-01-02 15:04:05")
			}
			wrote, err := r.Store.ImportPlayState(ctx, u.ID, id, w.Watched, w.PositionSecs, at)
			if err != nil {
				res.Error = err.Error()
				return res
			}
			if wrote {
				res.Written++
			}
		}
	}

	if req.Options.Playlists {
		lists, err := src.Playlists(ctx, srcUser)
		if err != nil {
			res.Error = err.Error()
			return res
		}
		taken := map[string]bool{}
		if u.ID != "" {
			if mine, err := r.Store.Playlists(ctx, u.ID, nil); err == nil {
				for _, p := range mine {
					taken[strings.ToLower(p.Name)] = true
				}
			}
		}
		for _, pl := range lists {
			var ids []string
			for _, t := range pl.Tracks {
				if id := matcher.Track(t); id != "" && canSee(id) {
					ids = append(ids, id)
				}
			}
			if len(ids) == 0 {
				continue
			}
			res.Playlists++
			res.Tracks += len(ids)
			if req.DryRun {
				continue
			}
			name := pl.Name
			if taken[strings.ToLower(name)] {
				name += " (from " + map[string]string{"plex": "Plex", "jellyfin": "Jellyfin"}[req.Kind] + ")"
			}
			created, err := r.Store.CreatePlaylist(ctx, u.ID, name, nil)
			if err != nil {
				res.Error = "creating playlist " + pl.Name + ": " + err.Error()
				return res
			}
			if err := r.Store.SetPlaylistTracks(ctx, u.ID, created.ID, ids); err != nil {
				res.Error = "filling playlist " + pl.Name + ": " + err.Error()
				return res
			}
		}
	}
	return res
}

func describe(w Watch) string {
	if w.Movie {
		if w.Year > 0 {
			return fmt.Sprintf("%s (%d)", w.Title, w.Year)
		}
		return w.Title
	}
	return fmt.Sprintf("%s S%02dE%02d", w.SeriesTitle, w.Season, w.Episode)
}

var notUsername = regexp.MustCompile(`[^a-z0-9._-]+`)

// suggestUsername makes a username from a source user's name.
func suggestUsername(name string) string {
	u := strings.Trim(notUsername.ReplaceAllString(strings.ToLower(name), "-"), "-.")
	if u == "" {
		u = "user"
	}
	return u
}

// createUser makes a regular account for a source user, with a free
// username and a temporary password to hand on.
func (r *Runner) createUser(ctx context.Context, name string) (store.User, string, error) {
	base := suggestUsername(name)
	username := base
	for n := 2; ; n++ {
		if _, err := r.Store.UserByUsername(ctx, username); errors.Is(err, store.ErrNotFound) {
			break
		} else if err != nil {
			return store.User{}, "", err
		}
		username = fmt.Sprintf("%s%d", base, n)
	}
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return store.User{}, "", err
	}
	pw := base64.RawURLEncoding.EncodeToString(b)
	hash, err := auth.HashPassword(pw)
	if err != nil {
		return store.User{}, "", err
	}
	u, err := r.Store.CreateUser(ctx, username, name, hash, store.RoleUser)
	if err != nil {
		return store.User{}, "", err
	}
	// Read back for the libraries the new account may see.
	if u, err = r.Store.UserByID(ctx, u.ID); err != nil {
		return store.User{}, "", err
	}
	return u, pw, nil
}
