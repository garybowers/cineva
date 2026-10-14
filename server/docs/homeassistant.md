# Home Assistant design

Status: agreed design, being built (October 2026). Cineva connects to the
household's Home Assistant so people can work the room they are watching
in (lights, amplifier, blinds), look at the cameras, and start a **mode**
such as "Movie mode" that sets the room up and plays a film.

## Principles

1. **Home Assistant is the automation engine; Cineva does not become one.**
   Lights, audio and anything conditional stay in Home Assistant, where
   they already work. A Cineva mode is a short list of steps that runs
   Home Assistant's scenes and scripts (or a single action) and then does
   the one thing Home Assistant cannot: play something in Cineva.
2. **Cineva tells Home Assistant what is playing**, as events on its event
   bus, so "lights up a little on pause" is an ordinary Home Assistant
   automation, as it is for Plex and Jellyfin.
3. **Nothing is visible until an admin exposes it.** Cineva sees only the
   entities an admin picks, never the whole house. Exposed entities follow
   the library access model: open to everyone, or restricted to chosen
   users and groups. **Cameras are restricted to no one (admins only) when
   first exposed**; an admin grants them deliberately.
4. **Browsers only talk to Cineva.** Control calls, camera snapshots and
   live camera video go through Cineva, which holds the only Home Assistant
   credential. This is the same rule as federation.
5. **Home Assistant is local to a server.** In a nebula, another member's
   users see this server's rooms only if they are signed in here; nothing
   about Home Assistant is synced or proxied between members.

## Terms

| Term | Meaning |
|---|---|
| **Connection** | The one Home Assistant instance a server talks to: URL, long-lived access token, name. |
| **Entity** | A Home Assistant entity (`light.lounge`). Cineva works with the domains listed under Controls. |
| **Exposed** | An entity an admin has made visible in Cineva, with its access list. |
| **Area** | A Home Assistant area ("Living room"). Cineva reads areas, it never edits them. |
| **Room** | A Cineva device linked to an area: Living-room TV is in the Living room. The player shows that room's controls. |
| **Mode** | A named list of steps an admin builds and users run: "Movie mode". |

## Connection

One connection per server, made from the admin Integrations page with the
instance's URL, a long-lived access token and a name. Saving checks the
token by opening the websocket and authenticating; nothing is kept unless
that succeeds.

The token is stored in the clear because Cineva presents it on every
call, like a Sonarr API key, and it is never returned by the API. The
admin page recommends a **dedicated Home Assistant user** for Cineva.
Firing events into Home Assistant requires that user to be an
administrator there (`fire_event` is admin-only in Home Assistant), so
playback events are a switch on the connection: with a non-admin user the
switch is off and the card says why. Everything else works with a
non-admin user.

Cineva holds one websocket to Home Assistant (`/api/websocket`) in a
managed goroutine, reconnecting with backoff (1 s doubling to 60 s). On
each connect it authenticates, loads `get_states`, the area, device and
entity registries (`config/area_registry/list`,
`config/device_registry/list`, `config/entity_registry/list`; an entity
with no area of its own takes its device's), asks `auth/current_user` for
`is_admin`, and subscribes to `state_changed`. The state of every entity
in a supported domain is kept in memory; nothing about Home Assistant's
state is written to the database. The registries are reloaded when Home
Assistant reports `area_registry_updated`, `entity_registry_updated` or
`device_registry_updated`.

While the websocket is down, the Rooms page and the player's room panel
say Home Assistant is unreachable and controls are disabled; modes refuse
to run with that reason. Disconnecting removes the connection only:
exposure, access and modes are kept, so reconnecting the same instance
restores everything. Entity ids that no longer exist are hidden.

## Controls

Supported domains and what users can do with them. The server holds this
list and refuses any other service or field; it is the only way user
input reaches Home Assistant.

| Domain | Services | Fields |
|---|---|---|
| `light` | `turn_on`, `turn_off`, `toggle` | `brightness_pct` (0 to 100) on `turn_on` |
| `switch`, `fan`, `input_boolean` | `turn_on`, `turn_off`, `toggle` | none |
| `cover` | `open_cover`, `close_cover`, `stop_cover` | none |
| `media_player` | `turn_on`, `turn_off`, `volume_set`, `volume_mute`, `select_source`, `media_play_pause` | `volume_level` (0 to 1), `is_volume_muted` (bool), `source` (one of the entity's `source_list`) |
| `scene` | `turn_on` | none |
| `script` | `turn_on` | none |
| `camera` | none (viewing only) | |

State reaches clients normalised, not as raw Home Assistant attributes:
on/off and availability, brightness as a percentage and whether it can be
dimmed, volume, mute, source and the source list, a cover's position, and
whether a camera can stream.

## Cameras

A camera is shown as a snapshot (`/api/camera_proxy/<entity>`, proxied),
refreshed by the client every 10 seconds while visible. Live view uses
Home Assistant's own HLS stream: Cineva asks for it with `camera/stream`,
which returns a tokenised `/api/hls/<token>/master_playlist.m3u8`, and
proxies the playlist, its media playlist, `init.mp4` and the
`segment/` and `part/` files under
`/api/v1/homeassistant/cameras/{entityId}/hls/`. Home Assistant's playlists
use relative paths, so they work behind the proxy unchanged. The stream
URL is cached per camera and asked for again when Home Assistant answers
404 (it drops idle streams). A camera without stream support (the
`supported_features` stream bit) offers snapshots only.

Cineva does not record, store or transcode camera video.

## Rooms

A device (a signed-in session, see Devices in CLAUDE.md) can be linked to
one Home Assistant area. Its own user sets it from the client's settings
("This device is in: Living room"), and an admin can set it for any
device from the admin Smart home page. The player on a device with a room
shows a Room button that opens that room's exposed, visible controls and
the modes that play there. The Rooms page in the client lists every area
with something the user can see, plus the cameras and modes.

## Modes

A mode is built by an admin and run by users, from the Rooms page, the
player's room panel (every mode the user may run, this room's first), or
a title's page ("Play in Movie mode"). Steps run in order:

| Step | Does |
|---|---|
| `action` | One allowed service on one entity, with its fields: `scene.turn_on scene.movie_night`, `media_player.select_source` with `source: HDMI 1`. The entity need not be exposed: building the mode is the admin's decision, and running it does not hand anyone that entity's controls. |
| `wait` | Pause 1 to 30 seconds, for an amplifier that has to wake before it takes an input. |
| `play` | Play on a chosen device: either a fixed title, or **the title the mode was started from**. |

A mode whose play step takes the started-from title needs one: run from a
title's page it has it; run from the Rooms page the client asks which
title (search). The play step sends the title to the device as "Play on"
does today; the device plays as whoever is signed in on it. The title must
be visible both to the person running the mode and to the device's user.
The device must have Cineva open, as for Play on; otherwise the step
fails with that reason.

Run from the player, a mode works around the film on screen. If the play
step's device is already playing that title, it is left playing rather
than started again: the mode sets the room, and a film paused there plays
once the room is ready. If the mode sends the title to another device (a
phone starting Movie mode for the living-room TV), the position is saved
first, the TV resumes from it, and the player that started it closes, as
it does for Play on.

Modes follow the access model (open, or restricted to users and groups).
A run is asynchronous: the request answers 202 with a run id, and the
runner's devices get `mode-run` events as steps complete or one fails. A
failing step stops the run; earlier steps are not undone.

Total waiting in a mode is capped at 60 seconds and a mode has at most 20
steps.

## Playback events into Home Assistant

When the switch is on, Cineva fires these on Home Assistant's event bus:

| Event | When |
|---|---|
| `cineva_playback_started` | A play starts |
| `cineva_playback_paused` | A playing item is paused |
| `cineva_playback_resumed` | A paused item plays again |
| `cineva_playback_stopped` | A play ends, however it ends |

Event data: `user` (username), `device` (the device's name), `device_id`,
`area_id` (the device's room, or empty), `item_id`, `title`, `subtitle`
(series and episode, or artist and album), `kind` (`movie`, `episode`,
`track`, ...), `position_secs`, `duration_secs`. An automation triggered on
`cineva_playback_paused` with `area_id: living_room` raises the living
room lights; the admin page shows a ready-made example.

## API

All under `/api/v1/homeassistant`. Admin:

| Method and path | Does |
|---|---|
| `GET /homeassistant` | Connection and its live status (configured, connected, version, is_admin, last error, counts). |
| `PUT /homeassistant` | Create or replace the connection; blank token keeps the current one. Checked live before saving. |
| `DELETE /homeassistant` | Disconnect. |
| `GET /homeassistant/entities` | Every entity in a supported domain, with area, state, and exposure and access. |
| `PUT /homeassistant/entities/{entityId}` | Expose or hide one, and set its access. A camera exposed without an access list is restricted to admins. |
| `GET /homeassistant/devices` | Every user's devices with their rooms. |
| `PUT /homeassistant/devices/{id}/area` | Set or clear a device's room. |
| `POST /homeassistant/modes`, `PATCH`/`DELETE /homeassistant/modes/{id}` | Build modes. |

Signed in:

| Method and path | Does |
|---|---|
| `GET /homeassistant/rooms` | What the caller may see: status, areas with their entities' states, cameras, and this device's room. |
| `POST /homeassistant/entities/{entityId}/action` | Run an allowed service on a visible entity. |
| `GET /homeassistant/cameras/{entityId}/snapshot` | JPEG snapshot of a visible camera. |
| `GET /homeassistant/cameras/{entityId}/hls/{file}` and `.../hls/{dir}/{file}` | Live view, proxied. |
| `GET /homeassistant/modes` | Modes the caller may run (an admin gets all, with access lists and steps). |
| `POST /homeassistant/modes/{id}/run` | Run one, optionally with the started-from title. |
| `PATCH /auth/sessions/current` | Gains `areaId`, to set this device's room. |

Events on `/api/v1/events`: `homeassistant` carries changed entity states
(each only to users who can see that entity) or, with no data, says to
refetch (connection, exposure, access, rooms or modes changed);
`mode-run` reports a run's progress to the user who started it.

## Building it

Server code is `internal/homeassistant` (websocket client, state cache,
controls whitelist, camera proxy, mode runner, playback events) with
storage in `internal/store/homeassistant.go` and migration
`0036_home_assistant.sql`. The admin UI gains a Home Assistant type on
the Integrations page and a Smart home page (Entities, Rooms, Modes). The
client gains the Rooms page, the player's room panel, "Play in ..." on
title pages, and the device's room in Settings.

## Open questions

- Climate (thermostats) and `select`/`number` entities are left out until
  someone asks; they add controls without changing the design.
- Two-way: a Home Assistant integration that shows Cineva devices as media
  players (and plays to them) is a separate Home Assistant custom
  component, not server work; it would live in `clients/` if built.
