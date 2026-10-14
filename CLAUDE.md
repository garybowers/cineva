# Cineva

Cineva is a modern, self-hosted media suite written in Go — a replacement for Plex, Jellyfin, Audiobookshelf, and Calibre-Web in one server. It manages **movies, TV, music, audiobooks, podcasts, ebooks, and comics** as first-class media domains: it scans libraries on disk, enriches them with metadata, and streams or serves to clients with on-the-fly transcoding when needed. It ships as a single binary with the web client and admin UI embedded and FFmpeg statically linked; only the system's C and C++ runtime libraries come from the host.

## Product requirements

All media domains share one schema, scanner framework, API shape, and per-user state model from day one — they are first-class in the architecture even though they ship in stages (see Milestones). Nothing gets built as a bolt-on.

### Common to all domains
- **Library management**: libraries follow the Jellyfin/Plex model. A library is created with exactly one media type (Movies, TV, Music, Audiobooks, Podcasts, Books) chosen at creation time and immutable afterwards; it points at one or more filesystem paths. You can have any number of libraries per type (e.g. "Kids Movies" and "Movies"), but a library never spans multiple media types. A scanner walks the paths, identifies files, extracts technical info via ffprobe (or format parsing for books), and matches against the domain's metadata provider for artwork and details.
- **Per-user state**: resume positions (video playback, audio listen position, book reading position), finished flags, and continue-watching/listening/reading rows — same model everywhere.
- **Personalisation**: the user is in charge of their own experience — home screen layout per surface (phone, tablet, desktop, TV), which rows and in what order, seasonal rows, what feeds recommendations, playback behaviour down to a single series. The admin sets defaults per server and per group and locks only what is theirs to decide (a child's content rating ceiling). Composed server-side so every client agrees. Design in [server/docs/personalisation.md](server/docs/personalisation.md).
- **Devices**: a device is a screen (a browser, later a native app), recognised by its own cookie, and it outlives the sessions signed in on it: signing out, switching profile or a password change leave its name ("Living-room TV"), room and device settings in place (design in [server/docs/devices.md](server/docs/devices.md)). It holds one session at a time. Users see, name, sign out and forget their devices; an admin sees every device on its Devices page and can set one to sign in by itself as a chosen non-admin user whenever nobody is signed in on it, with "Switch user" still opening the profile picker. The play queue stays on the session, so another of the user's devices can take it over ("Continue from…"), and a queue can be sent to a device that has the app open ("Play on…"). Positions never travel with a queue: resume points are per user and item already.
- **Users & auth**: local accounts with argon2id-hashed passwords (or a quick PIN on the "Who's watching?" profile picker), session tokens for browsers, API keys for clients. One admin role, regular user role. Each user has an avatar: an uploaded photo, or their initial on a colour they choose (a theme token, never a colour value). Libraries are open to every user by default; an admin can restrict one to chosen users and groups (groups are per server: "Kids", "Grown-ups"). Admins see everything.
- **Downloads**: any item can be downloaded from the web client — the original file, or a portable transcode prepared server-side — so laptops can carry media offline while travelling.
- **Built-in web client**: mobile-first responsive layout, installable as a PWA — until the native apps exist, a phone browser is the mobile interface, so every screen must work at phone width.
- **Admin UI**: manage libraries (with a server-paged Manage list for very large ones), trigger/monitor scans, manage users and their devices, view active streams/transcodes, integrations, server settings.
- **Integrations**: Sonarr, Radarr and Lidarr are connected from the admin UI (`internal/arr`). Cineva registers its own webhook in each, authenticated by a per-connection secret, so an import rescans the affected library within seconds; the scheduled rescan (`libraries.scan_interval`, 15 minutes by default) is the fallback. Users can also request missing films and series from search (`internal/requests`): an admin sets each user's permission (none, needs approval, automatic) and each connection's default quality profile and root folder, and the request follows the title from grab to import. The focus stays on playing what is already there.
- **Import from Plex and Jellyfin** (`internal/importer`, design in [server/docs/import.md](server/docs/import.md)): from the admin's Users page, an admin connects to the old server, maps its users to Cineva's (or creates them, with a temporary password shown once), and chooses what to bring: watch state (watched, positions, last played) and music playlists and favourites. Titles match by TMDB id, then by the end of the file path, then (music) by artist, album and title; a dry run lists what does not match. An import never overwrites newer Cineva state, so running it twice changes nothing.
- **Notifications** (`internal/notify`, design in [server/docs/notifications.md](server/docs/notifications.md)): the admin sets up household destinations (ntfy, Gotify, Discord, Telegram, webhook) for household and admin events (a request waiting, new in the library, storage or Home Assistant down, a failed scan); each user can add their own in Settings for personal events (their requests, new episodes of series they are watching). What one person watches never goes to a shared channel, a household message names only titles from the libraries chosen for it, and a user's own destination can reach only fixed services or the ntfy server the admin allows. Messages are queued and retried; nothing is sent anywhere no one set up.
- **Home Assistant** (`internal/homeassistant`, design in [server/docs/homeassistant.md](server/docs/homeassistant.md)): one connection per server, made from the Integrations page with a long-lived access token. An admin exposes chosen entities (lights, switches, fans, covers, media players, scenes, scripts, cameras) with the library access model; cameras start admins-only. A device can be put in a Home Assistant area (its room), so the player shows that room's controls; the client's Rooms page shows every room, the cameras (snapshots, and live view through Home Assistant's HLS, proxied) and the modes. A **mode** ("Movie mode") is an admin-built list of steps: Home Assistant actions from a fixed allow-list, waits, and playing a title on a device. Home Assistant stays the automation engine: Cineva fires `cineva_playback_*` events into it rather than growing its own triggers. Browsers never talk to Home Assistant, and nothing about it is federated.

### Video — movies & TV (stage 1, v1)
- Metadata from TMDB: artwork, synopsis, cast, episode data.
- Direct play when the client supports the file natively; on-the-fly HLS transcoding via ffmpeg when it doesn't. Seeking within transcoded streams must work (segment-based, restart-at-offset).
- Subtitles: text-based (SRT/ASS, embedded or sidecar) extracted and served as WebVTT for the player to overlay; image-based (PGS/VobSub) burned in during transcode.
- **Player — movies vs TV**: one player core, two presentations. Movies are standalone: play, resume, and return to the detail page when finished. TV is hierarchical (series → season → episode) and the player is next-up aware:
  - **Next-episode autoplay**: as an episode enters its end-credits window, the player overlays an up-next card (next episode's thumbnail + countdown) and auto-plays it unless dismissed. Where credits detection has found the real credit start (`internal/segments`, by the end theme episodes share) the card appears there and autoplay counts down 15 seconds; otherwise the heuristic window (final ~40 seconds) applies and autoplay waits for the end of the file. Autoplay is a per-user setting (on by default).
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
Several Cineva servers (e.g. ours, Mum and Dad's, Rob's) join a **nebula** and behave as **one Cineva**: whichever member's address you open, you get the same single UI across everyone's libraries, signed in as yourself. The full design is in [server/docs/federation.md](server/docs/federation.md); this is the contract.

- **Nebula membership**: every server has an Ed25519 identity keypair (from first startup). An admin creates a nebula; other servers join with a one-time invite code from any member, and joining introduces them to every member. Any server can remove itself from its nebula at any time on its own admin's say-so, dropping what it cached from the others (and they from it); a member can also block a peer locally. Underneath, each pair of members has its own mTLS link with pinned keys.
- **One UI from any member**: every member serves the same nebula-wide web client. Clients only ever talk to the server whose address they opened, which proxies media, metadata, sign-in and watch state from the other members.
- **Merged and per-server views**: libraries are merged by media type by default — one Movies, one TV Shows — with copies on different members matched by a content key (`tmdb:movie:<id>`, `tmdb:tv:<id>:s<n>e<n>`, …) and played from the best available copy. A scope picker narrows any view to a single member's libraries, and a member's own address remains reachable directly.
- **Reachability**: designed for the worst case — no member accepts inbound connections. Members connect outbound to a rendezvous (any reachable member, else a small relay on a machine the family controls), which helps them connect directly and relays when it cannot; mTLS runs end to end so a relay sees only ciphertext. Members already joined by a VPN (Tailscale, WireGuard, Slack's Nebula — no relation to the name) just use those addresses.
- **Metadata**: members pull incremental metadata (and intro/credits segments) from each other into a local cache, as `items` rows under libraries with `source = 'federated'`, so browsing, search and recommendations are instant and work unchanged.
- **Presence-gated visibility**: a heartbeat runs over each link; an offline member's titles leave every view, or switch to another copy — never broken entries, never dead play buttons. The cache is kept so they return instantly.
- **Playback**: the owning server transcodes its own media; the serving server proxies the stream. Remote quality defaults to what the link can carry.
- **Sign in anywhere**: every account belongs to one home server, the authority for who the user is and what they may see. Another member relays the credential check to the home server and holds a delegated, revocable session. A member's admin cannot grant a visiting user more than their home server allows.
- **Access control**: same model as on a single server — every library is open to everyone in the nebula unless its owning server restricts it to chosen users and groups, which may belong to any member. The home server vouches for a user's identity and group memberships; the owning server decides.
- **Watch state** stays on the user's home server, keyed by content key so it follows the user across copies and members; other members forward reads and writes to it and buffer writes while it is unreachable.
- **Versions**: the federation protocol is versioned (`/federation/v1`) with capability flags; members below the nebula's minimum are hidden with the reason shown, and the admin flags version drift.

### Offline sync for native clients (designed now, built post-v1)
Native mobile/laptop apps (planned, will live in `clients/`) need true offline media for travel without connectivity. The server side:

- **Download API**: a client requests an item at a profile (original, or a portable transcode — MP4 for video, Opus/AAC for audio; books are small enough to always take verbatim); transcoded downloads are prepared as async jobs the client polls, then fetched with resumable range requests.
- **Device registration**: each app instance registers as a device under its user; the server tracks what each device has downloaded so the admin UI can show it and users can manage storage remotely.
- **State sync**: clients play/read offline and batch progress locally, then push it on reconnect (last-write-wins per item). Resume positions survive a flight.
- The v1 web-client download button is the primitive; this milestone adds the device/sync layer on top. Design the download endpoints in the OpenAPI spec from the start so native apps aren't blocked on a redesign.

### Later (don't build speculatively)
- Visual credits detection (dark frames, scrolling text) for shows whose end music changes every episode — the audio comparison in `internal/segments` cannot see their credits, and they fall back to the fixed window
- Photo libraries
- Hardware-accelerated transcoding (VAAPI/QSV/NVENC)
- OIDC/SSO login
- Remote access relay for *clients* away from home (members of a federation nebula reach each other through the nebula's rendezvous; this is the phone-on-a-train case)
- PostgreSQL backend for very large libraries

### Licence
Cineva is AGPL-3.0-or-later (`LICENSE`); the bundled FFmpeg build is GPLv3 (`--enable-gpl --enable-version3`, with x264/x265). Every dependency must be compatible with AGPLv3: MIT, BSD, Apache-2.0, LGPL, GPLv3 and AGPLv3 are; GPLv2-only and anything with non-commercial or field-of-use restrictions are not. Check a new dependency's licence before adding it. Outside contributions come in under the Contributor Licence Agreement (`CLA.md`): signatories are listed in `CLA-SIGNATURES.txt`, every commit is signed off (`git commit -s`), and `scripts/check-cla.sh` enforces both on pull requests; the maintainer is exempt.

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
- **Transcoding**: ffmpeg via [ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo) (statically linked ffmpeg; CGO). Hardware transcoding uses the host's `libva`, loaded with `dlopen` at first use (`internal/media/libva_shim.c`) rather than linked — a VA-API driver is version-locked to the libva that loads it (`__vaDriverInit_<major>_<minor>`), so the copy bundled in the static archive only matches by luck, while a hard link would stop the binary starting at all on a host without libva. The shim keeps both properties: the host's libva is used where present, and where it is absent the server falls back to software like any machine without a GPU. Media inspection uses the same bindings (`internal/media.Probe`). The package is vendored as a git submodule at `third_party/ffmpeg-statigo` with a `replace` directive in go.mod — after cloning run `make setup` once to fetch the submodule, build the static library (not in git) and install the npm dependencies before building.

  **We build our own ffmpeg rather than using upstream's prebuilt library.** Upstream populates `HWAccels` only for VideoToolbox on macOS, so its Linux builds ship `--enable-hwaccel=` for nothing. VAAPI is the one backend with no standalone decoders — it decodes *only* through an hwaccel — so a stock library gives AMD (and Intel-via-VAAPI) hosts hardware encode but software decode. `third_party/patches/ffmpeg-statigo-hwaccel.patch` adds the six Linux VAAPI hwaccels plus the `pgssub` decoder, and the `$(LIB)` target applies it to the pinned submodule and builds from source. Re-applying the patch is a no-op, so a rebuild does not need a clean submodule. Keep the patch as the unit of change: it is also what we would raise upstream. Never replace the library with `cmd/download-lib` — hardware decode silently reverts to software and only the startup log says so.
- **Streaming**: HLS (fMP4 segments) for video transcodes; HTTP range requests for direct play and audio; Opus/AAC for audio transcodes.
- **Book handling**: EPUB/OPF parsed natively in Go; CBZ via `archive/zip`, CBR via a pure-Go RAR reader (e.g. `nwaples/rardecode`); OPDS 1.2 for the catalogue feed.
- **API**: REST, JSON, spec-first. The OpenAPI 3.1 spec lives at `server/docs/openapi.yaml` and is the contract — update the spec in the same change as the handlers. Third-party clients generate SDKs from it. The server embeds it (`server/docs`), serves it at `/api/v1/openapi.yaml`, and renders it as an interactive reference with Swagger UI at `/api/docs` (`server/ui/apidocs`, public). Two tests keep it honest: `docs/docs_test.go` (it parses, every `$ref` and path parameter resolves, every operation has a unique operationId and exactly one declared tag) and `internal/api/spec_test.go` (every registered `/api/v1` route is documented, and every documented operation is a route).
- **Web client & admin UI**: Svelte + Vite. Built to static assets and embedded in the Go binary with `go:embed`. EPUB rendering via epub.js (or foliate-js); audio playback via the Media Session API for lock-screen controls.
- **Metadata providers**: one interface per domain — TMDB (video), MusicBrainz + Cover Art Archive (music), Open Library (books/audiobooks), RSS (podcasts) — so alternatives can be added.
- **Federation**: Ed25519 server identity, nebulae joined by invite code, mTLS links with pinned peer keys over direct, VPN or relayed paths (outbound-only via a rendezvous), pull-based metadata sync, heartbeat-gated visibility, content-key matching for merged libraries and portable watch state (see Federation requirements above and `server/docs/federation.md`).

## Architecture & layout

```
server/                 Go module (all backend code)
  main.go               entrypoint — calls cmd.Execute(), nothing else
  cmd/                  Cobra commands (root.go, serve.go, ...) + Viper wiring
  internal/api/         Echo handlers, middleware, request/response types
  internal/library/     scanning framework + per-domain scanners/matchers, scheduled rescans
  internal/media/       media inspection, transcode sessions, HLS, audio streaming, hardware acceleration
  internal/playback/    registry of active playbacks (who is watching what, on which client)
  internal/metadata/    provider interfaces + TMDB/MusicBrainz/Open Library clients
  internal/segments/    intro & credits detection: audio fingerprints compared across a season's episodes
  internal/trickplay/   seek-bar preview sprite sheets, built in the background
  internal/background/  the gate background work runs through: schedule, pause, read-speed cap, yielding to playback
  internal/arr/         Sonarr/Radarr/Lidarr connections: webhook registration and the events that trigger rescans
  internal/requests/    requests for missing films and series: permissions, approval, sending to Sonarr/Radarr, progress
  internal/importer/    importing from Plex and Jellyfin: sources, title matching, the runner
  internal/notify/      notifications: destinations by kind, the event catalogue, routing, the send queue
  internal/homeassistant/ Home Assistant: websocket connection and state, the controls allow-list, camera proxy, modes, playback events
  internal/avatar/      uploaded pictures turned into avatars (upright, square, small JPEG)
  internal/events/      in-process event bus behind the /events stream; events can be addressed to one user or device
  internal/stats/       host and transcode statistics for the admin dashboard
  internal/store/       repository interfaces + SQLite implementation, migrations
  internal/auth/        sessions, password hashing, API keys
  internal/prefs/       preference registry and resolution: device, user, group and server layers, admin locks
  internal/version/     the version string, stamped in at build time
  internal/logging/     slog setup: pretty console or JSON, component loggers, the startup card
  internal/books/       (planned) EPUB/PDF/CBZ/CBR parsing, OPDS feed
  internal/podcast/     (planned) RSS polling, episode download/retention
  internal/federation/  (planned) nebula membership, invites, mTLS links, rendezvous/relay, sync, heartbeat, delegated sign-in
  ui/client/            built-in web client (Svelte)
  ui/admin/             admin UI (Svelte)
  ui/apidocs/           the API reference at /api/docs: Swagger UI over docs/openapi.yaml
  ui/themes/            one folder per theme: theme.css (design tokens) + theme.json manifest
  docs/                 openapi.yaml (embedded and served), design notes
clients/                standalone client apps — our planned native mobile/laptop apps and third-party clients — separate from server/ui
third_party/            ffmpeg-statigo submodule and the patch we build it with
.github/workflows/      CI: build and test on every push, release on a v* tag
```

Rules:
- Server code never lives outside `server/`; the built-in UIs live in `server/ui/client` and `server/ui/admin` (not `clients/`, which is reserved for standalone client apps).
- Cobra commands in `server/cmd/` do flag/config parsing and wiring only — construct services and call them. No business logic in commands or handlers.
- Long-running work (scans, transcodes, feed polls) runs in managed goroutines with context cancellation; expose progress over the API, never block a request on a scan.
- All configuration goes through Viper (config file + `CINEVA_*` env bindings + flags); nothing reads `os.Getenv` directly. `server/cineva.example.yaml` is committed and is the living reference for what is configurable: when a feature adds a config key, add it there with a comment naming its `CINEVA_*` env equivalent, in the same change. `server/cineva.yaml` is the local-dev copy and stays gitignored — it holds the TMDB key.
- New media domains reuse the shared item/library/user-state schema and scanner framework — a domain is a set of implementations of existing interfaces, not a parallel subsystem.

## Build, test & release

The Makefile at the repository root is the way in; all targets work from a fresh clone.

- `make setup` — fetches the ffmpeg-statigo submodule, applies `third_party/patches/ffmpeg-statigo-hwaccel.patch`, builds the static FFmpeg library (slow — most of an hour — and needed once per submodule bump), and installs the UIs' npm dependencies. Building FFmpeg needs `yasm nasm meson gperf python3` and a Rust toolchain with `cargo-c`.
- `make ui` — builds the web client, admin UI and API reference into `server/ui/*/dist`; `make build` runs it first. The built files are not committed: each dist folder holds only a `.gitkeep`, which every build puts back so go:embed has a file to match, and a binary built without the UIs serves a page saying to run `make ui` (and logs a warning at startup).
- `make run` — `go run . serve` from `server/`; `make test`, `make lint` (gofmt + go vet); `make build` writes `bin/cineva`.
- `make build` stamps `VERSION` (default: `git describe`) into `internal/version` and `TMDB_KEY` into `internal/metadata` via ldflags; `OUT` changes the output path. `TMDB_KEY` lives in the gitignored `local.mk` — never in a committed file, and the build recipes are silenced so make never echoes it.
- Never put a binary called `cineva` in a working directory the server runs from: Viper probes the extensionless name `cineva` as a config file and fails parsing the binary. That is why the binary goes to `bin/`, and why the README tells users to run it from its own data directory.
- The server needs the host's glibc and libstdc++ (cgo links them, and `libva` is loaded with `dlopen`, which rules out a static musl build); release binaries are built on Ubuntu 24.04, so they need glibc 2.39 or newer. The container image (`Dockerfile`, Debian 13) brings its own glibc, libstdc++, libva and VA-API drivers, so it runs anywhere; it packages a prebuilt binary from `dist/` rather than compiling one.

CI (`.github/workflows/ci.yml`) builds, lints and tests linux/amd64 and linux/arm64 on every push and pull request, caching the FFmpeg library on the submodule commit plus the patch's hash. Pushing a `v*` tag (`git tag v0.2.0 && git push origin v0.2.0`) also publishes a GitHub Release with `cineva-linux-amd64`, `cineva-linux-arm64` and `SHA256SUMS`; a tag containing a hyphen is a pre-release. The same tag also pushes a two-architecture image to Docker Hub, built from those binaries. Repository settings: secret `TMDB_KEY` (the key compiled into release binaries), secrets `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` with variable `DOCKERHUB_REPOSITORY` (where the image goes), and variable `ARM64_RUNNER` (the runner label for arm64, when GitHub's own arm64 runners are not available to the repository).

## Conventions

- **No emoji** in documentation, code comments, UI copy or CI step names — words, and Lucide icons in the UIs.
- **British English everywhere prose appears**: UI copy, documentation, code comments, commit messages, API descriptions (colour, catalogue, honours, -ise endings, licence as noun). Exceptions only where syntax or an external API dictates American spellings — CSS properties (`color:`), DOM/web APIs (`behavior: 'smooth'`), library identifiers (`Maximize` icon imports), and established wire-format field names.
- Logging goes through `log/slog`, one component logger per package: `var logger = logging.For("scan")` (`internal/logging`), then `logger.Warn("artwork download failed", "title", t, "err", err)` — a short lowercase message, values as attributes, never interpolated. Levels: Error for lost work or something the operator must act on, Warn for degraded-but-handled, Info for lifecycle an operator wants by default (keep it calm), Debug for per-item detail. Never the standard `log` package, and never `fmt.Print` from the server (CLI command output excepted). Output is aligned and coloured in a terminal, JSON elsewhere (`log.level`, `log.format`).
- Standard Go style: `gofmt`, `go vet`, and `golangci-lint` must pass (`make lint` runs all three, and CI runs `make lint`; the config is `server/.golangci.yml`). Errors are wrapped with `fmt.Errorf("...: %w", err)` and handled, never discarded.
- Table-driven tests alongside the code (`_test.go`); the store gets tested against a real temp SQLite file, not mocks.
- API routes are versioned under `/api/v1/`. Breaking the API means a new version, not a changed response shape.
- Database schema changes always come as a new migration file — never edit an applied migration.
- Frontend: TypeScript, Svelte 5 runes, no component library lock-in yet; keep the API client in one generated/typed module. Build mobile-first: new screens are designed at phone width and scale up, not the reverse.
- Theming: all UI colours come from the CSS custom-property token set (`--bg`, `--panel`, `--border`, `--text`, `--muted`, `--accent`, `--accent-2`, `--on-accent` (text on an accent fill), `--ok`, `--err`, plus `--chart-1..3` for chart series — categorical, fixed order, validated for colour-vision deficiency against `--panel`) — never hardcode a colour in a component. Themes live in `server/ui/themes/<id>/` (served at `/themes/`, listed by `/api/v1/themes`) and one theme skins both UIs; adding a token means updating every theme folder in the same change.

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
10. **Federation**: nebulae and invites, links and heartbeat, metadata sync with per-server scope, proxied playback, merged views by content key, sign-in anywhere with watch state at home, rendezvous/relay — staged 10a–10f in `server/docs/federation.md`. Groundwork worth doing before it (content keys on items, watch state keyed by them) is listed there under "Preparing now".
11. **Offline sync**: device registration, async download jobs, batched state sync — the server side for the native apps in `clients/`.
12. **Home Assistant**: connection, exposure and access, rooms, controls, cameras, modes and playback events (`server/docs/homeassistant.md`). Built alongside v1 from October 2026, outside the numbered order.

## Open questions (resolve before building the relevant milestone)

- Live TV / DVR — explicitly out of scope for now, revisit after v1
