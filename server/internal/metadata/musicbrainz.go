package metadata

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"sync"
	"time"
)

// MBTrack is one track of a MusicBrainz release.
type MBTrack struct {
	// Disc is the medium's position in the release, from 1; Pos the
	// track's on that disc.
	Disc  int
	Pos   int
	Title string
}

// MusicRelease is an album match from MusicBrainz.
type MusicRelease struct {
	MBID   string
	Title  string
	Year   int
	Tracks []MBTrack
}

// MusicProvider is the music-domain metadata interface (see CLAUDE.md).
type MusicProvider interface {
	SearchRelease(ctx context.Context, artist, album string) (MusicRelease, error)
	FrontCover(ctx context.Context, mbid string) ([]byte, error)
}

// MusicBrainz uses the open MusicBrainz + Cover Art Archive APIs: no key,
// but a descriptive User-Agent and at most one request per second.
type MusicBrainz struct {
	Client *http.Client

	mu   sync.Mutex
	last time.Time
}

func NewMusicBrainz() *MusicBrainz {
	return &MusicBrainz{Client: &http.Client{Timeout: 20 * time.Second}}
}

const mbUserAgent = "Cineva/0.1 (https://github.com/garybowers/cineva)"

// throttle enforces the 1 req/s courtesy limit across all callers.
func (m *MusicBrainz) throttle() {
	m.mu.Lock()
	defer m.mu.Unlock()
	if wait := 1100*time.Millisecond - time.Since(m.last); wait > 0 {
		time.Sleep(wait)
	}
	m.last = time.Now()
}

func (m *MusicBrainz) get(ctx context.Context, rawURL string, out any) error {
	m.throttle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", mbUserAgent)
	res, err := m.Client.Do(req)
	if err != nil {
		return fmt.Errorf("musicbrainz: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	if res.StatusCode != http.StatusOK {
		return fmt.Errorf("musicbrainz: status %d", res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(out)
}

func (m *MusicBrainz) SearchRelease(ctx context.Context, artist, album string) (MusicRelease, error) {
	query := fmt.Sprintf(`release:%q AND artist:%q`, album, artist)
	u := "https://musicbrainz.org/ws/2/release/?fmt=json&limit=1&query=" + url.QueryEscape(query)
	var search struct {
		Releases []struct {
			ID    string `json:"id"`
			Title string `json:"title"`
			Date  string `json:"date"`
		} `json:"releases"`
	}
	if err := m.get(ctx, u, &search); err != nil {
		return MusicRelease{}, err
	}
	if len(search.Releases) == 0 {
		return MusicRelease{}, ErrNotFound
	}
	r := search.Releases[0]
	out := MusicRelease{MBID: r.ID, Title: r.Title}
	if len(r.Date) >= 4 {
		out.Year, _ = strconv.Atoi(r.Date[:4])
	}

	// Tracklist needs a second lookup.
	var rel struct {
		Media []struct {
			Tracks []struct {
				Position int    `json:"position"`
				Title    string `json:"title"`
			} `json:"tracks"`
		} `json:"media"`
	}
	if err := m.get(ctx, "https://musicbrainz.org/ws/2/release/"+r.ID+"?fmt=json&inc=recordings", &rel); err == nil {
		// Positions restart on every disc, so each track is keyed by its
		// disc as well.
		for d, medium := range rel.Media {
			for _, t := range medium.Tracks {
				out.Tracks = append(out.Tracks, MBTrack{Disc: d + 1, Pos: t.Position, Title: t.Title})
			}
		}
	}
	return out, nil
}

// FrontCover fetches the release's front cover from the Cover Art Archive
// (nil when the release has no artwork there).
func (m *MusicBrainz) FrontCover(ctx context.Context, mbid string) ([]byte, error) {
	m.throttle()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://coverartarchive.org/release/"+mbid+"/front-500", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", mbUserAgent)
	res, err := m.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("cover art archive: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode == http.StatusNotFound {
		return nil, nil
	}
	if res.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("cover art archive: status %d", res.StatusCode)
	}
	return io.ReadAll(io.LimitReader(res.Body, 8<<20))
}
