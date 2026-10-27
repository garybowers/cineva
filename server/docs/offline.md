# Offline sync

Status: designed October 2026, the server side of milestone 11, written
for the Android phone app (`clients/android/docs/phone.md`) and reused by
every native app after it; stage 1 (bearer sign-in, music download jobs,
the ids list) built the same month. Music first, since that is what the
phone app does first; the video profiles are a later stage of the same
design.

## What exists

- **Downloads**: `GET /items/{id}/download?format=&quality=` serves a
  track's original file or a transcode (through the stage 5 audio cache,
  so a transcode streams while it encodes and is kept), and a ZIP of an
  album or an artist. Ranges work on originals and finished transcodes.
- **Devices and sessions** (`devices.md`): any client that signs in is a
  device holding one session, with a name, device preferences and the
  play queue; "Continue here" and "Play on" already move playback between
  devices, and the `events` stream tells a device when it is addressed.
- **Per-user state**: `POST /items/{id}/progress` (the heartbeat),
  `PUT /items/{id}/watched`, `PUT /items/{id}/rating`, play counts kept by
  the server from the heartbeats, and `updated_at` or `rated_at` on all
  of them.
- **Library listings**: the paged music listings, `?since=` deltas with
  `X-Items-Mark` on `/libraries/{id}/items`, artwork at stable URLs that
  change when the picture does.

Nothing here is a bolt-on: an app is a device, its state is the user's
state, and its downloads are files the server already serves.

## Signing in from an app

A native app signs in with `POST /auth/login` like the web client. Today
that answers with cookies (the session and the device); an app can keep
them, and OkHttp does, but a bearer token is the natural shape for an
app, so:

- `POST /auth/login` accepts `"bearer": true` and then answers with
  `token` as well as the user, and sets no cookies. The token is the
  session's token, which already exists; nothing new is stored.
- `authenticate` accepts `Authorization: Bearer <token>` for a session
  token as it does for an API key: an API key starts with `cnv_`, so the
  prefix says which lookup to make, both by hash.
- The device is made as `deviceForSignIn` makes one, keyed by a `device`
  object in the body (`{name: "Pixel 8", client: "Cineva Android 0.1"}`)
  the app sends on every sign-in with its own stable id, so the same
  phone is the same device whoever signs in on it, as a browser is.

## Download jobs

One path for the app whatever it asks for:

| Route | Body or parameters | Answer |
|---|---|---|
| `POST /downloads` | `{itemId, profile}`; profile `original`, `opus-128`, `opus-64`, later `mp4-720`, `mp4-1080` | the job: `{id, itemId, profile, status, bytes, url}` |
| `GET /downloads/{id}` | | the job, polled until `ready` or `failed` |
| `GET /downloads/{id}/file` | ranges | the file, resumable |
| `DELETE /downloads/{id}` | | forgets the job (the cached file stays for the cache's own eviction) |

`status` is `queued`, `preparing`, `ready` or `failed`. An original is
`ready` at once with `url` pointing at `/items/{id}/download`; an audio
transcode is `ready` as soon as the stage 5 cache has it, which for a
track is seconds, and `preparing` while it encodes; a video transcode
(later) runs through the background gate like intro detection, yielding
to playback. A whole album or playlist is many jobs, one per track, made
in one call with `{itemIds: [...]}`, so the app shows progress per track
and resumes what it had after a lost connection. Jobs belong to the user
and respect library access, and a device may hold at most a few hundred
unfinished ones.

## Sync plans

Decided 26 October 2026: the phone app takes its music with it the way
an iPod did, by a plan rather than by downloads, and the plan belongs to
the device on the server so the web client's Devices page can manage it
as iTunes managed an iPod.

- `sync_plans (device_id PRIMARY KEY, user_id, mode, rules, profile,
  wifi_only, charging_only, new_weeks, fill_bytes, keep_free_bytes,
  updated_at)`: `mode` is `everything` or `chosen`; `rules` is JSON of
  the chosen artist, album, playlist and genre ids; `new_weeks` adds
  anything added lately; `fill_bytes` adds unheard music up to a size,
  leaving `keep_free_bytes` free.
- `GET` and `PUT /auth/sessions/{id}/sync` read and write a device's
  plan (`current` for the caller's own); the Devices page shows it under
  the device with what the device holds.
- `GET /auth/sessions/current/sync/manifest?since=` is what should be on
  the device now: `{tracks: [{itemId, profile, bytes, albumId}], removed:
  [itemId], total, generatedAt}`, worked out from the plan against the
  library and the user's access, with the deltas since the device's last
  manifest so a big plan is not resent each time. Smart playlists are
  evaluated at manifest time. The device fetches what it lacks through
  the download jobs (one `POST /downloads` with the missing ids), deletes
  what `removed` names, and reports what it holds.
- A plan change, a library change (the `items` event) and the device's
  own schedule all trigger a sync; the server only ever answers the
  manifest.

## What each device holds

The app tells the server what it keeps, so the user's Devices page and
the admin's can say "Pixel 8: 412 tracks, 3.2 GB" and a lost phone can be
told to drop everything:

- `device_downloads (device_id, item_id, profile, bytes, stored_at)`,
  pruned with the device.
- `PUT /auth/sessions/current/downloads` with the whole list (the app's
  truth, sent after each change and on sign-in), `GET /auth/sessions/{id}
  /downloads` for the pages, `DELETE /auth/sessions/{id}/downloads` for
  "remove all from this device", which the device obeys at its next sync.
- Access revoked (a library restricted away from the user) marks the
  rows `revoked`, and the next sync answer tells the device to delete the
  files. Cineva never pretends a download is gone before the device says
  so.

## State sync

Offline, the app plays and records what happened; online again it sends
the batch and learns what other devices did:

```
POST /sync/state
{
  "since": "2026-10-26T07:00:00Z",
  "events": [
    {"itemId": "…", "kind": "progress", "positionSecs": 184, "at": "2026-10-26T08:12:31Z"},
    {"itemId": "…", "kind": "played", "at": "2026-10-26T08:15:02Z"},
    {"itemId": "…", "kind": "rating", "stars": 4, "at": "…"}
  ]
}
```

Answer: `{applied, changes: [{itemId, positionSecs, played, playCount,
lastPlayedAt, userRating, updatedAt}]}` for every item whose state for
this user changed since `since`, and `now` for the next call.

Rules: last write wins per item by `at` against the server's
`updated_at`; a `played` event counts a play once, by the same rule
`countsAsPlay` applies to live playback (the app sends what it observed,
the server decides whether it counts); events older than the server's
state are dropped, not merged. Nothing here is new state: progress,
watched, play counts and ratings are the tables that exist, and the live
heartbeat keeps using `/items/{id}/progress`.

## The library offline

The app keeps a local copy of the music libraries it may see, so the
library browses and searches without a connection:

- First pull: the paged listings, page by page, artists then albums then
  tracks, with artwork fetched as it is shown and kept.
- Refresh: `GET /libraries/{id}/items?since=<mark>` for what changed (it
  carries every music kind), and a new `GET /libraries/{id}/ids` (every
  item id in the library, a few hundred kilobytes for thirty thousand
  tracks) to find deletions, since a pruned row leaves no trace.
- The `items` event says when a refresh is worth making.

## Staging

1. **Bearer sign-in and music download jobs (built)**: `bearer` on login,
   session tokens as bearers, `POST /downloads` for tracks and albums over
   the audio cache, `/libraries/{id}/ids`. What the phone app's stage 3
   needs. As built: the answer to a bearer sign-in is the user plus
   `token` and `sessionId`; a transcode job is `queued` until its encode
   starts, `preparing` while it runs and `ready` with its byte size after;
   a job left queued by a restart is started again when it is next asked
   about; finished jobs are pruned after a day by the housekeeping sweep;
   `deviceId` on a job is the session that asked. An original job serves
   the library file directly (the first open question, decided: no copy).
2. **Sync plans and device downloads**: the plan table and routes, the
   manifest, the downloads table and its routes, the Devices page showing
   and editing a device's plan and what it holds, "remove all".
3. **State sync**: `/sync/state` with its rules and tests against
   concurrent live playback.
4. **Video profiles**: portable MP4 jobs through the background gate,
   subtitles bundled, for the app's video stage.

Each stage is documented in `openapi.yaml` in the same change, since
native apps are generated from it.

## Open questions

- Whether a download job for an original should copy the file into the
  cache (one path, counted storage) or serve from the library (no copy,
  but a share that is down fails the download).
- How long finished jobs are kept: a day is enough for an app to fetch
  what it asked for.
- Whether `/sync/state` should also carry the queue, or the queue stays
  on its own routes (it does in this design: it is a device's, not state
  that merges).
