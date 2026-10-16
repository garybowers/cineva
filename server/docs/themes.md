# Themes

Status: built (October 2026). How Cineva looks: the built-in themes, the
holiday themes that switch themselves on, and the themes people make.

## What a theme is

A theme is the design-token set both UIs are built against (CLAUDE.md,
Theming): `--bg`, `--panel`, `--border`, `--text`, `--muted`, `--accent`,
`--accent-2`, `--on-accent`, `--ok`, `--err` and `--chart-1..3`. It may add
a background picture:

- `--bg-image`: the picture, as `url(...)`.
- `--bg-dim`: how strongly `--bg` covers it, 0 to 1, so text and posters
  stay readable over any picture.

The web client draws the picture behind the whole app and the sign-in
screen (`.theme-backdrop` in `app.css`); the admin pages take a theme's
colours only.

A theme can also have an **effect**: `snow`, `leaves` (with the odd bat)
or `blossom`, drawn by `ThemeEffect.svelte` in the theme's own colours,
behind the page. It is never shown while anything plays, nor when the
system asks for reduced motion, and the `theme-effects` preference turns
it off.

## Built-in themes

Folders in `server/ui/themes/<id>/`: `theme.css` (the tokens on `:root`)
and `theme.json`:

```json
{ "name": "Christmas", "appearance": "dark", "occasion": "christmas",
  "background": "background.svg", "dim": 72, "effect": "snow" }
```

`background` names a file beside it, and `theme.css` points `--bg-image`
at it by absolute path (`/themes/christmas/background.svg`): a relative
`url()` inside a custom property resolves differently between browsers.
The holiday pictures are SVG, drawn for Cineva, so they are small and sharp
on any screen.

## Holiday themes

`internal/occasion` is the calendar: Halloween (1 October to 1 November),
Christmas (1 December to 1 January) and Easter (ten days before Easter
Sunday to Easter Monday, computed), read in the server's time zone.
`GET /themes/occasion` says which is on and its theme. Seasonal rows
(personalisation.md) are to use the same calendar.

While a holiday is on, a client shows its theme instead of the user's own
when:

- `holiday-themes` is on (the default; an admin can change the household
  default or lock it), and
- the user has not set this year's run aside. Choosing any theme while the
  holiday's shows does that: it stores `halloween-2026` in the hidden
  `holiday-skip` preference, so it follows them to every device, and
  Settings offers to bring the holiday back.

The window closing brings the user's own theme back with nothing to do.
The client paints what it painted last at startup (`cineva-painted-theme`
in local storage), so a page load in December does not flash the everyday
theme first, and looks at the calendar again every few hours, so a screen
left on starts Christmas on 1 December.

## Custom themes

Made in Settings, Appearance, "Make a theme" (not offered in TV mode):

- every colour of the token set, as `#rrggbb`, starting from any theme;
  appearance (dark or light) follows from the background colour;
- contrast checked as you go (text 4.5:1, links 3:1): a theme may be hard
  to read, but never by accident;
- a background picture (JPEG, PNG, GIF or WebP up to 15 MB, turned upright
  and scaled to fit 2560 pixels by `avatar.Backdrop`), how much the
  background colour covers it, and an effect;
- the draft shown on the whole app behind the dialogue as it is edited,
  and taken away on Cancel.

Stored in `custom_themes` (migration 0044) with ids `c-<uuid>`. The server
writes each as a stylesheet the same shape as a theme folder's, at
`/api/v1/themes/{id}/theme.css`, so both kinds load the same way; it emits
only `#rrggbb` colours and `/artwork/` picture paths, so a theme cannot
carry anything else into the page.

A custom theme is its maker's. An admin can share one with everyone
(`household`), which also makes it a choice for the household default on
the Defaults page, and can change or delete any. Deleting one sends anyone
using it back to the default theme. A `themes` event tells open screens
when one changes.

## Not yet

- The admin pages use a theme's colours but not its picture or effect.
- More occasions (New Year, Valentine's Day, Bonfire Night, and those on
  other calendars) come with seasonal rows, as the calendar grows.
- Themes cannot be exported or imported as files.
