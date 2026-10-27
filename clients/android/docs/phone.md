# The Android phone app

Status: designed October 2026; stage 1 (the skeleton and streaming) built
the same month and run in the emulator against a scratch server; stages
2 to 5 to follow. Music first, offline from the start, native. Video
follows as a later stage of the same app, and a TV app is a separate
decision for later.

## Why an app

The web client already works on a phone as a PWA. What it cannot do is
what people want from a phone with music on it: play in the background
with the lock screen, Bluetooth and the car in charge, keep albums on the
phone for a flight or a signal-free train, and start in under a second
from a cold launch. Those are what this app is for. Everything else
follows the web client, screen for screen, so the two feel like one
Cineva.

## Principles

- **The server decides, the app shows.** Listings, search, the play
  decision, the queue and the per-user state are the server's, through
  `/api/v1`, exactly as for the web client. The app adds a local copy for
  when the server is out of reach, never a second set of rules.
- **An app is a device.** It signs in like a browser, appears on the
  Devices page by name, holds the session queue, and takes part in
  Continue here and Play on. What it has downloaded is known to the
  server (`server/docs/offline.md`).
- **Take your music with you.** The iPod idea, decided 26 October 2026:
  not downloads one album at a time, but a plan for what lives on the
  phone, kept in step on its own, and manageable from the computer as
  iTunes managed an iPod. Synced music is the phone's library, first
  class; streaming is what happens for the rest when there is a signal.
  See "Sync" below.
- **One stack, no surprises.** Kotlin, Jetpack Compose with Material 3,
  Media3 for playback and the media session, Room for the local copy,
  OkHttp and kotlinx.serialization for the API, Coil for artwork,
  WorkManager for downloads, Hilt for wiring. All Apache-2.0, so the app
  can be AGPL-3.0-or-later like the server. No Firebase, no analytics,
  no Google Play services dependency.
- **British English in the UI; no emoji; Lucide-style line icons.**

## The bar

The app has to be one of the nicest-looking and easiest music apps
there are: good enough that someone leaves Plexamp or Spotify for it
(decided October 2026). That is a design job as much as an engineering
one, so the look is settled here and every screen is judged against it
with screenshots before a stage is called done.

### The mockups are the spec

Five screens were drawn and agreed on 26 October 2026 (Home, Library,
Album, Now playing, Search) at https://claude.ai/artifact/6nFQZbF6tRDD9bm4zjWLRY,
and their sources are kept in `docs/mockups/*.dc.html`: plain HTML with
the exact sizes, spacing, weights and colours, which is what a screen is
built to and judged against. Where this text and a mockup disagree, the
mockup wins. The measurements that matter: ground `#0e1116`, raised
strip `#1b2029`, text `#e8ebf0`, muted `#8a93a3`, hairlines `#1a1f28`;
Inter at 22/800 for a screen title, 17/700 for a row heading, 13/600 for
a cover's name, 12 for its sub-line, 11 uppercase with 0.08em tracking
for a section label; covers at 132 in a row, thirds edge to edge in the
grid with 12 between, 8 to 10 radius; the album cover 214 centred, now
playing's 334; the mini player a 56-high floating strip with a 2 px
progress hairline; the bottom bar 24 px icons over 11 px labels, the
active one in the art colour, no pill; Play a 46-high pill filled in the
art colour, Shuffle and Download quiet beside it; now playing's play
button 76 in white; 44 or 48 for every other target.

### Look: art-led, dark

- **The artwork is the interface.** Covers are large and sharp, the
  now-playing screen is the cover with the controls laid over a blurred,
  darkened copy of it, and the accent colour of now playing, the mini
  player and the notification is pulled from the playing cover (Palette
  on the image, cached per album), so the app changes colour with the
  music as Plexamp does.
- **Dark by default**, light as a choice, both from Cineva's own theme
  tokens (`--bg`, `--panel`, `--text`, `--muted`, `--accent` and the rest)
  mapped onto a Material colour scheme, so the phone and the web client
  are one Cineva. The art colour replaces the accent only where music is
  playing; the rest of the app stays on the theme.
- **Type and space**: one typeface (Inter, as the web client), a scale
  with real contrast between a heading and a caption, generous padding,
  no dividers where spacing does the job, no outlined cards.
- **Motion that explains**: the now-playing screen slides up from the
  mini player and the cover grows into place; a tapped album opens with
  its cover travelling; lists fade in rather than pop. Short, standard
  easing, nothing decorative, honours reduced-motion.
- **Pointer-free**: every target is at least 48 dp; the most used
  actions are where a thumb rests, at the bottom and the right.

### Easy: the rules

- **Carrying on is zero taps.** Home opens on "Pick up where you left
  off": the queue or the last album as one big card with Play, then
  Recently played, Recently added, Most played and playlists.
- **Anything in three taps.** Library tab, artist, album; or Search, type,
  result. No menus to find Play: it is on every artist, album, playlist
  and genre header, with Shuffle beside it.
- **Long press is the menu.** Hold a track or album for Play next, Add to
  queue, Add to playlist, Download, Go to artist or album, with the
  cover and title at the top so it is clear what is being acted on.
- **Swipe, do not hunt.** Swipe the mini player up for now playing, down
  to close it, sideways for next and previous; swipe a queue row away to
  remove it; pull to refresh.
- **The lock screen, Bluetooth and the car are first-class**: the
  notification shows the cover and the art colour, and every control the
  screen has works from them.
- **Nothing stalls.** Lists paint from what is cached and fill in; a play
  starts within a second on Wi-Fi; a download shows progress per track;
  offline shows the same screens with what is not here dimmed.
- **No letter strip**, no hamburger menu, no settings cog on every page:
  Settings lives behind the avatar at the top right.
- **Words**: British English, short, no jargon ("Download" not
  "Sync", "Play next" not "Enqueue").

## Where it lives

`clients/android/app/`, a Gradle project with the wrapper committed, one
module to start, built and run in the container under `clients/android/
sdk/` (see the README there). Package `com.garybowers.cineva`, application
id the same, minimum Android 9 (API 28), target 35.

```
app/src/main/kotlin/com/garybowers/cineva/
  api/        the API client: one class per area, hand-written against docs/openapi.yaml
  data/       Room: items, playlists, downloads, pending state events; repositories
  player/     Media3: the playback service, the queue, the server's play decision
  downloads/  WorkManager jobs, file storage, the device's download list
  sync/       library refresh, state sync, the events stream
  ui/         Compose screens, navigation, theme
```

The API client is hand-written, not generated: the music subset is a few
dozen calls, generated Kotlin from the spec is verbose and its models
would fight Room's, and a contract test keeps it honest instead: every
path the client uses must exist in `server/docs/openapi.yaml`, checked by
a JVM test that reads the spec, as `spec_test.go` does for the server.

## Signing in

A server address, a username and a password (or an API key made for
apps, which the Settings page offers for exactly this), checked with
`POST /auth/login` with `bearer: true` and the app's device description,
giving a session token kept in the Android keystore. Several servers can
be saved and switched between (the family's, later a nebula's). The
"Who's watching?" picker with PINs is a later stage: the first version
is one account per app install.

## Screens

Bottom navigation: **Listen**, **Library**, **Search**, **Sync**, with
the mini player above it and the now-playing screen over everything.

| Screen | What it shows | From |
|---|---|---|
| Listen | Continue listening (the queue), Recently played, Recently added, Most played, Playlists | `/libraries/{id}/listening`, `/home`, `/playlists` |
| Library | Artists, Albums, Songs, Genres as tabs; sorts and filter chips; a library picker when there is more than one. No letter strip: at phone size it is too small to be a target, and Search does the job (decided October 2026) | the paged listings |
| Artist | picture, biography, Play and Shuffle, albums, Appears on, top tracks | `/items/{id}/artist`, `/items/{id}/tracks` |
| Album | cover, information line, tracks with disc headings, Play, Shuffle, Add to queue, Download, ratings | `/items/{id}`, `/items/{id}/tracks` |
| Playlist | the user's, manual and smart; reorder and remove on manual ones | `/playlists/{id}` |
| Search | artists, albums and tracks as typed; local when offline | `/libraries/{id}/search` |
| Now playing | cover, title, artist and album links, stars, timeline, controls, quality badge, Up next (reorder, remove, jump, Save as playlist), Lyrics (scrolling when timed), Play on... | the queue record, `/items/{id}/lyrics` |
| Sync | "Your music, with you": what is on the phone and whether it is up to date, what is syncing now, the plan (everything, or chosen artists, albums, playlists and genres, new music, fill the spare room), and how (quality, Wi-Fi only, while charging). See "Sync" | the device's sync plan on the server, the local table |
| Settings | streaming quality on Wi-Fi and on mobile data, sync quality, gapless, the spectrum behind the cover, theme (the server's themes), the account and the server | the preference registry where a key exists (`music-quality`, `gapless`, `theme`) |

Theming (decided 26 October 2026): the server's themes, not the
phone's. The app reads `GET /themes` and the user's `theme` preference
and builds its colour scheme from the chosen theme's tokens at runtime,
custom themes and background pictures included, with the holiday themes
switching themselves on as the web client's do; Midnight's values are
only the fallback before the first fetch. The art colour from the
playing cover replaces the accent while music plays, nothing else.

## Playback

- Media3's ExoPlayer inside a `MediaSessionService`: background play,
  the lock screen, notification controls, Bluetooth buttons, audio focus,
  becoming-noisy, and later Android Auto, all from the one session.
- Every play starts with `POST /items/{id}/play` and the app's
  capabilities (the codecs ExoPlayer decodes: FLAC, MP3, AAC, Opus,
  Vorbis), so the server's `music-quality` and any admin lock decide
  whether the original or a transcode plays, and the badge shows the
  answer. On mobile data the app sends the mobile-data quality instead.
- Gapless: ExoPlayer joins tracks without a gap where the files carry
  gapless metadata (MP3 and AAC from a proper encoder, FLAC always, the
  server's Opus transcodes always), so an album plays as one.
- **The spectrum** (decided 26 October 2026): a Winamp-style analyser
  behind the now-playing cover, from a Media3 audio-processor tap on the
  player's own output (no microphone permission; works for local files;
  audio offload off so it runs) through a small FFT into about 24
  log-spaced bands with slow decay and falling peak caps, drawn in the
  art colour at low opacity between the blurred backdrop and the sharp
  cover. Off when now playing is closed, still under reduced motion,
  switchable in Settings.
- The queue is the session queue record: changes are written with `PUT
  /auth/sessions/current/queue` (debounced), the heartbeat carries the
  position, and a reload or another device restores it. Continue here
  and Play on arrive over the events stream (`/events`, server-sent) and
  the app answers as the web client does.
- Downloaded tracks play from the phone, even when online, through a
  local data source; the server is still told (`play`, progress), or the
  event is queued for the next sync when it cannot be.

## Sync

The fourth tab, and the reason the app exists. Its mockup is
`docs/mockups/Sync.dc.html`.

- **A plan, not a pile of downloads.** The phone has a sync plan: either
  **Everything**, or **Chosen** artists, albums, playlists (smart ones
  included, re-evaluated at each sync) and genres, plus **New music**
  (anything added in the last N weeks) and **Fill the spare room** (music
  you have not heard, up to a free-space floor, the iPod's autofill). The
  plan says how, too: quality (Opus 128k by default, original on
  request), Wi-Fi only, while charging. The plan lives on the server
  against the device (`offline.md`, "Sync plans"), so the web client's
  Devices page shows and edits it as iTunes managed an iPod, and a new
  phone signing in can adopt an old one's plan.
- **Kept in step on its own.** A sync runs when the plan changes, when
  the library changes (the `items` event, or the next foreground), and on
  a schedule under the plan's conditions. The server works out the
  manifest (what should be on the phone, with sizes), the app fetches
  what is missing through the download jobs and deletes what has left
  the plan, and reports what it holds. The Sync screen says where it is:
  up to date, syncing (with what and how much is left), waiting for
  Wi-Fi or a charger, or short of room.
- **Synced music is the library.** Browsing and search read the local
  copy always; a track on the phone plays from the phone even online. A
  track not on the phone streams when there is a signal and is dimmed
  with a small cloud glyph when there is not; a tap on it offline offers
  to add its album to the plan. The green tick on a track means it is
  here.
- **The one-off case** still exists: hold an album and choose "Keep on
  this phone", which adds it to the plan as a chosen album, so there is
  one mechanism, not two.
- Files live in the app's private storage, named by item id and profile,
  with the artwork beside them; Room holds the table; WorkManager runs
  the fetches with ranges, resumable.
- **The library copy**: Room mirrors the artists, albums and tracks of
  the libraries the user sees, pulled by pages at first sign-in and
  refreshed by `?since=` and the ids list when the `items` event or a
  foreground return says so. Browsing and search read Room always, so a
  lost connection changes nothing on screen; what is not downloaded is
  dimmed with its Play disabled when offline.
- **State**: progress, played, ratings and play counts observed offline
  go into a pending-events table and are sent with `POST /sync/state` on
  reconnect; the answer updates Room with what other devices did.
- **Removed access**: the manifest can say a track is revoked; the app
  deletes the file and says why on the Sync screen.

## Testing

- JVM unit tests for the queue logic, the sync merge, the download state
  machine and the API contract test against the spec.
- Instrumented tests and screenshots in the container's emulator against
  a scratch Cineva on the host (`10.0.2.2`), run by the agent building
  each stage; a real phone for audio focus, Bluetooth and the car, which
  an emulator cannot judge.
- `make android` builds the debug APK in the container; releases are
  signed APKs on GitHub Releases alongside the server's, with F-Droid and
  Play as later questions.

## Staging

1. **Skeleton and streaming**: the project, sign-in with a bearer token,
   the Library tabs and Search on the paged listings, Album and Artist,
   now playing with ExoPlayer streaming through the play decision, the
   media session and notification, the mini player. Needs the server's
   `bearer` sign-in (offline.md stage 1).
2. **The look, and queue parity** (built, see "As built: stage 2"):
   every stage 1 screen redrawn to the mockups (the server's theme
   tokens, the art colour, Inter, the now-playing sheet with its motion,
   long-press menus, swipes, the home card), then the session queue
   record, Up next, Continue here and Play on over the events stream,
   playlists, ratings, lyrics, Listen. Judged by screenshots against the
   mockups before it was called done.
2b. **Mixes and playlist adding**: built, see "As built: stage 3".
3. **Sync**: the plan and its screen, the manifest and the jobs, local
   playback, the Room library copy and refresh, pending state and
   `/sync/state`, the device's list on the server and the Devices page
   editing the plan. Needs offline.md stages 1 to 3 (sync plans).
4. **Polish**: Android Auto, the Who's watching picker with PINs, several
   servers, widgets, the holiday themes, a release build and signing.
5. **Video**: films and series, resume and next-up, the player with
   subtitles and the up-next card, video download profiles.

## As built: stage 1

The project is `clients/android/` (Gradle 8.9, AGP 8.7.3, Kotlin 2.0.21,
Compose BOM 2024.12, Media3 1.5.1, Hilt 2.53.1, Room 2.6.1 declared for
stage 3, OkHttp 4.12, kotlinx.serialization 1.7.3, Coil 2.7, WorkManager
2.10). Built and tested only in the container; nothing has run on a phone.

Built: sign-in with the server's bearer login (a password, or an API key
typed in the same box, which goes straight in as the bearer) and the
session in encrypted preferences; the device identity from a stable id
made once, so the phone is one device on the Devices page; the API
client in `api/` with the contract test; Listen (recently added,
recently played, most played), Library with Artists, Albums, Songs and
Genres paged as they scroll with a letter index and sort chips, a picker
when there are several music libraries; Search; Artist and Album pages
with Play and Shuffle; playback through ExoPlayer in a media session
service with the notification and lock-screen controls, every track
resolved through the server's play decision at the moment it opens (the
data source rewrites `cineva://item/<id>` to the answered URL); the
15-second heartbeat; the queue model (tested on the JVM) saved to the
session record a second after each change; the mini player; now playing
with the quality badge, timeline, shuffle and repeat, and Up next with
jump and remove.

Left out of stage 1, for the stages that own them: the events stream,
Continue here and Play on, playlists, ratings, lyrics, Settings (the
server's `music-quality` is applied by the server already; the app sends
no quality of its own), several servers, the Who's watching picker, Room
and anything offline, the TV pass. Known rough edges: artists have no
pictures until the server has looked them up; Songs plays what has
loaded so far; the icon is a placeholder; rotation restarts the sign-in
form.

## As built: stage 2

Every screen is drawn from Compose foundation to its mockup
(`ui/screens/Cineva.kt` holds the pieces: the page label and avatar,
word tabs, sort words, the cover cell and rail, song and album-track
rows, the pill, the slim bottom bar, the floating mini player, the thin
seek bar, the field, the sheet); MaterialTheme is plumbing only. Judged
against the mockups in the emulator, screen by screen:

- **Home**: the card ("Now playing" while something plays, "Carry on"
  from the saved queue, "Play again" from the last album) over the
  blurred cover in the art colour, then Recently played, Recently added,
  Most played and Playlists. As drawn.
- **Library**: word tabs, the sort row with the count, covers in thirds
  with letter placeholders, artists round. As drawn; the strings differ
  only where the data does.
- **Album**: the cover bleeding behind the header, 214 cover, the
  information line, Play filled in the art colour, Shuffle and Download
  quiet, the tracks with the playing row tinted and its bars. As drawn.
  The green tick shows for tracks on the phone once Sync exists.
- **Now playing**: a sheet from the mini player, the blurred cover
  behind, the spectrum (below) between it and the sharp cover, title,
  artist in the art colour, the heart, the thin seek bar with the
  quality badge between the times, the controls with the 76 dp white
  play, Up next and Lyrics as words with Play on and the queue either
  side; Up next and Lyrics open as panes in place of the cover; swipe
  down closes, swiping the cover sideways skips. As drawn.
- **Search**: the field with Cancel, artists round, albums at 112,
  songs as rows. As drawn.
- **The long-press sheet**, **Settings** and **Sign in** follow the
  same pieces; they had no mockup.

Changed on the way (the coordinator's calls, October 2026): the fourth
tab is **Sync**, a placeholder until its own stage; the **theme comes
from the server** (`data/Themes.kt`: `GET /themes`, the holiday from
`/themes/occasion`, the user's `theme`, `holiday-themes` and
`holiday-skip` preferences from `/auth/me/prefs`, the web client's rule
for which applies, cached for the first frame, Midnight only before the
first fetch; a theme's background picture sits behind Home, Library and
Sync dimmed by its own `dim`; Settings lists the server's themes and
saves the choice with `PUT /auth/me/prefs`); and a **spectrum** behind
the cover (`player/Spectrum.kt`: a `TeeAudioProcessor` on the player's
own sink, so no microphone permission and local files work, a radix-2
FFT over 1024-sample windows at about 30 frames a second, 24 log-spaced
bands from 40 Hz to 16 kHz with a short attack, slow decay and falling
peak caps, drawn on a Canvas in the art colour at low opacity rising
from the bottom to just under half the screen; it runs only while now
playing is open, stands still under reduced motion, and has its switch
in Settings).

Queue parity: the events stream carries takeovers (the phone stops and
says where it went), hand-offs (verified: a queue sent to the phone
with `POST /auth/sessions/{id}/play` started playing on it within a
second), ratings made elsewhere and theme changes; playlists list, open
and play, and the sheet adds to one or makes one; the heart rates five
stars and clears; lyrics scroll when timed; a dead session returns to
sign-in. Tests: themes (the holiday rule), the spectrum (FFT, bands,
mixing), the palette cache, event parsing, the queue, the API contract.

Still short of the bar: the cover does not travel from the tapped album
into its page (only the now-playing sheet animates); Up next rows are
removed with the cross or a swipe but not reordered by drag; the art
colour is the palette's vibrant swatch, which on some covers is cooler
than the warm one the mockup shows; a theme's effect (snow, leaves) is
not drawn; nothing has run on a phone, so audio focus, Bluetooth and
the lock screen are unjudged beyond the notification appearing.

## As built: stage 3 (mixes and playlist adding)

Built 26 October 2026 to `server/docs/mix.md`, matching the web client.

- **Mixes**: a Mix pill beside Play and Shuffle on Album and Artist,
  "Mix from this" in the long-press sheet for a track, album or artist.
  `PlayerRepository.startMix` asks for the first batch (`start=true`),
  replaces the queue and marks it a mix (`Queue.mix`, saved on the
  record); `MixRules.next` is the web client's extend rule as pure Kotlin
  (two tracks to come, a mix asks after its last entry, an ordinary queue
  starts a mix from its last track when `music-autoplay` is on, repeat
  asks nothing, an empty or failed batch is not asked again until the
  queue moves on), run after every transition, play, remove and handoff.
  Up next shows "Mix · from <seed>" with the basis in words, "Finding
  more…" while a batch loads, Stop mix (drops what has not played) and
  Save as playlist with the name prefilled. Handoff and takeover carry
  `mix`. The `music-autoplay` preference is read with the other server
  preferences (`ThemeStore`, which fetches them) and toggled in Settings.
- **Adding to a playlist**: the picker loads `GET /playlists?for=<track>`
  (by use, each saying whether it holds the track), the playlist used
  last leads (kept in app preferences), the first three are chips, a
  filter field appears above eight with Enter adding to the first match,
  a tick and "Add again" mark playlists that hold the track, and every
  add shows "Added to <name> · Undo" for two seconds (`Toasts`), the undo
  calling `DELETE /playlists/{id}/items/last`. From now playing the ⋯
  sheet opens with Add to playlist first.
- Tests: `MixRulesTest` (the extend rule and the basis words),
  `QueueTest` (where a mix begins, removing before it, stopping it), the
  contract test with the two new paths.

Falls short: the Up next pane opens scrolled to the playing row, which
can leave the mix heading just above the top; a held album lists
playlists by name since `?for=` takes a track. Not seen on a real phone.

## Open questions

- **Package name**: `com.garybowers.cineva` follows the repository; a
  domain of the project's own would be better before the first release,
  since a package name cannot change afterwards.
- **Minimum Android**: 9 (API 28) covers almost every phone in use and
  keeps Media3 and Compose simple; 8 would add little.
- **Theme**: decided October 2026: the server's themes, so a person's
  theme follows them to the phone; no Material or system colours.
- **Sync quality default**: Opus 128k keeps a thousand tracks under a
  gigabyte and sounds fine on a phone; original is there for the people
  who want it.
- **Fill the spare room**: by what rule (never heard, highest rated,
  random by artist), and how much to leave free by default.
