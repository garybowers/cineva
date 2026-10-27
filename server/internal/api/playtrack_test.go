package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/labstack/echo/v4"

	"github.com/garybowers/cineva/server/internal/events"
	"github.com/garybowers/cineva/server/internal/homeassistant"
	"github.com/garybowers/cineva/server/internal/playback"
	"github.com/garybowers/cineva/server/internal/prefs"
	"github.com/garybowers/cineva/server/internal/store"
)

// Playing a track is negotiated on the server: the device's music-quality
// preference decides the stream (an administrator's lock holds over the
// client's own choice), a device that cannot decode the file's codec is
// sent a transcode, and the answer says what will play. A repeat of the
// same play within the window is the same play: one history row, one
// audit entry.
func TestPlayTrackNegotiation(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	if err := st.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	artist, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindArtist, Title: "Alpha", Path: "artist://alpha"})
	if err != nil {
		t.Fatal(err)
	}
	album, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindAlbum, ParentID: artist.ID, Title: "First", Artist: "Alpha", Path: "/music/Alpha/First"})
	if err != nil {
		t.Fatal(err)
	}
	flac, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindTrack, ParentID: album.ID, Title: "One", Artist: "Alpha",
		Path: "/music/Alpha/First/01.flac", RuntimeSecs: 200, Container: "flac", AudioCodec: "flac", Bitrate: 900, SampleRate: 96000, BitDepth: 24})
	if err != nil {
		t.Fatal(err)
	}
	u, err := st.CreateUser(ctx, "viewer", "", "h", store.RoleUser)
	if err != nil {
		t.Fatal(err)
	}
	phone, err := st.CreateSession(ctx, "phone", u.ID, "2099-01-01 00:00:00", "Safari · iOS", "::1", "")
	if err != nil {
		t.Fatal(err)
	}
	s := &server{store: st, events: events.NewBus(), presence: newPresence(), ha: homeassistant.New(st),
		playbacks: playback.NewRegistry(time.Minute), prefs: prefs.NewService(st)}

	play := func(body string) (int, playInfo) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		rec := httptest.NewRecorder()
		c := echo.New().NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues(flac.ID)
		c.Set(ctxUser, u)
		c.Set(ctxSessionID, phone.ID)
		if err := s.playItem(c); err != nil {
			var he *echo.HTTPError
			if errors.As(err, &he) {
				return he.Code, playInfo{}
			}
			t.Fatalf("play: %v", err)
		}
		var info playInfo
		if err := json.Unmarshal(rec.Body.Bytes(), &info); err != nil {
			t.Fatalf("decode: %v %s", err, rec.Body.String())
		}
		return rec.Code, info
	}
	plays := func() int {
		t.Helper()
		totals, err := st.PlayTotals(ctx, 1)
		if err != nil {
			t.Fatal(err)
		}
		return totals.Plays
	}

	// No preference, no capabilities: the original, with its details.
	code, info := play(`{}`)
	if code != http.StatusOK || info.Mode != "direct" || info.URL != "/api/v1/items/"+flac.ID+"/stream" {
		t.Fatalf("original: %d %+v", code, info)
	}
	if info.Source.Audio != "direct" || info.Source.AudioCodec != "flac" || info.Source.Bitrate != 900 ||
		info.Source.SampleRate != 96000 || info.Source.BitDepth != 24 {
		t.Errorf("source: %+v", info.Source)
	}
	if plays() != 1 {
		t.Errorf("%d plays after one request", plays())
	}

	// The same device asking again at once (a seek to the start) is the
	// same play: the same answer, no second history row.
	if _, again := play(`{}`); again.URL != info.URL || plays() != 1 {
		t.Errorf("repeat: %+v, %d plays", again, plays())
	}

	// A different quality within the window is a new decision, not the
	// old answer: the client's own choice applies when nothing locks it.
	_, info = play(`{"quality":"opus-64"}`)
	if !strings.HasSuffix(info.URL, "?format=opus&quality=64") || info.Source.Audio != "transcode" ||
		info.Source.AudioCodec != "opus" || info.Source.Bitrate != 64 || info.Source.SampleRate != 48000 || info.Source.BitDepth != 0 {
		t.Errorf("opus-64: %+v", info)
	}
	s.recent = recentPlays{}

	// A device that cannot decode FLAC and does not list Opus gets AAC.
	_, info = play(`{"containers":["mp3"],"audioCodecs":["mp3","aac"]}`)
	if !strings.HasSuffix(info.URL, "?format=aac&quality=192") || info.Reason == "" {
		t.Errorf("aac fallback: %+v", info)
	}
	s.recent = recentPlays{}

	// An administrator's locked default wins over the client's choice.
	if err := s.prefs.SetDefaults(ctx, store.PrefOwnerServer, "", map[string]*store.PrefSetting{
		prefs.MusicQuality: {Value: "opus-128", Locked: true},
	}); err != nil {
		t.Fatal(err)
	}
	_, info = play(`{"quality":"original"}`)
	if !strings.HasSuffix(info.URL, "?format=opus&quality=128") {
		t.Errorf("locked: %+v", info)
	}
	// The registry knows the real mode and quality.
	if p, ok := s.playbacks.Get(u.ID, flac.ID); !ok || p.Mode != "transcode" || p.Quality != "opus-128" {
		t.Errorf("registry: %+v %v", p, ok)
	}
}
