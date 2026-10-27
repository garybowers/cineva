package subsonic

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestEncodeFormats(t *testing.T) {
	r := OK()
	r.MusicFolders = &MusicFolders{MusicFolder: []MusicFolder{{ID: "lib-1", Name: "Music"}}}

	var xmlOut strings.Builder
	ct, err := Encode(&xmlOut, "xml", "", r)
	if err != nil || !strings.HasPrefix(ct, "text/xml") {
		t.Fatalf("xml: %v %q", err, ct)
	}
	for _, want := range []string{
		`<subsonic-response xmlns="http://subsonic.org/restapi" status="ok" version="1.16.1" type="cineva"`,
		`openSubsonic="true"`, `<musicFolders><musicFolder id="lib-1" name="Music"></musicFolder></musicFolders>`,
	} {
		if !strings.Contains(xmlOut.String(), want) {
			t.Errorf("xml lacks %q:\n%s", want, xmlOut.String())
		}
	}

	var jsonOut strings.Builder
	ct, err = Encode(&jsonOut, "json", "", r)
	if err != nil || !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("json: %v %q", err, ct)
	}
	var decoded map[string]map[string]any
	if err := json.Unmarshal([]byte(jsonOut.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	env := decoded["subsonic-response"]
	if env["status"] != "ok" || env["version"] != "1.16.1" || env["openSubsonic"] != true {
		t.Errorf("json envelope %v", env)
	}
	if _, has := env["error"]; has {
		t.Errorf("json carries an empty error: %v", env)
	}
	folders := env["musicFolders"].(map[string]any)["musicFolder"].([]any)
	if len(folders) != 1 || folders[0].(map[string]any)["name"] != "Music" {
		t.Errorf("json folders %v", folders)
	}

	var jsonp strings.Builder
	ct, err = Encode(&jsonp, "jsonp", "cb", Failed(NewError(ErrNotFound, "gone")))
	if err != nil || !strings.HasPrefix(ct, "application/javascript") {
		t.Fatalf("jsonp: %v %q", err, ct)
	}
	if !strings.HasPrefix(jsonp.String(), `cb({"subsonic-response":{"status":"failed"`) ||
		!strings.Contains(jsonp.String(), `"error":{"code":70,"message":"gone"}`) {
		t.Errorf("jsonp %s", jsonp.String())
	}
}

func TestFormat(t *testing.T) {
	tests := []struct {
		query            string
		format, callback string
	}{
		{"", "xml", ""},
		{"f=xml", "xml", ""},
		{"f=JSON", "json", ""},
		{"f=jsonp&callback=handle", "jsonp", "handle"},
		{"f=jsonp", "jsonp", "callback"},
	}
	for _, tc := range tests {
		v, _ := url.ParseQuery(tc.query)
		f, cb := Format(v)
		if f != tc.format || cb != tc.callback {
			t.Errorf("Format(%q) = %q %q, want %q %q", tc.query, f, cb, tc.format, tc.callback)
		}
	}
}

func TestIndexArtists(t *testing.T) {
	idx := IndexArtists([]Artist{
		{Name: "The Beatles", SortName: "beatles"},
		{Name: "Björk", SortName: "bjork"},
		{Name: "100 gecs", SortName: "100 gecs"},
		{Name: "Arctic Monkeys", SortName: "arctic monkeys"},
	})
	got := []string{}
	for _, i := range idx {
		got = append(got, i.Name+":"+i.Artist[0].Name)
	}
	want := []string{"A:Arctic Monkeys", "B:The Beatles", "#:100 gecs"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("index %v, want %v", got, want)
	}
	if len(idx[1].Artist) != 2 {
		t.Errorf("B holds %d artists", len(idx[1].Artist))
	}
}

func TestLyricsFrom(t *testing.T) {
	synced := LyricsFrom(store.Lyrics{Synced: true, Text: "[ar:Someone]\n[00:12.50] First line\n[01:02] Second\n\n"}, "Someone", "Song")
	if !synced.Synced || len(synced.Line) != 2 || synced.Line[0].Start == nil || *synced.Line[0].Start != 12500 ||
		synced.Line[0].Value != "First line" || *synced.Line[1].Start != 62000 {
		t.Errorf("synced lyrics %+v", synced)
	}
	plain := LyricsFrom(store.Lyrics{Text: "one\ntwo"}, "", "")
	if plain.Synced || len(plain.Line) != 2 || plain.Line[1].Start != nil || plain.Line[1].Value != "two" {
		t.Errorf("plain lyrics %+v", plain)
	}
	if got := PlainLyrics(store.Lyrics{Synced: true, Text: "[00:01.00] a\n[00:02.00] b"}); got != "a\nb" {
		t.Errorf("plain of synced %q", got)
	}
}

func TestTimeAndMapping(t *testing.T) {
	if got := Time("2026-10-25 13:53:51"); got != "2026-10-25T13:53:51Z" {
		t.Errorf("Time = %q", got)
	}
	if Time("") != "" || Time("nonsense") != "" {
		t.Error("blank or bad times should be empty")
	}
	song := SongFrom(store.Item{ID: "t1", ParentID: "a1", Kind: store.KindTrack, Title: "Arabella", Album: "AM",
		AlbumArtist: "Arctic Monkeys", Artist: "Arctic Monkeys", Container: "flac", Poster: "/artwork/x.jpg",
		TrackNum: 4, RuntimeSecs: 207, Bitrate: 900, SampleRate: 44100, BitDepth: 16, UserRating: 5, AddedAt: "2026-01-02 03:04:05"})
	if song.CoverArt != "a1" || song.ContentType != "audio/flac" || song.Suffix != "flac" || song.Path != "Arctic Monkeys/AM/Arabella.flac" ||
		song.Starred == "" || song.MediaType != "song" || song.AlbumID != "a1" {
		t.Errorf("song %+v", song)
	}
	album := AlbumFrom(store.Item{ID: "a1", Kind: store.KindAlbum, Title: "AM", Compilation: true, ChildCount: 12}, 2400)
	if album.CoverArt != "" || !album.IsCompilation || album.SongCount != 12 || album.Duration != 2400 {
		t.Errorf("album %+v", album)
	}
	pl := PlaylistFrom(store.Playlist{ID: "p1", Name: "Mine", Covers: []string{"/artwork/c.jpg"}}, "gary")
	if pl.ID != "pl-p1" || pl.CoverArt != "pl-p1" || pl.Owner != "gary" {
		t.Errorf("playlist %+v", pl)
	}
	if id, ok := PlaylistID("pl-p1"); !ok || id != "p1" {
		t.Errorf("PlaylistID %q %v", id, ok)
	}
}
