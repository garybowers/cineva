# The Subsonic-compatible API

Status: built (October 2026), the thin first version from
[music.md](music.md) part 5. Cineva serves the Subsonic API (1.16.1) with
some OpenSubsonic additions at `/rest/`, so the music apps that speak it
(Symfonium, DSub, Ultrasonic, play:Sub, Substreamer, Feishin, Supersonic,
Sonixd, Tempo, Audinaut, and car and watch apps) can play a Cineva music
library. It is a shim over the same store, listings, playlists, ratings
and streaming the web client uses: nothing in Cineva changes shape for it,
and the native apps in `clients/` use `/api/v1`.

`subsonic.enabled` (`CINEVA_SUBSONIC_ENABLED`, on by default) turns it
off; the routes are then not mounted and answer 404. It is not part of
`openapi.yaml`, which describes `/api/v1`; the Subsonic specification and
this note describe it.

## Connecting a client

- **Server address**: Cineva's address, as the web client is opened
  (`http://cineva.local:4747`). No path.
- **Username and password**: the Cineva account's. Clients that offer it
  should use "legacy" or "password" authentication (see below).
- **Or an API key**: made in Settings, Connected apps, for a client that
  supports OpenSubsonic's `apiKey` sign-in. The key goes in the API key
  field with no username.

Responses are XML unless the client asks for JSON (`f=json`) or JSONP
(`f=jsonp&callback=`). Parameters may be in the query string or a form
POST body (the `formPost` extension), and calls work with or without the
`.view` suffix.

## Signing in, and its limits

Subsonic clients sign in on every request. Three ways are served:

| Parameters | What happens |
|---|---|
| `apiKey=` | Checked against Cineva's API keys. Sent on its own: `u`, `p`, `t` or `s` beside it is error 43. An unknown key is error 44. |
| `u=` and `p=` | An API key typed as the password (the usual way: see below), else the account password, plain or `enc:` hex, checked against the argon2id hash through the same throttle as the sign-in screen and then remembered (as a hash) for ten minutes so a client's many requests do not each cost a hash. |
| `u=` with `t=` and `s=` | The salted token, `md5(password + s)`, checked against the user's API keys made for music apps. A user with none gets error 41 and a message saying what to make. |

**The way in for an app is an API key.** Make one in Settings, API keys,
with "Usable as a password in music apps" on, and give the app your
username and the key as the password. Such a key keeps a sealed copy on
the server (AES-GCM under a key made once per server, migration 0047), so
the app's token-and-salt sign-in, every client's default, can be checked;
a key made without the option is a plain bearer key, hashed and never
read back, and works only where the client sends the password itself.
The account password works too, for a client set to password ("legacy")
authentication, but a key is better: revoking it in Settings signs that
app out and nothing else, and the account password never leaves the
browser. Token-and-salt sign-in against the account password itself is
not possible, since argon2id gives nothing to hash with.

A disabled account is refused like a wrong password. Every call that
errors carries `helpUrl`, pointing at Settings.

## What is served

Ids are Cineva's item ids: artists, albums and tracks share one id space,
so `getMusicDirectory` works at any level, and a music folder's id is the
library's. Playlist ids carry a `pl-` prefix.

| Group | Calls |
|---|---|
| System | `ping`, `getLicense` (always valid), `getOpenSubsonicExtensions` (`formPost`, `apiKeyAuthentication`, `transcodeOffset`, `songLyrics`) |
| Browsing | `getMusicFolders` (the music libraries the user may see), `getIndexes`, `getMusicDirectory` (a folder's artists, an artist's albums, an album's tracks), `getGenres`, `getArtists`, `getArtist`, `getAlbum`, `getSong`, `getArtistInfo`, `getArtistInfo2` (the biography and picture Cineva looked up, and starts looking up when the page is first asked for), `getAlbumInfo`, `getAlbumInfo2` |
| Lists | `getAlbumList`, `getAlbumList2` (random, newest, frequent, recent, starred, highest, alphabeticalByName, alphabeticalByArtist, byYear, byGenre), `getRandomSongs`, `getSongsByGenre`, `getTopSongs`, `getNowPlaying`, `getStarred`, `getStarred2` |
| Searching | `search2`, `search3`: every word of the query as a substring of the title, artist or album, names starting with the query first; an empty query lists everything |
| Playlists | `getPlaylists`, `getPlaylist`, `createPlaylist` (with `songId`s, or with `playlistId` to replace an existing one's songs), `updatePlaylist` (`name`, `songIdToAdd`, `songIndexToRemove`), `deletePlaylist`. The user's own only; a smart playlist is listed and played but its songs come from its rules, so adding or removing is error 50 |
| Media | `stream` (`format` mp3, opus, aac or ogg transcodes; `maxBitRate` picks the nearest step at or under it, and a cap the file exceeds transcodes to MP3 when no format is named; `raw` is the file itself; ranges work), `download` (a track's file, or a ZIP of an album or artist), `getCoverArt` (the item's or playlist's cover; `size` is accepted and ignored), `getAvatar`, `getLyrics`, `getLyricsBySongId` (timed lines where the file or its `.lrc` had them) |
| Annotation | `star`, `unstar`, `setRating`, `scrobble` |
| Users | `getUser` (oneself; others for an admin), `getUsers` (admin), `getScanStatus`, `startScan` (admin: an incremental scan of every music library) |
| Play queue | `getPlayQueue` answers that no queue is saved; `savePlayQueue` accepts and keeps nothing |
| Answered empty | `getBookmarks`, `getInternetRadioStations`, `getPodcasts`, `getNewestPodcasts`, `getVideos`, `getShares`, `getChatMessages` |
| Not served | Everything else (jukebox, sharing, user administration, HLS) answers error 70 |

A restricted library is absent from every call for a user it does not
name, and an item in one is not found.

### Stars are ratings

Cineva has star ratings (one to five) and no separate favourite flag, so:

- `star` rates the song, album or artist five stars; `unstar` clears its
  rating.
- `getStarred` and `getStarred2`, and the `starred` album list, are the
  items rated five. Their `starred` time is the item's own date added,
  since a rating keeps no time of its own.
- `setRating` is the rating itself, and `userRating` reports it.

So a client's hearts and Cineva's five stars are the same thing, both
ways; a four-star rating is not a star. music.md proposes a favourites
flag of its own, at which point `star` moves onto it.

### Plays

`scrobble` with `submission=true` (the default) counts a play of each song
for the user, as the web client's half-way rule would: the client says it
was played, so it was. `submission=false` is the now-playing report, which
registers the playback for the admin's Activity page and the web client's
"Continue here". `stream` registers a playback too. Neither opens the
play-history row the web client's player does; the count comes from the
scrobble.

## Left out of this version

See the Subsonic section of `BACKLOG.md`: per-app passwords (and so
token-and-salt sign-in), one device per app with its queue, a favourites
flag, the play queue calls, resized cover art, and the index-based queue
and playback-report extensions.
