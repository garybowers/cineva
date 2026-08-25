# Backlog

Deferred work and enhancement ideas, grouped by the milestone they extend.
Rule (see CLAUDE.md): when work is consciously punted during a milestone,
record it here in the same change — this file is how we find our way back.

## Milestone 1 — Skeleton
- [ ] Stop committing `server/ui/*/dist` once CI builds the UIs (committed today so a fresh clone can `go build` — go:embed needs the assets).
- [ ] Hoist shared frontend code (design tokens in `app.css`, `theme.ts`, API client patterns) into a shared package — currently duplicated between `ui/client` and `ui/admin`; do it when a third surface appears.

## Milestone 2 — Library & scan
- [x] **Prune deleted files**: successful scans sweep items not touched by the scan (updated_at cutoff), cascading to subtitles/play state.
- [x] Episode-level TMDB metadata: titles/overviews/stills via the season endpoint (tags and filenames still take precedence where present).
- [x] Season posters / overviews via the TMDB season endpoint.
- [ ] Worker pool for probing (scan is sequential; fine at hundreds of files, slow at tens of thousands).
- [x] Scan cancellation: POST /scans/{id}/cancel + admin Cancel button; scans stop within one file.
- [ ] Scheduled rescans and/or filesystem watch (`libraries.scan_interval` config).
- [ ] Fuzzy/multi-field search: search matches title substring only — add year, cast, person search when credits exist.
- [ ] Multi-version grouping: 40 movie folders hold two files of the same film (m4v + mkv); each becomes a separate item. Plex groups them as versions of one movie with a version picker at play time.
- [ ] Extras/trailers filtering: subfolders like Featurettes/ or -trailer suffixed files should attach to the movie, not appear as movies (none in the current library, but standard layouts have them).
- [ ] Cast/crew from embedded iTunMOVI plists (the mp4s carry full cast/directors/writers) + TMDB credits — needs the people/credits schema decision.
- [ ] NFO sidecar support (Kodi XML format) + local poster.jpg/fanart.jpg — the Jellyfin/Radarr/Sonarr interop layer; user's current library has none, so ecosystem value only.
- [ ] Ship a project TMDB API key as the built-in default (Jellyfin/Kodi model — permitted for open source with attribution), keeping metadata.tmdb.api_key as the user override; add the TMDB attribution line to the client Settings page.

## Milestone 3 — Playback
- [ ] **PGS/VobSub burn-in**: image-based subtitle tracks are cataloged and listed without a URL (API returns 422 with an explanation). Burn-in needs the video re-encode path plus a multi-input overlay filter graph. Nothing in the current library requires it.
- [ ] **Portable-transcode download** (H.264/AAC MP4 variant): deferred to milestone 8's async-job infrastructure — the download button serves the original file today.
- [ ] Audio track selection: multi-audio files always use the "best" stream; expose a picker like subtitles.
- [ ] Playlist duration accuracy: EXTINF entries use the nominal segment duration; real segments split on keyframes, so long seeks can drift by a few seconds. Fix = probe keyframe timestamps to emit exact durations.
- [ ] Seek-preview thumbnails (trickplay/BIF strips).
- [ ] Transcode session survival across server restart (sessions die with the process; clients recover by re-requesting play).
- [ ] Per-user default quality/autoplay stored server-side (after milestone 4; localStorage today).

## Milestone 5 — Web client (partially built early)
- [x] Real URL routing: name-based paths (/libraries/TV Shows/Succession/Season 2), browser back/forward, deep links with server-side SPA fallback.
- [ ] Cast & Crew row + ratings on detail pages (needs TMDB key + credits fetch in the scanner; see the Plex reference screenshots).
- [ ] Episode/backdrop imagery in lists instead of letter placeholders (TMDB stills).
- [ ] PWA: manifest + install + icon set (the mobile-first layout is done; installability is not).
- [ ] User menu behind the top-bar profile icon (after milestone 4).

## Milestone 7 — Audio (music shipped; audiobooks pending)
- [x] MusicBrainz + Cover Art Archive enrichment: queried only for albums missing art/year/titles (1 req/s throttle, proper User-Agent); canonical track titles for untagged files matched by track number.
- [ ] Optional local MusicBrainz mirror/dataset: MB publishes full DB dumps — a local mirror would remove the 1 req/s limit for very large untagged libraries.
- [ ] Transcoded ZIP downloads pre-warm serially — a whole artist as MP3 transcodes each track on the fly during the download; fine for albums, slow for artists. Consider a prepare-then-fetch job when milestone 8's async infrastructure lands.
- [ ] Gapless album playback (needs Web Audio API preloading/crossfeed; plain `<audio>` has a small gap between tracks).
- [ ] Audio transcode fallback for browser-unsupported codecs (wma, alac in some browsers) — everything currently direct plays.
- [ ] Artist pages (grid shows albums; an artist view listing their albums exists only via breadcrumb/parent navigation).
- [ ] Playlists (manual + smart), play counts / recently-played rows from the listen history already being recorded.
- [ ] Media Session API on the music bar (lock-screen / hardware-key controls).
- [ ] Audiobooks: M4B chapters, playback speed, precise resume (milestone item, not started).
- [ ] Podcasts (milestone 8 in the roadmap numbering): RSS subscriptions, polling, retention.

## Milestone 6 — Admin UI
- [ ] Multiple paths per library in the add form (API supports it; form takes one).
- [ ] Edit library paths after creation.
- [x] Scan progress (files seen / total, progress bar, live via the event stream) and cancellation UI. Still open: current file name, files/sec.
- [ ] `/fs/dirs` (folder picker) must be admin-only once milestone 4 auth lands — it exposes directory names server-wide.
- [ ] Dashboard stats are in-memory only (1h fine / 24h coarse) and reset on restart; persist coarse samples if longer history is wanted. Play history counts starts only — no duration/completion yet.
- [ ] Folder picker: multi-select to add several paths at once (pairs with multiple-paths-per-library above).

## Operational
- [ ] CI: build UIs, `go vet`/test, lint on push (no CI at all yet).
- [x] Stale `running` scans marked failed at server startup; the scans table doubles as the cross-process scan lock.
