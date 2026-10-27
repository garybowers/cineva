# Mixes, the endless queue, and adding to playlists

Status: designed October 2026; stage 1 (the engine and
`GET /items/{id}/mix` for tracks, albums and artists) built the same
month, stages 2 to 4 to follow. For the web client and the Android app
alike, on one server-side engine, as the music overhaul's rules require:
the server decides, the clients show.

The word is **mix**, never radio: "radio" is reserved for internet radio,
a later feature of its own. A mix is a playlist that builds itself from
a seed, for as long as you keep listening.

## What is wanted

1. **A mix you start on purpose.** From a song, an album, an artist, a
   playlist or a genre: "Mix from this" plays the seed's best and then
   keeps going with what belongs beside it, as a playlist that builds as
   it goes rather than a surprise. The gym is the case to design for: a
   mix from one upbeat song or from the gym playlist, and it runs for
   the whole session without a whole album ever being forced on you.
   Spotify's song radio and mixes are the reference.
1. **An endless queue.** With autoplay on, the music does not stop when
   the album does: a mix from the last track carries on.
2. **Really good recommendations.** Not "same genre", which in a big
   library means a thousand tracks tagged Rock. Similar artists by what
   the world listens to, the artist's best-known songs, what this
   household plays and rates, the era, and variety.
3. **Adding the song that is playing to a playlist** in one or two taps,
   from the now-playing screen, the mini player and every track row, in
   both clients, with the playlist you used last at the top.

## Mixes

### Seeds

A mix is grown from a seed: a track (the usual case: "Mix from
Dancing Queen"), an album, an artist, a playlist or a genre. The seed
gives the engine its artists, its genres and its era, and, for a track,
its own "sound" as far as the signals below can say.

### Signals

Ranked by weight. Every signal is computed on the server from what the
library and the household already hold, plus two open ListenBrainz
sources that need no key and are cached.

| Signal | Source | Note |
|---|---|---|
| **Similar artists** | ListenBrainz Labs `similar-artists` by the seed artist's MusicBrainz id, scored (ABBA gives Queen, The Beatles, Elton John, ...) | Cached per artist for 90 days in `artist_similar (artist_mbid, similar_mbid, name, score, fetched_at)`. Intersected with the library's artists by MBID, then by folded name for artists without one. An artist with no MBID is looked up on MusicBrainz by name first (the existing artist lookup does this; the mix asks for it when the seed is opened, not during a scan). |
| **An artist's best songs** | ListenBrainz `popularity/top-recordings-for-artist` by MBID (Dancing Queen first for ABBA) | Cached per artist for 90 days in `artist_top (artist_mbid, recording_mbid, name, rank, fetched_at)`; matched to local tracks by recording MBID, else by folded title under that artist. Keeps a mix from picking the eleventh track of a B-sides album. |
| **The household's taste** | play counts (the viewer's first, everyone's second), ratings, favourites | A five-star track from a similar artist beats an unrated one; a one-star track is never picked. |
| **Co-listening** | `play_events`: tracks played by the same user within an hour of the seed's artist, over the last year | What this household actually puts next to it. Weak alone, decisive with the others. |
| **Genre and era** | `item_genres` on tracks and albums, `year` | Overlap with the seed's genres as a bonus; a year within a decade of the seed's as a small bonus. Never a filter on their own, except as the fallback below. |
| **Freshness** | the viewer's recent plays and the current queue | Nothing played in the last three hours, nothing already queued, the seed's own album at most twice in the first twenty. |

A candidate's score is the weighted sum; the top of the list is then
**diversified**: at most two tracks by one artist in any twenty, no
two in a row, the seed artist's own tracks capped at a quarter of the
batch, and a little randomness so the same seed does not give the same
mix twice. Batches are twenty tracks.

**Fallback** when nothing has an MBID or ListenBrainz is unreachable:
the household's co-listening and taste, then genre and era, then the
same artist. The mix still plays; it is just less clever, and the
response says which signals it had (`basis`), so a client can say
"Based on what you play" rather than "Similar artists".

**Never**: tracks from libraries the user cannot see, tracks under 30
seconds, and the same recording twice (two copies of Dancing Queen by
MBID or by artist and title).

### API

| Route | Parameters | Answer |
|---|---|---|
| `GET /items/{id}/mix` | `size` (20, max 50), `exclude` (ids already queued, comma separated, up to 1,000), `after` (the last track played, so a long mix drifts from where it is rather than from the seed alone), `start` (true for the first batch of a mix started on purpose: it opens with the seed's best, see below) | `{seed: Item, basis: ["similar-artists", "top-songs", "household", "genre"], tracks: [Item]}` with the viewer's plays and ratings on each. The item may be a track, an album or an artist |
| `GET /playlists/{id}/mix` | as above | a mix seeded by the playlist's tracks and artists, weighted by how often they appear: the gym playlist, carried on |
| `GET /libraries/{id}/mix` | `genre`, as above | a genre mix, or the whole library's |

**What the first batch opens with** depends on the seed, since a mix
started on purpose is not the same as one that carries a queue on:

- a track: the track itself first, then the mix;
- an album: three or four of the album's best (by the household's plays
  and ratings, then ListenBrainz's ranks), spread through the first
  batch rather than in a block, so you hear the album without hearing
  the album;
- an artist: their best-known songs leading, their similar artists
  filling in;
- a playlist: a few of the playlist's own tracks, then what belongs with
  them; the playlist is the taste, not the tracklist.

The engine lives in `internal/mix`: the signals as small functions
over the store, the scoring, the diversifier, the ListenBrainz client
with its throttle (one request a second, one User-Agent, cached) and
tests on a fixture library. Lookups for a seed that lacks cached
similarity run in the background when the mix is first asked for; the
first batch comes from what is already known, and the next one is
better. Documented in `openapi.yaml` in the same change.

### Starting a mix

**Mix** sits beside Play and Shuffle wherever Play is: the album and
artist pages' header, the playlist page, a genre tile, and the long-
press sheet and track menu on every track ("Mix from this"). Starting
one replaces the queue with the first batch and marks the queue as a
mix, which Up next shows under its own heading, **"Mix · from Be Here
Now"**, with the basis as its sub-line ("Similar artists and what you
play"). As the batch nears its end the client asks for the next one
(`after` the current track, `exclude` the queue) and adds it under the
same heading, so what is coming is always visible before it plays. The
person can drag a mix track up, remove one, rate one, or stop the mix
with one tap, and the queue goes back to being just the queue.

### The endless queue

- A person preference, **`music-autoplay`** (on by default; the Music
  section of Settings; "Keep playing when the queue ends"): when a
  queue that is not a mix ends, carry on with a mix from the last track
  played, shown in Up next exactly as a started mix is. The web client
  and the app both honour it; the server owns the choice.
- Playing a single track from search or a row starts a one-track queue,
  so with autoplay on the mix begins at once after it: that is the
  Spotify behaviour that was asked for. Playing an album or playlist
  plays it through first.
- The queue record (`devices.md`) gains `mix: {seedId, kind, after}` so
  a handoff to another device carries the mix on from the same place,
  and **Save as playlist** on Up next saves the mix's tracks so far as a
  manual playlist named after the seed ("Mix from Dancing Queen", or a
  name typed over it: "Gym").
- Ratings while a mix plays feed back: a five-star raises that artist's
  weight for the rest of this mix, a one-star drops the artist from it.

## Adding to a playlist

What exists: a track menu item that opens a picker of manual playlists
sorted by name, with New playlist. What is wanted is the two-tap path,
and the one-tap path for the playlists used most.

- **Playlists are ordered by use**, not name, wherever they are offered
  for adding: `playlists.last_added_at`, set on every add, orders the
  picker, with a filter box when there are more than eight. Listing
  playlists for browsing keeps its name order.
- **One tap from now playing and the mini player**: an "Add to playlist"
  control beside the heart (web: in the bar and on `/now-playing`; app:
  the ⋯ on now playing opens the sheet with Add to playlist at the top).
  The picker opens with the last three playlists used as large chips at
  the top, then the rest.
- **The sheet remembers**: after adding to a playlist, the next sheet
  shows "Add to Driving" as the first chip, so a session of adding
  songs to one playlist is one tap per song.
- **Already there** is said, not silently duplicated: the picker marks
  playlists the track is already in with a tick, and tapping one offers
  "Add again" or nothing.
- **Feedback is a toast with undo**: "Added to Driving · Undo", two
  seconds, on both clients; undo removes the last-added entry.
- **Web keyboard**: `p` on the now-playing page opens the picker for the
  playing track; typing filters; Enter adds to the first match.
- **The API**: `POST /playlists/{id}/items` exists; `GET /playlists`
  gains `?for=<itemId>` so the picker learns in one request which
  playlists already hold the track, and the order by use. A new
  `DELETE /playlists/{id}/items/last` undoes the last add (the position
  the add returned).

## Storage

- Migration 0049: `artist_similar` and `artist_top` caches as above;
  `playlists.last_added_at`; the `music-autoplay` preference needs no
  table (the registry).
- The queue record gains `mix` (no migration: it is JSON).

## As built: stage 1

`internal/mix` (the engine, the ListenBrainz client), `internal/store/
mix.go` (the queries and caches), `internal/api/mix.go` (the route),
migration 0049. The weights: similarity 3, a best-known song 2, the
viewer's own plays 1.5, co-listening 1.5, the rating 1 (from -1 to 1),
the household's plays 0.75, genre overlap 0.75, era 0.25, and up to 0.5
of chance; each signal is normalised to the pool, so a track that leads
on similarity and plays scores about 5 and a genre-only one under 1.

Deviations from the design above:

- **ListenBrainz's top recordings now need a user token** (its popularity
  endpoint answers 401 without one, "due to bad actors and AI scrapers";
  the Labs similarity graph is still open). `metadata.listenbrainz.token`
  (`CINEVA_METADATA_LISTENBRAINZ_TOKEN`) takes a free account's token;
  without one the engine notes the refusal, pauses top-song lookups for
  an hour, and the `top-songs` basis never appears. Similar artists, the
  household's listening, genre and era still make the mix.
- Co-listening reaches back as far as play history is kept (90 days by
  default), not a year.
- When a small library cannot satisfy the diversity rules, they relax one
  at a time, the per-artist window first and the seed album's cap last,
  so a batch is filled rather than cut short.
- The similar-artists fetch for a seed, and up to ten top-song fetches
  for its similar artists, run in the background on the first ask; the
  second ask has them.

## As built: stage 2

The web client, and the small server additions it needed.

- **Server**: the `music-autoplay` preference (person scope, on, in the
  Music section); `mix` on the queue record (`store.QueueMix`: seed id
  and kind, title, the last `after`, the index the mix's entries begin
  at, the basis), carried through save, get, takeover and play-on with no
  migration; `GET /playlists?for=<track>` (most recently added to first,
  never-added by name after, each with `holds`); `DELETE /playlists/{id}/
  items/last?count=` (the undo of an append). Tests in the store and api
  packages; `openapi.yaml` updated.
- **Mixes**: Mix beside Play and Shuffle on the album and artist pages,
  "Mix from this" in every track's menu (the queue's rows included);
  starting one asks for the first batch with `start=true`, replaces the
  queue and marks it a mix. Up next shows "Mix · from <seed>" with the
  basis in words ("Similar artists and what you play"); with two tracks
  left the client asks for the next batch (`after` the last entry,
  `exclude` the queue) and adds it under the heading, saying "finding
  more…" meanwhile; Stop mix drops what it added and has not played;
  Save suggests "Mix from <seed>" as the playlist's name. With the
  autoplay preference on, an ordinary queue two tracks from its end
  starts a mix from its last track the same way, shown under its own
  heading after the queue's own entries; repeat on means no mix. Handoff
  carries the record's `mix`, so another device goes on growing it.
- **Adding to a playlist**: the picker lists by use with the last three
  as chips, a filter box above eight playlists (Enter adds to the first
  match), a tick and "Add again" where the track is already in; an Add
  to playlist control sits beside the stars in the mini bar (desktop) and
  on the now-playing screen, and `p` there opens it; the notice says
  "Added to <name>" with Undo for a few seconds, which removes what was
  added.

Deviations: Mix on the playlist page and the genre tile wait for stage 4,
which gives the server playlist and genre mixes; the queue row's menu is
the same TrackMenu as everywhere, so "Mix from this" is there without a
separate menu. A batch that comes back empty, or a failed request, stops
the asking until the queue moves on, so a mix cannot loop on the server.
Nothing was verified in a browser: the client was built and type-checked
and the server paths exercised with tests.

## Staging

1. **The engine and `GET /items/{id}/mix`** for tracks, albums and
   artists, with the ListenBrainz caches and the fallback, tested on a
   fixture; the endpoint and `curl` are enough to judge it. Built.
2. **Mixes in the web client**: Mix beside Play, the Mix section in Up
   next, stop and save, the autoplay preference and the endless queue,
   the queue record's `mix`. Built, with the playlist-adding changes
   below; see "As built: stage 2".
3. **The same in the Android app**, plus the one-tap playlist adding in
   both clients and the playlist ordering by use.
4. **Playlist and genre mixes**, and the ratings feedback loop.

## Open questions

- **Whether a mix may leave the library.** Everything above stays
  inside what the household owns, which is the point of Cineva. A later
  "Discover" row could show what ListenBrainz suggests that is *not* in
  the library, feeding the request flow (Lidarr), but that is a
  different feature.
- **ListenBrainz's own LB Radio** (`explore/lb-radio`) now needs a user
  token. It could be an optional, per-user integration later for
  prompts such as "artist:(abba) tag:(disco)"; the engine here does not
  depend on it, and it would belong with internet radio anyway.
- **How loud the household signal should be** in a house of one versus
  a house of five: whether "everyone's plays" should count at all for a
  child's profile, given the content ceiling that personalisation.md
  gives admins.
