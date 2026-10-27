# Jellyfin-compatible API

Status: designed October 2026; stages 1 (sign-in and browsing) and 2
(playback) built the same month, by the Subsonic shim's pattern; stages 3
and 4 to follow. What is served as built is in the table under "As
built". Decided over a Plex shim:
Plex clients authenticate and find servers through plex.tv, which only
knows Plex's own servers, so no shim could ever be reached by them, while
the Jellyfin apps connect to any server by address and their API is
documented.

## Why

Cineva's own client is a web app, and its native apps (`clients/`) do not
exist yet. The Jellyfin apps do: official, open-source apps for Android
TV, Fire TV, Roku, LG webOS, Samsung Tizen, iOS, tvOS and Android, plus
Swiftfin, Findroid, Jellyfin Media Player, the Kodi add-on, Finamp for
music and Infuse. Speaking their API puts Cineva on every television and
phone today, with what Cineva already does well underneath: the scan, the
metadata, the HLS transcoder, the watch state and next-up, intro and
credits detection. Emby clients largely follow, since Jellyfin kept Emby's
API.

It is a shim, as the Subsonic one is: nothing in Cineva changes shape for
it. `/api/v1` stays the first-class API and the native apps will use it.

## Where it lives

`internal/jellyfin` holds the types (BaseItemDto and friends, with the
exact JSON names Jellyfin's OpenAPI spec gives them), the mapping from
`store.Item` and viewer state, the device-profile reading and the
authorisation-header parsing. The handlers live in `internal/api/
jellyfin*.go` so they reuse the server's own playback, session and
registry code, mounted at the root (Jellyfin clients expect `/System/
Info/Public`, `/Users/...` and so on at the server's base URL; a `/emby/`
prefix is accepted too, since some clients add it). `jellyfin.enabled`
(default true, `CINEVA_JELLYFIN_ENABLED`) turns it off: the routes answer
404. Documented by Jellyfin's own OpenAPI spec and `docs/jellyfin.md`
(this file, which grows a served-calls table as the shim is built), not
`openapi.yaml`, which describes `/api/v1`.

Ids: Jellyfin ids are 32 hex digits. Cineva's item ids are UUIDs, so an
id crosses the shim with its dashes removed and comes back with them put
back. Libraries are items too (`CollectionFolder`), keyed by their
library id the same way; a user's id is their user id.

The server says it is Jellyfin `10.10.7` (`Version`) with `ProductName`
"Cineva", so clients use the current endpoints and show the right name.
`ServerId` is the server's identity key fingerprint, stable across
restarts. UDP discovery on port 7359 ("who is JellyfinServer?") is
answered, so the apps find the server on the LAN without typing an
address.

## Signing in

Jellyfin clients send `Authorization: MediaBrowser Client="...",
Device="...", DeviceId="...", Version="...", Token="..."` (older ones
`X-Emby-Authorization`, or the token alone in `X-Emby-Token`,
`X-MediaBrowser-Token` or `api_key=`). The shim reads all of them.

- `GET /Users/Public` is the profile picker: Cineva's users that the
  picker shows, so a television's Jellyfin app shows "Who's watching?"
  as Cineva's own client does.
- `POST /Users/AuthenticateByName` `{Username, Pw}` checks the account
  password through the sign-in throttle (a PIN is not accepted here; a
  user without a password cannot sign in from a Jellyfin client until
  they set one). On success the shim creates a **Cineva session on a
  Cineva device**: the device is the one whose secret is derived from
  the client's `DeviceId` (so the same app on the same television is the
  same device every time, and it appears on the user's Devices page and
  the admin's as "Jellyfin for Android TV on Fire TV"), and the session's
  token is returned as `AccessToken`. Every later call is an ordinary
  session lookup, so the session slides, can be signed out from Cineva's
  Devices page, and carries the play queue like any other. `DeviceId` is
  asserted by the client, as it is for Jellyfin itself, so a device made
  this way is never one that signs in by itself (`auto_user_id` is
  ignored for it).
- `api_key=` with a Cineva API key also works, for scripts, and a key
  typed as the password in `AuthenticateByName` signs in as its user, so
  an app need never hold the account password (see `subsonic.md`).
- Quick Connect (`/QuickConnect/*`) answers "not enabled"; it can map
  onto a code shown in Cineva's Settings later.

Library access is the user's: a restricted library is absent from
`/Users/{id}/Views` and everything under it, and an item in one is 404.

## What is served

Jellyfin's API is large; the apps need a modest subset to browse and
play, and tolerate empty answers for the rest. The first version serves:

| Group | Calls |
|---|---|
| System | `/System/Info/Public`, `/System/Info`, `/System/Ping`, `/System/Endpoint`, `/Branding/Configuration`, `/Branding/Css` (empty), `/Localization/*` (fixed English answers), `/DisplayPreferences/{id}` (get and post, stored per user in `user_prefs` under a `jellyfin.` key), `/Plugins` (empty), `/ScheduledTasks` (empty) |
| Users | `/Users/Public`, `/Users/AuthenticateByName`, `/Users/Me`, `/Users/{id}`, `/Users/{id}/Views`, `/Sessions/Logout`, `/Sessions/Capabilities` and `/Capabilities/Full` (accepted; the capabilities are kept on the registry entry for Play on) |
| Browsing | `/Users/{id}/Items` and `/Items` with `ParentId`, `IncludeItemTypes`, `Recursive`, `SortBy`, `SortOrder`, `StartIndex`, `Limit`, `SearchTerm`, `Genres`, `Years`, `Filters` (IsPlayed, IsUnplayed, IsResumable, IsFavorite), `NameStartsWith`, `Fields`; `/Users/{id}/Items/{id}`, `/Items/{id}/Ancestors`, `/Users/{id}/Items/Latest`, `/Users/{id}/Items/Resume`, `/Shows/NextUp`, `/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`, `/Items/{id}/Similar` (the "because" rows), `/Genres`, `/Persons` and `/Items/{id}/People`-style credits, `/Items/Filters` and `/Items/Filters2`, `/Search/Hints`, `/Items/{id}/Intros` and `/ThemeMedia` (empty) |
| Images | `/Items/{id}/Images/Primary`, `/Backdrop/{n}`, `/Thumb`, `/Logo` (404), `/Users/{id}/Images/Primary` (the avatar); `tag` is a hash of the artwork path, `maxWidth`/`maxHeight`/`quality` resize through the avatar package's resizer with a small on-disk cache |
| Playback | `POST /Items/{id}/PlaybackInfo` (the decision, below), `/Videos/{id}/stream` and `/stream.{container}` (direct play with ranges, `Static=true`), `/Videos/{id}/master.m3u8` and the segment and init calls it names (an HLS session), `/Videos/{id}/{source}/Subtitles/{index}/Stream.vtt` and `/Subtitles/{index}/{start}/Stream.vtt`, `/Videos/{id}/Trickplay/{width}/tiles.m3u8` and the tile images (the seek-bar previews, when built), `/Audio/{id}/stream` and `/Audio/{id}/universal` (music, below) |
| State | `/Sessions/Playing`, `/Sessions/Playing/Progress`, `/Sessions/Playing/Stopped`, `/Sessions/Playing/Ping`, `/Users/{id}/PlayedItems/{id}` (post and delete), `/Users/{id}/FavoriteItems/{id}` (post and delete, onto the favourites that the Subsonic work adds, or a five-star rating until then), `/UserItems/{id}/UserData`, `/Items/{id}/UserData` |
| Sessions | `/Sessions` (the user's own devices and what they play, from the registry), `/Sessions/{id}/Playing` and `/Playing/{command}` (Play on and the transport commands, carried by Cineva's handoff and takeover), `/socket` (below) |
| Music | `/Artists`, `/Artists/AlbumArtists`, `/Artists/{name}`, albums and tracks through `/Items` with `IncludeItemTypes=MusicAlbum,Audio`, `/Items/{id}/InstantMix` (the artist's and genre's tracks), `/Playlists` and `/Playlists/{id}/Items` (the user's own), `/Audio/{id}/universal` (direct or transcoded through the music overhaul's negotiation and streamed cache) |
| Answered empty | `/LiveTv/*`, `/Channels`, `/Collections` (until collections map), `/Studios`, `/Years` (or from items), `/Notifications`, `/SyncPlay/*` (not enabled), `/Devices` (admin, from Cineva's devices) |

Unknown calls answer 404 with Jellyfin's problem shape, which the apps
treat as "not here" rather than as an error.

## The item

`BaseItemDto` from a `store.Item`: `Id`, `Name`, `ServerId`, `Type`
(Movie, Series, Season, Episode, MusicArtist, MusicAlbum, Audio,
CollectionFolder), `ParentId`, `SeriesId`, `SeriesName`, `SeasonId`,
`SeasonName`, `IndexNumber` (episode or track), `ParentIndexNumber`
(season or disc), `ProductionYear`, `PremiereDate` (from `air_date`),
`Overview`, `Taglines`, `Genres`, `OfficialRating` (certification),
`CommunityRating`, `RunTimeTicks` (seconds × 10,000,000), `Container`,
`Path` (only to admins), `ImageTags {Primary}`, `BackdropImageTags`,
`ProviderIds {Tmdb, MusicBrainzAlbum, MusicBrainzTrack, MusicBrainzArtist}`,
`People` (from credits), `Album`, `AlbumArtist`, `Artists`, `AlbumId`,
`MediaType` (Video or Audio), `IsFolder`, `ChildCount`,
`RecursiveItemCount`, `LocationType` FileSystem, and `MediaSources` and
`MediaStreams` on request (`Fields=MediaSources`): the file as one source
with its video, audio and subtitle streams from the probe and the
subtitle tracks Cineva extracted.

`UserData` is the viewer's: `PlaybackPositionTicks` and `Played` from
play state, `PlayCount` and `LastPlayedDate` from play counts, `IsFavorite`
from favourites, `UnplayedItemCount` on series and seasons from the
episodes, `Key`. Series and seasons get `Played` when every episode is.

Libraries come back from `/Users/{id}/Views` as `CollectionFolder` items
with `CollectionType` movies, tvshows or music, named as in Cineva.

## The playback decision

`POST /Items/{id}/PlaybackInfo` carries the client's `DeviceProfile`:
`DirectPlayProfiles` (containers and codecs it plays as they are),
`TranscodingProfiles` (what it wants when it cannot: HLS with h264 and
aac, mostly), `SubtitleProfiles` (external, embed, hls or encode), and
`MaxStreamingBitrate`. The shim turns that into Cineva's own
`capabilities` (containers, video codecs, audio codecs, a quality from
the bit rate ceiling, a burned subtitle where the profile cannot take it
any other way) and makes the same decision `POST /items/{id}/play` makes:

- **Direct play**: `SupportsDirectPlay: true` and the client fetches
  `/Videos/{id}/stream?Static=true`, served with ranges as Cineva's own
  direct play is, through the storage deadline.
- **Transcode**: an HLS session is started as for Cineva's player, and
  `TranscodingUrl` points at its playlist: `/Videos/{id}/master.m3u8?
  PlaySessionId=<Cineva session id>&api_key=...`. Cineva's playlist is a
  complete VOD playlist (`#EXT-X-ENDLIST`, every segment listed) whose
  segments are encoded on demand, restarting at the requested offset, so
  a Jellyfin client seeks by asking for a segment, as Cineva's own player
  does, with nothing to translate. Audio language, subtitle choice and
  the resume position follow the viewer's preferences as in `playItem`.
- `MediaSources[0]` lists the streams either way, with external subtitle
  streams carrying `DeliveryUrl` to the WebVTT the server extracted and
  `DeliveryMethod: External`, so a client overlays them on a direct play
  rather than forcing a transcode.

The registry entry is the same as for any playback: the device's name,
the item, the mode and quality, so Activity, Continue here and the Home
Assistant events see a Jellyfin app exactly as they see the web client.

`/Sessions/Playing/Progress` is the heartbeat (`POST /items/{id}/
progress`): position from `PositionTicks`, paused from `IsPaused`; it
also updates the session's queue position. `Stopped` ends the playback,
and marks the item watched when the position is past the threshold as
the web client's stop does. `PlayedItems` is `PUT /items/{id}/watched`.
Next-up and resume rows come from Cineva's own next-up and continue-
watching logic, so a television running the Jellyfin app and a phone
running Cineva's client agree on what to play next.

## Remote control and the socket

Jellyfin clients open `/socket?api_key=...&deviceId=...` and expect
`KeepAlive` messages; without it some apps show an error and all lose
remote control. The shim accepts the socket, answers `KeepAlive`, and
sends `Play` and `Playstate` messages when Cineva's handoff and takeover
address the session (Play on... from the web client plays on a Fire TV
running the Jellyfin app), and `UserDataChanged` when the viewer's state
changes elsewhere, so the app's watched ticks keep up. Nothing else rides
the socket at first.

## Music

The Jellyfin music apps (Finamp, the official apps' music views) work
through the same item model with `MusicArtist`, `MusicAlbum` and `Audio`
items, the artists and album-artists listings, the playlists, and
`/Audio/{id}/universal`, which asks for a container and codec list and a
bit rate: the music overhaul's negotiation answers it with the original
or a streamed transcode from the bounded cache. Lyrics come from
`/Audio/{id}/Lyrics` (the `lyrics` table).

## As built (stages 1 and 2)

| Group | Served |
|---|---|
| System | `/System/Info/Public`, `/System/Info`, `/System/Ping`, `/System/Endpoint`, `/Branding/Configuration`, `/Branding/Css`, `/Localization/*` (fixed empty answers), `/DisplayPreferences/{id}` (get and post, kept per user as sent), `/Plugins`, `/ScheduledTasks`, `/Packages` (empty), `/QuickConnect/Enabled` (false) |
| Users | `/Users/Public`, `/Users/AuthenticateByName`, `/Users/Me`, `/Users/{id}`, `/Users`, `/Sessions/Logout`, `/Sessions/Capabilities` and `/Capabilities/Full` (accepted) |
| Browsing | `/Users/{id}/Views` and `/UserViews`; `/Users/{id}/Items` and `/Items` with ParentId, IncludeItemTypes, Recursive, SortBy, SortOrder, StartIndex, Limit, SearchTerm, Genres, GenreIds, Years, Filters, NameStartsWith, Ids, IsPlayed, IsFavorite; `/Users/{id}/Items/{id}` and `/Items/{id}` (with MediaSources, streams, genres and credits); `/Items/{id}/Ancestors`; `/Users/{id}/Items/Latest`; `/Users/{id}/Items/Resume` (and `/UserItems/Resume`); `/Shows/NextUp`, `/Shows/{id}/Seasons`, `/Shows/{id}/Episodes`; `/Items/{id}/Similar`; `/Genres`; `/Items/Filters` and `/Filters2`; `/Search/Hints`; `/Artists` and `/Artists/AlbumArtists`; empty answers for intros, theme media, special features, trailers, persons, studios, years, collections, channels, Live TV, notifications and SyncPlay |
| Images | `/Items/{id}/Images/Primary`, `Backdrop`, `Thumb` (the poster or backdrop file as it is; sizes ignored), `/Users/{id}/Images/Primary` |
| Playback | `POST /Items/{id}/PlaybackInfo` (and GET), `/Videos/{id}/stream` (ranges, `Static`), `/Videos/{id}/master.m3u8` (the HLS session's playlist, rewritten so every segment carries the token), `/Videos/{id}/hls/{session}/{file}`, `/Videos/{id}/{source}/Subtitles/{index}/Stream.vtt`, `/Items/{id}/Download` |
| State | `/Sessions/Playing`, `/Playing/Progress`, `/Playing/Stopped`, `/Playing/Ping`, `/Users/{id}/PlayedItems/{id}` (a series or season marks its episodes), `/Users/{id}/FavoriteItems/{id}`, `/UserItems/{id}/UserData` (get and post), `/Sessions` (the user's devices and what each plays), `/socket` (keep-alive only) |

Not yet: music playback and `/Artists/{name}` (stage 3; tracks list but
`/Audio/{id}/universal` answers 404), remote control, image resizing,
trickplay, UDP discovery, Quick Connect, playlists. Verified with curl
against the Jellyfin web client's call sequence; the apps themselves are
stage 4's compatibility table.

Two details decided while building: the server's own `/play/{session}`
routes need the browser's cookie, so the shim serves the playlist and
segments at `/Videos/{id}/hls/{session}/...` with `api_key` on every
line (relative segment names would drop it); and a device for a Jellyfin
client is keyed by an HMAC of its `DeviceId` under the identity key, so
it is stable without being guessable from the id alone.

## What stays out

- Quick Connect, SyncPlay, Live TV, DVR, collections and playlists of
  video, user management and the dashboard (admins use Cineva's own
  admin UI; the shim never creates or edits users), plugin and
  scheduled-task APIs, image uploads, and the metadata editor.
- Transcoding to anything but HLS, and DASH.
- Books, podcasts and photos until Cineva has them.

## Storage

Nothing new: sessions and devices as they are (the device secret for a
Jellyfin device is derived from its `DeviceId`), display preferences in
`user_prefs`, favourites from the Subsonic follow-up, the image resize
cache on disk under `<data>/cache/images`.

## Staging

1. **Sign-in and browsing, built**: system, users, views, items, images,
   the item mapping, search, next-up and resume.
2. **Playback, built**: PlaybackInfo, direct play, HLS, subtitles,
   progress and watched, sessions and the socket's keep-alive.
3. **Remote control and music**: Play on and the transport commands over
   the socket, the music listings and `/Audio/{id}/universal`, Finamp.
4. **Polish**: trickplay tiles, image resizing, UDP discovery, the
   compatibility table in this file (web, Android TV, Fire TV, Roku,
   webOS, Tizen, iOS, tvOS, Android, Swiftfin, Findroid, Kodi, Infuse).

## Open questions

- **Served at the root** or under a prefix such as `/jellyfin/`: the apps
  accept a base URL with a path, so a prefix would keep the root clean,
  but UDP discovery announces an address without a path and a few apps
  strip paths. The root, with `/emby/` accepted, is the safer default.
- **Version to claim**: a recent 10.10 gets the modern endpoints; a client
  newer than the claimed version may try calls the shim lacks, which it
  must answer 404 cleanly.
- **Transcoding bit rate**: whether `MaxStreamingBitrate` picks one of
  Cineva's quality levels (1080p, 720p, 480p) or sets the encoder's bit
  rate directly.
