package library

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"sync"
	"testing"

	"github.com/garybowers/cineva/server/internal/media"
	"github.com/garybowers/cineva/server/internal/metadata"
	"github.com/garybowers/cineva/server/internal/store"
)

func TestDiscNumberOf(t *testing.T) {
	tests := []struct {
		name string
		want int
	}{
		{"CD1", 1}, {"Disc 2", 2}, {"disk3", 3}, {"CD 1 - Live", 1}, {"Disc One", 1},
		{"Disc Two: Rarities", 2}, {"CD10 bonus", 10},
		{"Discography", 0}, {"Disco Inferno", 0}, {"CD1a", 0}, {"2001 - Album", 0}, {"", 0},
	}
	for _, tc := range tests {
		if got := discNumberOf(tc.name); got != tc.want {
			t.Errorf("discNumberOf(%q) = %d, want %d", tc.name, got, tc.want)
		}
	}
}

func TestPrimaryArtist(t *testing.T) {
	tests := []struct{ in, want string }{
		{"Arctic Monkeys", "arctic monkeys"},
		{"Calvin Harris feat. Rihanna", "calvin harris"},
		{"Calvin Harris ft. Rihanna", "calvin harris"},
		{"Simon & Garfunkel", "simon"},
		{"Nina Simone, Live", "nina simone"},
	}
	for _, tc := range tests {
		if got := primaryArtist(tc.in); got != tc.want {
			t.Errorf("primaryArtist(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// fakeMusic answers release searches from a table and records what it was
// asked.
type fakeMusic struct {
	releases map[string]metadata.MusicRelease // "artist|album" -> release
	asked    []string
}

func (f *fakeMusic) SearchRelease(_ context.Context, artist, album string) (metadata.MusicRelease, error) {
	f.asked = append(f.asked, artist+"|"+album)
	if rel, ok := f.releases[artist+"|"+album]; ok {
		return rel, nil
	}
	return metadata.MusicRelease{}, metadata.ErrNotFound
}

func (f *fakeMusic) FrontCover(context.Context, string) ([]byte, error) {
	return []byte("cover from musicbrainz"), nil
}

// musicTree writes empty audio files under root and returns their paths.
func musicTree(t *testing.T, root string, paths ...string) {
	t.Helper()
	for _, p := range paths {
		full := filepath.Join(root, p)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// A scan builds the tree from tags, decides compilations over the whole
// album, reads lyrics, and leaves MusicBrainz to the pass after it, which
// fills what is missing and names the untitled tracks.
func TestMusicScanAlbums(t *testing.T) {
	root := t.TempDir()
	musicTree(t, root,
		"Arctic Monkeys/2013 - AM/01 - Do I Wanna Know.flac",
		"Arctic Monkeys/2013 - AM/02 - R U Mine.flac",
		"Arctic Monkeys/2013 - AM/02 - R U Mine.lrc",
		"Mixed/Party/01.mp3", "Mixed/Party/02.mp3", "Mixed/Party/03.mp3",
		"Duets/Together/01 - Alone.flac", "Duets/Together/02 - With a Friend.flac",
		"Box/Set/CD 1 - Early/01 - First.flac", "Box/Set/Disc Two/01 - Later.flac",
	)
	if err := os.WriteFile(filepath.Join(root, "Arctic Monkeys/2013 - AM/02 - R U Mine.lrc"),
		[]byte("[00:12.00] Are you mine?\r\n[00:20.00] Tomorrow\r\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, t.TempDir())
	m.CoverArt = func(string) []byte { return nil }
	tags := map[string]media.AudioInfo{
		"01 - Do I Wanna Know.flac": {Title: "Do I Wanna Know?", Artist: "Arctic Monkeys", AlbumArtist: "Arctic Monkeys",
			Album: "AM", TrackNum: 1, Year: 2013, Genre: "Rock", RuntimeSecs: 272, Bitrate: 900, SampleRate: 44100, BitDepth: 16, Channels: 2,
			MBReleaseID: "rel-am", MBRecordingID: "rec-diwk", MBArtistID: "art-am", Lyrics: "Have you got colour in your cheeks?"},
		"02 - R U Mine.flac": {Title: "R U Mine?", Artist: "Arctic Monkeys", AlbumArtist: "Arctic Monkeys",
			Album: "AM", TrackNum: 2, Year: 2013, Genre: "Rock; Indie", RuntimeSecs: 201},
		// An untagged compilation: no album artist, three artists, no titles.
		"01.mp3": {Artist: "Britney Spears", TrackNum: 1, RuntimeSecs: 200},
		"02.mp3": {Artist: "Blur", TrackNum: 2, RuntimeSecs: 200},
		"03.mp3": {Artist: "Pulp", TrackNum: 3, RuntimeSecs: 200},
		// One guest on one track is not a compilation.
		"01 - Alone.flac":         {Title: "Alone", Artist: "Nina", Album: "Together", TrackNum: 1, RuntimeSecs: 100},
		"02 - With a Friend.flac": {Title: "With a Friend", Artist: "Nina feat. Simone", Album: "Together", TrackNum: 2, RuntimeSecs: 100},
		"01 - First.flac":         {Title: "First", Artist: "Box", Album: "Set", TrackNum: 1, RuntimeSecs: 100},
		"01 - Later.flac":         {Title: "Later", Artist: "Box", Album: "Set", TrackNum: 1, RuntimeSecs: 100},
	}
	// Probes run in parallel (probeAudioBatch), so the record needs a lock.
	var probed []string
	var probedMu sync.Mutex
	m.ProbeAudio = func(path string) (media.AudioInfo, error) {
		probedMu.Lock()
		probed = append(probed, filepath.Base(path))
		probedMu.Unlock()
		info, ok := tags[filepath.Base(path)]
		if !ok {
			t.Fatalf("no tags for %s", path)
		}
		info.Container = strings.TrimPrefix(filepath.Ext(path), ".")
		return info, nil
	}
	mb := &fakeMusic{releases: map[string]metadata.MusicRelease{
		"Various Artists|Party": {},
		"Nina|Together": {MBID: "rel-together", Year: 2001, Tracks: []metadata.MBTrack{
			{Disc: 1, Pos: 1, Title: "Alone (canonical)"}}},
	}}
	m.Music = mb

	res, err := m.RunScanSync(ctx, lib, false)
	if err != nil || res.Status != "completed" {
		t.Fatalf("scan: %v %+v", err, res)
	}
	items, err := st.ItemsByLibrary(ctx, lib.ID)
	if err != nil {
		t.Fatal(err)
	}
	byTitle := map[string]store.Item{}
	for _, it := range items {
		byTitle[string(it.Kind)+":"+it.Title] = it
	}

	// Every file was probed, each once, and the provider was asked once per
	// album that lacked something (AM has no cover here), after the scan
	// rather than during it, and never about Various Artists.
	sort.Strings(probed)
	if len(probed) != 9 {
		t.Errorf("probed %v", probed)
	}
	sort.Strings(mb.asked)
	if !slices.Equal(mb.asked, []string{"Arctic Monkeys|AM", "Box|Set", "Nina|Together"}) {
		t.Errorf("asked %v", mb.asked)
	}

	// The tagged album, with the file's details, ids and lyrics.
	am := byTitle["album:AM"]
	if am.Artist != "Arctic Monkeys" || am.Year != 2013 || am.MBID != "rel-am" || am.Compilation {
		t.Errorf("AM: %+v", am)
	}
	diwk := byTitle["track:Do I Wanna Know?"]
	if diwk.Album != "AM" || diwk.AlbumArtist != "Arctic Monkeys" || diwk.Bitrate != 900 || diwk.SampleRate != 44100 ||
		diwk.BitDepth != 16 || diwk.Channels != 2 || diwk.MBID != "rec-diwk" || diwk.Container != "flac" {
		t.Errorf("track: %+v", diwk)
	}
	if l, _ := st.LyricsFor(ctx, diwk.ID); l == nil || l.Synced || l.Text != "Have you got colour in your cheeks?" {
		t.Errorf("tag lyrics: %+v", l)
	}
	if l, _ := st.LyricsFor(ctx, byTitle["track:R U Mine?"].ID); l == nil || !l.Synced || !strings.HasPrefix(l.Text, "[00:12.00] Are you mine?\n") {
		t.Errorf(".lrc lyrics: %+v", l)
	}
	if g, _ := st.GenresForItem(ctx, am.ID); !slices.Equal(g, []string{"Indie", "Rock"}) {
		t.Errorf("album genres %v", g)
	}
	if byTitle["artist:Arctic Monkeys"].MBID != "art-am" {
		t.Errorf("artist mbid %q", byTitle["artist:Arctic Monkeys"].MBID)
	}

	// The untagged compilation sits under Various Artists, not under
	// Britney Spears, and no empty artist rows were made for the others.
	party := byTitle["album:Party"]
	if party.Artist != "Various Artists" || !party.Compilation || party.ParentID != byTitle["artist:Various Artists"].ID {
		t.Errorf("Party: %+v", party)
	}
	for _, name := range []string{"Britney Spears", "Blur", "Pulp"} {
		if _, ok := byTitle["artist:"+name]; ok {
			t.Errorf("empty artist row for %s", name)
		}
	}
	// Its tracks keep their own credits and, with no title tag, their
	// numbers as titles.
	if tr := byTitle["track:02"]; tr.Artist != "Blur" || tr.AlbumArtist != "Various Artists" || tr.TrackNum != 2 {
		t.Errorf("compilation track: %+v", tr)
	}

	// One guest does not make a compilation; the pass filled the year and
	// id from the release and named the untitled track... except Alone had
	// a title tag, so it keeps it.
	together := byTitle["album:Together"]
	if together.Artist != "Nina" || together.Compilation || together.Year != 2001 || together.MBID != "rel-together" || together.Poster == "" {
		t.Errorf("Together: %+v", together)
	}
	if _, ok := byTitle["track:Alone"]; !ok {
		t.Errorf("a tagged title was renamed: %v", keysOf(byTitle))
	}

	// Disc folders with titles are discs of one album.
	set := byTitle["album:Set"]
	if set.Title != "Set" {
		t.Fatalf("Set: %+v", set)
	}
	if first, later := byTitle["track:First"], byTitle["track:Later"]; first.ParentID != set.ID || later.ParentID != set.ID ||
		first.DiscNum != 1 || later.DiscNum != 2 {
		t.Errorf("discs: %+v %+v", first, later)
	}
	albums := 0
	for _, it := range items {
		if it.Kind == store.KindAlbum {
			albums++
		}
	}
	if albums != 4 {
		t.Errorf("%d albums", albums)
	}

	// A second scan reads nothing again and asks the provider only about
	// the album it found nothing for, once its retry time has passed (it
	// has not).
	probed, mb.asked = nil, nil
	if res, err := m.RunScanSync(ctx, lib, false); err != nil || res.Status != "completed" {
		t.Fatalf("rescan: %v %+v", err, res)
	}
	if len(probed) != 0 || len(mb.asked) != 0 {
		t.Errorf("rescan probed %v, asked %v", probed, mb.asked)
	}
}

func keysOf(m map[string]store.Item) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// The pass names untitled tracks from the release, by disc and position.
func TestMusicLookupNamesUntitledTracks(t *testing.T) {
	root := t.TempDir()
	musicTree(t, root, "Band/Live/CD1/01.flac", "Band/Live/CD2/01.flac")
	st := newLibTestStore(t)
	ctx := context.Background()
	lib, err := st.CreateLibrary(ctx, "Music", store.MediaMusic, []string{root})
	if err != nil {
		t.Fatal(err)
	}
	m := NewManager(st, nil, t.TempDir())
	m.CoverArt = func(string) []byte { return nil }
	m.ProbeAudio = func(path string) (media.AudioInfo, error) {
		return media.AudioInfo{Artist: "Band", Album: "Live", TrackNum: 1, RuntimeSecs: 100}, nil
	}
	m.Music = &fakeMusic{releases: map[string]metadata.MusicRelease{
		"Band|Live": {MBID: "rel-live", Year: 1979, Tracks: []metadata.MBTrack{
			{Disc: 1, Pos: 1, Title: "Opening Night"}, {Disc: 2, Pos: 1, Title: "Closing Night"}}},
	}}
	if res, err := m.RunScanSync(ctx, lib, false); err != nil || res.Status != "completed" {
		t.Fatalf("scan: %v %+v", err, res)
	}
	items, _ := st.ItemsByLibrary(ctx, lib.ID)
	got := map[int]string{}
	for _, it := range items {
		if it.Kind == store.KindTrack {
			got[it.DiscNum] = it.Title
		}
	}
	if got[1] != "Opening Night" || got[2] != "Closing Night" {
		t.Errorf("titles by disc: %v", got)
	}
}
