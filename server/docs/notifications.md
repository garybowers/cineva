# Notifications design

Status: agreed design, being built (October 2026). Cineva tells people
what happened, wherever they already look: an ntfy or Gotify app, a
Discord channel, a Telegram chat, or any webhook.

## Principles

1. **Two audiences.** The admin sets up **household** destinations (the
   family Discord, the admin's own ntfy) for household and admin events.
   Each user can add **personal** destinations in their Settings for
   events about them: their requests, new episodes of what they watch.
2. **What one person watches never lands in a shared channel.** Personal
   events go only to that person's own destinations.
3. **Library access holds.** A household "new in the library" message
   names titles only from the libraries the admin chose for that
   destination (by default, the ones open to everyone). A personal
   message names only titles its recipient can see.
4. **Nothing leaves the server unless someone set it up.** No destination
   exists by default and nothing is sent anywhere else.
5. **Users cannot point the server at the network.** A personal
   destination can only reach fixed services (Telegram, Discord) or the
   ntfy server the admin allows. Arbitrary URLs (generic webhooks, a
   Gotify server, a self-hosted ntfy) are for the admin's household
   destinations only, so a user cannot make Cineva probe the household
   network.

## Destinations

| Kind | Settings | Household | Personal |
|---|---|---|---|
| `ntfy` | server, topic, optional access token | any server | the admin's chosen server only |
| `gotify` | server, application token | yes | no |
| `discord` | webhook URL (`https://discord.com/api/webhooks/...`) | yes | yes |
| `telegram` | bot token, chat id | yes | yes |
| `webhook` | URL, optional shared secret (sent as `X-Cineva-Secret`) | yes | no |

Each destination has a name, an on/off switch, the events it receives,
and (household only) the libraries its "new in the library" messages may
name. Secrets (tokens, Discord webhook URLs, the webhook secret) are
stored as given, never returned whole (the last four characters only),
and kept when an update leaves them blank, as API keys are elsewhere.

Every destination has a Test button, and shows when it last delivered and
the last error, if any.

## Events

Household and admin, chosen per household destination:

| Event | When |
|---|---|
| `request.waiting` | A request needs an admin's approval |
| `request.failed` | A request could not be sent to Sonarr or Radarr |
| `library.new` | A scan added titles: one message per scan and library ("3 films, 12 episodes: ...") |
| `storage.down` / `storage.up` | A library's storage became unreachable, or came back |
| `scan.failed` | A library scan failed |
| `homeassistant.down` / `homeassistant.up` | The Home Assistant connection was lost, or came back |

Personal, chosen per personal destination:

| Event | When |
|---|---|
| `request.approved` | Your request was approved and sent |
| `request.declined` | Your request was declined (with the admin's note) |
| `request.available` | What you asked for is in the library |
| `episode.new` | A new episode of a series you are watching: one message per series per scan |

"A series you are watching" is one with an episode watched or part-watched
by you in the last 180 days, in a library you can see.

## Messages

A message has a title, a body, an optional link and a priority (high for
storage down, Home Assistant down and a failed scan). Links point into
Cineva at the admin's **public address** setting (`notify.public_url`,
set in the admin's Notifications page, suggested from the address the
admin is using); with none set, messages carry no links. Each kind gets
the message in its own form: ntfy's title, priority and click headers,
Gotify's priority, a Discord embed, Telegram text with the link, and for a
webhook the JSON `{event, title, body, url, priority, sentAt}`.

## Delivery

Messages go onto an in-process queue and a worker sends them, so nothing
that triggers one waits for it. A failed send (a network error, or a 5xx
or 429 answer) is retried twice, after 10 seconds and then a minute; a 4xx
is not retried, since the settings are wrong. The destination records the
outcome. Messages are not persisted: one queued when the server stops is
lost, which for notifications is acceptable.

Batching keeps chatter down: new titles are collected through a scan and
sent at its end, one message per library (household) and one per series
(personal).

## API

Admin:

- `GET /notifications`: household destinations, the event and kind
  catalogue, and the settings (`publicUrl`, `ntfyServer` for personal
  destinations).
- `POST /notifications/destinations`, `PATCH`/`DELETE
  /notifications/destinations/{id}`, `POST .../{id}/test`.
- `PUT /notifications/settings`: `publicUrl`, `ntfyServer`.

Signed in:

- `GET /auth/me/notifications`: the caller's destinations, and the kinds
  and events they may use (personal ntfy only appears when the admin has
  set a server).
- `POST /auth/me/notifications`, `PATCH`/`DELETE
  /auth/me/notifications/{id}`, `POST .../{id}/test`.

## Building it

`internal/notify` holds the kinds (one sender per kind), the queue and
worker, the event catalogue and message wording; storage is
`internal/store/notify.go` and migration `0042_notifications.sql`. Hooks:
the requests service (each status change), the storage monitor's change
callback, the scan's end (failure, and the new titles it collected), and
the Home Assistant service's connection changes. The admin gets a
Notifications page; the client gets a Notifications section in Settings.

## Not in this

- Browser push notifications for the installed web app (they need a push
  service and per-device subscriptions): later, with the native apps.
- Email: no SMTP setup in Cineva for now.
