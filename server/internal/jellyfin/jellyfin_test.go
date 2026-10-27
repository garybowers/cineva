package jellyfin

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"

	"github.com/garybowers/cineva/server/internal/store"
)

func TestIDs(t *testing.T) {
	uuid := "4a600227-4a0d-4779-9757-9f18143c7c0c"
	if got := ID(uuid); got != "4a6002274a0d477997579f18143c7c0c" {
		t.Errorf("ID = %q", got)
	}
	if got := ParseID("4A6002274A0D477997579F18143C7C0C"); got != uuid {
		t.Errorf("ParseID = %q", got)
	}
	for _, other := range []string{"local", "pl-abc", "", "4a6002274a0d477997579f18143c7c0"} {
		if ID(other) != other || ParseID(other) != other {
			t.Errorf("%q should pass through", other)
		}
	}
	rock := DerivedID("genre", "Rock")
	if rock != DerivedID("genre", "Rock") || rock == DerivedID("genre", "Pop") || len(rock) != 32 {
		t.Error("derived ids must be stable 32-digit ids")
	}
	if Ticks(90) != 900_000_000 || Secs(900_000_000) != 90 {
		t.Error("ticks")
	}
}

func TestParseAuth(t *testing.T) {
	tests := []struct {
		header string
		want   Auth
	}{
		{`MediaBrowser Client="Jellyfin Android TV", Device="Fire TV", DeviceId="abc123", Version="0.17.0", Token="tok"`,
			Auth{Client: "Jellyfin Android TV", Device: "Fire TV", DeviceID: "abc123", Version: "0.17.0", Token: "tok"}},
		{`Emby Client="Infuse", Device="Apple TV, lounge", DeviceId="x", Version="7"`,
			Auth{Client: "Infuse", Device: "Apple TV, lounge", DeviceID: "x", Version: "7"}},
		{`MediaBrowser Token="only"`, Auth{Token: "only"}},
		{`Bearer whatever`, Auth{}},
	}
	for _, tc := range tests {
		if got := ParseAuth(tc.header); got != tc.want {
			t.Errorf("ParseAuth(%q) = %+v, want %+v", tc.header, got, tc.want)
		}
	}
}

func TestItemMapping(t *testing.T) {
	m := Mapper{ServerID: "srv", Paths: true}
	series := store.Item{ID: "11111111-1111-4111-8111-111111111111", Kind: store.KindSeries, Title: "Succession", Poster: "/artwork/s.jpg", Backdrop: "/artwork/b.jpg", SeasonCount: 4, EpisodeCount: 39, Status: "Ended", LastAirDate: "2023-05-28", TmdbID: 76331}
	season := store.Item{ID: "22222222-2222-4222-8222-222222222222", Kind: store.KindSeason, Title: "Season 1", SeasonNum: 1, ParentID: series.ID}
	ep := store.Item{ID: "33333333-3333-4333-8333-333333333333", Kind: store.KindEpisode, Title: "Celebration", SeasonNum: 1, EpisodeNum: 1,
		ParentID: season.ID, RuntimeSecs: 3674, AirDate: "2018-06-03", Container: "mkv", VideoCodec: "h264", Path: "/tv/s01e01.mkv",
		Watched: true, PlayCount: 2, LastPlayedAt: "2026-10-01 20:00:00", UserRating: 5}

	b := m.Item(ep, Context{Series: &series, Season: &season, Genres: []string{"Drama"}})
	if b.Type != "Episode" || b.MediaType != "Video" || b.IsFolder || b.ID != ID(ep.ID) || b.ServerID != "srv" {
		t.Errorf("episode: %+v", b)
	}
	if b.SeriesName != "Succession" || b.SeriesID != ID(series.ID) || b.SeasonID != ID(season.ID) || b.SeasonName != "Season 1" {
		t.Errorf("episode context: %+v", b)
	}
	if *b.IndexNumber != 1 || *b.ParentIndexNumber != 1 || b.RunTimeTicks != Ticks(3674) || b.PremiereDate != "2018-06-03T00:00:00.0000000Z" {
		t.Errorf("episode numbers: %+v", b)
	}
	if b.Path != "/tv/s01e01.mkv" || b.Genres[0] != "Drama" || b.GenreItems[0].ID != DerivedID("genre", "Drama") {
		t.Errorf("episode path/genres: %+v", b)
	}
	if !b.UserData.Played || b.UserData.PlayCount != 2 || !b.UserData.IsFavorite || b.UserData.LastPlayedDate != "2026-10-01T20:00:00.0000000Z" {
		t.Errorf("user data: %+v", b.UserData)
	}
	if b.SeriesPrimaryImageTag != ImageTag("/artwork/s.jpg") || b.ParentBackdropItemID != ID(series.ID) {
		t.Errorf("parent images: %+v", b)
	}
	if m2 := (Mapper{}); m2.Item(ep, Context{}).Path != "" {
		t.Error("paths are for admins only")
	}

	sb := m.Item(series, Context{})
	if sb.Type != "Series" || !sb.IsFolder || sb.ChildCount != 4 || sb.RecursiveItemCount != 39 || sb.Status != "Ended" ||
		sb.ProviderIds["Tmdb"] != "76331" || sb.ImageTags["Primary"] == "" || len(sb.BackdropImageTags) != 1 || sb.EndDate == "" {
		t.Errorf("series: %+v", sb)
	}

	movie := store.Item{ID: "44444444-4444-4444-8444-444444444444", Kind: store.KindMovie, Title: "Heat", Year: 1995, Tagline: "A Los Angeles crime saga", Certification: "15", Rating: 8.3, PositionSecs: 600, RuntimeSecs: 10200}
	mb := m.Item(movie, Context{Cast: []store.Person{{ID: "p1", Name: "Al Pacino", Role: "Vincent Hanna"}}, Crew: []store.Person{{ID: "p2", Name: "Michael Mann", Role: "Director"}}})
	if mb.Type != "Movie" || mb.VideoType != "VideoFile" || mb.Taglines[0] == "" || mb.OfficialRating != "15" || mb.CommunityRating != 8.3 || mb.ProductionYear != 1995 {
		t.Errorf("movie: %+v", mb)
	}
	if len(mb.People) != 2 || mb.People[0].Type != "Actor" || mb.People[1].Type != "Director" {
		t.Errorf("people: %+v", mb.People)
	}
	if mb.UserData.PlaybackPositionTicks != Ticks(600) || mb.UserData.PlayedPercentage == nil || int(*mb.UserData.PlayedPercentage) != 5 {
		t.Errorf("resume: %+v", mb.UserData)
	}

	artist := store.Item{ID: "55555555-5555-4555-8555-555555555555", Kind: store.KindArtist, Title: "ABBA", ChildCount: 9, MBID: "mb-artist"}
	album := store.Item{ID: "66666666-6666-4666-8666-666666666666", Kind: store.KindAlbum, Title: "Arrival", Artist: "ABBA", ParentID: artist.ID, Year: 1976, ChildCount: 10, Poster: "/artwork/a.jpg"}
	track := store.Item{ID: "77777777-7777-4777-8777-777777777777", Kind: store.KindTrack, Title: "Dancing Queen", Artist: "ABBA", Album: "Arrival", AlbumArtist: "ABBA", ParentID: album.ID, TrackNum: 2, DiscNum: 1, Bitrate: 900, Poster: "/artwork/a.jpg", MBID: "mb-rec"}
	ab := m.Item(album, Context{Artist: &artist})
	if ab.Type != "MusicAlbum" || ab.AlbumArtist != "ABBA" || ab.AlbumArtists[0].ID != ID(artist.ID) || ab.ChildCount != 10 || ab.PrimaryImageAspectRatio != 1 {
		t.Errorf("album: %+v", ab)
	}
	tb := m.Item(track, Context{Artist: &artist})
	if tb.Type != "Audio" || tb.MediaType != "Audio" || tb.Album != "Arrival" || tb.AlbumID != ID(album.ID) || *tb.IndexNumber != 2 || *tb.ParentIndexNumber != 1 ||
		tb.Bitrate != 900000 || tb.ProviderIds["MusicBrainzTrack"] != "mb-rec" || tb.AlbumPrimaryImageTag == "" || tb.ArtistItems[0].ID != ID(artist.ID) {
		t.Errorf("track: %+v", tb)
	}
	if m.Item(artist, Context{}).ProviderIds["MusicBrainzArtist"] != "mb-artist" {
		t.Error("artist mbid")
	}

	lib := m.Library(store.Library{ID: "88888888-8888-4888-8888-888888888888", Name: "Films", MediaType: store.MediaMovies})
	if lib.Type != "CollectionFolder" || lib.CollectionType != "movies" || !lib.IsFolder {
		t.Errorf("library: %+v", lib)
	}

	// The JSON carries Jellyfin's names and the lists are never null.
	data, _ := json.Marshal(m.Item(movie, Context{}))
	for _, want := range []string{`"Id":`, `"ServerId":"srv"`, `"UserData":{`, `"Genres":[]`, `"ImageTags":{}`, `"BackdropImageTags":[]`, `"LocationType":"FileSystem"`} {
		if !strings.Contains(string(data), want) {
			t.Errorf("json lacks %s: %s", want, data)
		}
	}
}

func TestStreamsAndSource(t *testing.T) {
	it := store.Item{ID: "4a600227-4a0d-4779-9757-9f18143c7c0c", Kind: store.KindMovie, Container: "mkv", VideoCodec: "hevc", Width: 3840, Height: 2160, RuntimeSecs: 100, SizeBytes: 100_000_000, Path: "/m/x.mkv"}
	audio := []store.AudioTrack{{StreamIndex: 1, Codec: "eac3", Language: "eng", LanguageName: "English", Channels: 6, BitRate: 640000, Default: true}}
	subs := []store.SubtitleTrack{
		{ID: "s1", StreamIndex: 2, Codec: "subrip", Language: "eng"},
		{ID: "s2", StreamIndex: 3, Codec: "hdmv_pgs_subtitle", Language: "eng"},
		{ID: "s3", StreamIndex: 0, Codec: "srt", Source: "sidecar", Path: "/m/x.en.srt", Language: "eng"},
	}
	streams := Streams(it, audio, subs, func(st store.SubtitleTrack) string { return "/vtt/" + st.ID })
	if len(streams) != 5 || streams[0].Type != "Video" || streams[0].Codec != "hevc" || streams[0].Height != 2160 {
		t.Fatalf("streams: %+v", streams)
	}
	if a := streams[1]; a.Type != "Audio" || a.Index != 1 || a.Channels != 6 || a.BitRate != 640000 || !a.IsDefault || !strings.Contains(a.DisplayTitle, "5.1") {
		t.Errorf("audio: %+v", a)
	}
	if s := streams[2]; s.Type != "Subtitle" || !s.IsTextSubtitleStream || s.DeliveryMethod != "External" || s.DeliveryURL != "/vtt/s1" || s.Codec != "subrip" {
		t.Errorf("text subtitle: %+v", s)
	}
	if s := streams[3]; s.IsTextSubtitleStream || s.DeliveryMethod != "Encode" || s.Codec != "PGSSUB" {
		t.Errorf("image subtitle: %+v", s)
	}
	if s := streams[4]; !s.IsExternal || s.Path != "/m/x.en.srt" {
		t.Errorf("sidecar: %+v", s)
	}
	src := Source(it, streams, "")
	if src.Bitrate != 8_000_000 || src.Name != "x" || src.Container != "mkv" || src.ID != ID(it.ID) || !src.SupportsTranscoding {
		t.Errorf("source: %+v", src)
	}
}

func TestReadProfile(t *testing.T) {
	p := &DeviceProfile{
		MaxStreamingBitrate: 20_000_000,
		DirectPlayProfiles: []DirectPlayProfile{
			{Container: "mkv,mp4,m4v", Type: "Video", VideoCodec: "h264,h265", AudioCodec: "aac,ac3,eac3"},
			{Container: "mp3,flac", Type: "Audio"},
		},
		SubtitleProfiles: []SubtitleProfile{{Format: "srt", Method: "External"}},
	}
	got := Read(p)
	if !slices.Equal(got.Containers, []string{"mkv", "mp4", "m4v"}) {
		t.Errorf("containers %v", got.Containers)
	}
	if !slices.Equal(got.VideoCodecs, []string{"h264", "hevc"}) || !slices.Equal(got.AudioCodecs, []string{"aac", "ac3", "eac3"}) {
		t.Errorf("codecs %v %v", got.VideoCodecs, got.AudioCodecs)
	}
	if got.MaxBitrate != 20_000_000 || !got.ExternalSubtitles {
		t.Errorf("rest: %+v", got)
	}
	// A profile naming no codec plays any; no profile at all plays nothing
	// in particular, which the server's defaults then decide.
	any := Read(&DeviceProfile{DirectPlayProfiles: []DirectPlayProfile{{Container: "mp4", Type: "Video"}}})
	if !slices.Contains(any.VideoCodecs, "av1") || !slices.Contains(any.AudioCodecs, "flac") {
		t.Errorf("any codec: %+v", any)
	}
	if none := Read(nil); none.Containers != nil || none.VideoCodecs != nil {
		t.Errorf("nil profile: %+v", none)
	}

	tests := []struct {
		max, file int
		want      string
	}{
		{0, 50_000_000, ""}, {120_000_000, 50_000_000, ""}, {10_000_000, 50_000_000, "1080p"},
		{5_000_000, 50_000_000, "720p"}, {2_000_000, 50_000_000, "480p"}, {2_000_000, 0, ""},
	}
	for _, tc := range tests {
		if got := QualityFor(tc.max, tc.file); got != tc.want {
			t.Errorf("QualityFor(%d, %d) = %q, want %q", tc.max, tc.file, got, tc.want)
		}
	}
}

func TestHelpers(t *testing.T) {
	if !slices.Equal(ParseList(" Movie, Series ,,Episode"), []string{"Movie", "Series", "Episode"}) {
		t.Error("ParseList")
	}
	if !HasAny(nil, "x") || !HasAny([]string{"movie"}, "Movie") || HasAny([]string{"movie"}, "Series") {
		t.Error("HasAny")
	}
	if Atoi(" 12 ", 0) != 12 || Atoi("x", 7) != 7 {
		t.Error("Atoi")
	}
	if Kind("MusicAlbum") != store.KindAlbum || Kind("BoxSet") != "" || Type(store.KindTrack) != "Audio" {
		t.Error("kinds")
	}
}
