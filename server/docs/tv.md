# TV mode

Status: built (October 2026). The web client on a television, worked with
a remote: a Fire TV Stick's browser now, and the native TV apps later.

## One client, two presentations

TV mode is not a second app. It is the same client with `<html
class="tv">`: every screen and feature is the one desktop has, sized for
across the room and worked with a D-pad. Desktop keeps its mouse, keyboard
and touch layout unchanged.

## Choosing it

A device preference, `interface` (Settings, Appearance): **Automatic**,
**Desktop** or **TV**. Automatic picks TV on a television's own browser
(Fire TV, Android TV and Google TV, LG webOS, Samsung Tizen, Sony Bravia,
HbbTV) and Desktop everywhere else. Being a device preference it stays
with the screen whoever signs in on it, and it is read from the device's
local copy before sign-in, so the profile picker is already in TV mode.
Changing it switches at once.

## What TV mode changes

- **Size.** The client is sized in rem, so TV mode sets the root size
  from the screen's width: 16px at 1280 wide, 24px at 1080p. Icons, drawn
  at fixed pixel sizes, are zoomed by the same factor (`--tv-scale`).
- **Spatial navigation** (`lib/tv.svelte.ts`). The arrows move focus to the
  nearest focusable thing in that direction, preferring things in line
  (the same row going sideways, the same column going up or down). A row
  (`data-nav-group`) remembers where focus left it, so down then up comes
  back to the same poster. Focus stays inside the topmost dialogue while
  one is open. In a text field the sideways arrows move the caret.
- **Back** (`Backspace`, `GoBack`, `BrowserBack` and Escape) closes the
  dialogue on top, else goes back a page.
- **Arriving on a page** puts the focus somewhere, since a remote has no
  other way to find it: back on the poster it was left from after Back,
  else on the page's main action (Play), opened at the top of the page,
  else the first poster in view. Entering the rail lands on the page it is
  on.
- **Focus** is the only cursor: a thick ring, and a focused poster lifts.
  Whatever has focus scrolls into view clear of the screen's edges.
- **Home** keeps its banner on screen while the rows scroll under it, and
  the banner previews whichever title has focus.
- **Pointer-only controls go**: row scroll arrows (focus scrolls rows),
  the poster-size slider, the hover-only watched tick.
- **The player** fills the screen and takes the remote: with the controls
  hidden, OK and play/pause play and pause, left and right skip 10 seconds
  (rewind and fast-forward 30), up or down bring the controls back with the
  focus on play, and Back leaves; with a control focused the arrows move
  between controls and Back hides them.
- **Signing in** with a PIN shows an on-screen number pad instead of the
  television's keyboard.

## No administration on a TV

The admin pages are for a computer or phone. In TV mode the client offers
no way into them (the Admin link, Administration in Settings, and admin
tools such as editing intro and credits times are gone), and the admin
app, opened on a TV anyway, shows a notice pointing to a computer or phone
instead. This keeps a shared screen simple; it is not a lock, since a TV is
only a browser and its TV-ness can be changed in Settings. What actually
keeps admin powers off the sofa is the account: a screen can never sign
itself in as an admin, and an admin should not sign in on a shared screen.

## Fire TV

Fire TV's Silk browser opens Cineva as it is. A home-screen app (a thin
Android wrapper around the web client, with the remote's keys passed
through) belongs with the native apps in `clients/`; TV mode is what it
will show.

## Not yet

- Search on a TV uses the television's own keyboard in the top bar's box.
- The Rooms page and the admin pages are usable but not tuned for a remote.
