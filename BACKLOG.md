# Backlog

Deferred work and enhancement ideas, grouped by the milestone they extend.
Rule (see CLAUDE.md): when work is consciously punted during a milestone,
record it here in the same change — this file is how we find our way back.

## Milestone 1 — Skeleton
- [ ] Stop committing `server/ui/*/dist` once CI builds the UIs (committed today so a fresh clone can `go build` — go:embed needs the assets).
- [ ] Hoist shared frontend code (design tokens in `app.css`, `theme.ts`, API client patterns) into a shared package — currently duplicated between `ui/client` and `ui/admin`; do it when a third surface appears.

## Milestone 2 — Library & scan
- [ ] **Prune deleted files**: rescans upsert but never remove items whose files vanished from disk. Needs mark-and-sweep per scan.
- [ ] Episode-level TMDB metadata: episode titles/overviews/still images (currently parsed from filenames; series get TMDB data, episodes don't).
- [ ] Season posters / artwork.
- [ ] Worker pool for probing (scan is sequential; fine at hundreds of files, slow at tens of thousands).
- [ ] Scan cancellation (context plumbing exists, no abort endpoint/UI).
- [ ] Scheduled rescans and/or filesystem watch (`libraries.scan_interval` config).
- [ ] Fuzzy/multi-field search: search matches title substring only — add year, cast, person search when credits exist.

## Milestone 3 — Playback
- [ ] **PGS/VobSub burn-in**: image-based subtitle tracks are cataloged and listed without a URL (API returns 422 with an explanation). Burn-in needs the video re-encode path plus a multi-input overlay filter graph. Nothing in the current library requires it.
- [ ] **Portable-transcode download** (H.264/AAC MP4 variant): deferred to milestone 8's async-job infrastructure — the download button serves the original file today.
- [ ] Audio track selection: multi-audio files always use the "best" stream; expose a picker like subtitles.
- [ ] Playlist duration accuracy: EXTINF entries use the nominal segment duration; real segments split on keyframes, so long seeks can drift by a few seconds. Fix = probe keyframe timestamps to emit exact durations.
- [ ] Seek-preview thumbnails (trickplay/BIF strips).
- [ ] Transcode session survival across server restart (sessions die with the process; clients recover by re-requesting play).
- [ ] Per-user default quality/autoplay stored server-side (after milestone 4; localStorage today).

## Milestone 5 — Web client (partially built early)
- [ ] Real URL routing: browser back/forward, deep links, shareable URLs (breadcrumb trail maps cleanly onto routes like `/library/tv/succession/s02`).
- [ ] Cast & Crew row + ratings on detail pages (needs TMDB key + credits fetch in the scanner; see the Plex reference screenshots).
- [ ] Episode/backdrop imagery in lists instead of letter placeholders (TMDB stills).
- [ ] PWA: manifest + install + icon set (the mobile-first layout is done; installability is not).
- [ ] User menu behind the top-bar profile icon (after milestone 4).

## Milestone 7 — Audio (music shipped; audiobooks pending)
- [ ] MusicBrainz + Cover Art Archive enrichment (metadata is tags-first only; provider interface still to build).
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
- [ ] Scan progress detail (files/sec, current file) and scan cancellation UI.

## Operational
- [ ] CI: build UIs, `go vet`/test, lint on push (no CI at all yet).
- [ ] Stale `running` scans: a scan interrupted by server kill stays `running` in the DB forever; mark orphaned scans failed at startup.
