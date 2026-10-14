# Devices design

Status: built (October 2026), migrations 0039 and 0040. Replaces "a device
is a session" (migration 0029).

## The problem it solved

A device used to be a browser session. Its name ("Living-room TV"), its
Home Assistant room and its own preferences all hung off the session row,
so every way a session ends made the screen a stranger: signing out,
switching profile on "Who's watching?", a password change, or expiry. The
living-room TV lost its name and room, its quality setting went back to
the default, and every mode that played on it failed until an admin
picked the device again.

## The model

**A device is a screen**: one browser profile, or one native app
instance (milestone 11). It outlives the sessions signed in on it, and it
belongs to no one: the living-room TV is the same device whoever chooses
themselves on "Who's watching?".

| Belongs to the device | Stays with the session (the person on it) |
|---|---|
| Name ("Living-room TV") | Who is signed in, and until when |
| Home Assistant room | The play queue |
| Device-scoped preferences (video and music quality) | |
| Who it signs in as by itself, if anyone | |

The queue stays with the person: a child signing in on the TV must not
find their parent's queue there, and "Continue from..." moves *your*
queue between *your* screens.

A device has **one session at a time**. Signing in on it, including
choosing another profile, ends the session that was there. That session
**takes the device's id**, so everything that already keyed on a
session's public id (presence, Play on, the handoff event, playbacks,
Home Assistant rooms and modes, device preferences) means the screen
without changing. `events.Event.Session` carries that id.

Sessions slide: `auth.session_days` counts from the last use, so a screen
in use stays signed in.

## Identity

A device is recognised by a `cineva_device` cookie: a random secret,
HttpOnly, SameSite=Lax, Secure as the session cookie is, kept for 400 days
(the longest browsers allow) and renewed on use. Only its hash is stored.
The public id is separate, so it never reveals the secret.

Signing in puts the session on the device the cookie names. Without a
cookie, a browser still signed in keeps the device its session is on, and
a browser that is neither gets a new device. A browser signed in before
devices existed is given its cookie on its next request.

On an ordinary device the cookie is not a credential: it signs no one in,
it only says which screen a sign-in happens on. On a device that signs in
by itself (below) it does sign someone in, so it is as sensitive as a
remembered sign-in.

## Signing in by itself

An admin can set a device to sign in as a chosen user whenever nobody is
signed in on it (`PATCH /devices/{id}`, `autoUserId`). The living-room TV
opens as "Family" without anyone typing anything; someone can still switch
to themselves on the profile picker with their PIN or password, and when
they sign out the TV goes back to Family.

- It happens only in `GET /auth/status`, the check the client makes as it
  opens, so a device never changes person in the middle of anything. A
  session that lapses while the client is open sends it back through
  that check.
- On such a device the client's menu says "Switch user" rather than
  "Sign out" (both end the session and show the picker), and the picker
  offers "Back to Family".
- An admin account cannot be chosen: whoever is in front of the device
  would have the admin pages. A user who later becomes an admin or is
  disabled is no longer signed in automatically. Deleting the user clears
  the setting.
- The admin page recommends an account made for the screen, with only the
  libraries everyone may see.

## Lifecycle

- **Sign out, profile switch, password change, expiry**: the session ends;
  the device, its name, room, preferences, automatic user and modes stay.
- **Sign a device out remotely** (Devices in Settings, or the admin's user
  page): ends its session. The device stays known.
- **Forget a device** (`DELETE /auth/sessions/{id}?forget=true` by whoever
  is signed in on it, or `DELETE /devices/{id}` by an admin): removes it
  with its name, room and preferences, and ends its session.
- **Pruning**: a device with no session that has not been used for 90 days
  (`store.DeviceUnusedDays`) is forgotten by the scheduled housekeeping.

## Continue here

What one of a user's devices plays, another can carry on. Each device in
`/auth/sessions` says what it is `playing` (title, position as of its last
heartbeat, paused), from the activity registry, and the client shows a
strip above the bottom of the screen ("Playing on Living-room TV ·
Continue here") on every other device of theirs that is playing nothing
itself. A paused device is not offered there, so a tab left paused does not
nag for ever; the Queue page's "Continue from..." reaches it.

Continuing (`POST /auth/sessions/{id}/takeover`):

1. The server sends the playing device a `takeover` event. It pauses and
   reports where it stopped.
2. The server waits for that report, up to three seconds, else goes with
   the last heartbeat (up to 15 seconds behind).
3. It answers with what to play: the device's queue when it is on it, else
   the title and, for a track, the rest of its album. A film or episode
   resumes from its saved position as ever; a track starts at
   `positionSecs`, since music keeps no resume state.

The activity registry knows one playback per user and title, so once the
new device plays it, a paused device still showing it must not speak for
it: its paused heartbeats and its stop are ignored while another device is
playing the title (`Registry.HeldElsewhere`). Pressing play on it again
takes the title back.

## Storage

- `devices (id, secret_hash, name, client, client_ip, ha_area,
  auto_user_id, created_at, last_seen_at)`.
- `sessions.id` is the device's id; `name` and `ha_area` moved from
  `sessions` to `devices`. A trigger signs a forgotten device out, and
  another stamps the device's last use when its session ends.
- `device_prefs.device_id` references `devices` (it referenced sessions).
- 0039 made every existing session a device with the same id, so modes and
  device preferences carried over untouched.

## API

- `/auth/sessions` lists the caller's devices as before; `id` is the
  device's. `DELETE /auth/sessions/{id}` signs one out; `?forget=true`
  forgets it, this one included.
- `GET /devices`, `PATCH /devices/{id}` (name, room, automatic user) and
  `DELETE /devices/{id}` are the admin's Devices page.
- `GET /auth/status` gains `autoUser`.

## Not in this

- Native apps (milestone 11) register through the same table with an
  app-held secret instead of a cookie; the registration endpoint comes
  with that milestone.
- Federation: a visiting user signed in here is on one of this server's
  devices, like anyone else. Devices are never synced between members.
