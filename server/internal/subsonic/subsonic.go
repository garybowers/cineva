// Package subsonic is the wire format of the Subsonic API (1.16.1) with the
// OpenSubsonic additions: the response envelope in XML, JSON and JSONP, the
// error codes, the element types, and the mapping from Cineva's items onto
// them. The handlers live in the api package (server/docs/subsonic.md).
package subsonic

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/garybowers/cineva/server/internal/store"
	"github.com/garybowers/cineva/server/internal/version"
)

// Version is the Subsonic API version served.
const Version = "1.16.1"

// Type names the server in every response.
const Type = "cineva"

// Error codes, as the Subsonic specification and the OpenSubsonic
// apiKeyAuthentication extension define them.
const (
	ErrGeneric          = 0
	ErrMissingParam     = 10
	ErrClientTooOld     = 20
	ErrServerTooOld     = 30
	ErrWrongCredential  = 40
	ErrTokenUnsupported = 41
	ErrAuthUnsupported  = 42
	ErrAuthConflict     = 43
	ErrInvalidAPIKey    = 44
	ErrNotAuthorised    = 50
	ErrTrialOver        = 60
	ErrNotFound         = 70
)

// Error is a failed response's error, and the error type handlers return
// to produce one.
type Error struct {
	Code    int    `xml:"code,attr" json:"code"`
	Message string `xml:"message,attr" json:"message"`
	HelpURL string `xml:"helpUrl,attr,omitempty" json:"helpUrl,omitempty"`
}

func (e *Error) Error() string { return fmt.Sprintf("subsonic error %d: %s", e.Code, e.Message) }

// NewError makes a Subsonic error with a code and message.
func NewError(code int, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Response is the subsonic-response envelope. Exactly one payload field is
// set on a successful response; the rest are omitted.
type Response struct {
	XMLName       xml.Name `xml:"http://subsonic.org/restapi subsonic-response" json:"-"`
	Status        string   `xml:"status,attr" json:"status"`
	Version       string   `xml:"version,attr" json:"version"`
	Type          string   `xml:"type,attr" json:"type"`
	ServerVersion string   `xml:"serverVersion,attr" json:"serverVersion"`
	OpenSubsonic  bool     `xml:"openSubsonic,attr" json:"openSubsonic"`

	Error *Error `xml:"error,omitempty" json:"error,omitempty"`

	License                *License             `xml:"license,omitempty" json:"license,omitempty"`
	OpenSubsonicExtensions []Extension          `xml:"openSubsonicExtensions,omitempty" json:"openSubsonicExtensions,omitempty"`
	MusicFolders           *MusicFolders        `xml:"musicFolders,omitempty" json:"musicFolders,omitempty"`
	Indexes                *Indexes             `xml:"indexes,omitempty" json:"indexes,omitempty"`
	Directory              *Directory           `xml:"directory,omitempty" json:"directory,omitempty"`
	Genres                 *Genres              `xml:"genres,omitempty" json:"genres,omitempty"`
	Artists                *ArtistsID3          `xml:"artists,omitempty" json:"artists,omitempty"`
	Artist                 *ArtistWithAlbumsID3 `xml:"artist,omitempty" json:"artist,omitempty"`
	Album                  *AlbumWithSongsID3   `xml:"album,omitempty" json:"album,omitempty"`
	Song                   *Child               `xml:"song,omitempty" json:"song,omitempty"`
	ArtistInfo             *ArtistInfo          `xml:"artistInfo,omitempty" json:"artistInfo,omitempty"`
	ArtistInfo2            *ArtistInfo          `xml:"artistInfo2,omitempty" json:"artistInfo2,omitempty"`
	AlbumInfo              *AlbumInfo           `xml:"albumInfo,omitempty" json:"albumInfo,omitempty"`
	AlbumList              *AlbumList           `xml:"albumList,omitempty" json:"albumList,omitempty"`
	AlbumList2             *AlbumList2          `xml:"albumList2,omitempty" json:"albumList2,omitempty"`
	RandomSongs            *Songs               `xml:"randomSongs,omitempty" json:"randomSongs,omitempty"`
	SongsByGenre           *Songs               `xml:"songsByGenre,omitempty" json:"songsByGenre,omitempty"`
	TopSongs               *TopSongs            `xml:"topSongs,omitempty" json:"topSongs,omitempty"`
	NowPlaying             *NowPlaying          `xml:"nowPlaying,omitempty" json:"nowPlaying,omitempty"`
	Starred                *Starred             `xml:"starred,omitempty" json:"starred,omitempty"`
	Starred2               *Starred2            `xml:"starred2,omitempty" json:"starred2,omitempty"`
	SearchResult2          *SearchResult2       `xml:"searchResult2,omitempty" json:"searchResult2,omitempty"`
	SearchResult3          *SearchResult3       `xml:"searchResult3,omitempty" json:"searchResult3,omitempty"`
	Playlists              *Playlists           `xml:"playlists,omitempty" json:"playlists,omitempty"`
	Playlist               *PlaylistWithSongs   `xml:"playlist,omitempty" json:"playlist,omitempty"`
	Lyrics                 *Lyrics              `xml:"lyrics,omitempty" json:"lyrics,omitempty"`
	LyricsList             *LyricsList          `xml:"lyricsList,omitempty" json:"lyricsList,omitempty"`
	User                   *User                `xml:"user,omitempty" json:"user,omitempty"`
	Users                  *Users               `xml:"users,omitempty" json:"users,omitempty"`
	ScanStatus             *ScanStatus          `xml:"scanStatus,omitempty" json:"scanStatus,omitempty"`
	PlayQueue              *PlayQueue           `xml:"playQueue,omitempty" json:"playQueue,omitempty"`
	Bookmarks              *Empty               `xml:"bookmarks,omitempty" json:"bookmarks,omitempty"`
	InternetRadioStations  *Empty               `xml:"internetRadioStations,omitempty" json:"internetRadioStations,omitempty"`
	Podcasts               *Empty               `xml:"podcasts,omitempty" json:"podcasts,omitempty"`
	NewestPodcasts         *Empty               `xml:"newestPodcasts,omitempty" json:"newestPodcasts,omitempty"`
	Videos                 *Empty               `xml:"videos,omitempty" json:"videos,omitempty"`
	Shares                 *Empty               `xml:"shares,omitempty" json:"shares,omitempty"`
	ChatMessages           *Empty               `xml:"chatMessages,omitempty" json:"chatMessages,omitempty"`
}

// OK is a successful envelope with no payload yet.
func OK() *Response {
	return &Response{Status: "ok", Version: Version, Type: Type, ServerVersion: version.Version, OpenSubsonic: true}
}

// Failed is a failed envelope carrying the error.
func Failed(e *Error) *Response {
	r := OK()
	r.Status = "failed"
	r.Error = e
	return r
}

// Empty is an element with nothing in it: what a feature Cineva does not
// have answers with, so a client that lists bookmarks or podcasts sees
// none rather than an error.
type Empty struct{}

type License struct {
	Valid          bool   `xml:"valid,attr" json:"valid"`
	Email          string `xml:"email,attr,omitempty" json:"email,omitempty"`
	LicenseExpires string `xml:"licenseExpires,attr,omitempty" json:"licenseExpires,omitempty"`
}

// Extension is one OpenSubsonic extension the server supports.
type Extension struct {
	Name     string `xml:"name,attr" json:"name"`
	Versions []int  `xml:"versions" json:"versions"`
}

type MusicFolders struct {
	MusicFolder []MusicFolder `xml:"musicFolder" json:"musicFolder"`
}

type MusicFolder struct {
	ID   string `xml:"id,attr" json:"id"`
	Name string `xml:"name,attr" json:"name"`
}

// Artist is an artist in the folder-style calls (getIndexes, search2,
// getStarred); ArtistID3 the same in the ID3 calls. They share a shape.
type Artist struct {
	ID             string `xml:"id,attr" json:"id"`
	Name           string `xml:"name,attr" json:"name"`
	CoverArt       string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	ArtistImageURL string `xml:"artistImageUrl,attr,omitempty" json:"artistImageUrl,omitempty"`
	AlbumCount     int    `xml:"albumCount,attr" json:"albumCount"`
	Starred        string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	UserRating     int    `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	MusicBrainzID  string `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	SortName       string `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
}

type ArtistID3 = Artist

type Index struct {
	Name   string   `xml:"name,attr" json:"name"`
	Artist []Artist `xml:"artist" json:"artist"`
}

type Indexes struct {
	LastModified    int64   `xml:"lastModified,attr" json:"lastModified"`
	IgnoredArticles string  `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []Index `xml:"index" json:"index"`
}

type ArtistsID3 struct {
	IgnoredArticles string  `xml:"ignoredArticles,attr" json:"ignoredArticles"`
	Index           []Index `xml:"index" json:"index"`
}

type ArtistWithAlbumsID3 struct {
	ArtistID3
	Album []AlbumID3 `xml:"album" json:"album"`
}

type AlbumID3 struct {
	ID            string `xml:"id,attr" json:"id"`
	Name          string `xml:"name,attr" json:"name"`
	Artist        string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	ArtistID      string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	CoverArt      string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	SongCount     int    `xml:"songCount,attr" json:"songCount"`
	Duration      int    `xml:"duration,attr" json:"duration"`
	PlayCount     int    `xml:"playCount,attr,omitempty" json:"playCount,omitempty"`
	Played        string `xml:"played,attr,omitempty" json:"played,omitempty"`
	Created       string `xml:"created,attr" json:"created"`
	Starred       string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	Year          int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre         string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	UserRating    int    `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	MusicBrainzID string `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	IsCompilation bool   `xml:"isCompilation,attr,omitempty" json:"isCompilation,omitempty"`
	SortName      string `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
}

type AlbumWithSongsID3 struct {
	AlbumID3
	Song []Child `xml:"song" json:"song"`
}

// Child is a song, or an album or artist as a directory entry.
type Child struct {
	ID                    string `xml:"id,attr" json:"id"`
	Parent                string `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	IsDir                 bool   `xml:"isDir,attr" json:"isDir"`
	Title                 string `xml:"title,attr" json:"title"`
	Album                 string `xml:"album,attr,omitempty" json:"album,omitempty"`
	Artist                string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	Track                 int    `xml:"track,attr,omitempty" json:"track,omitempty"`
	Year                  int    `xml:"year,attr,omitempty" json:"year,omitempty"`
	Genre                 string `xml:"genre,attr,omitempty" json:"genre,omitempty"`
	CoverArt              string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
	Size                  int64  `xml:"size,attr,omitempty" json:"size,omitempty"`
	ContentType           string `xml:"contentType,attr,omitempty" json:"contentType,omitempty"`
	Suffix                string `xml:"suffix,attr,omitempty" json:"suffix,omitempty"`
	TranscodedContentType string `xml:"transcodedContentType,attr,omitempty" json:"transcodedContentType,omitempty"`
	TranscodedSuffix      string `xml:"transcodedSuffix,attr,omitempty" json:"transcodedSuffix,omitempty"`
	Duration              int    `xml:"duration,attr,omitempty" json:"duration,omitempty"`
	BitRate               int    `xml:"bitRate,attr,omitempty" json:"bitRate,omitempty"`
	SamplingRate          int    `xml:"samplingRate,attr,omitempty" json:"samplingRate,omitempty"`
	BitDepth              int    `xml:"bitDepth,attr,omitempty" json:"bitDepth,omitempty"`
	ChannelCount          int    `xml:"channelCount,attr,omitempty" json:"channelCount,omitempty"`
	Path                  string `xml:"path,attr,omitempty" json:"path,omitempty"`
	PlayCount             int    `xml:"playCount,attr,omitempty" json:"playCount,omitempty"`
	Played                string `xml:"played,attr,omitempty" json:"played,omitempty"`
	DiscNumber            int    `xml:"discNumber,attr,omitempty" json:"discNumber,omitempty"`
	Created               string `xml:"created,attr,omitempty" json:"created,omitempty"`
	Starred               string `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	AlbumID               string `xml:"albumId,attr,omitempty" json:"albumId,omitempty"`
	ArtistID              string `xml:"artistId,attr,omitempty" json:"artistId,omitempty"`
	Type                  string `xml:"type,attr,omitempty" json:"type,omitempty"`
	MediaType             string `xml:"mediaType,attr,omitempty" json:"mediaType,omitempty"`
	UserRating            int    `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	MusicBrainzID         string `xml:"musicBrainzId,attr,omitempty" json:"musicBrainzId,omitempty"`
	SortName              string `xml:"sortName,attr,omitempty" json:"sortName,omitempty"`
}

type Directory struct {
	ID         string  `xml:"id,attr" json:"id"`
	Parent     string  `xml:"parent,attr,omitempty" json:"parent,omitempty"`
	Name       string  `xml:"name,attr" json:"name"`
	Starred    string  `xml:"starred,attr,omitempty" json:"starred,omitempty"`
	UserRating int     `xml:"userRating,attr,omitempty" json:"userRating,omitempty"`
	PlayCount  int     `xml:"playCount,attr,omitempty" json:"playCount,omitempty"`
	Child      []Child `xml:"child" json:"child"`
}

type Genres struct {
	Genre []Genre `xml:"genre" json:"genre"`
}

type Genre struct {
	SongCount  int    `xml:"songCount,attr" json:"songCount"`
	AlbumCount int    `xml:"albumCount,attr" json:"albumCount"`
	Value      string `xml:",chardata" json:"value"`
}

type ArtistInfo struct {
	Biography      string `xml:"biography,omitempty" json:"biography,omitempty"`
	MusicBrainzID  string `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
	LastFmURL      string `xml:"lastFmUrl,omitempty" json:"lastFmUrl,omitempty"`
	SmallImageURL  string `xml:"smallImageUrl,omitempty" json:"smallImageUrl,omitempty"`
	MediumImageURL string `xml:"mediumImageUrl,omitempty" json:"mediumImageUrl,omitempty"`
	LargeImageURL  string `xml:"largeImageUrl,omitempty" json:"largeImageUrl,omitempty"`
}

type AlbumInfo struct {
	Notes          string `xml:"notes,omitempty" json:"notes,omitempty"`
	MusicBrainzID  string `xml:"musicBrainzId,omitempty" json:"musicBrainzId,omitempty"`
	LastFmURL      string `xml:"lastFmUrl,omitempty" json:"lastFmUrl,omitempty"`
	SmallImageURL  string `xml:"smallImageUrl,omitempty" json:"smallImageUrl,omitempty"`
	MediumImageURL string `xml:"mediumImageUrl,omitempty" json:"mediumImageUrl,omitempty"`
	LargeImageURL  string `xml:"largeImageUrl,omitempty" json:"largeImageUrl,omitempty"`
}

type AlbumList struct {
	Album []Child `xml:"album" json:"album"`
}

type AlbumList2 struct {
	Album []AlbumID3 `xml:"album" json:"album"`
}

type Songs struct {
	Song []Child `xml:"song" json:"song"`
}

type TopSongs = Songs

type NowPlaying struct {
	Entry []NowPlayingEntry `xml:"entry" json:"entry"`
}

type NowPlayingEntry struct {
	Child
	Username   string `xml:"username,attr" json:"username"`
	MinutesAgo int    `xml:"minutesAgo,attr" json:"minutesAgo"`
	PlayerID   int    `xml:"playerId,attr" json:"playerId"`
	PlayerName string `xml:"playerName,attr,omitempty" json:"playerName,omitempty"`
}

type Starred struct {
	Artist []Artist `xml:"artist" json:"artist"`
	Album  []Child  `xml:"album" json:"album"`
	Song   []Child  `xml:"song" json:"song"`
}

type Starred2 struct {
	Artist []ArtistID3 `xml:"artist" json:"artist"`
	Album  []AlbumID3  `xml:"album" json:"album"`
	Song   []Child     `xml:"song" json:"song"`
}

type SearchResult2 = Starred

type SearchResult3 = Starred2

type Playlists struct {
	Playlist []Playlist `xml:"playlist" json:"playlist"`
}

type Playlist struct {
	ID        string `xml:"id,attr" json:"id"`
	Name      string `xml:"name,attr" json:"name"`
	Comment   string `xml:"comment,attr,omitempty" json:"comment,omitempty"`
	Owner     string `xml:"owner,attr,omitempty" json:"owner,omitempty"`
	Public    bool   `xml:"public,attr" json:"public"`
	SongCount int    `xml:"songCount,attr" json:"songCount"`
	Duration  int    `xml:"duration,attr" json:"duration"`
	Created   string `xml:"created,attr" json:"created"`
	Changed   string `xml:"changed,attr" json:"changed"`
	CoverArt  string `xml:"coverArt,attr,omitempty" json:"coverArt,omitempty"`
}

type PlaylistWithSongs struct {
	Playlist
	Entry []Child `xml:"entry" json:"entry"`
}

type Lyrics struct {
	Artist string `xml:"artist,attr,omitempty" json:"artist,omitempty"`
	Title  string `xml:"title,attr,omitempty" json:"title,omitempty"`
	Value  string `xml:",chardata" json:"value"`
}

type LyricsList struct {
	StructuredLyrics []StructuredLyrics `xml:"structuredLyrics" json:"structuredLyrics"`
}

type StructuredLyrics struct {
	DisplayArtist string `xml:"displayArtist,attr,omitempty" json:"displayArtist,omitempty"`
	DisplayTitle  string `xml:"displayTitle,attr,omitempty" json:"displayTitle,omitempty"`
	Lang          string `xml:"lang,attr" json:"lang"`
	Offset        int    `xml:"offset,attr" json:"offset"`
	Synced        bool   `xml:"synced,attr" json:"synced"`
	Line          []Line `xml:"line" json:"line"`
}

type Line struct {
	Start *int64 `xml:"start,attr,omitempty" json:"start,omitempty"`
	Value string `xml:",chardata" json:"value"`
}

type User struct {
	Username            string   `xml:"username,attr" json:"username"`
	Email               string   `xml:"email,attr,omitempty" json:"email,omitempty"`
	ScrobblingEnabled   bool     `xml:"scrobblingEnabled,attr" json:"scrobblingEnabled"`
	AdminRole           bool     `xml:"adminRole,attr" json:"adminRole"`
	SettingsRole        bool     `xml:"settingsRole,attr" json:"settingsRole"`
	DownloadRole        bool     `xml:"downloadRole,attr" json:"downloadRole"`
	UploadRole          bool     `xml:"uploadRole,attr" json:"uploadRole"`
	PlaylistRole        bool     `xml:"playlistRole,attr" json:"playlistRole"`
	CoverArtRole        bool     `xml:"coverArtRole,attr" json:"coverArtRole"`
	CommentRole         bool     `xml:"commentRole,attr" json:"commentRole"`
	PodcastRole         bool     `xml:"podcastRole,attr" json:"podcastRole"`
	StreamRole          bool     `xml:"streamRole,attr" json:"streamRole"`
	JukeboxRole         bool     `xml:"jukeboxRole,attr" json:"jukeboxRole"`
	ShareRole           bool     `xml:"shareRole,attr" json:"shareRole"`
	VideoConversionRole bool     `xml:"videoConversionRole,attr" json:"videoConversionRole"`
	Folder              []string `xml:"folder" json:"folder"`
}

type Users struct {
	User []User `xml:"user" json:"user"`
}

type ScanStatus struct {
	Scanning bool  `xml:"scanning,attr" json:"scanning"`
	Count    int64 `xml:"count,attr" json:"count"`
}

type PlayQueue struct {
	Current   string  `xml:"current,attr,omitempty" json:"current,omitempty"`
	Position  int64   `xml:"position,attr,omitempty" json:"position,omitempty"`
	Username  string  `xml:"username,attr" json:"username"`
	Changed   string  `xml:"changed,attr" json:"changed"`
	ChangedBy string  `xml:"changedBy,attr" json:"changedBy"`
	Entry     []Child `xml:"entry" json:"entry"`
}

// ---- Encoding ----

// Format is how a client asked for its response: "xml" (the default),
// "json" or "jsonp" (with a callback).
func Format(params url.Values) (format, callback string) {
	switch strings.ToLower(params.Get("f")) {
	case "json":
		return "json", ""
	case "jsonp":
		cb := params.Get("callback")
		if cb == "" {
			cb = "callback"
		}
		return "jsonp", cb
	}
	return "xml", ""
}

// Encode writes the envelope in the format asked for and returns the
// content type to send with it.
func Encode(w io.Writer, format, callback string, r *Response) (string, error) {
	switch format {
	case "json", "jsonp":
		body, err := json.Marshal(map[string]*Response{"subsonic-response": r})
		if err != nil {
			return "", fmt.Errorf("encode subsonic json: %w", err)
		}
		if format == "jsonp" {
			_, err = fmt.Fprintf(w, "%s(%s);", callback, body)
			return "application/javascript; charset=utf-8", err
		}
		_, err = w.Write(body)
		return "application/json; charset=utf-8", err
	}
	if _, err := io.WriteString(w, xml.Header); err != nil {
		return "", err
	}
	if err := xml.NewEncoder(w).Encode(r); err != nil {
		return "", fmt.Errorf("encode subsonic xml: %w", err)
	}
	return "text/xml; charset=utf-8", nil
}

// ---- Mapping from Cineva's items ----

// Time turns a stored time ("2006-01-02 15:04:05", UTC) into the ISO 8601
// form the API uses; an empty or unparseable one stays empty.
func Time(stored string) string {
	if stored == "" {
		return ""
	}
	for _, layout := range []string{time.DateTime, time.RFC3339} {
		if t, err := time.Parse(layout, stored); err == nil {
			return t.UTC().Format(time.RFC3339)
		}
	}
	return ""
}

// contentTypes by container (the file extension the scanner records).
var contentTypes = map[string]string{
	"flac": "audio/flac", "mp3": "audio/mpeg", "m4a": "audio/mp4", "aac": "audio/aac",
	"ogg": "audio/ogg", "opus": "audio/ogg", "wav": "audio/wav", "wma": "audio/x-ms-wma",
}

// ContentType is the MIME type of a container, "application/octet-stream"
// for one not known.
func ContentType(container string) string {
	if ct, ok := contentTypes[strings.ToLower(container)]; ok {
		return ct
	}
	return "application/octet-stream"
}

// starred is the attribute value for an item the user starred: the API
// wants a time, and the rating has none to give, so it is the item's own.
func starred(it store.Item) string {
	if it.UserRating == 5 {
		return Time(it.AddedAt)
	}
	return ""
}

// SongFrom is a track as a song.
func SongFrom(t store.Item) Child {
	path := t.Title + "." + t.Container
	if t.Album != "" {
		path = t.Album + "/" + path
	}
	if t.AlbumArtist != "" {
		path = t.AlbumArtist + "/" + path
	}
	return Child{
		ID: t.ID, Parent: t.ParentID, IsDir: false, Title: t.Title, Album: t.Album, Artist: t.Artist,
		Track: t.TrackNum, Year: t.Year, CoverArt: coverOf(t), Size: t.SizeBytes,
		ContentType: ContentType(t.Container), Suffix: strings.ToLower(t.Container),
		Duration: t.RuntimeSecs, BitRate: t.Bitrate, SamplingRate: t.SampleRate, BitDepth: t.BitDepth,
		ChannelCount: t.Channels, Path: path, PlayCount: t.PlayCount, Played: Time(t.LastPlayedAt),
		DiscNumber: t.DiscNum, Created: Time(t.AddedAt), Starred: starred(t), AlbumID: t.ParentID,
		Type: "music", MediaType: "song", UserRating: t.UserRating, MusicBrainzID: t.MBID, SortName: t.SortTitle,
	}
}

// coverOf is the cover art id for an item: a track shares its album's, so
// a client caches one picture per album.
func coverOf(it store.Item) string {
	if it.Poster == "" {
		return ""
	}
	if it.Kind == store.KindTrack && it.ParentID != "" {
		return it.ParentID
	}
	return it.ID
}

// AlbumFrom is an album in the ID3 calls. duration is the sum of its
// tracks' running times, 0 when the caller did not have it.
func AlbumFrom(a store.Item, duration int) AlbumID3 {
	return AlbumID3{
		ID: a.ID, Name: a.Title, Artist: a.Artist, ArtistID: a.ParentID, CoverArt: coverOf(a),
		SongCount: a.ChildCount, Duration: duration, PlayCount: a.PlayCount, Played: Time(a.LastPlayedAt),
		Created: Time(a.AddedAt), Starred: starred(a), Year: a.Year, UserRating: a.UserRating,
		MusicBrainzID: a.MBID, IsCompilation: a.Compilation, SortName: a.SortTitle,
	}
}

// AlbumChild is an album as a directory entry, for the folder-style calls.
func AlbumChild(a store.Item) Child {
	return Child{
		ID: a.ID, Parent: a.ParentID, IsDir: true, Title: a.Title, Album: a.Title, Artist: a.Artist,
		Year: a.Year, CoverArt: coverOf(a), Created: Time(a.AddedAt), PlayCount: a.PlayCount,
		Played: Time(a.LastPlayedAt), Starred: starred(a), ArtistID: a.ParentID,
		MediaType: "album", UserRating: a.UserRating, MusicBrainzID: a.MBID, SortName: a.SortTitle,
	}
}

// ArtistFrom is an artist; imageURL is the absolute URL of their picture
// when they have one.
func ArtistFrom(a store.Item, imageURL string) Artist {
	return Artist{
		ID: a.ID, Name: a.Title, CoverArt: coverOf(a), ArtistImageURL: imageURL, AlbumCount: a.ChildCount,
		Starred: starred(a), UserRating: a.UserRating, MusicBrainzID: a.MBID, SortName: a.SortTitle,
	}
}

// ArtistChild is an artist as a directory entry.
func ArtistChild(a store.Item) Child {
	return Child{ID: a.ID, IsDir: true, Title: a.Title, Artist: a.Title, CoverArt: coverOf(a),
		Created: Time(a.AddedAt), MediaType: "artist", UserRating: a.UserRating, SortName: a.SortTitle}
}

// IgnoredArticles is what the index leaves off the front of a name, as
// the API reports it.
const IgnoredArticles = "The An A"

// IndexArtists groups artists by the first letter of their sort name,
// with everything that is not a letter under "#", in index order.
func IndexArtists(artists []Artist) []Index {
	by := map[string][]Artist{}
	var order []string
	for _, a := range artists {
		l := "#"
		if r := firstRune(a.SortName); unicode.IsLetter(r) {
			l = strings.ToUpper(string(r))
		}
		if _, ok := by[l]; !ok {
			order = append(order, l)
		}
		by[l] = append(by[l], a)
	}
	sortStrings(order)
	out := make([]Index, 0, len(order))
	for _, l := range order {
		out = append(out, Index{Name: l, Artist: by[l]})
	}
	return out
}

func firstRune(s string) rune {
	for _, r := range s {
		return r
	}
	return 0
}

// sortStrings orders index names with "#" last, as the API's clients show
// them.
func sortStrings(ss []string) {
	for i := 1; i < len(ss); i++ {
		for j := i; j > 0 && less(ss[j], ss[j-1]); j-- {
			ss[j], ss[j-1] = ss[j-1], ss[j]
		}
	}
}

func less(a, b string) bool {
	if a == "#" {
		return false
	}
	if b == "#" {
		return true
	}
	return a < b
}

// PlaylistFrom is a playlist's summary; owner is its user's name.
func PlaylistFrom(p store.Playlist, owner string) Playlist {
	pl := Playlist{
		ID: "pl-" + p.ID, Name: p.Name, Owner: owner, SongCount: p.TrackCount, Duration: p.DurationSecs,
		Created: Time(p.CreatedAt), Changed: Time(p.UpdatedAt),
	}
	if p.Smart != nil {
		pl.Comment = "Smart playlist, read-only here"
	}
	if len(p.Covers) > 0 {
		pl.CoverArt = "pl-" + p.ID
	}
	return pl
}

// PlaylistID is the Cineva id behind a playlist id, and whether it was one.
func PlaylistID(id string) (string, bool) {
	return strings.CutPrefix(id, "pl-")
}

// lrcLine matches a timed lyrics line, "[mm:ss.xx] words".
var lrcLine = regexp.MustCompile(`^\[(\d{1,3}):(\d{2})(?:[.:](\d{1,3}))?\]\s?(.*)$`)

// LyricsFrom turns stored lyrics into the OpenSubsonic structured form:
// timed lines carry their start in milliseconds, plain ones do not.
func LyricsFrom(l store.Lyrics, artist, title string) StructuredLyrics {
	out := StructuredLyrics{DisplayArtist: artist, DisplayTitle: title, Lang: "xxx", Synced: l.Synced}
	for _, raw := range strings.Split(l.Text, "\n") {
		raw = strings.TrimRight(raw, "\r")
		if !l.Synced {
			out.Line = append(out.Line, Line{Value: raw})
			continue
		}
		m := lrcLine.FindStringSubmatch(raw)
		if m == nil {
			// Tags such as [ar:...] and blank lines carry no time.
			if strings.HasPrefix(raw, "[") || strings.TrimSpace(raw) == "" {
				continue
			}
			out.Line = append(out.Line, Line{Value: raw})
			continue
		}
		mins, _ := strconv.Atoi(m[1])
		secs, _ := strconv.Atoi(m[2])
		frac := 0
		if m[3] != "" {
			f := m[3]
			for len(f) < 3 {
				f += "0"
			}
			frac, _ = strconv.Atoi(f[:3])
		}
		start := int64(mins*60000 + secs*1000 + frac)
		out.Line = append(out.Line, Line{Start: &start, Value: m[4]})
	}
	return out
}

// PlainLyrics is the lyrics as one text for the older getLyrics call,
// with any timings stripped.
func PlainLyrics(l store.Lyrics) string {
	if !l.Synced {
		return l.Text
	}
	var b strings.Builder
	for _, raw := range strings.Split(l.Text, "\n") {
		if m := lrcLine.FindStringSubmatch(strings.TrimRight(raw, "\r")); m != nil {
			b.WriteString(m[4])
			b.WriteByte('\n')
		}
	}
	return strings.TrimSpace(b.String())
}
