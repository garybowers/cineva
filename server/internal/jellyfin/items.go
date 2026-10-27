package jellyfin

import (
	"path/filepath"
	"strings"

	"github.com/garybowers/cineva/server/internal/store"
)

// Mapper turns Cineva's items into Jellyfin's, with what every item needs
// to carry: the server's id and whether the viewer may see file paths.
type Mapper struct {
	ServerID string
	// Paths includes each file's path, which Jellyfin shows only to
	// administrators.
	Paths bool
}

// Context is what a listing knows about an item's surroundings that the
// item itself does not carry: an episode's series and season, an album's
// artist, an item's genres and credits. Any of it may be absent.
type Context struct {
	Series, Season, Artist *store.Item
	Genres                 []string
	Cast, Crew             []store.Person
}

// Type is the Jellyfin item type for a Cineva kind.
func Type(kind store.ItemKind) string {
	switch kind {
	case store.KindMovie:
		return "Movie"
	case store.KindSeries:
		return "Series"
	case store.KindSeason:
		return "Season"
	case store.KindEpisode:
		return "Episode"
	case store.KindArtist:
		return "MusicArtist"
	case store.KindAlbum:
		return "MusicAlbum"
	case store.KindTrack:
		return "Audio"
	}
	return "Folder"
}

// Kind is the Cineva kind for a Jellyfin item type, "" for one Cineva
// has no items of (BoxSet, Playlist, Photo).
func Kind(typ string) store.ItemKind {
	switch strings.ToLower(typ) {
	case "movie":
		return store.KindMovie
	case "series":
		return store.KindSeries
	case "season":
		return store.KindSeason
	case "episode":
		return store.KindEpisode
	case "musicartist":
		return store.KindArtist
	case "musicalbum":
		return store.KindAlbum
	case "audio":
		return store.KindTrack
	}
	return ""
}

// MediaType is Video, Audio or Unknown.
func MediaType(kind store.ItemKind) string {
	switch kind {
	case store.KindMovie, store.KindEpisode:
		return "Video"
	case store.KindTrack:
		return "Audio"
	}
	return "Unknown"
}

// IsFolder reports whether a kind holds other items.
func IsFolder(kind store.ItemKind) bool {
	switch kind {
	case store.KindSeries, store.KindSeason, store.KindArtist, store.KindAlbum:
		return true
	}
	return false
}

// CollectionType is Jellyfin's name for a library's media type.
func CollectionType(t store.MediaType) string {
	switch t {
	case store.MediaMovies:
		return "movies"
	case store.MediaTV:
		return "tvshows"
	case store.MediaMusic:
		return "music"
	}
	return string(t)
}

// Library is a library as a client's home screen lists it.
func (m Mapper) Library(lib store.Library) BaseItem {
	return BaseItem{
		Name: lib.Name, ServerID: m.ServerID, ID: ID(lib.ID), Type: "CollectionFolder",
		CollectionType: CollectionType(lib.MediaType), IsFolder: true, LocationType: "FileSystem",
		MediaType: "Unknown", ExternalUrls: []any{}, Genres: []string{}, RemoteTrailers: []any{},
		ProviderIds: map[string]string{}, Studios: []any{}, GenreItems: []NameGUIDPair{},
		Tags: []string{}, ImageTags: map[string]string{}, BackdropImageTags: []string{},
		LockedFields: []string{}, SortName: store.SortKey(lib.Name), CanDownload: false,
		DisplayPreferencesID: ID(lib.ID), Etag: ImageTag(lib.UpdatedAt),
	}
}

// Genre is a genre as a client lists it, with a stable derived id.
func (m Mapper) Genre(name string) BaseItem {
	return BaseItem{
		Name: name, ServerID: m.ServerID, ID: DerivedID("genre", name), Type: "Genre",
		IsFolder: true, LocationType: "FileSystem", MediaType: "Unknown",
		ExternalUrls: []any{}, Genres: []string{}, RemoteTrailers: []any{}, ProviderIds: map[string]string{},
		Studios: []any{}, GenreItems: []NameGUIDPair{}, Tags: []string{}, ImageTags: map[string]string{},
		BackdropImageTags: []string{}, LockedFields: []string{}, SortName: store.SortKey(name),
	}
}

// Item is one of Cineva's items as a client sees it. The viewer's state on
// the item (Watched, PositionSecs, PlayCount, UserRating) becomes UserData.
func (m Mapper) Item(it store.Item, cx Context) BaseItem {
	b := BaseItem{
		Name: it.Title, OriginalTitle: it.OriginalTitle, ServerID: m.ServerID, ID: ID(it.ID),
		Type: Type(it.Kind), MediaType: MediaType(it.Kind), IsFolder: IsFolder(it.Kind),
		LocationType: "FileSystem", DateCreated: isoTime(it.AddedAt), Etag: ImageTag(it.UpdatedAt + it.Poster),
		SortName: it.SortTitle, Overview: it.Overview, OfficialRating: it.Certification,
		CommunityRating: it.Rating, ProductionYear: it.Year, RunTimeTicks: Ticks(it.RuntimeSecs),
		ParentID: ID(it.ParentID), Status: it.Status, Container: it.Container,
		Width: it.Width, Height: it.Height, CanDownload: true,
		ExternalUrls: []any{}, Genres: []string{}, RemoteTrailers: []any{}, ProviderIds: map[string]string{},
		Studios: []any{}, GenreItems: []NameGUIDPair{}, Tags: []string{}, ImageTags: map[string]string{},
		BackdropImageTags: []string{}, LockedFields: []string{},
	}
	if b.SortName == "" {
		b.SortName = store.SortKey(it.Title)
	}
	if it.Tagline != "" {
		b.Taglines = []string{it.Tagline}
	}
	if it.AirDate != "" {
		b.PremiereDate = it.AirDate + "T00:00:00.0000000Z"
	} else if it.Year > 0 {
		b.PremiereDate = itoa(it.Year) + "-01-01T00:00:00.0000000Z"
	}
	if it.TmdbID > 0 {
		b.ProviderIds["Tmdb"] = itoa(it.TmdbID)
	}
	if it.MBID != "" {
		switch it.Kind {
		case store.KindArtist:
			b.ProviderIds["MusicBrainzArtist"] = it.MBID
		case store.KindAlbum:
			b.ProviderIds["MusicBrainzAlbum"] = it.MBID
		case store.KindTrack:
			b.ProviderIds["MusicBrainzTrack"] = it.MBID
		}
	}
	if m.Paths && it.Path != "" && !strings.HasPrefix(it.Path, "artist://") {
		b.Path = it.Path
	}
	if it.Poster != "" {
		b.ImageTags["Primary"] = ImageTag(it.Poster)
		b.PrimaryImageAspectRatio = 2.0 / 3
		if it.Kind == store.KindEpisode {
			b.PrimaryImageAspectRatio = 16.0 / 9
		}
		if it.Kind == store.KindAlbum || it.Kind == store.KindArtist || it.Kind == store.KindTrack {
			b.PrimaryImageAspectRatio = 1
		}
	}
	if it.Backdrop != "" {
		b.BackdropImageTags = []string{ImageTag(it.Backdrop)}
		b.ImageTags["Thumb"] = ImageTag(it.Backdrop)
	}
	if len(cx.Genres) > 0 {
		b.Genres = cx.Genres
		for _, g := range cx.Genres {
			b.GenreItems = append(b.GenreItems, NameGUIDPair{Name: g, ID: DerivedID("genre", g)})
		}
	}
	for _, p := range cx.Cast {
		b.People = append(b.People, Person{Name: p.Name, ID: ID(p.ID), Role: p.Role, Type: "Actor", PrimaryImageTag: ImageTag(p.Profile)})
	}
	for _, p := range cx.Crew {
		typ := "Writer"
		if strings.EqualFold(p.Role, "Director") {
			typ = "Director"
		} else if strings.EqualFold(p.Role, "Producer") {
			typ = "Producer"
		}
		b.People = append(b.People, Person{Name: p.Name, ID: ID(p.ID), Role: p.Role, Type: typ, PrimaryImageTag: ImageTag(p.Profile)})
	}

	switch it.Kind {
	case store.KindMovie:
		b.VideoType = "VideoFile"
		if it.CollectionName != "" {
			b.Tags = []string{}
		}
	case store.KindSeries:
		b.ChildCount = it.SeasonCount
		b.RecursiveItemCount = it.EpisodeCount
		if it.Status != "" {
			b.Status = seriesStatus(it.Status)
		}
		if it.LastAirDate != "" && it.Status == "Ended" {
			b.EndDate = it.LastAirDate + "T00:00:00.0000000Z"
		}
	case store.KindSeason:
		b.IndexNumber = IntPtr(it.SeasonNum)
		if cx.Series != nil {
			b.SeriesName, b.SeriesID = cx.Series.Title, ID(cx.Series.ID)
			b.SeriesPrimaryImageTag = ImageTag(cx.Series.Poster)
			b.ParentBackdropItemID = ID(cx.Series.ID)
			if cx.Series.Backdrop != "" {
				b.ParentBackdropImageTags = []string{ImageTag(cx.Series.Backdrop)}
			}
			if it.Poster == "" && cx.Series.Poster != "" {
				b.ParentPrimaryImageItemID, b.ParentPrimaryImageTag = ID(cx.Series.ID), ImageTag(cx.Series.Poster)
			}
		}
	case store.KindEpisode:
		b.VideoType = "VideoFile"
		b.IndexNumber = IntPtr(it.EpisodeNum)
		b.ParentIndexNumber = IntPtr(it.SeasonNum)
		if cx.Season != nil {
			b.SeasonID, b.SeasonName = ID(cx.Season.ID), cx.Season.Title
		}
		if cx.Series != nil {
			b.SeriesName, b.SeriesID = cx.Series.Title, ID(cx.Series.ID)
			b.SeriesPrimaryImageTag = ImageTag(cx.Series.Poster)
			b.ParentBackdropItemID = ID(cx.Series.ID)
			if cx.Series.Backdrop != "" {
				b.ParentBackdropImageTags = []string{ImageTag(cx.Series.Backdrop)}
			}
			b.ParentLogoItemID = ""
		}
	case store.KindArtist:
		b.ChildCount = it.ChildCount
	case store.KindAlbum:
		b.ChildCount = it.ChildCount
		b.AlbumArtist = it.Artist
		b.Artists = []string{it.Artist}
		if cx.Artist != nil {
			b.AlbumArtists = []NameGUIDPair{{Name: cx.Artist.Title, ID: ID(cx.Artist.ID)}}
			b.ArtistItems = b.AlbumArtists
		} else if it.ParentID != "" {
			b.AlbumArtists = []NameGUIDPair{{Name: it.Artist, ID: ID(it.ParentID)}}
			b.ArtistItems = b.AlbumArtists
		}
	case store.KindTrack:
		b.IndexNumber = IntPtr(it.TrackNum)
		if it.DiscNum > 0 {
			b.ParentIndexNumber = IntPtr(it.DiscNum)
		}
		b.Album, b.AlbumID = it.Album, ID(it.ParentID)
		b.AlbumArtist = it.AlbumArtist
		b.Artists = []string{it.Artist}
		b.ArtistItems = []NameGUIDPair{{Name: it.Artist, ID: DerivedID("artist", strings.ToLower(it.Artist))}}
		if cx.Artist != nil {
			b.AlbumArtists = []NameGUIDPair{{Name: cx.Artist.Title, ID: ID(cx.Artist.ID)}}
			if strings.EqualFold(cx.Artist.Title, it.Artist) {
				b.ArtistItems = b.AlbumArtists
			}
		}
		if it.Poster != "" {
			b.AlbumPrimaryImageTag = ImageTag(it.Poster)
		}
		b.Bitrate = it.Bitrate * 1000
	}
	b.UserData = UserDataFor(it)
	return b
}

// UserDataFor is the viewer's state the item carries, as Jellyfin keeps
// it. A five-star rating stands in for a favourite until Cineva has
// favourites of its own.
func UserDataFor(it store.Item) *UserData {
	ud := &UserData{
		PlaybackPositionTicks: Ticks(it.PositionSecs), PlayCount: it.PlayCount,
		IsFavorite: it.UserRating == 5, Played: it.Watched, Key: ID(it.ID), ItemID: ID(it.ID),
	}
	if it.LastPlayedAt != "" {
		ud.LastPlayedDate = isoTime(it.LastPlayedAt)
	} else if it.WatchedAt != "" {
		ud.LastPlayedDate = isoTime(it.WatchedAt)
	}
	if it.PositionSecs > 0 && it.RuntimeSecs > 0 && !it.Watched {
		pct := float64(it.PositionSecs) * 100 / float64(it.RuntimeSecs)
		ud.PlayedPercentage = &pct
	}
	if it.UserRating > 0 {
		r := float64(it.UserRating) * 2 // Jellyfin's rating is out of ten
		ud.Rating = &r
	}
	return ud
}

// Streams describes a video's file as Jellyfin media streams: the video,
// its audio tracks and its subtitles. subtitleURL gives the address of a
// text subtitle as WebVTT; image subtitles can only be burned in.
func Streams(it store.Item, audio []store.AudioTrack, subs []store.SubtitleTrack, subtitleURL func(store.SubtitleTrack) string) []MediaStream {
	var out []MediaStream
	if it.VideoCodec != "" {
		out = append(out, MediaStream{
			Codec: it.VideoCodec, Type: "Video", Index: 0, Width: it.Width, Height: it.Height,
			DisplayTitle: strings.ToUpper(it.VideoCodec) + " " + itoa(it.Height) + "p", IsDefault: true,
			VideoRange: "SDR", VideoRangeType: "SDR", TimeBase: "1/90000",
		})
	}
	for _, a := range audio {
		title := strings.TrimSpace(strings.Join(nonEmpty(a.LanguageName, a.Title, strings.ToUpper(a.Codec), channelsLabel(a.Channels)), " - "))
		out = append(out, MediaStream{
			Codec: a.Codec, Language: a.Language, Type: "Audio", Index: a.StreamIndex, Title: a.Title,
			DisplayTitle: title, Channels: a.Channels, BitRate: int(a.BitRate), IsDefault: a.Default, TimeBase: "1/48000",
		})
	}
	for _, st := range subs {
		ms := MediaStream{
			Codec: subtitleCodec(st.Codec), Language: st.Language, Type: "Subtitle", Index: st.StreamIndex,
			Title: st.Title, DisplayTitle: strings.TrimSpace(strings.Join(nonEmpty(st.Language, st.Title, st.Codec), " - ")),
			IsForced: st.Forced, IsExternal: st.Source == "sidecar",
		}
		if st.TextCodec() {
			ms.IsTextSubtitleStream, ms.SupportsExternalStream = true, true
			ms.DeliveryMethod = "External"
			if subtitleURL != nil {
				ms.DeliveryURL = subtitleURL(st)
			}
		} else {
			ms.DeliveryMethod = "Encode"
		}
		if st.Source == "sidecar" {
			ms.Path = st.Path
		}
		out = append(out, ms)
	}
	if out == nil {
		out = []MediaStream{}
	}
	return out
}

// subtitleCodec is Jellyfin's name for a subtitle codec.
func subtitleCodec(codec string) string {
	switch strings.ToLower(codec) {
	case "subrip", "srt":
		return "subrip"
	case "ass", "ssa":
		return "ass"
	case "webvtt", "vtt":
		return "webvtt"
	case "hdmv_pgs_subtitle", "pgs":
		return "PGSSUB"
	case "dvd_subtitle", "vobsub":
		return "DVDSUB"
	}
	return codec
}

// Source is the file behind a video as a MediaSource, with its streams.
func Source(it store.Item, streams []MediaStream, name string) MediaSource {
	src := MediaSource{
		Protocol: "File", ID: ID(it.ID), Type: "Default", Container: it.Container, Size: it.SizeBytes,
		Name: name, RunTimeTicks: Ticks(it.RuntimeSecs), SupportsTranscoding: true, SupportsProbing: true,
		VideoType: "VideoFile", MediaStreams: streams, MediaAttachments: []any{}, Formats: []string{},
		RequiredHTTPHeaders: map[string]string{}, ETag: ImageTag(it.UpdatedAt),
	}
	if it.RuntimeSecs > 0 && it.SizeBytes > 0 {
		src.Bitrate = int(it.SizeBytes * 8 / int64(it.RuntimeSecs))
	}
	if name == "" {
		src.Name = strings.TrimSuffix(filepath.Base(it.Path), filepath.Ext(it.Path))
	}
	return src
}

// Hint is an item as a search result.
func (m Mapper) Hint(it store.Item, cx Context) SearchHint {
	h := SearchHint{
		ItemID: ID(it.ID), ID: ID(it.ID), Name: it.Title, Type: Type(it.Kind), IsFolder: IsFolder(it.Kind),
		MediaType: MediaType(it.Kind), ProductionYear: it.Year, RunTimeTicks: Ticks(it.RuntimeSecs),
		PrimaryImageTag: ImageTag(it.Poster), BackdropImageTag: ImageTag(it.Backdrop),
	}
	if it.Backdrop != "" {
		h.BackdropImageItemID = ID(it.ID)
	}
	switch it.Kind {
	case store.KindEpisode:
		h.IndexNumber, h.ParentIndexNumber = IntPtr(it.EpisodeNum), IntPtr(it.SeasonNum)
		if cx.Series != nil {
			h.Series = cx.Series.Title
		}
	case store.KindTrack:
		h.IndexNumber = IntPtr(it.TrackNum)
		h.Album, h.AlbumID, h.AlbumArtist = it.Album, ID(it.ParentID), it.AlbumArtist
		h.Artists = []string{it.Artist}
	case store.KindAlbum:
		h.AlbumArtist = it.Artist
		h.Artists = []string{it.Artist}
	}
	return h
}

// seriesStatus is Jellyfin's spelling of a series' status.
func seriesStatus(s string) string {
	switch strings.ToLower(s) {
	case "ended", "canceled", "cancelled":
		return "Ended"
	}
	return "Continuing"
}

// isoTime turns a stored "2006-01-02 15:04:05" into Jellyfin's ISO form.
func isoTime(t string) string {
	if t == "" {
		return ""
	}
	if len(t) == 19 && t[10] == ' ' {
		return t[:10] + "T" + t[11:] + ".0000000Z"
	}
	return t
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func nonEmpty(parts ...string) []string {
	var out []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			out = append(out, p)
		}
	}
	return out
}

func channelsLabel(n int) string {
	switch n {
	case 0:
		return ""
	case 1:
		return "Mono"
	case 2:
		return "Stereo"
	case 6:
		return "5.1"
	case 8:
		return "7.1"
	}
	return itoa(n) + "ch"
}
