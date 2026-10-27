# Music

Status: designed October 2026; stages 1 to 4 built (the server groundwork,
the client's structure and session queue, browsing, now playing), stage 5's
server side built (streamed, cached transcodes and play negotiation; the
client's two engines are still to do), and stage 6 built thin (the Subsonic
shim, `subsonic.md`, with API keys usable as app passwords; favourites
to follow). Music shipped in milestone 7
and is in daily use; this note is the overhaul of it: how
the library is browsed, how the queue and the now-playing screen work, how
tracks are played, how the client code is laid out, and a Subsonic-compatible
API so other music apps can use the server.

## The problem

Music works, and it was built album by album as needs came up. Five things
now stand in its way.

1. **The library view does not scale.** Opening a music library fetches
   every artist, album and track in one response (`GET
   /libraries/{id}/items`): 33,000 rows of 42 columns for the library this
   was measured on (204 artists, 2,022 albums, 29,534 tracks), each with its
   absolute path, decorated with three whole-library maps of the viewer's
   play state, play counts and ratings. The client then groups, detects
   compilations, filters and renders every shelf at once. Search is a
   full-table scan through a user-defined function, artist pages evaluate
   `LIKE` conditions over every track, and listing playlists evaluates
   every smart playlist in full.
2. **The queue is not a thing.** The music queue lives in one tab's memory:
   it is gone on reload, other devices cannot see it, "Play on..." does not
   offer it, and "Continue here" rebuilds the current album and loses the
   playlist, the shuffle and the repeat. There is no screen that shows what
   is up next across albums, nor any way to reorder or remove. The
   listening view shows one album's tracks and a row of discs.
3. **Playback waits for whole files.** A track is fetched and decoded to
   PCM in full before it sounds (a long FLAC is 100 MB decoded, three are
   kept), a transcode is encoded to completion before its first byte is
   served, and two requests for the same transcode write the same file.
   The transcode cache is never evicted, never notices a replaced file,
   and sits in the transcode directory, which is often RAM. Shuffle is a
   memoryless random pick that never ends, seeking to the start records a
   second play, and an admin's stop is ignored.
4. **The code is where it grew.** Everything but the player and the pages
   under `music/` is in `App.svelte` (6,273 lines): the library view, the
   listening view, the album page, the queue functions, the routing. Two
   buttons called "Queue" do different things. Nothing on a music screen
   knows about TV mode: no navigation groups, a track menu the arrows can
   leave, download `<select>`s that a D-pad fires by accident.
5. **Only our client can play it.** People have music apps they like, on
   phones, desktops and car head units. Nearly all of them speak the
   Subsonic API, which Navidrome, Gonic, Airsonic and Ampache serve.
   Cineva does not.

## Principles

- **The server does the work.** Listing, grouping, sorting, searching,
  queue keeping and format choice happen on the server, so the web client,
  the native apps and Subsonic clients all see the same library and the
  same queue, and a 30,000-track library costs a page at a time.
- **One queue per person per screen.** The queue stays with the session,
  as `devices.md` has it, and it is whole: order, shuffle, repeat, where in
  it, and where in the track. Anything that moves playback between
  screens moves all of that.
- **Every screen at phone width and with a D-pad.** New music screens
  follow `tv.md` from the start, not as a pass afterwards.
- **One track list, one card, one menu.** An album page, a playlist, a top
  tracks list, the Songs tab and the queue show tracks the same way with
  the same menu, so a fix lands everywhere.
- **Audio first, music second.** The queue, the engine, the now-playing
  screen and the Subsonic shim are the audio core that audiobooks and
  podcasts (milestones 7 and 8) will reuse: chapters, speed and precise
  resume are additions to them, not a second player.

## Part 1: Browsing the library

### What the client sees

A music library page has sub-tabs, as a video library has Recommended,
Library, Genres and Collections:

| Tab | What it shows |
|---|---|
| **Listen** | The rows: Continue listening (the queue, if one is saved), Recently played, Recently added, Most played, Playlists, a Genres rail. The shelves that exist today become these rows; nothing else from the old view survives here. |
| **Artists** | A grid of pictures (round, falling back to the newest cover) with name and album count; a letter index down the side (phone: a touch strip; TV: a column the D-pad enters); sort by name, recently added or most played; a search box narrowing the grid. Compilations appear as one entry, "Various Artists". The **artist rail** (the list of names down the left of today's view) stays, as a column beside every tab and beside an open artist's or album's page on desktop, so moving between artists is one click; it is fed by the paged artists listing with its own filter box, and on a phone it is the Artists tab's list layout. |
| **Albums** | A grid of covers with title, artist and year; the same letter index on the name sort; sorts by name, artist, year, recently added, recently played, most played; filter chips for genre, decade and compilations. |
| **Songs** | A paged list of every track, sorted by title, artist, album, recently added, most played or rating; a search box. |
| **Genres** | Tiles with a mosaic of four covers and an album count; a tile opens Albums filtered to the genre. |
| **Playlists** | The existing page, moved under the library. |

Pages are fetched as the grid scrolls (an `IntersectionObserver` near the
end asks for the next page), 60 at a time, and the letter index jumps by
asking for the page that starts at that letter. The 30-second poll and the
`items` event refetch the page in view, nothing more. The `?since=` delta
and its `X-Items-Mark` header stay for video and go unused by music.

**The album page** keeps its layout (cover, title, artist link, Listen,
Shuffle, Playlist, Download, the track list) and gains an information line
("2013 · 12 tracks · 41 min · FLAC 24-bit 96 kHz"), disc headings on
multi-disc albums, and a "by this artist" rail of the artist's other
albums. **The artist page** keeps `ArtistView`; its Play, Shuffle and Add
to queue stop fetching one album at a time (below). Tracks still have no
page: a track opens its album with the row highlighted.

The buttons are named by what they do, everywhere: **Play** (replaces the
queue and plays), **Shuffle**, **Play next** and **Add to queue** (insert
after the current track or append), **Add to playlist**, **Download**. The
"Queue" button that replaced the queue goes.

### API

Paged listings follow the admin Manage list: `?page=` and `?size=` (60
by default, 200 at most), returning `{items, total, page, size}`.

| Route | Parameters | Returns |
|---|---|---|
| `GET /libraries/{id}/artists` | `q`, `sort` (name, added, plays), `letter`, `page`, `size` | artists, plus `index: {"A": 12, "B": 30, ...}` on the name sort so the letter strip is drawn from counts |
| `GET /libraries/{id}/albums` | `q`, `artist`, `genre`, `decade`, `year`, `compilations`, `sort` (name, artist, year, added, played, plays, rating, random), `letter`, `page`, `size` | albums, with `index` on the name sort |
| `GET /libraries/{id}/tracks` | `q`, `genre`, `sort` (title, artist, album, added, played, plays, rating, random), `page`, `size` | tracks, each carrying `album` and `albumId` |
| `GET /libraries/{id}/genres` | | genres with album and track counts (today: track counts only) |
| `GET /libraries/{id}/search` | `q` | `{artists, albums, tracks}`, five, five and twenty, from the full-text index |
| `GET /items/{id}/tracks` | `shuffle` | every track under an artist or album, in play order, in one request: what Play, Shuffle and Add to queue send to the queue; a genre's tracks come from `/libraries/{id}/tracks?genre=`, a playlist's from its own endpoint |
| `GET /items/{id}/lyrics` | | a track's lyrics (`synced`, `text`), 404 when it has none |
| `GET /items/{id}` | | unchanged; an album's tracks are decorated for the viewer, an artist's albums now are too |
| `GET /items/{id}/artist` | | unchanged in shape; its queries use the new columns and indexes |

`sort=random` with `size=` is how "Shuffle library" and "Shuffle genre"
pick their tracks: the queue holds at most 1,000 entries, so a whole
library is never a queue, a random thousand of it is. Each row carries the
viewer's plays and rating and, for an artist or album, `childCount` (its
albums or tracks); `GET /libraries/{id}/genres` counts albums (`count`)
and tracks (`tracks`) for a music library.

The global `GET /search` keeps its flat 20 results for the top bar and
reads the same full-text index, so film and series search gets faster for
free. `GET /libraries/{id}/items` stays as it is for video and for
compatibility; the web client stops calling it for music.

### Schema (migration 0045)

Columns on `items`, filled by the scanner and the probe, all optional:

- `album` (tracks): the album title, so a track stands alone in a search
  result, a Songs row, the now-playing screen and a Subsonic `song`.
- `album_artist` (tracks): the album's artist, distinct from the track's
  own, so "Appears on" and compilations are a column, not a `LIKE` over
  the library.
- `compilation` (albums, 0/1): set from the `compilation` tag, a "Various"
  album artist, or two or more distinct track artists at the end of the
  album's scan (today the first track decides, so an untagged compilation
  hangs under one artist and every other artist is an empty row).
- `bitrate`, `sample_rate`, `bit_depth`, `channels` (tracks): for the
  information line, the quality badge on the now-playing screen, and the
  Subsonic `bitRate`, `samplingRate`, `bitDepth` and `channelCount` fields.
- `mbid` (artists, albums, tracks): the MusicBrainz artist, release and
  recording id, read from the tags Picard writes (`MUSICBRAINZ_ALBUMID`,
  `MUSICBRAINZ_TRACKID`, `MUSICBRAINZ_ARTISTID`) or kept when the
  enrichment looked the release up. It is the music **content key** for
  federation (`mb:release:<id>`, `mb:recording:<id>`, `mb:artist:<id>`), the
  Subsonic `musicBrainzId`, and what later lets the enrichment skip the
  search request.
- `sort_title` (artists, albums): "Beatles, The" for the letter index and
  Subsonic's `ignoredArticles`.

Indexes that the listings and sorts need and do not have:
`items (library_id, kind, title)`, `items (library_id, kind, added_at)`,
`items (library_id, kind, artist)`, `items (library_id, kind, year)`,
`items (updated_at)` (for `?since=`), `play_events (user_id, item_id)`.

Genres are written for albums as well as tracks (the union of the album's
tracks' genres), so the Genres tab and the `genre` filter list albums
without a join through tracks. The migration fills the album title, album
artist, compilation flag, sort key and album genres for existing
libraries; the file details, ids and lyrics arrive with the next full
rescan.

**Search as typed** is a word match: every word of the query must be a
substring of the title, the artist or (tracks) the album, folded like the
existing search (case and accents, so "bjork" finds Björk), with titles
that start with the query first. An FTS5 index was considered and left
out: the search tests pin substring semantics ("100%" must not find "1000
Ways", which token-prefix matching would), and FTS5 over `items` needs a
stable integer rowid the table does not have. Measured on the 29,534-track
library, the word match costs 40 ms over the albums and 130 ms over the
tracks, so it is not the problem it was assumed to be; FTS5 stays an
option in `BACKLOG.md` if a bigger library measures slow.

### Scanner

`scanMusic` stays one pass but stops being serial and untransacted:

- Probes run through the probe pool the TV scanner uses (BACKLOG, "The
  music scanner still reads tags serially").
- One transaction per album: its tracks' upserts, genres and the album
  row, with the compilation decision and the album's genres made when the
  folder is complete.
- MusicBrainz release lookups leave the scan loop. After the scan, a
  background pass (in line for a CLI scan, which has nothing else to do)
  looks up the albums missing a cover or a year and the ones the scan
  found untitled tracks in, at the 1-per-second limit as artist lookups
  already do, so a first scan of a big untagged library finishes in
  minutes and fills in over the following hours. It stores the release
  `mbid`, and matches canonical titles by disc and position (BACKLOG:
  first disc only). An album missing only its id is not looked up: on
  the measured library that would have been 1,893 requests for ids
  alone. A miss is recorded in `album_lookups` and retried after 30 days.
- Disc folders match `CD1`, `Disc 2`, `disk3`, `CD 1 - Live` and `Disc
  One`, not only the bare forms.
- The probe keeps bitrate, sample rate, bit depth and channels, and reads
  embedded lyrics (`LYRICS`, `USLT`, `UNSYNCEDLYRICS`) and an `.lrc`
  sidecar into `lyrics (item_id, synced, text)` for the now-playing
  screen and the Subsonic `getLyricsBySongId`. No online lyric lookup.

## Part 2: The queue and now playing

### The queue

The queue is on the session, as today (`sessions.queue`), and grows from
ids and an index into the whole of what a screen is playing:

```json
{
  "entries": [{"id": "…", "ord": 0}, {"id": "…", "ord": 7}],
  "index": 3,
  "shuffle": true,
  "repeat": "off",
  "positionSecs": 41.5,
  "source": {"kind": "album", "id": "…", "title": "AM"},
  "updatedAt": "2026-10-25T10:12:00Z"
}
```

`entries` are in play order; `ord` is each entry's place in the unshuffled
order, so turning shuffle off restores it and turning it on shuffles only
what has not played. `source` is what the queue was made from (an album,
artist, playlist, genre, library or search) for "Playing from AM" and for
"Add this to a playlist". The video queue uses the same record; a queue
holds tracks or videos, never both, and starting one kind stops the other,
as it does now.

The client owns the queue in memory and writes it whole (`PUT
/auth/sessions/current/queue`, debounced a second) on every change of
order, shuffle or repeat; `positionSecs` is written by the 15-second
heartbeat (`POST /items/{id}/progress`), which already carries it, so a
reload or another device is never more than 15 seconds behind. At most
1,000 entries. On load the client fetches its own queue and restores it
paused at its position, so a reload keeps the music and `/now-playing`
is a page that exists whenever a queue does. The Subsonic `getPlayQueue`
and `savePlayQueue` read and write the same record (Part 5), so a phone
app's queue is a device's queue in Cineva.

**Between devices.** "Continue here" and "Play on..." carry the whole
record. The device that was playing *stops* (its bar closes with
"Continued on Living-room TV") rather than sitting paused for ever; its
queue stays saved, so "Continue from..." can bring it back. The handoff
event carries the position. Takeover from a device that cannot be pushed
(a Subsonic client has no event stream) goes with the last heartbeat, as
`devices.md` already allows.

**Next-track logic stays in the client**: the server does not decide what
plays next in music as it does for episodes, because gapless scheduling
needs the next track in hand before the current one ends. The server
holds the queue; the client walks it.

### Now playing

`/now-playing` replaces `/listening`. It is one screen in three sizes:

- **Phone**: full screen, opened by tapping the mini bar, closed by
  swiping down or Back. The cover fills the width; under it the title,
  artist and album (both links), the stars, the timeline with times, the
  controls (shuffle, previous, play, next, repeat), and a row of small
  actions: volume, quality, Play on..., the room's controls, the track
  menu. A tab bar at the bottom switches between **Now playing**, **Up
  next** and **Lyrics**.
- **Desktop**: the same, as a panel on the right of the screen over the
  page, so browsing goes on while it is open; Up next is a list beside
  the cover rather than a tab.
- **TV**: full screen; the volume goes (the television has its own) and
  the player claims the remote as the video player does: OK plays and
  pauses, left and right skip within the track, up and down move between
  the controls and the Up next list, Back closes it.

**Up next** is the queue: the current track at the top, what follows
below, "Playing from AM" over it with the source as a link. Rows have the
one track list's menu plus Remove and, where there is no pointer, Move up
and Move down; with a pointer, drag to reorder. Over the list: Clear,
Shuffle, Save as playlist. A tap on a row jumps to it. Rows that have
played are above the current one, dimmed.

**Lyrics** shows the track's lyrics when it has any, scrolling with the
music when they are timed, otherwise as text; the tab is absent when
there are none.

The **quality badge** says what is playing ("FLAC 24/96", "Opus 128k")
from the play response, and opens the quality choices; a locked
`music-quality` shows the others as set by the administrator (BACKLOG:
the lock is not enforced in the menu).

The **mini bar** stays, and it sits at the top of the screen under the
top bar, not the bottom (changed October 2026 while the old view was
still in use; the page's sticky things make room for it through
`--music-bar`): on a phone a compact strip
(cover, title, artist, play/pause, next) that opens the screen when
tapped; on desktop the current bar with its stars, timeline and volume,
plus a queue button with the count; on TV a strip that appears when
anything plays and takes the first press of OK. The per-track download
`<select>` leaves the bar for the track menu.

**Media Session** gets the album name and artwork at more than one size,
and the handlers stay as they are.

### Preferences

`music-quality` stays device-scoped and lockable, and is now resolved
by the server at play (Part 3). New registry entries, both device scoped:

| Key | Values | Default | What it does |
|---|---|---|---|
| `gapless` | on, off | on (off where the device reports little memory, see Part 3) | Decode the next track ahead and join it exactly |
| `music-start` | now-playing, bar | bar | Whether Play opens the now-playing screen or just starts the bar (replaces the Listen/Queue distinction) |
| `music-bar` | top, bottom | top | Where the mini bar sits (person scoped: it follows the account). Settings shows these under their own Music section, with `music-quality`. |

## Part 3: Playing a track

### Server

Built (October 2026): everything below, with one deviation. The cache
key uses the file's size and modification time as planned; the cache
directory is `<database's directory>/cache/audio`, since the server had
no other notion of a data directory, and `dlcache` under the transcode
scratch is gone. AAC transcodes are written as fragmented MP4 so they
play while they grow. The repeat-play window is 30 seconds per device
and track.

- **`POST /items/{id}/play` negotiates for tracks** as it does for video.
  It takes the client's capabilities (the codecs it decodes), resolves the
  device's `music-quality` (so a lock holds, and a native app need not
  know the preference exists), and answers with the stream URL including
  any `format` and `quality`, the format and sample rate that will play
  (for the badge), and the duration. The registry entry records the real
  mode and quality rather than "direct / original" for every track.
- **Transcodes stream while they encode.** The encoder writes to the
  cache file and the response reads behind it, chunked, from the first
  packet; a request for a finished file is served with ranges as now. One
  encode per key at a time (singleflight): a second request for the same
  track and format reads the same growing file. The key includes the
  file's size and mtime, so a replaced file is re-encoded.
- **The cache moves and is bounded.** `dlcache` leaves the transcode
  directory (often RAM) for `<data>/cache/audio`, capped by
  `transcode.audio_cache_mb` (2,048 by default;
  `CINEVA_TRANSCODE_AUDIO_CACHE_MB`), least recently used first, in
  `cineva.example.yaml` with the rest.
- **Seeking is not a new play.** Play is recorded once per track per
  queue position; seeking, including to the start, and "previous" within
  the first three seconds, do not post `play` again.
- **An admin's stop stops music.** The 409 on the heartbeat ends playback
  in the client, as the video player does.
- **The heartbeat updates the queue position** (Part 2).
- **ZIP downloads with a format** prepare their transcodes through the
  same singleflight and cache, four at a time rather than serially (the
  prepare-then-fetch job stays for milestone 11).

### Client

The engine is split in two behind one interface (`music/player/engine.ts`:
`load(track, offset)`, `play`, `pause`, `seek`, `prepare(next)`, `onEnded`,
`position`), with the queue store choosing which plays each track:

- **The element engine** plays through an `<audio>` element wired into the
  Web Audio graph (`MediaElementAudioSourceNode`, so volume and any later
  effects are the same code). It starts within a second from a range
  request, seeks with ranges, costs almost no memory, and preloads the
  next track in a second element. The join between two elements is not
  sample-accurate: a gap of tens of milliseconds, inaudible between
  separate songs, audible between the movements of a live album.
- **The buffer engine** is today's: the next track decoded to PCM ahead of
  time and started at the exact sample the current one ends on. Gapless,
  and expensive in memory and in time to first sound.

The rule: the first track of any play action, every seek, and every track
reached by shuffle or by jumping start on the element engine, so sound
starts at once. A track that follows the previous one in its own album,
with `gapless` on, is decoded ahead and joined by the buffer engine; the
decoded cache holds the current and the next (not three), and is dropped
whenever the element engine takes over. With `gapless` off, or where
`navigator.deviceMemory` is under 4 GB, the element engine plays
everything and album joins get the small gap. WebCodecs `AudioDecoder`
would allow chunked decoding with the buffer engine's precision and the
element engine's memory; it is not yet in every browser we need, and is
the noted path once it is.

This is the one part of the note to prototype before committing: an
afternoon with both engines on a live album and a phone decides whether
the rule above is right or the buffer engine should go altogether.

## Part 4: Where the code lives

`App.svelte` keeps routing and mounting; the music screens move out:

```
ui/client/src/music/
  library/
    MusicLibrary.svelte     the tabs and the page header
    ListenTab.svelte        the rows (ListeningRows today)
    ArtistsTab.svelte, AlbumsTab.svelte, SongsTab.svelte, GenresTab.svelte
    LetterIndex.svelte      the A-Z strip or column
    AlbumGrid.svelte        the cover grid, paged, with the hover and focus play button
  AlbumPage.svelte          out of the generic detail page
  ArtistView.svelte         as now, on /items/{id}/tracks for its whole-artist actions
  TrackList.svelte          the one track list: disc headings, Stars, TrackMenu, virtualised above 200 rows
  TrackMenu.svelte, Stars.svelte, AddToPlaylist.svelte, Mosaic.svelte
  PlaylistsView.svelte, SmartEditor.svelte, smart.ts
  player/
    queue.svelte.ts         the queue store: entries, index, shuffle, repeat, position, source; load and save; handoff
    engine.ts               the engine interface, ElementEngine, BufferEngine
    mediaSession.ts
    NowPlaying.svelte       the screen in its three sizes
    MiniBar.svelte
    UpNext.svelte
    Lyrics.svelte
  actions.svelte.ts         as now, plus the one trackTime (App.svelte's copy goes)
```

`lib/queue.ts` becomes the shared session-queue client for video and
music (one record, one save path), with localStorage kept as the mirror
for first paint. `lib/api.ts` stays the single API module. Routes:
`/libraries/<name>` opens Listen; `/libraries/<name>/artists`, `/albums`,
`/songs`, `/genres`, `/playlists`; `/libraries/<name>/<artist>` and
`/<artist>/<album>` as now; `/now-playing`. A deep link resolves titles
through `/libraries/{id}/search`, not by fetching the library.

TV mode, from `tv.md`, on every music screen: `data-nav-group` on each
row, grid and the letter index, `data-nav-id` on every card so Back
returns to it, `primary()` knowing the music page's Play, the track menu
a dialogue scope (Back closes it), every download `<select>` replaced by
a menu, card play buttons visible on focus as well as hover, and the
now-playing screen claiming the keys. `tv.md` gains a Music section.

## Part 5: A Subsonic-compatible API

### Why Subsonic

The Subsonic API (1.16.1) with the OpenSubsonic extensions is what
Navidrome, Gonic, Airsonic-Advanced and Ampache serve and what Symfonium,
DSub, Ultrasonic, play:Sub, Substreamer, Feishin, Supersonic, Sonixd,
Tempo, Audinaut and the car and watch apps speak. Serving it lets people
keep the app they like and reach Cineva's music from a car head unit. It
is a shim: nothing in Cineva changes shape for it, and the native apps in
`clients/` still use `/api/v1`.

### Where it lives

`internal/subsonic`, mounted at `/rest/` (`/rest/ping.view` and
`/rest/ping`), `subsonic.enabled` in the config (on by default,
`CINEVA_SUBSONIC_ENABLED`); off, the routes answer 404. It is documented
by the Subsonic specification and `docs/subsonic.md` (which calls are
served, which answer empty, which error), not `openapi.yaml`, which
describes `/api/v1`; `spec_test.go` checks `/api/v1` routes only and is
unaffected. GET and form POST (the `formPost` extension), `f=xml` (the
default), `json` and `jsonp` with `callback`, the `subsonic-response`
envelope with `status`, `version` "1.16.1", `type` "cineva",
`serverVersion` and `openSubsonic: true`.

### Signing in

Subsonic clients send `u` with either `p` (the password, plain or
`enc:`-hex) or `t` and `s` (an MD5 of the password and a salt). Both need
a password the server can read back, and Cineva keeps only argon2id
hashes. So:

- **API keys as app passwords** (built October 2026, migration 0047, in
  place of the separate app passwords first proposed). A key made in
  Settings with "Usable as a password in music apps" keeps a sealed copy
  (AES-256-GCM under a key made once per server and kept beside its
  identity in `server_identity.secrets_key`), so an app's token sign-in
  (`t` = md5(key + `s`)) is checked against the user's such keys, and the
  key typed as `p` is accepted by its hash. The account password itself
  never does token sign-in; typed as `p` it works for a client set to
  password ("legacy") authentication. Failures go through the sign-in
  throttle, and revoking the key signs that app out alone.
- **API keys.** OpenSubsonic's `apiKeyAuthentication` (`apiKey=` with no
  `u`) maps onto Cineva's existing API keys, which are the better choice
  for any client that supports it. Error 42 for a mechanism we do not
  serve, 43 for conflicting parameters (`u` with `apiKey`), 44 for an
  unknown key, 40 for a wrong user or password, with `helpUrl` pointing at
  Settings.

Each app password, and each API key used this way, is a **device**: it
appears on the user's Devices page and the admin's as "Symfonium on
Pixel" (from `c` and the app password's name), holds a session, carries
the queue, and shows up in Activity while it streams. Library access is
the user's: a restricted library is absent from `getMusicFolders` and
everything under it; `startScan` and `getUsers` need an admin.

### What is served

Ids are Cineva's item ids (artists, albums and tracks share one id space,
so `getMusicDirectory` works for either level); playlists are `pl-<id>`.

| Group | Calls |
|---|---|
| System | `ping`, `getLicense` (valid, no expiry), `getOpenSubsonicExtensions` (`formPost`, `apiKeyAuthentication`, `transcodeOffset`, `songLyrics`, `indexBasedQueue`) |
| Browsing | `getMusicFolders` (the music libraries the user sees), `getIndexes`, `getMusicDirectory` (the artist, album, track tree, not real folders), `getGenres`, `getArtists`, `getArtist`, `getAlbum`, `getSong`, `getArtistInfo`, `getArtistInfo2`, `getAlbumInfo`, `getAlbumInfo2`, `getSimilarSongs`, `getSimilarSongs2` (the artist's and genre's tracks; no acoustic similarity), `getTopSongs` |
| Lists | `getAlbumList`, `getAlbumList2` (random, newest, frequent, recent, starred, alphabeticalByName, alphabeticalByArtist, byYear, byGenre), `getRandomSongs`, `getSongsByGenre`, `getNowPlaying`, `getStarred`, `getStarred2` |
| Searching | `search2`, `search3` (the full-text index) |
| Playlists | `getPlaylists`, `getPlaylist`, `createPlaylist`, `updatePlaylist`, `deletePlaylist`; smart playlists are read-only to them |
| Media | `stream` (`format`, `maxBitRate`, `timeOffset`, `estimateContentLength`), `download`, `getCoverArt` (`size` resizes, cached), `getAvatar`, `getLyrics`, `getLyricsBySongId` |
| Annotation | `star`, `unstar`, `setRating`, `scrobble` |
| Play queue | `getPlayQueue`, `savePlayQueue`, `getPlayQueueByIndex`, `savePlayQueueByIndex` (the session queue) |
| User | `getUser`, `getUsers` (admin), `getScanStatus`, `startScan` (admin) |
| Answered empty | `getBookmarks`, `getInternetRadioStations`, `getPodcasts` (until milestone 8), `getVideos`, `getShares`, `getChatMessages` |
| Error 70 or not implemented | `jukeboxControl`, `createUser` and friends, `createShare`, `hls` |

**Annotation maps onto Cineva's own state**, so the web client sees it:

- `scrobble` with `submission=true` counts a play (the client says it
  played; the half-way rule is for our own player) and closes the play
  event; `submission=false` is the now-playing report, which registers
  the playback so Activity and "Continue here" see it.
- `setRating` is the 1-to-5 star rating; albums and artists become
  ratable too (the table allows it, the API did not).
- `star` needs a favourite flag Cineva does not have. Mapping it to five
  stars would be lossy. So `favourites (user_id, item_id, added_at)` for
  tracks, albums and artists, with a heart in the track menu and on album
  and artist pages, a Favourites row under Listen, a smart-playlist rule
  ("is a favourite"), and the importer's favourites from Plex and
  Jellyfin landing here rather than in a playlist.

**Streaming** goes through the same play registration, negotiation and
cache as the web client: `stream` registers a playback for the device
with the client's name, serves the original or the transcode (`format`
mp3, opus, aac; `maxBitRate` picks the quality step), honours ranges and
`timeOffset`, and ends the playback when the connection closes. A locked
`music-quality` applies to these devices too.

**Testing**: table-driven tests against the XML and JSON envelopes for
every served call; a compatibility table in `docs/subsonic.md` filled in
by trying Symfonium, DSub, play:Sub, Feishin, Supersonic and Tempo
against a real library.

## Storage summary

- 0045 (built): `items.album`, `album_artist`, `compilation`, `bitrate`,
  `sample_rate`, `bit_depth`, `channels`, `mbid`, `sort_title`; the
  indexes above; `lyrics`; `album_lookups`.
- 0046 (built): `sessions.queue` grows to the record in Part 2 (the
  existing `{itemIds, index}` is rewritten as `entries` with `ord` in
  order, `repeat` off, no source, and still read if a client sends it).
  `GET /auth/sessions/current/queue` returns the device's own, resolved.
- 0047: `app_passwords`, `server_identity.secrets_key`, `favourites`.

## Staging

Each stage ships on its own and leaves the existing client working.

1. **Groundwork (server), built.** Migration 0045, the scanner changes,
   the probe fields, the paged listings, `/items/{id}/tracks`,
   `/items/{id}/lyrics`, `/libraries/{id}/search`. The old library view
   keeps working on `/libraries/{id}/items`. One full rescan of each music
   library brings the file details, ids and lyrics.
2. **Structure (client), built.** The music screens and functions out of
   `App.svelte` into `music/`; the queue store on the session record
   (0046); the one `TrackList`; the button names. Built together with
   stage 3 rather than before it: the old shelves depended on the
   whole-library fetch that stage 3 removes, so moving them unchanged and
   then replacing them would have been the same work twice.
3. **Browsing, built.** The tabs, the letter index, the grids, the album
   page, the Listen rows, the artist rail as a persistent column. The
   shelves are gone. Deep links resolve through the library search rather
   than a listing. Playlists keep their own route (`/playlists`) with a
   tab that leads there.
4. **Queue and now playing, built.** `/now-playing` in its three sizes, Up
   next, Lyrics, handoff carrying the whole queue, restore on reload, Media
   Session, the TV pass over every music screen, `tv.md`. The engine
   itself is still the Web Audio decoder (stage 5); the `gapless`
   preference switches the decoding ahead off.
5. **Engine.** Streamed transcodes with singleflight and the bounded cache,
   play negotiation for tracks, seek without re-play on the server (built,
   October 2026); the two client engines behind one interface after the
   prototype, the client's handling of the admin stop (to do).
6. **Subsonic.** App passwords and Connected apps in Settings, favourites,
   the shim and `docs/subsonic.md`, the compatibility table.

`BACKLOG.md` gains a Music overhaul section as each stage lands, and
`openapi.yaml` changes in the same commits as the handlers.

## Open questions

- **Gapless on phones.** The element engine with its small gap, or the
  buffer engine at the memory cost: the prototype in Part 3 decides, and
  the `gapless` preference lets a person choose anyway.
- **Favourites** as a new flag (proposed) or star mapped onto a rating.
- **The artist rail** stays (decided October 2026): it is wanted beside
  an open artist or album, not only beside the shelves.
- **Lyrics** are in scope as read-from-tags only; they can be dropped
  from stage 1 and 4 without affecting anything else.
- **Subsonic on by default**, or off until an admin turns it on from
  Integrations. Decided October 2026: wanted quickly and thin, so the
  first version shims the calls onto what exists (API keys and the
  account password for sign-in, token auth answered with error 41, star
  mapped onto a five-star rating) and the app passwords, device-per-app
  and favourites follow.
- **Crossfade** is not in this design (the buffer engine could do it; the
  element engine could approximate it); it goes on the backlog unless
  wanted now.
