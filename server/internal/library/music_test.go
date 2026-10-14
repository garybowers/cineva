package library

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

// Replacing an album folder's cover image is picked up by an incremental
// scan: the album and its tracks get the new artwork under a new URL, and
// no track is read again to do it. An album scanned before covers were
// fingerprinted adopts its fingerprint instead of being reread.
func TestMusicCoverChange(t *testing.T) {
	root := t.TempDir()
	albumDir := filepath.Join(root, "Artist", "2001 - Album")
	if err := os.MkdirAll(albumDir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"01 - One.flac", "02 - Two.flac"} {
		if err := os.WriteFile(filepath.Join(albumDir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	cover := filepath.Join(albumDir, "Cover.jpg")
	if err := os.WriteFile(cover, []byte("first cover"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	artDir := t.TempDir()
	m := NewManager(st, nil, artDir)
	probes := 0
	m.ProbeAudio = func(string) (media.AudioInfo, error) {
		probes++
		return media.AudioInfo{RuntimeSecs: 180, Genre: "Rock; Pop"}, nil
	}
	m.CoverArt = func(string) []byte { return nil }

	scan := func() {
		t.Helper()
		probes = 0
		res, err := m.RunScanSync(ctx, lib, false)
		if err != nil || res.Status != "completed" {
			t.Fatalf("scan: %v %+v", err, res)
		}
	}
	posters := func() map[string]string {
		t.Helper()
		items, err := st.ItemsByLibrary(ctx, lib.ID)
		if err != nil {
			t.Fatal(err)
		}
		out := map[string]string{}
		for _, it := range items {
			if it.Kind != store.KindArtist {
				out[it.Title] = it.Poster
			}
		}
		return out
	}
	allSame := func(p map[string]string) string {
		t.Helper()
		var first string
		for title, poster := range p {
			if poster == "" || (first != "" && poster != first) {
				t.Fatalf("posters = %v, want the album's cover on the album and every track (%s differs)", p, title)
			}
			first = poster
		}
		return first
	}
	cached := func(poster string) string {
		t.Helper()
		name, _, _ := strings.Cut(strings.TrimPrefix(poster, "/artwork/"), "?")
		data, err := os.ReadFile(filepath.Join(artDir, name))
		if err != nil {
			t.Fatal(err)
		}
		return string(data)
	}

	scan()
	before := allSame(posters())
	if got := cached(before); got != "first cover" {
		t.Fatalf("cached cover = %q", got)
	}
	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range items {
		if it.Kind == store.KindTrack {
			if g, err := st.GenresForItem(ctx, it.ID); err != nil || !reflect.DeepEqual(g, []string{"Pop", "Rock"}) {
				t.Fatalf("%s genres = %v (%v), want the tag split", it.Title, g, err)
			}
		}
	}

	scan()
	if probes != 0 || allSame(posters()) != before {
		t.Fatalf("unchanged rescan: %d probes, posters %v", probes, posters())
	}

	if err := os.WriteFile(cover, []byte("second, larger cover"), 0o644); err != nil {
		t.Fatal(err)
	}
	later := time.Now().Add(time.Minute)
	if err := os.Chtimes(cover, later, later); err != nil {
		t.Fatal(err)
	}
	scan()
	after := allSame(posters())
	if probes != 0 {
		t.Fatalf("cover change read %d tracks again, want none", probes)
	}
	if after == before || cached(after) != "second, larger cover" {
		t.Fatalf("after replacing the cover: poster %q (was %q), cached %q", after, before, cached(after))
	}

	// Scanned before covers were fingerprinted: adopt, do not reread.
	stamps, err := st.FileStamps(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	album := stamps[albumDir]
	if err := st.SetAlbumArtwork(ctx, album.ID, "", 0); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(cover, later.Add(time.Minute), later.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	scan()
	if got := allSame(posters()); got != after {
		t.Fatalf("unrecorded fingerprint reread the cover: %q, want %q kept", got, after)
	}
	if stamps, _ = st.FileStamps(ctx, lib.ID); stamps[albumDir].SidecarStamp == 0 {
		t.Fatal("fingerprint not adopted")
	}
}

// An album whose tracks sit in disc folders takes its cover from the album
// folder when it has one, else from the disc folders, first disc first
// (by number, not by name), and within a folder by the usual cover names.
// A cover replaced in a disc folder is picked up by an incremental scan.
func TestMusicDiscFolderCover(t *testing.T) {
	tests := []struct {
		name   string
		covers map[string]string // path under the album folder -> contents
		want   string
	}{
		{"album folder only", map[string]string{"cover.jpg": "album"}, "album"},
		{"album folder before disc", map[string]string{"folder.jpg": "album", "CD1/cover.jpg": "disc 1"}, "album"},
		{"first disc", map[string]string{"CD2/cover.jpg": "disc 2", "CD1/cover.jpg": "disc 1"}, "disc 1"},
		{"first disc before better name", map[string]string{"Disc 2/cover.jpg": "disc 2", "Disc 1/front.png": "disc 1"}, "disc 1"},
		{"by number, not name", map[string]string{"CD10/cover.jpg": "disc 10", "CD2/cover.jpg": "disc 2"}, "disc 2"},
		{"best name within a disc", map[string]string{"Disk1/front.jpg": "front", "Disk1/Cover.JPG": "cover"}, "cover"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			albumDir := filepath.Join(root, "Artist", "2001 - Album")
			write := func(rel, data string) {
				t.Helper()
				p := filepath.Join(albumDir, rel)
				if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			write("CD1/01 - One.flac", "x")
			write("CD2/01 - Two.flac", "x")
			for rel, data := range tc.covers {
				write(rel, data)
			}

			st := newLibTestStore(t)
			ctx := context.Background()
			lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{root})
			if err != nil {
				t.Fatal(err)
			}
			artDir := t.TempDir()
			m := NewManager(st, nil, artDir)
			probes := 0
			m.ProbeAudio = func(string) (media.AudioInfo, error) {
				probes++
				return media.AudioInfo{RuntimeSecs: 180}, nil
			}
			m.CoverArt = func(string) []byte { return nil }
			cover := func() string {
				t.Helper()
				probes = 0
				if res, err := m.RunScanSync(ctx, lib, false); err != nil || res.Status != "completed" {
					t.Fatalf("scan: %v %+v", err, res)
				}
				items, err := st.ItemsByLibrary(ctx, lib.ID)
				if err != nil {
					t.Fatal(err)
				}
				for _, it := range items {
					if it.Kind == store.KindAlbum {
						name, _, _ := strings.Cut(strings.TrimPrefix(it.Poster, "/artwork/"), "?")
						data, err := os.ReadFile(filepath.Join(artDir, name))
						if err != nil {
							t.Fatalf("album poster %q: %v", it.Poster, err)
						}
						return string(data)
					}
				}
				t.Fatal("no album")
				return ""
			}

			if got := cover(); got != tc.want {
				t.Fatalf("cover = %q, want %q", got, tc.want)
			}
			// Replace whichever file won; the next scan rereads only the cover.
			for rel, data := range tc.covers {
				if data == tc.want {
					write(rel, "replaced "+data)
					later := time.Now().Add(time.Minute)
					if err := os.Chtimes(filepath.Join(albumDir, rel), later, later); err != nil {
						t.Fatal(err)
					}
				}
			}
			if got := cover(); got != "replaced "+tc.want || probes != 0 {
				t.Fatalf("after replacing: cover = %q with %d probes, want %q with none", got, probes, "replaced "+tc.want)
			}
		})
	}
}

func TestSplitGenres(t *testing.T) {
	tests := []struct {
		tag  string
		want []string
	}{
		{"", nil},
		{"Rock", []string{"Rock"}},
		{"Rock; Pop", []string{"Rock", "Pop"}},
		{"Jazz/Funk", []string{"Jazz", "Funk"}},
		{"Rock;rock; ;Indie", []string{"Rock", "Indie"}},
		{"Hip Hop|R&B", []string{"Hip Hop", "R&B"}},
	}
	for _, tc := range tests {
		if got := splitGenres(tc.tag); !reflect.DeepEqual(got, tc.want) {
			t.Errorf("splitGenres(%q) = %v, want %v", tc.tag, got, tc.want)
		}
	}
}

// fakeArtists is a music provider that can also look artists up.
type fakeArtists struct {
	info  metadata.ArtistInfo
	err   error
	calls *int
}

func (fakeArtists) SearchRelease(context.Context, string, string) (metadata.MusicRelease, error) {
	return metadata.MusicRelease{}, metadata.ErrNotFound
}
func (fakeArtists) FrontCover(context.Context, string) ([]byte, error) { return nil, nil }
func (f fakeArtists) ArtistInfo(context.Context, string) (metadata.ArtistInfo, error) {
	*f.calls++
	return f.info, f.err
}

// An artist is looked up once; what was found goes on the artist, a miss
// is remembered, and placeholder artists are never looked up.
func TestLookUpArtist(t *testing.T) {
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{"/music"})
	if err != nil {
		t.Fatal(err)
	}
	save := func(title string) store.Item {
		t.Helper()
		it, err := st.UpsertItem(ctx, store.Item{LibraryID: lib.ID, Kind: store.KindArtist, Title: title, Path: "artist://" + title})
		if err != nil {
			t.Fatal(err)
		}
		return it
	}
	known, unknown, various := save("Known"), save("Obscure"), save("Various Artists")
	calls := 0
	m := NewManager(st, nil, t.TempDir())
	due := func(a store.Item) bool {
		t.Helper()
		a, err := st.Item(ctx, a.ID)
		if err != nil {
			t.Fatal(err)
		}
		ok, err := m.ArtistLookupDue(ctx, a)
		if err != nil {
			t.Fatal(err)
		}
		return ok
	}
	if due(known) {
		t.Fatal("due with no provider that looks artists up")
	}

	m.Music = fakeArtists{info: metadata.ArtistInfo{MBID: "mb", Image: []byte("JPEG"), Bio: "A band.", BioURL: "https://en.wikipedia.org/wiki/Known"}, calls: &calls}
	if !due(known) || due(various) {
		t.Fatal("want Known due and Various Artists never")
	}
	if err := m.LookUpArtist(ctx, known); err != nil {
		t.Fatal(err)
	}
	got, err := st.Item(ctx, known.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got.Poster, "/artwork/artist-") || got.Overview != "A band." {
		t.Fatalf("artist = %q / %q", got.Poster, got.Overview)
	}
	if due(known) {
		t.Fatal("still due after a lookup that found everything")
	}

	m.Music = fakeArtists{err: metadata.ErrNotFound, calls: &calls}
	if err := m.LookUpArtist(ctx, unknown); err != nil {
		t.Fatal(err)
	}
	if due(unknown) {
		t.Fatal("a miss is looked up again straight away")
	}
	// A failure is not remembered, so the next visit tries again.
	other := save("Flaky")
	m.Music = fakeArtists{err: errors.New("network down"), calls: &calls}
	if err := m.LookUpArtist(ctx, other); err == nil || !due(other) {
		t.Fatalf("failed lookup: err %v, due %v; want an error and still due", err, due(other))
	}
}
