// Package jellyfin holds the types and mappings of the Jellyfin-compatible
// API (server/docs/jellyfin.md): the JSON shapes Jellyfin's clients expect,
// named exactly as its OpenAPI spec names them, and the translation of
// Cineva's items, users and viewer state into them. The handlers live in
// internal/api, where they reuse the server's playback and session code.
package jellyfin

import (
	"crypto/sha1"
	"encoding/hex"
	"strconv"
	"strings"
)

// Version is the Jellyfin version the server claims, so clients use the
// current endpoints; ProductName is what they show.
const (
	Version     = "10.10.7"
	ProductName = "Cineva"
)

// TicksPerSecond is Jellyfin's unit of time: 100 nanoseconds.
const TicksPerSecond = 10_000_000

// Ticks converts seconds to Jellyfin ticks.
func Ticks(secs int) int64 { return int64(secs) * TicksPerSecond }

// Secs converts Jellyfin ticks to whole seconds.
func Secs(ticks int64) int { return int(ticks / TicksPerSecond) }

// ID is a Cineva id as Jellyfin's clients expect it: a UUID is written
// without its dashes (32 hex digits); any other id passes through.
func ID(id string) string {
	if len(id) == 36 && id[8] == '-' && id[13] == '-' && id[18] == '-' && id[23] == '-' {
		return strings.ReplaceAll(id, "-", "")
	}
	return id
}

// ParseID puts the dashes back into a 32-digit id; any other id passes
// through, so an id that was never a UUID round-trips too.
func ParseID(id string) string {
	id = strings.TrimSpace(id)
	if len(id) != 32 {
		return id
	}
	for i := 0; i < len(id); i++ {
		c := id[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return id
		}
	}
	id = strings.ToLower(id)
	return id[:8] + "-" + id[8:12] + "-" + id[12:16] + "-" + id[16:20] + "-" + id[20:]
}

// DerivedID is a stable 32-digit id for something that has no row of its
// own, such as a genre: the same name always gives the same id.
func DerivedID(kind, name string) string {
	sum := sha1.Sum([]byte(kind + ":" + name))
	return hex.EncodeToString(sum[:16])
}

// ImageTag is the tag a client sends back for an image, so a changed
// picture gets a new URL: a hash of the artwork path, which already carries
// a hash of the image.
func ImageTag(path string) string {
	if path == "" {
		return ""
	}
	sum := sha1.Sum([]byte(path))
	return hex.EncodeToString(sum[:8])
}

// Auth is what a client says about itself in its Authorization header:
// `MediaBrowser Client="Jellyfin Android", Device="Pixel", DeviceId="...",
// Version="2.6", Token="..."` (older clients call the header
// X-Emby-Authorization and the scheme Emby).
type Auth struct {
	Client   string
	Device   string
	DeviceID string
	Version  string
	Token    string
}

// ParseAuth reads the header's key="value" pairs, in any order, with or
// without the scheme word.
func ParseAuth(header string) Auth {
	var a Auth
	h := strings.TrimSpace(header)
	for _, scheme := range []string{"MediaBrowser ", "Emby "} {
		if len(h) > len(scheme) && strings.EqualFold(h[:len(scheme)], scheme) {
			h = h[len(scheme):]
			break
		}
	}
	for _, part := range splitQuoted(h) {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"`)
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "client":
			a.Client = v
		case "device":
			a.Device = v
		case "deviceid":
			a.DeviceID = v
		case "version":
			a.Version = v
		case "token":
			a.Token = v
		}
	}
	return a
}

// splitQuoted splits on commas outside double quotes: a device name may
// hold a comma.
func splitQuoted(s string) []string {
	var out []string
	var cur strings.Builder
	quoted := false
	for _, r := range s {
		switch {
		case r == '"':
			quoted = !quoted
			cur.WriteRune(r)
		case r == ',' && !quoted:
			out = append(out, cur.String())
			cur.Reset()
		default:
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		out = append(out, cur.String())
	}
	return out
}

// ---- Types, named as Jellyfin's OpenAPI spec names them ----

// SystemInfoPublic answers /System/Info/Public: what a client needs before
// signing in.
type SystemInfoPublic struct {
	LocalAddress           string `json:"LocalAddress"`
	ServerName             string `json:"ServerName"`
	Version                string `json:"Version"`
	ProductName            string `json:"ProductName"`
	OperatingSystem        string `json:"OperatingSystem"`
	ID                     string `json:"Id"`
	StartupWizardCompleted bool   `json:"StartupWizardCompleted"`
}

// SystemInfo answers /System/Info for a signed-in client.
type SystemInfo struct {
	SystemInfoPublic
	OperatingSystemDisplayName string `json:"OperatingSystemDisplayName"`
	HasPendingRestart          bool   `json:"HasPendingRestart"`
	IsShuttingDown             bool   `json:"IsShuttingDown"`
	SupportsLibraryMonitor     bool   `json:"SupportsLibraryMonitor"`
	WebSocketPortNumber        int    `json:"WebSocketPortNumber"`
	CanSelfRestart             bool   `json:"CanSelfRestart"`
	CanLaunchWebBrowser        bool   `json:"CanLaunchWebBrowser"`
	ProgramDataPath            string `json:"ProgramDataPath"`
	WebPath                    string `json:"WebPath"`
	ItemsByNamePath            string `json:"ItemsByNamePath"`
	CachePath                  string `json:"CachePath"`
	LogPath                    string `json:"LogPath"`
	InternalMetadataPath       string `json:"InternalMetadataPath"`
	TranscodingTempPath        string `json:"TranscodingTempPath"`
	HasUpdateAvailable         bool   `json:"HasUpdateAvailable"`
	EncoderLocation            string `json:"EncoderLocation"`
	SystemArchitecture         string `json:"SystemArchitecture"`
	CompletedInstallations     []any  `json:"CompletedInstallations"`
}

// EndpointInfo answers /System/Endpoint.
type EndpointInfo struct {
	IsLocal     bool `json:"IsLocal"`
	IsInNetwork bool `json:"IsInNetwork"`
}

// BrandingOptions answers /Branding/Configuration.
type BrandingOptions struct {
	LoginDisclaimer     string `json:"LoginDisclaimer"`
	CustomCSS           string `json:"CustomCss"`
	SplashscreenEnabled bool   `json:"SplashscreenEnabled"`
}

// User is a UserDto: an account as a client shows it.
type User struct {
	Name                      string            `json:"Name"`
	ServerID                  string            `json:"ServerId"`
	ID                        string            `json:"Id"`
	PrimaryImageTag           string            `json:"PrimaryImageTag,omitempty"`
	HasPassword               bool              `json:"HasPassword"`
	HasConfiguredPassword     bool              `json:"HasConfiguredPassword"`
	HasConfiguredEasyPassword bool              `json:"HasConfiguredEasyPassword"`
	EnableAutoLogin           bool              `json:"EnableAutoLogin"`
	LastLoginDate             string            `json:"LastLoginDate,omitempty"`
	LastActivityDate          string            `json:"LastActivityDate,omitempty"`
	Configuration             UserConfiguration `json:"Configuration"`
	Policy                    UserPolicy        `json:"Policy"`
}

// UserConfiguration is the per-user settings Jellyfin clients read; fixed
// here to Jellyfin's defaults, since Cineva keeps its own preferences.
type UserConfiguration struct {
	PlayDefaultAudioTrack      bool     `json:"PlayDefaultAudioTrack"`
	SubtitleLanguagePreference string   `json:"SubtitleLanguagePreference"`
	DisplayMissingEpisodes     bool     `json:"DisplayMissingEpisodes"`
	GroupedFolders             []string `json:"GroupedFolders"`
	SubtitleMode               string   `json:"SubtitleMode"`
	DisplayCollectionsView     bool     `json:"DisplayCollectionsView"`
	EnableLocalPassword        bool     `json:"EnableLocalPassword"`
	OrderedViews               []string `json:"OrderedViews"`
	LatestItemsExcludes        []string `json:"LatestItemsExcludes"`
	MyMediaExcludes            []string `json:"MyMediaExcludes"`
	HidePlayedInLatest         bool     `json:"HidePlayedInLatest"`
	RememberAudioSelections    bool     `json:"RememberAudioSelections"`
	RememberSubtitleSelections bool     `json:"RememberSubtitleSelections"`
	EnableNextEpisodeAutoPlay  bool     `json:"EnableNextEpisodeAutoPlay"`
	CastReceiverID             string   `json:"CastReceiverId"`
}

// DefaultConfiguration is Jellyfin's own defaults.
func DefaultConfiguration() UserConfiguration {
	return UserConfiguration{
		PlayDefaultAudioTrack: true, SubtitleMode: "Default",
		GroupedFolders: []string{}, OrderedViews: []string{}, LatestItemsExcludes: []string{}, MyMediaExcludes: []string{},
		HidePlayedInLatest: true, RememberAudioSelections: true, RememberSubtitleSelections: true,
		EnableNextEpisodeAutoPlay: true,
	}
}

// UserPolicy says what the user may do. Everything a viewer needs is on;
// administration stays in Cineva's own admin UI.
type UserPolicy struct {
	IsAdministrator                  bool     `json:"IsAdministrator"`
	IsHidden                         bool     `json:"IsHidden"`
	IsDisabled                       bool     `json:"IsDisabled"`
	MaxParentalRating                *int     `json:"MaxParentalRating"`
	BlockedTags                      []string `json:"BlockedTags"`
	AllowedTags                      []string `json:"AllowedTags"`
	EnableUserPreferenceAccess       bool     `json:"EnableUserPreferenceAccess"`
	AccessSchedules                  []any    `json:"AccessSchedules"`
	BlockUnratedItems                []string `json:"BlockUnratedItems"`
	EnableRemoteControlOfOtherUsers  bool     `json:"EnableRemoteControlOfOtherUsers"`
	EnableSharedDeviceControl        bool     `json:"EnableSharedDeviceControl"`
	EnableRemoteAccess               bool     `json:"EnableRemoteAccess"`
	EnableLiveTvManagement           bool     `json:"EnableLiveTvManagement"`
	EnableLiveTvAccess               bool     `json:"EnableLiveTvAccess"`
	EnableMediaPlayback              bool     `json:"EnableMediaPlayback"`
	EnableAudioPlaybackTranscoding   bool     `json:"EnableAudioPlaybackTranscoding"`
	EnableVideoPlaybackTranscoding   bool     `json:"EnableVideoPlaybackTranscoding"`
	EnablePlaybackRemuxing           bool     `json:"EnablePlaybackRemuxing"`
	ForceRemoteSourceTranscoding     bool     `json:"ForceRemoteSourceTranscoding"`
	EnableContentDeletion            bool     `json:"EnableContentDeletion"`
	EnableContentDeletionFromFolders []string `json:"EnableContentDeletionFromFolders"`
	EnableContentDownloading         bool     `json:"EnableContentDownloading"`
	EnableSyncTranscoding            bool     `json:"EnableSyncTranscoding"`
	EnableMediaConversion            bool     `json:"EnableMediaConversion"`
	EnabledDevices                   []string `json:"EnabledDevices"`
	EnableAllDevices                 bool     `json:"EnableAllDevices"`
	EnabledChannels                  []string `json:"EnabledChannels"`
	EnableAllChannels                bool     `json:"EnableAllChannels"`
	EnabledFolders                   []string `json:"EnabledFolders"`
	EnableAllFolders                 bool     `json:"EnableAllFolders"`
	InvalidLoginAttemptCount         int      `json:"InvalidLoginAttemptCount"`
	LoginAttemptsBeforeLockout       int      `json:"LoginAttemptsBeforeLockout"`
	MaxActiveSessions                int      `json:"MaxActiveSessions"`
	EnablePublicSharing              bool     `json:"EnablePublicSharing"`
	BlockedMediaFolders              []string `json:"BlockedMediaFolders"`
	BlockedChannels                  []string `json:"BlockedChannels"`
	RemoteClientBitrateLimit         int      `json:"RemoteClientBitrateLimit"`
	AuthenticationProviderID         string   `json:"AuthenticationProviderId"`
	PasswordResetProviderID          string   `json:"PasswordResetProviderId"`
	SyncPlayAccess                   string   `json:"SyncPlayAccess"`
	EnableCollectionManagement       bool     `json:"EnableCollectionManagement"`
	EnableSubtitleManagement         bool     `json:"EnableSubtitleManagement"`
	EnableLyricManagement            bool     `json:"EnableLyricManagement"`
}

// PolicyFor is the policy for an account: a viewer's, with the admin flag
// for Cineva's admins.
func PolicyFor(admin, disabled bool) UserPolicy {
	return UserPolicy{
		IsAdministrator: admin, IsDisabled: disabled,
		BlockedTags: []string{}, AllowedTags: []string{}, AccessSchedules: []any{}, BlockUnratedItems: []string{},
		EnableUserPreferenceAccess: true, EnableSharedDeviceControl: true, EnableRemoteAccess: true,
		EnableMediaPlayback: true, EnableAudioPlaybackTranscoding: true, EnableVideoPlaybackTranscoding: true,
		EnablePlaybackRemuxing: true, EnableContentDeletionFromFolders: []string{}, EnableContentDownloading: true,
		EnableSyncTranscoding: true, EnabledDevices: []string{}, EnableAllDevices: true,
		EnabledChannels: []string{}, EnableAllChannels: true, EnabledFolders: []string{}, EnableAllFolders: true,
		LoginAttemptsBeforeLockout: -1, BlockedMediaFolders: []string{}, BlockedChannels: []string{},
		AuthenticationProviderID: "Jellyfin.Server.Implementations.Users.DefaultAuthenticationProvider",
		PasswordResetProviderID:  "Jellyfin.Server.Implementations.Users.DefaultPasswordResetProvider",
		SyncPlayAccess:           "None",
	}
}

// AuthenticationResult answers /Users/AuthenticateByName.
type AuthenticationResult struct {
	User        User        `json:"User"`
	SessionInfo SessionInfo `json:"SessionInfo"`
	AccessToken string      `json:"AccessToken"`
	ServerID    string      `json:"ServerId"`
}

// SessionInfo is one signed-in device and what it plays.
type SessionInfo struct {
	PlayState                PlayState    `json:"PlayState"`
	AdditionalUsers          []any        `json:"AdditionalUsers"`
	Capabilities             Capabilities `json:"Capabilities"`
	RemoteEndPoint           string       `json:"RemoteEndPoint"`
	PlayableMediaTypes       []string     `json:"PlayableMediaTypes"`
	ID                       string       `json:"Id"`
	UserID                   string       `json:"UserId"`
	UserName                 string       `json:"UserName"`
	Client                   string       `json:"Client"`
	LastActivityDate         string       `json:"LastActivityDate"`
	LastPlaybackCheckIn      string       `json:"LastPlaybackCheckIn"`
	DeviceName               string       `json:"DeviceName"`
	DeviceID                 string       `json:"DeviceId"`
	ApplicationVersion       string       `json:"ApplicationVersion"`
	IsActive                 bool         `json:"IsActive"`
	SupportsMediaControl     bool         `json:"SupportsMediaControl"`
	SupportsRemoteControl    bool         `json:"SupportsRemoteControl"`
	NowPlayingQueue          []any        `json:"NowPlayingQueue"`
	NowPlayingQueueFullItems []any        `json:"NowPlayingQueueFullItems"`
	HasCustomDeviceName      bool         `json:"HasCustomDeviceName"`
	ServerID                 string       `json:"ServerId"`
	NowPlayingItem           *BaseItem    `json:"NowPlayingItem,omitempty"`
	SupportedCommands        []string     `json:"SupportedCommands"`
}

// PlayState is where a session's playback is.
type PlayState struct {
	PositionTicks       int64  `json:"PositionTicks,omitempty"`
	CanSeek             bool   `json:"CanSeek"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	VolumeLevel         int    `json:"VolumeLevel,omitempty"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex,omitempty"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex,omitempty"`
	MediaSourceID       string `json:"MediaSourceId,omitempty"`
	PlayMethod          string `json:"PlayMethod,omitempty"`
	RepeatMode          string `json:"RepeatMode"`
	PlaybackOrder       string `json:"PlaybackOrder"`
}

// Capabilities is what a client says it can do (POST /Sessions/Capabilities).
type Capabilities struct {
	PlayableMediaTypes           []string       `json:"PlayableMediaTypes"`
	SupportedCommands            []string       `json:"SupportedCommands"`
	SupportsMediaControl         bool           `json:"SupportsMediaControl"`
	SupportsPersistentIdentifier bool           `json:"SupportsPersistentIdentifier"`
	DeviceProfile                *DeviceProfile `json:"DeviceProfile,omitempty"`
}

// QueryResult wraps a list with its count, as every listing does.
type QueryResult struct {
	Items            []BaseItem `json:"Items"`
	TotalRecordCount int        `json:"TotalRecordCount"`
	StartIndex       int        `json:"StartIndex"`
}

// EmptyResult is a listing with nothing in it.
func EmptyResult() QueryResult { return QueryResult{Items: []BaseItem{}} }

// BaseItem is a BaseItemDto: a film, series, season, episode, artist,
// album, track, library or genre as a client sees it. Fields that do not
// apply are left out of the JSON.
type BaseItem struct {
	Name                     string            `json:"Name"`
	OriginalTitle            string            `json:"OriginalTitle,omitempty"`
	ServerID                 string            `json:"ServerId"`
	ID                       string            `json:"Id"`
	Etag                     string            `json:"Etag,omitempty"`
	DateCreated              string            `json:"DateCreated,omitempty"`
	CanDelete                bool              `json:"CanDelete"`
	CanDownload              bool              `json:"CanDownload"`
	SortName                 string            `json:"SortName,omitempty"`
	PremiereDate             string            `json:"PremiereDate,omitempty"`
	ExternalUrls             []any             `json:"ExternalUrls"`
	MediaSources             []MediaSource     `json:"MediaSources,omitempty"`
	Path                     string            `json:"Path,omitempty"`
	OfficialRating           string            `json:"OfficialRating,omitempty"`
	Overview                 string            `json:"Overview,omitempty"`
	Taglines                 []string          `json:"Taglines,omitempty"`
	Genres                   []string          `json:"Genres"`
	CommunityRating          float64           `json:"CommunityRating,omitempty"`
	RunTimeTicks             int64             `json:"RunTimeTicks,omitempty"`
	ProductionYear           int               `json:"ProductionYear,omitempty"`
	IndexNumber              *int              `json:"IndexNumber,omitempty"`
	ParentIndexNumber        *int              `json:"ParentIndexNumber,omitempty"`
	RemoteTrailers           []any             `json:"RemoteTrailers"`
	ProviderIds              map[string]string `json:"ProviderIds"`
	IsFolder                 bool              `json:"IsFolder"`
	ParentID                 string            `json:"ParentId,omitempty"`
	Type                     string            `json:"Type"`
	People                   []Person          `json:"People,omitempty"`
	Studios                  []any             `json:"Studios"`
	GenreItems               []NameGUIDPair    `json:"GenreItems"`
	ParentLogoItemID         string            `json:"ParentLogoItemId,omitempty"`
	ParentBackdropItemID     string            `json:"ParentBackdropItemId,omitempty"`
	ParentBackdropImageTags  []string          `json:"ParentBackdropImageTags,omitempty"`
	LocalTrailerCount        int               `json:"LocalTrailerCount"`
	UserData                 *UserData         `json:"UserData,omitempty"`
	RecursiveItemCount       int               `json:"RecursiveItemCount,omitempty"`
	ChildCount               int               `json:"ChildCount,omitempty"`
	SeriesName               string            `json:"SeriesName,omitempty"`
	SeriesID                 string            `json:"SeriesId,omitempty"`
	SeasonID                 string            `json:"SeasonId,omitempty"`
	SpecialFeatureCount      int               `json:"SpecialFeatureCount"`
	DisplayPreferencesID     string            `json:"DisplayPreferencesId,omitempty"`
	Status                   string            `json:"Status,omitempty"`
	Tags                     []string          `json:"Tags"`
	PrimaryImageAspectRatio  float64           `json:"PrimaryImageAspectRatio,omitempty"`
	Artists                  []string          `json:"Artists,omitempty"`
	ArtistItems              []NameGUIDPair    `json:"ArtistItems,omitempty"`
	Album                    string            `json:"Album,omitempty"`
	CollectionType           string            `json:"CollectionType,omitempty"`
	AlbumID                  string            `json:"AlbumId,omitempty"`
	AlbumPrimaryImageTag     string            `json:"AlbumPrimaryImageTag,omitempty"`
	SeriesPrimaryImageTag    string            `json:"SeriesPrimaryImageTag,omitempty"`
	AlbumArtist              string            `json:"AlbumArtist,omitempty"`
	AlbumArtists             []NameGUIDPair    `json:"AlbumArtists,omitempty"`
	SeasonName               string            `json:"SeasonName,omitempty"`
	MediaStreams             []MediaStream     `json:"MediaStreams,omitempty"`
	VideoType                string            `json:"VideoType,omitempty"`
	ImageTags                map[string]string `json:"ImageTags"`
	BackdropImageTags        []string          `json:"BackdropImageTags"`
	ParentPrimaryImageItemID string            `json:"ParentPrimaryImageItemId,omitempty"`
	ParentPrimaryImageTag    string            `json:"ParentPrimaryImageTag,omitempty"`
	Chapters                 []any             `json:"Chapters,omitempty"`
	LocationType             string            `json:"LocationType"`
	MediaType                string            `json:"MediaType"`
	EndDate                  string            `json:"EndDate,omitempty"`
	LockedFields             []string          `json:"LockedFields"`
	LockData                 bool              `json:"LockData"`
	Width                    int               `json:"Width,omitempty"`
	Height                   int               `json:"Height,omitempty"`
	Container                string            `json:"Container,omitempty"`
	Bitrate                  int               `json:"Bitrate,omitempty"`
	ChannelID                string            `json:"ChannelId,omitempty"`
}

// NameGUIDPair names something with an id, as genres and artists are
// listed on an item.
type NameGUIDPair struct {
	Name string `json:"Name"`
	ID   string `json:"Id"`
}

// Person is a credited person on an item.
type Person struct {
	Name            string `json:"Name"`
	ID              string `json:"Id"`
	Role            string `json:"Role,omitempty"`
	Type            string `json:"Type"`
	PrimaryImageTag string `json:"PrimaryImageTag,omitempty"`
}

// UserData is the viewer's own state on an item (UserItemDataDto).
type UserData struct {
	Rating                *float64 `json:"Rating,omitempty"`
	PlayedPercentage      *float64 `json:"PlayedPercentage,omitempty"`
	UnplayedItemCount     *int     `json:"UnplayedItemCount,omitempty"`
	PlaybackPositionTicks int64    `json:"PlaybackPositionTicks"`
	PlayCount             int      `json:"PlayCount"`
	IsFavorite            bool     `json:"IsFavorite"`
	Likes                 *bool    `json:"Likes,omitempty"`
	LastPlayedDate        string   `json:"LastPlayedDate,omitempty"`
	Played                bool     `json:"Played"`
	Key                   string   `json:"Key"`
	ItemID                string   `json:"ItemId"`
}

// MediaSource is one file behind an item, with its streams and how it can
// be played.
type MediaSource struct {
	Protocol                            string            `json:"Protocol"`
	ID                                  string            `json:"Id"`
	Path                                string            `json:"Path,omitempty"`
	Type                                string            `json:"Type"`
	Container                           string            `json:"Container,omitempty"`
	Size                                int64             `json:"Size,omitempty"`
	Name                                string            `json:"Name"`
	IsRemote                            bool              `json:"IsRemote"`
	ETag                                string            `json:"ETag,omitempty"`
	RunTimeTicks                        int64             `json:"RunTimeTicks,omitempty"`
	ReadAtNativeFramerate               bool              `json:"ReadAtNativeFramerate"`
	IgnoreDts                           bool              `json:"IgnoreDts"`
	IgnoreIndex                         bool              `json:"IgnoreIndex"`
	GenPtsInput                         bool              `json:"GenPtsInput"`
	SupportsTranscoding                 bool              `json:"SupportsTranscoding"`
	SupportsDirectStream                bool              `json:"SupportsDirectStream"`
	SupportsDirectPlay                  bool              `json:"SupportsDirectPlay"`
	IsInfiniteStream                    bool              `json:"IsInfiniteStream"`
	UseMostCompatibleTranscodingProfile bool              `json:"UseMostCompatibleTranscodingProfile"`
	RequiresOpening                     bool              `json:"RequiresOpening"`
	RequiresClosing                     bool              `json:"RequiresClosing"`
	RequiresLooping                     bool              `json:"RequiresLooping"`
	SupportsProbing                     bool              `json:"SupportsProbing"`
	VideoType                           string            `json:"VideoType,omitempty"`
	MediaStreams                        []MediaStream     `json:"MediaStreams"`
	MediaAttachments                    []any             `json:"MediaAttachments"`
	Formats                             []string          `json:"Formats"`
	Bitrate                             int               `json:"Bitrate,omitempty"`
	RequiredHTTPHeaders                 map[string]string `json:"RequiredHttpHeaders"`
	TranscodingURL                      string            `json:"TranscodingUrl,omitempty"`
	TranscodingSubProtocol              string            `json:"TranscodingSubProtocol,omitempty"`
	TranscodingContainer                string            `json:"TranscodingContainer,omitempty"`
	DefaultAudioStreamIndex             *int              `json:"DefaultAudioStreamIndex,omitempty"`
	DefaultSubtitleStreamIndex          *int              `json:"DefaultSubtitleStreamIndex,omitempty"`
	HasSegments                         bool              `json:"HasSegments"`
}

// MediaStream is one stream of a file: video, audio or subtitle.
type MediaStream struct {
	Codec                  string `json:"Codec,omitempty"`
	Language               string `json:"Language,omitempty"`
	TimeBase               string `json:"TimeBase,omitempty"`
	Title                  string `json:"Title,omitempty"`
	VideoRange             string `json:"VideoRange,omitempty"`
	VideoRangeType         string `json:"VideoRangeType,omitempty"`
	DisplayTitle           string `json:"DisplayTitle,omitempty"`
	IsInterlaced           bool   `json:"IsInterlaced"`
	BitRate                int    `json:"BitRate,omitempty"`
	Channels               int    `json:"Channels,omitempty"`
	SampleRate             int    `json:"SampleRate,omitempty"`
	IsDefault              bool   `json:"IsDefault"`
	IsForced               bool   `json:"IsForced"`
	IsHearingImpaired      bool   `json:"IsHearingImpaired"`
	Height                 int    `json:"Height,omitempty"`
	Width                  int    `json:"Width,omitempty"`
	Type                   string `json:"Type"`
	Index                  int    `json:"Index"`
	IsExternal             bool   `json:"IsExternal"`
	DeliveryMethod         string `json:"DeliveryMethod,omitempty"`
	DeliveryURL            string `json:"DeliveryUrl,omitempty"`
	IsExternalURL          *bool  `json:"IsExternalUrl,omitempty"`
	IsTextSubtitleStream   bool   `json:"IsTextSubtitleStream"`
	SupportsExternalStream bool   `json:"SupportsExternalStream"`
	Path                   string `json:"Path,omitempty"`
	Level                  int    `json:"Level,omitempty"`
}

// PlaybackInfoResponse answers /Items/{id}/PlaybackInfo.
type PlaybackInfoResponse struct {
	MediaSources  []MediaSource `json:"MediaSources"`
	PlaySessionID string        `json:"PlaySessionId"`
	ErrorCode     string        `json:"ErrorCode,omitempty"`
}

// PlaybackInfoRequest is what a client sends to PlaybackInfo: its profile
// and its choices.
type PlaybackInfoRequest struct {
	UserID               string         `json:"UserId"`
	MaxStreamingBitrate  int            `json:"MaxStreamingBitrate"`
	StartTimeTicks       int64          `json:"StartTimeTicks"`
	AudioStreamIndex     *int           `json:"AudioStreamIndex"`
	SubtitleStreamIndex  *int           `json:"SubtitleStreamIndex"`
	MediaSourceID        string         `json:"MediaSourceId"`
	DeviceProfile        *DeviceProfile `json:"DeviceProfile"`
	EnableDirectPlay     *bool          `json:"EnableDirectPlay"`
	EnableDirectStream   *bool          `json:"EnableDirectStream"`
	EnableTranscoding    *bool          `json:"EnableTranscoding"`
	AllowVideoStreamCopy *bool          `json:"AllowVideoStreamCopy"`
	AllowAudioStreamCopy *bool          `json:"AllowAudioStreamCopy"`
	AutoOpenLiveStream   bool           `json:"AutoOpenLiveStream"`
}

// DeviceProfile is what a client can play (the parts read here).
type DeviceProfile struct {
	Name                string               `json:"Name"`
	MaxStreamingBitrate int                  `json:"MaxStreamingBitrate"`
	MaxStaticBitrate    int                  `json:"MaxStaticBitrate"`
	DirectPlayProfiles  []DirectPlayProfile  `json:"DirectPlayProfiles"`
	TranscodingProfiles []TranscodingProfile `json:"TranscodingProfiles"`
	SubtitleProfiles    []SubtitleProfile    `json:"SubtitleProfiles"`
	CodecProfiles       []any                `json:"CodecProfiles"`
}

// DirectPlayProfile is a container with the codecs the client plays in it.
type DirectPlayProfile struct {
	Container  string `json:"Container"`
	AudioCodec string `json:"AudioCodec"`
	VideoCodec string `json:"VideoCodec"`
	Type       string `json:"Type"` // Audio, Video, Photo
}

// TranscodingProfile is what the client wants when it cannot direct play.
type TranscodingProfile struct {
	Container  string `json:"Container"`
	Type       string `json:"Type"`
	VideoCodec string `json:"VideoCodec"`
	AudioCodec string `json:"AudioCodec"`
	Protocol   string `json:"Protocol"`
	Context    string `json:"Context"`
}

// SubtitleProfile is how the client can take a subtitle format.
type SubtitleProfile struct {
	Format   string `json:"Format"`
	Method   string `json:"Method"` // Encode, Embed, External, Hls
	Language string `json:"Language"`
}

// PlaybackProgress is what a client reports on start, progress and stop.
type PlaybackProgress struct {
	ItemID              string `json:"ItemId"`
	SessionID           string `json:"SessionId"`
	MediaSourceID       string `json:"MediaSourceId"`
	PositionTicks       *int64 `json:"PositionTicks"`
	IsPaused            bool   `json:"IsPaused"`
	IsMuted             bool   `json:"IsMuted"`
	PlayMethod          string `json:"PlayMethod"`
	PlaySessionID       string `json:"PlaySessionId"`
	AudioStreamIndex    *int   `json:"AudioStreamIndex"`
	SubtitleStreamIndex *int   `json:"SubtitleStreamIndex"`
	CanSeek             bool   `json:"CanSeek"`
	Failed              bool   `json:"Failed"`
}

// DisplayPreferences is a client's stored view settings, kept as the
// client sent them.
type DisplayPreferences struct {
	ID                 string            `json:"Id"`
	ViewType           string            `json:"ViewType,omitempty"`
	SortBy             string            `json:"SortBy"`
	IndexBy            string            `json:"IndexBy,omitempty"`
	RememberIndexing   bool              `json:"RememberIndexing"`
	PrimaryImageHeight int               `json:"PrimaryImageHeight"`
	PrimaryImageWidth  int               `json:"PrimaryImageWidth"`
	CustomPrefs        map[string]string `json:"CustomPrefs"`
	ScrollDirection    string            `json:"ScrollDirection"`
	ShowBackdrop       bool              `json:"ShowBackdrop"`
	RememberSorting    bool              `json:"RememberSorting"`
	SortOrder          string            `json:"SortOrder"`
	ShowSidebar        bool              `json:"ShowSidebar"`
	Client             string            `json:"Client"`
}

// DefaultDisplayPreferences is what a client gets before it has saved any.
func DefaultDisplayPreferences(id, client string) DisplayPreferences {
	return DisplayPreferences{
		ID: id, SortBy: "SortName", PrimaryImageHeight: 250, PrimaryImageWidth: 250,
		CustomPrefs: map[string]string{}, ScrollDirection: "Horizontal", ShowBackdrop: true,
		SortOrder: "Ascending", Client: client,
	}
}

// QueryFilters answers /Items/Filters; QueryFiltersLegacy /Items/Filters2
// is the older shape with plain lists.
type QueryFilters struct {
	Genres []NameGUIDPair `json:"Genres"`
	Tags   []string       `json:"Tags"`
}

type QueryFiltersLegacy struct {
	Genres          []string `json:"Genres"`
	Tags            []string `json:"Tags"`
	OfficialRatings []string `json:"OfficialRatings"`
	Years           []int    `json:"Years"`
}

// SearchHint is one search result; SearchHintResult the list.
type SearchHint struct {
	ItemID              string   `json:"ItemId"`
	ID                  string   `json:"Id"`
	Name                string   `json:"Name"`
	MatchedTerm         string   `json:"MatchedTerm,omitempty"`
	IndexNumber         *int     `json:"IndexNumber,omitempty"`
	ProductionYear      int      `json:"ProductionYear,omitempty"`
	ParentIndexNumber   *int     `json:"ParentIndexNumber,omitempty"`
	PrimaryImageTag     string   `json:"PrimaryImageTag,omitempty"`
	ThumbImageTag       string   `json:"ThumbImageTag,omitempty"`
	ThumbImageItemID    string   `json:"ThumbImageItemId,omitempty"`
	BackdropImageTag    string   `json:"BackdropImageTag,omitempty"`
	BackdropImageItemID string   `json:"BackdropImageItemId,omitempty"`
	Type                string   `json:"Type"`
	IsFolder            bool     `json:"IsFolder"`
	RunTimeTicks        int64    `json:"RunTimeTicks,omitempty"`
	MediaType           string   `json:"MediaType"`
	Series              string   `json:"Series,omitempty"`
	Album               string   `json:"Album,omitempty"`
	AlbumID             string   `json:"AlbumId,omitempty"`
	AlbumArtist         string   `json:"AlbumArtist,omitempty"`
	Artists             []string `json:"Artists,omitempty"`
}

type SearchHintResult struct {
	SearchHints      []SearchHint `json:"SearchHints"`
	TotalRecordCount int          `json:"TotalRecordCount"`
}

// ThemeMedia answers /Items/{id}/ThemeMedia: nothing, in three lists.
type ThemeMedia struct {
	ThemeVideosResult     QueryResult `json:"ThemeVideosResult"`
	ThemeSongsResult      QueryResult `json:"ThemeSongsResult"`
	SoundtrackSongsResult QueryResult `json:"SoundtrackSongsResult"`
}

// Problem is the error body Jellyfin sends (RFC 7807).
type Problem struct {
	Type   string `json:"type"`
	Title  string `json:"title"`
	Status int    `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// Message is a websocket message on /socket.
type Message struct {
	MessageType string `json:"MessageType"`
	MessageID   string `json:"MessageId,omitempty"`
	Data        any    `json:"Data,omitempty"`
}

// ParseList splits a comma-separated query value, dropping blanks.
func ParseList(v string) []string {
	var out []string
	for _, p := range strings.Split(v, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}

// HasAny reports whether list holds any of the wanted values, compared
// without case; an empty list matches everything.
func HasAny(list []string, wanted ...string) bool {
	if len(list) == 0 {
		return true
	}
	for _, l := range list {
		for _, w := range wanted {
			if strings.EqualFold(l, w) {
				return true
			}
		}
	}
	return false
}

// IntPtr is a pointer to n, for the optional numbers.
func IntPtr(n int) *int { return &n }

// Atoi is strconv.Atoi with a default.
func Atoi(s string, def int) int {
	if n, err := strconv.Atoi(strings.TrimSpace(s)); err == nil {
		return n
	}
	return def
}
