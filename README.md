# Cineva

Cineva is a self-hosted media server for films, TV, music — and, in time,
audiobooks, podcasts, ebooks and comics. It scans your libraries, fetches
artwork and details, and streams to a browser on any device, transcoding on
the fly when a device cannot play a file as it is. It is one program: the web
client and the admin UI are built in, and there is nothing else to install.

It is written in Go for Linux, and is under active development. Video and
music are in daily use; audiobooks, podcasts and books are on the way. The
video work is done by FFmpeg itself, built into the binary through
[ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo) — see
[Thanks](#thanks).

![Cineva's home screen](docs/screenshots/home.webp)

## Why another media server?

Because Plex, for years the obvious choice, has stopped putting the people
who run it first. Your server, your disks and your files, and yet:

- **Watching your own films away from home now costs money.** Since April
  2025 Plex no longer offers remote playback of personal media as a free
  feature: you, or whoever runs the server, need a Plex Pass, or a separate
  Remote Watch Pass. The same change put the Plex Pass up to $6.99 a month,
  $69.99 a year or $249.99 for life
  ([MacRumors](https://www.macrumors.com/2025/03/19/plex-price-increase)).
- **Using your own graphics card is a paid extra.** Hardware-accelerated
  streaming, which lets a modest server transcode for several screens, is a
  premium feature that needs an active Plex Pass
  ([Plex support](https://support.plex.tv/articles/115002178853-using-hardware-accelerated-streaming/)).
- **Your account lives on their servers, and so does the risk.** Signing in
  goes through plex.tv even when the server is in the next room, so when
  Plex or your internet connection is down, apps can refuse to play files on
  your own network
  ([Plex forums](https://forums.plex.tv/t/plex-by-default-is-useless-even-as-a-local-server-when-internet-down/930093)).
  And in September 2025, as in 2022, Plex asked users to reset their
  passwords after a breach exposed email addresses, usernames, hashed
  passwords and authentication data
  ([TechCrunch](https://techcrunch.com/2025/09/09/plex-urges-users-to-change-passwords-after-data-breach/)).
- **What you watch has been treated as theirs to share.** Plex's "Week in
  Review" emails told people what their friends had been watching, an
  opt-out feature that caught many by surprise
  ([404 Media](https://www.404media.co/plex-users-fear-discover-together-week-in-review-feature-will-leak-porn-habits-to-their-friends-and-family/)).
- **Your library is no longer the point of the app.** The 2025 redesign
  dropped features people used, such as Watch Together, and left many
  feeling their own media now comes second to Plex's ad-supported films and
  live channels ([HotHardware](https://hothardware.com/news/plex-punts-a-popular-feature),
  [Plex forums](https://forums.plex.tv/t/new-ui-is-an-awful-experience/931048)).

Cineva goes the other way. It runs entirely on your own machine and needs
an account with nobody: users, passwords and watch history stay in its
database, on your disk, and it plays your files on your network whether or
not the internet is up. Nothing is behind a paywall. There is no telemetry:
the only things it fetches from the internet are artwork and details for
your titles. There are no ads or bundled channels, and what you watch is
never shared with anyone unless you send it somewhere yourself. It is free
software under the AGPL, so it stays that way.

## What it does

### Watching and listening

- **Libraries** — Movies, TV and Music, each pointing at one or more folders.
  Scans are incremental and run every 15 minutes by default, so new files
  appear on their own; Sonarr, Radarr and Lidarr can trigger one the moment
  they import something.
- **Metadata** — posters, backdrops, synopses, cast and episode details from
  TMDB; music from embedded tags, MusicBrainz and the Cover Art Archive.
  Search ignores case and accents: "amelie" finds *Amélie*.
- **Playback** — direct play when the browser can handle the file, otherwise
  HLS transcoding with seeking. Text subtitles are served as WebVTT, and the
  seek bar shows preview pictures.
- **Hardware transcoding, free** — the GPU decodes and encodes, so a small
  machine can transcode 4K for several screens at once: AMD and
  Intel through VA-API, NVIDIA through NVENC, and Intel Quick Sync. Cineva
  finds what the machine has when it starts and uses it, falling back to
  the processor where there is none. Nothing to buy or unlock.
- **Series** — next-up tracking, intro and credits detection from the audio
  episodes share (with "Skip intro", and times that can be corrected by
  hand), and an up-next card that autoplays the next episode.
- **Music** — albums and artists, gapless playback, play counts, star
  ratings, and playlists: made by hand, or smart playlists that fill
  themselves from rules ("rated four stars or more, not played in a month").
- **Continue here** — while one of your devices plays, the others offer to
  carry on: pick up on your phone what the TV is playing, and the TV pauses.
  A queue can also be sent the other way ("Play on…").
- **TV mode** — the same client sized for across the room and worked with a
  remote: arrow keys move between posters, Back goes back, and the player
  takes the remote. It switches on by itself in a television's own browser
  (Fire TV, Android TV, LG, Samsung).
- **Downloads** — any title can be downloaded for offline viewing; albums and
  artists as a ZIP, original or transcoded.
- **Themes** — six built in, and your own: pick every colour (with a
  readability check as you go), add a background picture and a gentle
  effect, and an admin can share one with the household. Halloween,
  Christmas and Easter themes switch themselves on for the season, with
  snow, drifting leaves or blossom, and step aside when you would rather
  keep yours.
- **Installable** — the web client works at phone width and installs as an
  app (PWA).

### Your household

- **People** — accounts with passwords or a quick PIN, a "Who's watching?"
  profile picker with photos, groups, and per-library access. Everyone has
  their own preferences (audio and subtitle languages, quality, intro
  skipping, autoplay); an admin sets the defaults and can lock what is
  theirs to decide.
- **Devices** — every screen is a device you can name ("Living-room TV") and
  sign out remotely. A shared screen can sign itself in as a chosen profile,
  with "Switch user" a PIN away. The admin pages are never offered on a TV.
- **Home Assistant** — rooms, lights, blinds, the amplifier and cameras in
  Cineva, exposed only to the people the admin chooses. Modes such as "Movie
  mode" dim the lights, close the blinds, set the amplifier and start the
  film in one press, and the player shows the controls of the room it is in.
- **Requests** — people ask for films and series that are not there yet; an
  admin approves them (or lets some people skip that), and Sonarr or Radarr
  fetches them.
- **Notifications** — to ntfy, Gotify, Discord, Telegram or a webhook: for
  the household (a request waiting, something new, storage down) and for
  each person (their requests, new episodes of what they are watching).
- **Moving in** — bring watch history, music playlists and favourites over
  from Plex or Jellyfin.

## Screenshots

| | |
|---|---|
| ![A film's page](docs/screenshots/film.webp) | ![A season, with the next episode to resume](docs/screenshots/season.webp) |
| A film | A season, ready to resume |
| ![An album with star ratings](docs/screenshots/album.webp) | ![Theme previews in Settings](docs/screenshots/themes.webp) |
| An album, with ratings | Themes |
| ![TV mode, focus on a poster](docs/screenshots/tv.webp) | ![The phone offering to continue what the TV plays](docs/screenshots/phone.webp) |
| TV mode, worked with a remote | Continue on the phone what the TV is playing |
| ![The player with the room's controls and modes](docs/screenshots/player-room.webp) | ![Rooms: modes, controls and cameras](docs/screenshots/rooms.webp) |
| The living room's controls, from the player | Rooms, modes and cameras from Home Assistant |
| ![Building modes in the admin UI](docs/screenshots/admin-modes.webp) | |
| Modes in the admin UI | |

The Home Assistant pictures are of a demonstration instance, not a real
house.

## Roadmap

Where Cineva is, and where it is going. There are no dates: things ship
when they work properly. Finer-grained plans and known gaps are in
[BACKLOG.md](BACKLOG.md).

**Working today**

- Films and TV: scanning, TMDB metadata, direct play and transcoding with
  hardware acceleration, subtitles, intro and credits detection, next-up and
  autoplay.
- Music: albums and artists, gapless playback, ratings, manual and smart
  playlists.
- The web client on phones, desktops and televisions (TV mode), installable
  as an app; the admin UI.
- Accounts, profiles, groups and per-library access; per-person
  preferences with household defaults; devices, "Continue here" and "Play
  on…".
- Home Assistant rooms, cameras and modes; Sonarr, Radarr and Lidarr;
  requests; notifications; importing from Plex and Jellyfin.

**Next**

- **Audiobooks** — M4B with chapters, multi-file books, playback speed and
  exact resume.
- **Podcasts** — subscribe by RSS feed; new episodes downloaded and old ones
  cleared by a retention rule.
- **Books and comics** — EPUB with a built-in reader that remembers your
  place, PDF, CBZ and CBR with a page reader, and an OPDS catalogue for
  reader apps such as KOReader.
- **More of it your way** — per-series and per-film settings (languages,
  subtitles, intro skipping), home screens arranged per device, saved views,
  control over recommendations, spoiler protection, seasonal rows, and an
  age-rating limit for children's profiles
  ([design](server/docs/personalisation.md)).
- **Requests** — telling people when what they asked for has arrived, and a
  calendar of upcoming episodes.
- **Hardware transcoding** — NVIDIA and Intel Quick Sync work, but have had
  far less testing than AMD through VA-API; HDR tone mapping on the GPU.

**Later**

- **Federation** — several households' servers (ours, the parents', a
  friend's) joined into one Cineva: open any of them and see everyone's
  libraries together, signed in as yourself, with watch history that follows
  you. Works even when no server can accept incoming connections
  ([design](server/docs/federation.md)).
- **Native apps** — phone, tablet and laptop apps with true offline
  downloads for travelling, syncing what you watched when you are back
  online. A Fire TV app wrapping TV mode comes with them.
- **Not yet decided** — photo libraries, sign-in through an identity provider
  (OIDC), a relay for watching away from home without opening ports, and
  PostgreSQL for very large libraries.
- **Live TV and recording** — watch an HDHomeRun's channels on any screen
  and record on a schedule: a programme guide (XMLTV, Schedules Direct),
  series recordings of new episodes, pause and rewind live TV. Tuner cards
  in the server after that. On the backlog, not yet designed.

## Free and open software

Cineva is free software, and is built to stay that way.

- **The licence protects you.** Cineva is under the GNU Affero General Public
  Licence, version 3 or later. You may use it, study it, change it and share
  it; and anyone who runs a changed version as a service for other people
  must offer them its source too, so improvements cannot be locked away on
  someone's server.
- **Contributions can never become proprietary.** Contributors keep the
  copyright in their work. The [Contributor Licence Agreement](CLA.md) lets
  the project be relicensed only under a licence approved by the Open Source
  Initiative or the Free Software Foundation — never a closed one.
- **Made from free software, all the way down.** Go, FFmpeg (a GPLv3 build,
  with x264 and x265), [ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo),
  SQLite, Svelte. Every dependency is checked before it is added: MIT, BSD,
  Apache, LGPL, GPLv3 and AGPLv3 are welcome; anything GPLv2-only, or with
  non-commercial or field-of-use restrictions, is not.
- **Yours, not a platform.** No account with us or anyone else, no
  telemetry, no DRM, no store and no ads. Your library and everyone's watch
  history sit in one SQLite file on your own disk.
- **Open to other software.** Everything the web client does goes through a
  documented REST API ([OpenAPI 3.1](server/docs/openapi.yaml)), so anyone
  can write a client or a script against it, and OPDS will open the book
  libraries to reader apps.

## How AI is used

Cineva is written with the help of AI coding assistants, mainly
[Claude Code](https://claude.com/product/claude-code). It is said here
plainly because you should know how the software you run is made, and what
keeps it sound.

- **A person is in charge.** The maintainer decides what Cineva does and how
  it should look and behave, sends back what is not right, and uses it every
  day on real servers, televisions and phones. The assistant does not decide
  what gets built or what ships.
- **The rules are written down.** [CLAUDE.md](CLAUDE.md) sets out the
  requirements, architecture, conventions and licence rules that every
  change must follow — the same document human contributors read.
- **Everything is checked the same way.** Changes come with tests (the
  database is tested against real SQLite files, and the API against its
  published specification), and every push is built, linted and tested on
  amd64 and arm64 in CI, whoever or whatever wrote the change.
- **No AI in the product.** Cineva has no AI features and talks to no AI
  service. Intro and credits detection compares the audio that episodes
  share; recommendations are plain rules over your own library and history.
  Nothing you watch is sent anywhere, to train a model or otherwise.
- **Contributions.** Changes written with an assistant are welcome, on the
  same terms as any other: you are responsible for what you submit, must
  understand it and have tested it, and sign it off under the
  [CLA](CLA.md).

## Requirements

- Linux on amd64 (x86-64) or arm64.
- Either Docker, or for the plain binary: glibc 2.39 or newer — Ubuntu 24.04,
  Debian 13, Fedora 40 or later. The binary carries FFmpeg inside it but uses
  the system's C and C++ runtime libraries; the container image brings its
  own, so with Docker the host's versions do not matter.
- For hardware transcoding: a GPU with a VA-API driver (`mesa-va-drivers` or
  `intel-media-va-driver`; the container image includes both). Without one,
  Cineva transcodes in software.
- To watch: a browser from 2023 or later — Chrome or Edge 111, Firefox 113,
  Safari 16.2 (iOS and iPadOS 16.2 on phones and tablets). The interface's
  colours are mixed with CSS `color-mix()`, which older browsers lack.

## Running with Docker

The image is the easiest way in: it runs on any distribution, and includes
the GPU drivers for hardware transcoding.

```sh
docker run -d --name cineva \
  -p 4747:4747 \
  -v cineva-data:/data \
  -v /path/to/media:/media:ro \
  --device /dev/dri \
  --group-add "$(stat -c %g /dev/dri/renderD128)" \
  --restart unless-stopped \
  <dockerhub-user>/cineva:latest
```

- `/data` holds the database, the artwork cache and, if you want one, a
  `cineva.yaml`. Keep it on a volume or a host folder so it survives
  upgrades.
- Mount your media read-only and point libraries at the paths inside the
  container (`/media/Movies`, `/media/TV`).
- `--device /dev/dri` and `--group-add` (the host's `render` group) let it
  use the GPU; leave both out to transcode in software.
- Settings can also be passed as environment variables, for example
  `-e CINEVA_LOG_LEVEL=debug`.

With Compose:

```yaml
services:
  cineva:
    image: <dockerhub-user>/cineva:latest
    ports: ["4747:4747"]
    volumes:
      - cineva-data:/data
      - /path/to/media:/media:ro
    devices: ["/dev/dri:/dev/dri"]
    group_add: ["989"]  # the host's render group: stat -c %g /dev/dri/renderD128
    restart: unless-stopped
volumes:
  cineva-data:
```

Upgrade with `docker pull` and recreating the container; the database
migrates itself on start.

## Installing the binary

Download the binary for your machine from the
[Releases](https://github.com/garybowers/cineva/releases) page, along with
`SHA256SUMS`, and install it:

```sh
sha256sum --check --ignore-missing SHA256SUMS
sudo install -m 755 cineva-linux-amd64 /usr/local/bin/cineva
cineva version
```

With the GitHub CLI: `gh release download --repo garybowers/cineva --pattern 'cineva-linux-*' --pattern SHA256SUMS`.

## First run

Cineva keeps its database, artwork cache and configuration in the directory
it is started from. Give it a directory of its own:

```sh
sudo mkdir -p /var/lib/cineva && sudo chown "$USER" /var/lib/cineva
cd /var/lib/cineva
cineva serve
```

Run it from a directory of its own rather than from wherever the binary
sits: Cineva looks for a configuration file called `cineva` in its working
directory, and a binary of that name there would be read as one.

Then open `http://<server>:4747` in a browser:

1. The first visit asks you to create the admin account. (Or create it from
   the command line: `cineva user add <name> --admin`.)
2. Open the admin UI (`http://<server>:4747/admin`, or "Admin" in the account
   menu), choose **Libraries**, and add a library: its type — Movies, TV or
   Music — and the folders it should scan.
3. Press **Scan** on the new library — or leave it to the scheduled rescan,
   which picks it up within 15 minutes. Titles appear as they are found.

Release binaries include a TMDB API key. A binary built without one needs a
key of your own (free from [themoviedb.org](https://www.themoviedb.org/settings/api)),
set as `metadata.tmdb.api_key` in the configuration.

### Running as a service

```ini
# /etc/systemd/system/cineva.service
[Unit]
Description=Cineva media server
After=network-online.target
Wants=network-online.target

[Service]
User=cineva
Group=cineva
# render: access to the GPU for hardware transcoding
SupplementaryGroups=render video
WorkingDirectory=/var/lib/cineva
ExecStart=/usr/local/bin/cineva serve
Restart=on-failure

[Install]
WantedBy=multi-user.target
```

```sh
sudo useradd --system --home /var/lib/cineva cineva
sudo chown -R cineva:cineva /var/lib/cineva
sudo systemctl enable --now cineva
```

The `cineva` user needs read access to your media folders.

## Configuration

Settings come, in order of precedence, from command-line flags, `CINEVA_*`
environment variables, a configuration file, and built-in defaults. The file
is `cineva.yaml` in the working directory, else `/etc/cineva/cineva.yaml`, or
wherever `--config` points.
[`server/cineva.example.yaml`](server/cineva.example.yaml) lists every setting
with its environment variable; the ones most often changed:

| Setting | Default | Environment variable |
|---|---|---|
| `server.port` | `4747` | `CINEVA_SERVER_PORT` |
| `database.path` | `cineva.db` | `CINEVA_DATABASE_PATH` |
| `libraries.artwork_dir` | `artwork` | `CINEVA_LIBRARIES_ARTWORK_DIR` |
| `libraries.scan_interval` | `15m` (`0` turns it off) | `CINEVA_LIBRARIES_SCAN_INTERVAL` |
| `transcode.dir` | a directory under `/tmp` | `CINEVA_TRANSCODE_DIR` |
| `transcode.hwaccel` | `auto` | `CINEVA_TRANSCODE_HWACCEL` |
| `metadata.tmdb.api_key` | built in to releases | `CINEVA_METADATA_TMDB_API_KEY` |
| `auth.secure_cookies` | `false` — set `true` behind HTTPS | `CINEVA_AUTH_SECURE_COOKIES` |
| `log.level` | `info` (`debug`, `warn`, `error`; or `--log-level`) | `CINEVA_LOG_LEVEL` |
| `log.format` | `auto` — readable in a terminal, JSON otherwise | `CINEVA_LOG_FORMAT` |

`cineva config validate` prints the effective configuration.

Other commands: `cineva scan [library]` scans from the command line, and
`cineva user add` / `cineva user passwd` create accounts and reset passwords.

## API

Everything the web client does goes through a REST API, which apps and
scripts can use too: create an API key under Account settings and send it as
`Authorization: Bearer <key>`. An interactive reference is served at
`http://<server>:4747/api/docs`, and the OpenAPI 3.1 specification it is built
from at `/api/v1/openapi.yaml`, for generating client libraries.

## Integrations

All of these are set up in the admin UI, under **Integrations**.

- **Sonarr, Radarr and Lidarr** — add each with its URL and API key (in
  Sonarr: Settings, General, Security). Cineva registers a webhook called
  "Cineva" in it, and from then on each import, upgrade, rename or deletion
  triggers a rescan of the affected library. If it runs in a container or
  on another machine, set "Cineva address" to the address it can reach
  Cineva on. Sonarr and Radarr also take the requests people make, each
  with a default quality profile and folder.
- **Home Assistant** — add its address and a long-lived access token (made
  on your profile page in Home Assistant). Then, on **Smart home**: expose
  the entities Cineva may use and choose who may use each (cameras start
  admins-only), put devices in rooms, and build modes. Cineva also fires
  `cineva_playback_*` events into Home Assistant, for automations such as
  raising the lights on pause.
- **Notifications** — household destinations are added under
  **Notifications**; each person adds their own in Settings.
- **Plex and Jellyfin** — **Users**, then "Import from Plex or Jellyfin":
  match the old server's users to Cineva's (or create them), run a dry run
  to see what does not match, then import. Running it twice changes
  nothing, and newer Cineva state is never overwritten.

## Building from source

You need Go 1.27, Node.js 22, git and a C compiler. Building the FFmpeg
library additionally needs `yasm nasm meson gperf python3` and a Rust
toolchain with `cargo-c` (`cargo install cargo-c`).

```sh
git clone --recurse-submodules git@github.com:garybowers/cineva.git
cd cineva
make setup   # builds the static FFmpeg library (slow, once) and installs the UI dependencies
make ui      # builds the web client, admin UI and API reference (make build does this too)
make run     # runs the server from server/
make build   # writes bin/cineva
```

`make test` and `make lint` run the Go tests and checks. To compile a TMDB key
into your builds, put `TMDB_KEY = <key>` in `local.mk` at the top of the
repository — it is ignored by git.

The FFmpeg library is built from source with
[ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo)'s own
build, plus a small patch adding the Linux VA-API hardware decoders, rather
than downloaded; [CLAUDE.md](CLAUDE.md) explains why, and describes the
code's layout and conventions. The patch is kept as one change so it can be
offered upstream.

### Releases

Every push and pull request is built and tested by GitHub Actions
([`.github/workflows/ci.yml`](.github/workflows/ci.yml)). Pushing a tag that
starts with `v` also publishes a release:

```sh
git tag v0.2.0
git push origin v0.2.0
```

The release carries `cineva-linux-amd64`, `cineva-linux-arm64` and
`SHA256SUMS`, with the version stamped into the binary, and the same binaries
go to Docker Hub as a two-architecture image tagged `0.2.0`, `0.2`, `0` and
`latest`. A tag with a hyphen (`v0.2.0-rc.1`) is published as a pre-release,
and its image gets only its own tag, never `latest`.

To build the image locally, put a binary in `dist/` and build:

```sh
make build OUT=dist/cineva-linux-amd64
docker build -t cineva .
```

A binary built on a recent rolling distribution may need a newer glibc than
the image's Debian 13 provides; release binaries are built on Ubuntu 24.04
for that reason.

The workflow uses repository settings: a `TMDB_KEY` secret for the built-in
key; `DOCKERHUB_USERNAME` and `DOCKERHUB_TOKEN` secrets and a
`DOCKERHUB_REPOSITORY` variable (for example `garybowers/cineva`) to publish
the image; and an optional `ARM64_RUNNER` variable to build arm64 somewhere
other than GitHub's arm64 runners.

## Contributing

Bug reports, ideas and pull requests are welcome — see
[CONTRIBUTING.md](CONTRIBUTING.md) for how to set up, what a change needs, and
how it is reviewed.

## Thanks

Cineva stands on [FFmpeg](https://ffmpeg.org), and it reaches FFmpeg
through **[ffmpeg-statigo](https://github.com/linuxmatters/ffmpeg-statigo)**,
from [Martin Wimpress](https://github.com/flexiondotorg) and the
[Linux Matters](https://linuxmatters.sh) podcast. It is not a wrapper around
the `ffmpeg` command but Go bindings to the real FFmpeg libraries, built
statically into the program, with hardware acceleration and generated
bindings covering most of FFmpeg's API. It is why Cineva can be a single
binary with nothing to install beside it: no system FFmpeg to hunt down, no
version mismatches, the same codecs everywhere. Every probe, transcode,
subtitle extraction and seek-bar preview here goes through it. Thank you,
Martin, and thank you to Chandler Newman, whose
[ffmpeg-go](https://github.com/csnewman/ffmpeg-go) it grew from. If you
build anything in Go that touches audio or video, start there — and give
[Linux Matters](https://linuxmatters.sh) a listen.

Metadata comes from [TMDB](https://www.themoviedb.org),
[MusicBrainz](https://musicbrainz.org) and the
[Cover Art Archive](https://coverartarchive.org).

## Licence

Copyright (C) 2026 Gary Bowers.

Cineva is free software, licensed under the GNU Affero General Public Licence,
version 3 or (at your option) any later version — see [LICENSE](LICENSE). You
may use, study, change and share it. If you distribute it, or run a modified
version as a service that other people use over a network, you must offer
them its source code under the same licence.

The binaries also contain FFmpeg, built with GPL-licensed components (x264,
x265) under the GPL version 3, which is compatible with Cineva's licence.
The FFmpeg bindings, ffmpeg-statigo, are MIT-licensed.

Cineva uses the TMDB API but is not endorsed or certified by TMDB.
