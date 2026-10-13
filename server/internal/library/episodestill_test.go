package library

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// An episode's image is its TMDB still, with embedded cover art only as the
// fallback. Embedded art used to win whenever a file had any, and in an
// episode that is nearly always the series' or season's portrait cover — a
// third of the library showed the same cover on every row of a season.
func TestEpisodeStillOutranksEmbeddedArt(t *testing.T) {
	stills := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/still.jpg" {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write([]byte("still"))
	}))
	defer stills.Close()

	tests := []struct {
		name     string
		stillURL string
		embedded bool
		// want is the poster's prefix in the artwork cache: "tmdb-" for the
		// still, "embed-" for the cover art, "" for nothing.
		want string
		// extracts is whether the embedded art should have been read.
		extracts bool
	}{
		{"still and cover art", stills.URL + "/still.jpg", true, "tmdb-", false},
		{"still only", stills.URL + "/still.jpg", false, "tmdb-", false},
		{"cover art only", "", true, "embed-", true},
		{"still fails to download", stills.URL + "/missing.jpg", true, "embed-", true},
		{"neither", "", false, "", false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			epDir := filepath.Join(root, "Breaking Bad", "Season 1")
			if err := os.MkdirAll(epDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(epDir, "Breaking.Bad.S01E01.mkv"), []byte("x"), 0o644); err != nil {
				t.Fatal(err)
			}

			st := newLibTestStore(t)
			ctx := context.Background()
			lib, err := st.CreateLibrary(ctx, "TV", store.MediaTV, []string{root})
			if err != nil {
				t.Fatal(err)
			}
			extracted := false
			m := &Manager{
				Store:      st,
				ArtworkDir: t.TempDir(),
				Probe: func(string) (media.Info, error) {
					return media.Info{RuntimeSecs: 2700, Container: "mkv", HasCoverArt: tc.embedded}, nil
				},
				CoverArt: func(string) []byte {
					extracted = true
					return []byte("cover")
				},
				Video: stubVideo{
					tv: metadata.VideoResult{TmdbID: 1396, Title: "Breaking Bad", Year: 2008},
					season: metadata.SeasonResult{Episodes: []metadata.EpisodeResult{{
						Num: 1, Title: "Pilot", StillURL: tc.stillURL,
					}}},
				},
			}
			if _, _, err := m.scanTV(ctx, lib, fullPlan(t, m, lib), func(int, int, int) {}); err != nil {
				t.Fatalf("scanTV: %v", err)
			}

			items, err := st.ItemsByLibrary(ctx, lib.ID)
			if err != nil {
				t.Fatal(err)
			}
			var episode store.Item
			for _, it := range items {
				if it.Kind == store.KindEpisode {
					episode = it
				}
			}
			got := strings.TrimPrefix(episode.Poster, "/artwork/")
			switch {
			case tc.want == "" && got != "":
				t.Errorf("poster = %q, want none", episode.Poster)
			case tc.want != "" && !strings.HasPrefix(got, tc.want):
				t.Errorf("poster = %q, want a %s image", episode.Poster, tc.want)
			}
			if extracted != tc.extracts {
				t.Errorf("cover art read = %v, want %v", extracted, tc.extracts)
			}
		})
	}
}
