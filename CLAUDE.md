# Cineva

Cineva is a modern, self-hosted media suite written in Go — a replacement for Plex, Jellyfin, Audiobookshelf, and Calibre-Web in one server. It manages **movies, TV, music, audiobooks, podcasts, ebooks, and comics** as first-class media domains: it scans libraries on disk, enriches them with metadata, and streams or serves to clients with on-the-fly transcoding when needed. It ships as a single static binary with the web client and admin UI embedded.

## Product requirements

All media domains share one schema, scanner framework, API shape, and per-user state model from day one — they are first-class in the architecture even though they ship in stages (see Milestones). Nothing gets built as a bolt-on.

### Common to all domains
- **Library management**: libraries follow the Jellyfin/Plex model. A library is created with exactly one media type (Movies, TV, Music, Audiobooks, Podcasts, Books) chosen at creation time and immutable afterwards; it points at one or more filesystem paths. You can have any number of libraries per type (e.g. "Kids Movies" and "Movies"), but a library never spans multiple media types. A scanner walks the paths, identifies files, extracts technical info via ffprobe (or format parsing for books), and matches against the domain's metadata provider for artwork and details.
- **Per-user state**: resume positions (video playback, audio listen position, book reading position), finished flags, and continue-watching/listening/reading rows — same model everywhere.
- **Users & auth**: local accounts with argon2id-hashed passwords, session tokens for browsers, API keys for clients. One admin role, regular user role. Per-user library grants.
- **Downloads**: any item can be downloaded from the web client — the original file, or a portable transcode prepared server-side — so laptops can carry media offline while travelling.
- **Built-in web client**: mobile-first responsive layout, installable as a PWA — until the native apps exist, a phone browser is the mobile interface, so every screen must work at phone width.
- **Admin UI**: manage libraries, trigger/monitor scans, manage users, view active streams/transcodes, server settings.

### Video — movies & TV (stage 1, v1)
- Metadata from TMDB: artwork, synopsis, cast, episode data.
- Direct play when the client supports the file natively; on-the-fly HLS transcoding via ffmpeg when it doesn't. Seeking within transcoded streams must work (segment-based, restart-at-offset).
- Subtitles: text-based (SRT/ASS, embedded or sidecar) extracted and served as WebVTT for the player to overlay; image-based (PGS/VobSub) burned in during transcode.
- **Player — movies vs TV**: one player core, two presentations. Movies are standalone: play, resume, and return to the detail page when finished. TV is hierarchical (series → season → episode) and the player is next-up aware:
  - **Next-episode autoplay**: as an episode enters its end-credits window, the player overlays an up-next card (next episode's thumbnail + countdown) and auto-plays it unless dismissed. In v1 the credits window is a heuristic (final ~40 seconds, configurable); later, credits *detection* replaces the heuristic with the real credit start. Autoplay is a per-user setting (on by default).
  - **Next-up logic** lives server-side (`/api/v1/.../next-up`), not in the client: crossing the watched threshold (~90%) marks the episode watched and advances the series' next-up pointer, so the web client and future native apps behave identically.

### Audio — music, audiobooks, podcasts (stage 2)
- **Music**: FLAC/MP3/AAC/OGG/Opus. Metadata from embedded tags first, enriched via MusicBrainz + Cover Art Archive. Album/artist browsing, gapless album playback, direct play or transcode to Opus/AAC for bandwidth.
- **Audiobooks**: M4B with chapter support, plus multi-file MP3 sets treated as one book. Chapter navigation, playback speed control, precise resume. Metadata from embedded tags, enriched via the book metadata provider.
- **Podcasts**: subscribe by RSS feed URL; the server polls feeds and auto-downloads new episodes per a retention policy. Episode metadata comes from the feed itself.

### Books — ebooks & comics (stage 3)
- **Ebooks**: EPUB + PDF. Built-in web reader for EPUB with position sync (reading position is per-user state like everything else); PDF served to the browser's native viewer. Metadata from embedded EPUB/OPF data, enriched via Open Library.
- **Comics/manga**: CBZ + CBR archives with a page-based web reader.
- **OPDS catalogue feed** so external reader apps (KOReader, Calibre, Kobo-style clients) can browse and download natively.

### Federation (designed now, built post-v1)
Two or more Cineva servers (e.g. one at home, one at a relative's house) link as peers so remote libraries appear alongside local ones — to the user it's just another library in the list.

- **Identity**: every server generates an Ed25519 identity keypair on first startup. Pairing happens in the admin UI: exchange public keys + `host:port`, and both sides must approve the link (mutual trust, no one-way follows).
- **Transport**: a dedicated federation port (separate from the main HTTP port — this is the only thing exposed to the internet) speaking mTLS, each side pinning the peer's exchanged key. All federation traffic — metadata sync, heartbeat, media — flows over this link.
- **Metadata**: peers sync library metadata and artwork into a local cache periodically, so browsing remote libraries is instant and offline-cheap. Sync is pull-based with incremental updates.
- **Presence-gated visibility**: a heartbeat runs over the link; when a peer is unreachable, its libraries simply disappear from every client's library list — never broken entries, never dead play buttons. The cache is kept so the library reappears instantly on reconnect.
- **Playback**: the owning server transcodes its own media; the local server proxies the stream over the federation link to its clients. Clients never talk to the remote server directly.
- **Access control**: per-user grants, same model as local libraries.
- **Watch state** stays on the user's home server; it is never written to the peer.

### Offline sync for native clients (designed now, built post-v1)
Native mobile/laptop apps (planned, will live in `clients/`) need true offline media for travel without connectivity. The server side:

- **Download API**: a client requests an item at a profile (original, or a portable transcode — MP4 for video, Opus/AAC for audio; books are small enough to always take verbatim); transcoded downloads are prepared as async jobs the client polls, then fetched with resumable range requests.
- **Device registration**: each app instance registers as a device under its user; the server tracks what each device has downloaded so the admin UI can show it and users can manage storage remotely.
- **State sync**: clients play/read offline and batch progress locally, then push it on reconnect (last-write-wins per item). Resume positions survive a flight.
- The v1 web-client download button is the primitive; this milestone adds the device/sync layer on top. Design the download endpoints in the OpenAPI spec from the start so native apps aren't blocked on a redesign.

### Later (don't build speculatively)
- Intro & credits detection: fingerprint analysis across a season's episodes to find intro/credit boundaries, powering a "Skip intro" button and replacing the fixed credits-window heuristic for next-episode autoplay
- Photo libraries
- Hardware-accelerated transcoding (VAAPI/QSV/NVENC)
- OIDC/SSO login
- Remote access relay (NAT traversal for clients away from home)
- PostgreSQL backend for very large libraries

### Non-goals
- No DRM, no paid content store, no bundled "free channels"
- No telemetry/phone-home
- Windows/macOS server support is best-effort; Linux is the target platform

## Technology choices

- **Language**: Go (latest stable). Linux-first.
- **CLI & config**: [Cobra](https://github.com/spf13/cobra) for the command-line interface, [Viper](https://github.com/spf13/viper) for configuration. Config file is YAML; precedence: flags > `CINEVA_*` env vars > config file > defaults.
- **CLI subcommands (v1)**: `serve`, `scan`, `user add`/`user passwd` (bootstrap the first admin, reset passwords), `config validate`, `version`. No `migrate` command — migrations auto-apply at startup.
- **HTTP framework**: [Echo](https://echo.labstack.com/). Handlers stay thin; business logic lives in service packages.
- **Database**: SQLite via `modernc.org/sqlite` (pure Go, keeps the binary CGO-free). All data access goes through repository interfaces in `server/internal/store` so a PostgreSQL implementation can be added later — never call SQL from handlers or services directly.
- **Migrations**: versioned SQL files embedded with `go:embed`, applied at startup.
- **Transcoding**: ffmpeg via [ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo) (statically linked ffmpeg; CGO). Hardware transcoding uses the host's `libva`, loaded with `dlopen` at first use (`internal/media/libva_shim.c`) rather than linked — a VA-API driver is version-locked to the libva that loads it (`__vaDriverInit_<major>_<minor>`), so the copy bundled in the static archive only matches by luck, while a hard link would stop the binary starting at all on a host without libva. The shim keeps both properties: the host's libva is used where present, and where it is absent the server falls back to software like any machine without a GPU. Media inspection uses the same bindings (`internal/media.Probe`). The package is vendored as a git submodule at `third_party/ffmpeg-statigo` with a `replace` directive in go.mod — after cloning run `make setup` once to fetch the submodule, the static libraries (not in git) and the npm dependencies before building.
- **Streaming**: HLS (fMP4 segments) for video transcodes; HTTP range requests for direct play and audio; Opus/AAC for audio transcodes.
- **Book handling**: EPUB/OPF parsed natively in Go; CBZ via `archive/zip`, CBR via a pure-Go RAR reader (e.g. `nwaples/rardecode`); OPDS 1.2 for the catalogue feed.
- **API**: REST, JSON, spec-first. The OpenAPI 3.1 spec lives at `server/docs/openapi.yaml` and is the contract — update the spec in the same change as the handlers. Third-party clients generate SDKs from it.
- **Web client & admin UI**: Svelte + Vite. Built to static assets and embedded in the Go binary with `go:embed`. EPUB rendering via epub.js (or foliate-js); audio playback via the Media Session API for lock-screen controls.
- **Metadata providers**: one interface per domain — TMDB (video), MusicBrainz + Cover Art Archive (music), Open Library (books/audiobooks), RSS (podcasts) — so alternatives can be added.
- **Federation**: Ed25519 server identity, mTLS on a dedicated port with pinned peer keys, pull-based metadata sync, heartbeat-gated visibility (see Federation requirements above).

## Architecture & layout

```
server/                 Go module (all backend code)
  main.go               entrypoint — calls cmd.Execute(), nothing else
  cmd/                  Cobra commands (root.go, serve.go, ...) + Viper wiring
  internal/api/         Echo handlers, middleware, request/response types
  internal/library/     scanning framework + per-domain scanners/matchers
  internal/media/       ffprobe inspection, transcode sessions, HLS, audio streaming
  internal/books/       EPUB/PDF/CBZ/CBR parsing, OPDS feed
  internal/podcast/     RSS polling, episode download/retention
  internal/metadata/    provider interfaces + TMDB/MusicBrainz/Open Library clients
  internal/federation/  peer identity, pairing, mTLS listener, sync, heartbeat
  internal/store/       repository interfaces + SQLite implementation, migrations
  internal/auth/        sessions, password hashing, API keys
  ui/client/            built-in web client (Svelte)
  ui/admin/             admin UI (Svelte)
  ui/themes/            one folder per theme: theme.css (design tokens) + theme.json manifest
  docs/                 openapi.yaml, design notes
clients/                standalone client apps — our planned native mobile/laptop apps and third-party clients — separate from server/ui
```

Rules:
- Server code never lives outside `server/`; the built-in UIs live in `server/ui/client` and `server/ui/admin` (not `clients/`, which is reserved for standalone client apps).
- Cobra commands in `server/cmd/` do flag/config parsing and wiring only — construct services and call them. No business logic in commands or handlers.
- Long-running work (scans, transcodes, feed polls) runs in managed goroutines with context cancellation; expose progress over the API, never block a request on a scan.
- All configuration goes through Viper (config file + `CINEVA_*` env bindings + flags); nothing reads `os.Getenv` directly. `server/cineva.yaml` is the local-dev config and the living reference: when a feature adds a config key, add it there with a comment naming its `CINEVA_*` env equivalent, in the same change.
- New media domains reuse the shared item/library/user-state schema and scanner framework — a domain is a set of implementations of existing interfaces, not a parallel subsystem.

## Conventions

- **British English everywhere prose appears**: UI copy, documentation, code comments, commit messages, API descriptions (colour, catalogue, honours, -ise endings, licence as noun). Exceptions only where syntax or an external API dictates American spellings — CSS properties (`color:`), DOM/web APIs (`behavior: 'smooth'`), library identifiers (`Maximize` icon imports), and established wire-format field names.
- Standard Go style: `gofmt`, `go vet`, and `golangci-lint` must pass. Errors are wrapped with `fmt.Errorf("...: %w", err)` and handled, never discarded.
- Table-driven tests alongside the code (`_test.go`); the store gets tested against a real temp SQLite file, not mocks.
- API routes are versioned under `/api/v1/`. Breaking the API means a new version, not a changed response shape.
- Database schema changes always come as a new migration file — never edit an applied migration.
- Frontend: TypeScript, Svelte 5 runes, no component library lock-in yet; keep the API client in one generated/typed module. Build mobile-first: new screens are designed at phone width and scale up, not the reverse.
- Theming: all UI colours come from the CSS custom-property token set (`--bg`, `--panel`, `--border`, `--text`, `--muted`, `--accent`, `--accent-2`, `--ok`, `--err`, plus `--chart-1..3` for chart series — categorical, fixed order, validated for colour-vision deficiency against `--panel`) — never hardcode a colour in a component. Themes live in `server/ui/themes/<id>/` (served at `/themes/`, listed by `/api/v1/themes`) and one theme skins both UIs; adding a token means updating every theme folder in the same change.

## Milestones

v1 = milestones 1–6 (video end-to-end). Later stages are ordered but can flex.

Deferred items and enhancement ideas are tracked per-milestone in
[BACKLOG.md](BACKLOG.md) — when punting something during a milestone, add a
backlog entry in the same change. A milestone can be called done with open
backlog entries; it cannot be called done with undocumented gaps.

1. **Skeleton**: go.mod, Cobra root + `serve`/`version`/`config validate` commands, Viper YAML config, Echo server with health endpoint, SQLite store with migrations, embedded placeholder UI. `go run . serve` (from `server/`) serves something. Schema bakes in the server identity keypair, typed media domains, and a library-source abstraction (local path vs federated peer) from day one.
2. **Library & scan**: library CRUD, scanner framework, video scanner with ffprobe extraction, TMDB matching, artwork caching, `scan` CLI command.
3. **Video playback**: direct play with range requests, HLS transcode sessions with seeking, WebVTT extraction + burn-in fallback, watch-state tracking, server-side next-up endpoint, item download (original + portable transcode).
4. **Auth & users**: accounts, sessions, roles, per-user state and library grants, `user add`/`user passwd` CLI commands.
5. **Web client**: full browse/detail/playback experience for video, mobile-first and PWA-installable, download button, up-next card with next-episode autoplay.
6. **Admin UI**: library/user/stream management.
7. **Audio**: music + audiobook scanners (tags, MusicBrainz), audio streaming and transcode, gapless albums, chapters/speed/resume, music + audiobook player UIs.
8. **Podcasts**: RSS subscriptions, feed polling, episode auto-download and retention, episode player.
9. **Books**: ebook/comic scanners, Open Library matching, EPUB web reader with position sync, CBZ/CBR page reader, PDF passthrough, OPDS feed.
10. **Federation**: identity pairing UI, federation listener, metadata sync, heartbeat/visibility, proxied playback across all domains.
11. **Offline sync**: device registration, async download jobs, batched state sync — the server side for the native apps in `clients/`.

## Open questions (resolve before building the relevant milestone)

- Music: full-text search / smart playlists scope for milestone 7
- Live TV / DVR — explicitly out of scope for now, revisit after v1
