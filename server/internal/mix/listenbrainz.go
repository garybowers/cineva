package mix

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/garybowers/cineva/server/internal/store"
)

// Source is where a mix learns about artists beyond the library: which
// are alike, and which of their songs the world listens to most.
// ListenBrainz is the one implementation; tests use a fake.
type Source interface {
	SimilarArtists(ctx context.Context, mbid string) ([]store.SimilarArtist, error)
	TopRecordings(ctx context.Context, mbid string) ([]store.TopRecording, error)
}

// ErrNotFound says the source knows nothing about the artist.
var ErrNotFound = errors.New("listenbrainz: not found")

// ErrToken says the endpoint wants a user token that is not configured:
// ListenBrainz gates its popularity data (the top recordings) behind one
// since 2026, free with an account, while the Labs similarity graph is
// open. Set metadata.listenbrainz.token.
var ErrToken = errors.New("listenbrainz: a user token is required for this endpoint")

// ListenBrainz reads two open ListenBrainz endpoints that need no key:
// the Labs similar-artists graph and the popularity service's top
// recordings per artist. One request a second, as a courtesy and because
// the answers are cached for 90 days anyway.
type ListenBrainz struct {
	// LabsBase and APIBase are the two hosts; the zero value uses the
	// public ones.
	LabsBase string
	APIBase  string
	// Algorithm names the similarity graph: the session-based one is the
	// current default and the only one documented to clients.
	Algorithm string
	Client    *http.Client
	// Pace is the least time between requests; the zero value is the
	// one-a-second courtesy pace. Tests shorten it.
	Pace time.Duration
	// Token is a ListenBrainz user token (free with an account), sent on
	// every request; without one the top recordings are refused.
	Token string

	mu   sync.Mutex
	last time.Time
}

const (
	lbLabsBase  = "https://labs.api.listenbrainz.org"
	lbAPIBase   = "https://api.listenbrainz.org"
	lbAlgorithm = "session_based_days_7500_session_300_contribution_5_threshold_10_limit_100_filter_True_skip_30"
	lbUserAgent = "Cineva/0.1 (https://github.com/garybowers/cineva)"
	lbPace      = 1100 * time.Millisecond
)

func NewListenBrainz() *ListenBrainz {
	return &ListenBrainz{Client: &http.Client{Timeout: 20 * time.Second}}
}

func (l *ListenBrainz) throttle() {
	l.mu.Lock()
	defer l.mu.Unlock()
	pace := l.Pace
	if pace == 0 {
		pace = lbPace
	}
	if wait := pace - time.Since(l.last); wait > 0 {
		time.Sleep(wait)
	}
	l.last = time.Now()
}

func (l *ListenBrainz) get(ctx context.Context, rawURL string, out any) error {
	l.throttle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", lbUserAgent)
	if l.Token != "" {
		req.Header.Set("Authorization", "Token "+l.Token)
	}
	client := l.Client
	if client == nil {
		client = http.DefaultClient
	}
	res, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("listenbrainz: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode == http.StatusUnauthorized || res.StatusCode == http.StatusForbidden {
		return ErrToken
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("listenbrainz: status %d", res.StatusCode)
	}
	if err := json.NewDecoder(res.Body).Decode(out); err != nil {
		return fmt.Errorf("listenbrainz: %w", err)
	}
	return nil
}

func (l *ListenBrainz) labs() string {
	if l.LabsBase != "" {
		return l.LabsBase
	}
	return lbLabsBase
}

func (l *ListenBrainz) api() string {
	if l.APIBase != "" {
		return l.APIBase
	}
	return lbAPIBase
}

// SimilarArtists is the artists ListenBrainz's listeners play alongside
// this one, best match first. An artist it has no data for is ErrNotFound.
func (l *ListenBrainz) SimilarArtists(ctx context.Context, mbid string) ([]store.SimilarArtist, error) {
	alg := l.Algorithm
	if alg == "" {
		alg = lbAlgorithm
	}
	q := url.Values{"artist_mbids": {mbid}, "algorithm": {alg}}
	var raw []struct {
		MBID  string `json:"artist_mbid"`
		Name  string `json:"name"`
		Score int    `json:"score"`
	}
	if err := l.get(ctx, l.labs()+"/similar-artists/json?"+q.Encode(), &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	out := make([]store.SimilarArtist, 0, len(raw))
	for _, r := range raw {
		if r.MBID != "" {
			out = append(out, store.SimilarArtist{MBID: r.MBID, Name: r.Name, Score: r.Score})
		}
	}
	return out, nil
}

// TopRecordings is an artist's best-known recordings, most listened first.
func (l *ListenBrainz) TopRecordings(ctx context.Context, mbid string) ([]store.TopRecording, error) {
	var raw []struct {
		MBID string `json:"recording_mbid"`
		Name string `json:"recording_name"`
	}
	if err := l.get(ctx, l.api()+"/1/popularity/top-recordings-for-artist/"+url.PathEscape(mbid), &raw); err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, ErrNotFound
	}
	out := make([]store.TopRecording, 0, len(raw))
	for i, r := range raw {
		if r.MBID != "" {
			out = append(out, store.TopRecording{MBID: r.MBID, Name: r.Name, Rank: i + 1})
		}
	}
	return out, nil
}
