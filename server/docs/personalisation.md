# Personalisation design

Status: proposed design, October 2026. Stage 1 (the preference registry,
layers, admin defaults and locks) is built, and the Preferences section
describes it as built; the rest is not yet, beyond what is marked as
existing. Deferred and staged work is listed in BACKLOG.md under
"Personalisation".

## The problem

Plex looks good but decides almost everything for you: the home screen,
which rows exist and what counts as a recommendation. Jellyfin lets you
change more, but its options are sparse, scattered across settings pages,
and mostly stored per browser. Neither lets someone sit down and say "this
is how I want my Cineva to look and behave" and have every screen they own
agree.

Cineva should put the person watching in charge of their own experience.
They choose what the home screen holds and in what order, what counts
towards recommendations, how playback behaves, show by show if they like,
and how much the interface tells them. The admin sets sensible defaults for
the household. The admin locks a setting only where the decision really is
theirs, such as what a child may watch.

## Principles

1. **The user decides; the admin suggests.** Every setting in this document
   is the user's to change unless an admin has locked it. Admin defaults are
   starting points, not rules. A lock is the exception, it is visible to the
   user ("Set by your administrator"), and it never hides that a setting
   exists.
2. **Customise in place.** Every row, card, show and player control that has
   a setting offers it where you find it: a row's menu, a series' menu, the
   player's menu. The Settings page lists everything, but nobody should need
   it to change what they are looking at.
3. **The server composes; clients render.** Home rows, recommendations,
   next-up and resolved preferences are built on the server, as `/home` and
   `/next-up` already are. The web client, the TV app and the phone apps
   draw what they are given. A layout set on the laptop is the layout on the
   TV, and a new client cannot drift from the old ones.
4. **Person or device, deliberately.** Most preferences belong to the person
   and follow them everywhere. A few belong to the device in front of them:
   volume (already, see `ui/client/src/lib/prefs.ts`), quality on mobile
   data, and the layout of a 10-foot screen. Every preference states which
   it is.
5. **Every setting has a good default.** A user who never opens Settings
   gets a home screen that is already right. Customisation is for
   preferences, not for repairing the defaults.
6. **Always reversible.** Every customised surface has "Reset to default",
   and resetting one thing never resets another.

## Preferences

### Before the registry

`user_prefs` (migration 0018) stored opaque string key/value pairs per user,
read and written through `GET`/`PUT /auth/prefs`. The client owned the
meaning of each key; the server read three of them when playback started
(`audio-lang`, `sub-mode`, `sub-lang`). The local mirror in `prefs.ts` gave
a synchronous value at first paint.

That worked for one client and one person. Three things it could not do:

- An admin cannot set a default for the household or for a group, because
  the server does not know what keys exist, their types or their defaults.
- An admin cannot lock a setting for a group.
- A native client cannot render a Settings screen for keys it does not know
  about, so every client would hardcode the same list and drift apart.

### A registry of preferences

The server keeps a registry, in Go (`internal/prefs`), of every preference
it knows about. Adding one is one entry, with no migration:

| Field | Meaning |
|---|---|
| `key` | `autoplay`, `sub-mode`, `skip-intro`, ... (the existing keys are kept) |
| `type` | `bool`, `enum` (with its values), `int` (with a range), `string`, `list` |
| `default` | the built-in value used when no layer sets one |
| `scope` | `person` or `device` |
| `itemScoped` | whether it can also be set per series, film or library (below); stage 2, not in the registry yet |
| `lockable` | whether an admin may lock it; false for things like theme, where a lock would only be petty |
| `section` | which Settings section it appears under, so every client lays out the same page |
| `label`, `description` | the Settings wording, so every client words it the same |
| `options` | an enum's choices, each with a label and, where it helps, a description of what it does |
| `hideWhen` | hidden while another preference has a value: subtitle language while subtitles are off |

Values are strings at every layer. A `bool` is `on` or `off`, because
`autoplay` was stored that way before the registry existed; a `list` is a
JSON array of strings. Every key the web client used before the registry
kept its name and values.

What is registered (`internal/prefs/registry.go`):

| Key | Type | Default | Scope |
|---|---|---|---|
| `quality` | enum: auto, original, 1080p, 720p, 480p | auto | device |
| `audio-lang` | enum: "" (the file's default) or a language | "" | person |
| `sub-mode` | enum: off, forced, foreign, always | off | person |
| `sub-lang` | enum: "" (same as the audio) or a language | "" | person |
| `autoplay` | bool | on | person |
| `skip-intro` | enum: auto, button, off | button | person |
| `skip-credits` | enum: next, card, end | card | person |
| `music-quality` | enum: original, opus-128, opus-64 | original | device |
| `gapless` | bool | on | device |
| `music-start` | enum: bar, now-playing | bar | device |
| `music-bar` | enum: top, bottom | top | person |
| `music-autoplay` | bool | on | person |
| `theme` | string, not lockable | midnight | person |
| `sidebar-size` | enum: compact, regular, large | regular | device |

`skip-credits`: `next` starts the next episode as soon as detected credits
begin (only with autoplay on; with it off it behaves as `card`), `card` is
the up-next card at the credits with a 15-second countdown, and `end` lets
the credits play out, treating the episode as one with none detected (the
card in the last 40 seconds, autoplay at the end of the file).
`skip-intro`: `auto` skips a detected intro once as it starts, and seeking
back into it shows the button instead of skipping again.

Volume and the play queue stay out of the registry: volume is the
browser's own (localStorage), and the queue is kept on the session device
by its own endpoints.

Keys the registry does not know are still stored as they are today. A
client keeps a client-only preference without asking the server first.

### Layers

A preference resolves through these layers, most specific first:

```
item override (this series / this film / this library)
  device (only for scope = device)
    user
      group default (the user's groups, in the admin's order)
        server default
          built-in default
```

A lock set at the server or group layer stops the user and item layers
below it from applying. Where a user belongs to several groups that set
the same key, the admin orders the groups, and the first group in that
order wins. "Kids" can then sit above "Grown-ups" for a child who is in
both.

Locks are looked at first: the first admin layer, in that order (groups,
then the server), that has locked the key decides it outright. A group's
lock therefore beats the server's, and a server lock beats an unlocked
group default. Without a lock the first layer with a value decides. A
stored value that does not suit its key (a choice since withdrawn) is
skipped as if it were not set, so it can never reach playback. A device
value applies only to a device-scoped key, and for those the user's own
value is still the fallback, so values stored before device scope existed
keep working on every device until one sets its own.

### Item overrides

These are the "a lot more user control" part, and something neither Plex
nor Jellyfin offers properly. A person can override a preference for one
series, film or library:

- Watch this anime in Japanese with English subtitles, and everything else
  in English.
- Always skip the intro of this show automatically, and show the button
  everywhere else.
- Never autoplay the next episode of this documentary series.
- Play this library's music at original quality, and stream the podcasts
  library at 96 kbps.

Overrides live on the person, not the device. They are set from the series,
film or library menu ("Playback for this show...") and from the player's
menus ("Always for this show"). Settings has a list of every override, with
"Clear" next to each.

### Storage

- `user_prefs` stays as it is: the user layer.
- `pref_defaults (owner_kind, owner_id, key, value, locked)`, where
  `owner_kind` is `server` or `group`: the admin's layers (migration 0037).
  A group's rows go with the group.
- `device_prefs (device_id, key, value)`: device-scoped values. A device is
  already a session (migration 0029), so signing a device out forgets its
  values; a password change carries the caller's own device's values to the
  session that replaces it. Native app instances will be devices too
  (milestone 11).
- `user_groups.priority`: the admin's order of groups, lowest first.
- `user_item_prefs (user_id, item_id, key, value)`: item overrides. Keyed
  by item id for now; under federation these move to content keys, like
  watch state (see `federation.md`, "Preparing now").

### API

The account's own routes live under `/auth` like the rest of them, so the
design's `/me/prefs` is `/auth/me/prefs`, and a device's are under
`/auth/sessions`, where devices already are.

- `GET /auth/me/prefs` returns every resolved preference with its value,
  the layer it came from, whether it is locked, and its `default` (what
  applies without the viewer's own choices, which is what Reset goes back
  to), plus the registry entries and sections, so a client can draw
  Settings without hardcoding them. It resolves for the caller's own
  session device; `?device=` resolves for another of their devices.
  `?item=` arrives with item overrides.
- `PUT /auth/me/prefs` sets the user's own values: a string sets a key (an
  empty string is a value: "the file's own audio"), `null` clears it.
  `GET`/`PUT /auth/sessions/{id}/prefs` (`current` for the caller's own)
  read and set a device's values, and take only device-scoped keys.
  Setting a locked key is a 409 naming the lock; clearing one is allowed.
- `GET`/`PUT`/`DELETE /me/items/{id}/prefs` manage item overrides (stage 2).
- `GET /prefs/defaults` is the admin's whole table: the registry, the
  server's defaults and every group's in priority order. `PUT
  /prefs/defaults` and `GET`/`PUT /groups/{id}/prefs` set the server's and
  a group's, as `{key: {value, locked}}` with `null` to clear, and `PUT
  /groups/order` orders the groups. Each change is in the audit log.
- A `prefs` event on `/events` tells clients to fetch their preferences
  again: to the user's devices when they change their own, to everyone when
  an admin changes a default.
- `/auth/prefs` keeps working, as the raw user layer: it reads and writes
  values as stored, without locks or validation (a lock still wins, and an
  invalid value is skipped, when the value is resolved). Playback reads
  resolved values, overrides included once they exist.

## The home screen

### What exists

`GET /home` (`internal/api/home.go`) returns a fixed structure: Continue
Watching, Next Up, New Episodes, each library's Recently Added, music
shelves, then the first two recommendation rows of each film and TV
library. The order and contents are hardcoded.

### Rows from a layout

The home screen becomes a **layout**: an ordered list of rows, each a row
type plus its settings. The server builds the rows from the layout. The
client draws each row by its presentation and never by its type, so a new
row type needs no client change.

A row in a layout:

```yaml
id: r7                 # stable within the layout, so menus and edits can refer to it
type: genre            # from the catalogue below
title: "Friday night"  # optional; the type supplies one otherwise
scope:                 # which libraries it draws from
  libraries: [lib-movies, lib-kids-movies]   # or "all of type movie", or one library
params: { genre: Thriller, unwatched: true }
style: poster          # poster | landscape | hero | compact | top10
size: 20               # items before "See all"
order: random-daily    # where the type allows a choice
pinned: false          # admin rows only: users cannot hide a locked pinned row
```

### Row catalogue

| Type | What it holds | Notes |
|---|---|---|
| `continue` | Part-watched films and episodes, one card per series | exists |
| `next-up` | The next unstarted episode of each series in progress | exists |
| `on-deck` | `continue` and `next-up` merged into one row, Plex style | a user choice between this and the two above |
| `new-episodes` | Episodes aired lately | exists |
| `recent` | Recently added | exists per library; gains a merged "all video" form |
| `because` | Like the last thing you finished | exists |
| `top-rated` | Highly rated and unseen | exists |
| `genre` | One genre, any filters | exists as an automatic row; becomes choosable |
| `collection` | One collection, in release order | |
| `person` | Films and series with this actor or director | from the person page: "Add to home" |
| `saved-view` | A saved library filter (below) | the general case |
| `playlist` | A playlist | music and video |
| `watchlist` | Things you marked to watch later | needs a watchlist: one more per-user flag |
| `random` | Something unwatched, re-picked each day | "I don't know what I want" |
| `seasonal` | Films and episodes for the holiday coming up: Halloween, Christmas, ... | appears on its own in its window; see Seasonal rows |
| `recently-watched` | What you finished lately | for rewatches and "what was that called" |
| `requests` | Your requests to Sonarr and Radarr, with progress | from `internal/requests` |
| `music-*` | The existing music shelves | exists |
| `audiobooks-continue`, `podcasts-new`, `reading` | One per later domain | milestones 7-9; the row model is shared from day one |

### Saved views

A saved view is a library filter and sort with a name: "Unwatched 1980s
action under 100 minutes", "Films Dad hasn't seen", "4K HDR". It can be
opened from the library's sub-tabs, pinned to the sidebar, or added to home
as a row.

This needs the grid's filters to run on the server. Today the client
filters a library it has fetched whole. The filter vocabulary becomes a
query on `GET /libraries/{id}/items`: watched state, genre, decade or year
range, runtime range, rating, content rating, resolution, HDR, audio
language, added within, and person. A saved view is that query, stored per
user in `saved_views (id, user_id, name, query, sort)`. An admin can share
one with a group, and it then appears in those users' Add row sheet.

### Banner

The banner is the layout's first slot, with its own setting: what you are
watching (today's behaviour), the first item of a chosen row, a featured
collection, a random unwatched title, or off. With off, the rows start at
the top. That suits a phone, where the banner costs a whole screen.

### Surfaces

A TV wants five or six rows with large landscape cards and nothing
dense. A phone wants a compact banner, or none, and more rows. A desktop
sits in between. Each layout belongs to a **surface**: `phone`, `tablet`,
`desktop` or `tv`. The client sends its surface with every `/home` request.
A missing layout falls back: `tv` to `desktop`, `tablet` to `desktop`,
`phone` to the built-in phone default. Editing on the phone changes the
phone layout. "Use this layout everywhere" copies it to the other surfaces.

The layout belongs to the person and the surface, not the device. The
living-room TV and the bedroom TV share one layout. Which surface a device
is can be overridden per device: a laptop plugged into a projector can call
itself `tv`.

### Who owns a layout

The same layers as preferences: built-in, then server, then group, then
user. A user who has not edited their home sees their group's layout, or
the server's. Their first edit copies that layout and changes the copy.
"Reset to default" drops the copy.

An admin can add **pinned rows** to the server or a group layout, such as
"Staff picks" or "Film club, this month". A pinned row
appears in every user's layout at its position, even in layouts the users
have customised. Users can hide or move it unless the admin locked it, and
a hidden pinned row comes back if the admin changes it.

Rows can have a **date window** (`from`, `to`, yearly or once) and a
**time window** (Saturday mornings), so a temporary row needs no reminder
to remove it. Holidays have their own row type, which brings its own
window (next section).

### Seasonal rows

In October the home screen should offer Halloween films; in December,
Christmas films and the Christmas specials of the series you have. Nobody
should have to build that row each year, or remember to remove it in
January.

**The calendar.** The server has a built-in list of occasions. Each one
has a date rule, a lead-in (how long before the day the row appears), a
tail (how long after), and how its titles are found:

| Occasion | Window (default) | Found by |
|---|---|---|
| Halloween | 1 Oct to 1 Nov | TMDB keywords `halloween`, `trick or treating`; family-friendly horror for a child's ceiling |
| Bonfire Night | 1 to 6 Nov | keyword `guy fawkes` (sparse; off by default) |
| Thanksgiving | 2 weeks before the fourth Thursday of November | keyword `thanksgiving` (off by default outside the US) |
| Christmas | 1 Dec to 1 Jan | keywords `christmas`, `santa claus`, `christmas eve`; episodes titled or aired as Christmas specials |
| New Year | 27 Dec to 2 Jan | keyword `new year's eve` |
| Valentine's Day | 7 to 15 Feb | keywords `valentine's day`; romance as a fallback |
| Easter | 10 days before Easter Sunday to Easter Monday (computed) | keyword `easter` |
| Summer | late July to August | keyword `summer vacation`, `summer camp` |

Occasions whose dates move by a calendar other than the Gregorian one
(Hanukkah, Diwali, Eid, Lunar New Year) need a date table. They are added
when someone wants them, with their dates carried as data for the next
decade, not computed.

**Finding the titles.** TMDB keywords are the main source: they are
assigned by people, mostly accurate, and cost no extra request: `keywords` joins
`credits` in the `append_to_response` of the detail call the scanner
already makes. The scanner stores them in `item_keywords`, and a metadata
refresh back-fills the existing library. A user's and the admin's
additions and removals are kept in
`occasion_items (owner_kind, owner_id, occasion, item_id, included)`. For television, a
series rarely carries a Christmas keyword, but its Christmas specials do
stand out: an episode whose title contains the occasion's words
("Christmas", "Xmas", "Halloween") or that aired within the occasion's
week. The row then holds those episodes, playable on their own, which is
what "the Doctor Who Christmas specials" means.

**The user's say.** Each occasion is a preference (`seasonal-<occasion>`:
on, off), plus `seasonal-rows` to turn all of them off. A user can:

- add any title to an occasion ("Add to Christmas", in the item's menu),
  and remove one ("Not a Christmas film"). Both are per user. Die Hard is
  therefore whatever you say it is.
- move the seasonal row like any other, or hide it for this year only
  ("Hide until next year").
- let the occasion take the banner while it is on (`seasonal-banner`, off
  by default).
- choose unwatched only, or include favourites they have seen (the films
  people rewatch every December are the point of the row, so the default
  includes watched titles, unlike every other recommendation row).

**The admin's say.** An admin turns occasions on or off for the server (the
default set follows `metadata.cert_region`, so a GB server does not offer
Thanksgiving), adjusts windows, and adds titles to an occasion for
everyone ("Add to Christmas for everyone"). Their additions and removals
are the defaults; a user's own win. A child's content ceiling applies as
everywhere else, so a Kids profile's Halloween row is Hocus Pocus, not
Halloween (1978).

Where the seasonal row goes: if the user's layout has a `seasonal` row, it
fills that slot in season and disappears outside it. A layout with none
gets one inserted after Continue Watching while an occasion is on, unless
the user turned seasonal rows off. Two occasions that overlap (Christmas
and New Year) are two rows, each on its own.

The library gets the same thing: in season, a library's Recommended tab
leads with the occasion's titles from that library, and the Genres tab
gains a "Christmas" entry beside the genres.

### Editing

- **Each row's menu**: Hide, Move up/down, Style, Size, Rename, Edit filter
  (for genre and saved-view rows), "See all".
- **Edit home** mode: rows become a reorderable list (drag on desktop, move
  buttons on phone and TV), each with the menu above, and "Add row" opens
  the catalogue with a preview of what each would hold right now.
- **From elsewhere**: "Add to home" on a collection, a person, a genre
  listing, a playlist or a saved view.
- **Reset to default** in Edit home and in Settings.

### API

- `GET /home/rows?surface=tv` returns the resolved layout with each row's
  items, in a new shape:
  `{ banner, rows: [{ id, type, title, style, seeMore, items }] }`. Rows
  beyond the first eight are returned without items and are filled with
  `GET /home/rows/{rowId}?surface=`. A TV with thirty rows paints at once
  and loads the rest as it scrolls.
- `GET`/`PUT`/`DELETE /me/home-layout?surface=` read, replace or reset the
  user's layout. `GET /home/row-types` returns the catalogue: each type's
  parameters and allowed styles, so a client can draw Add row without
  hardcoding it.
- `GET`/`PUT /home-layouts/server?surface=` and
  `/groups/{id}/home-layout?surface=` are the admin's layouts.
- `GET /home` stays until the web client has moved, as the API rules
  require, and is then removed in the next API version.

Every row is filtered to the viewer's library grants and to their content
rating ceiling (below), as `/home` is today.

## Recommendations

Recommendations stay rule-based and say why (see BACKLOG, milestone 5:
"one household's history has nothing to train on, and guessing badly is
worse than an honest 'here is what you have not seen'"). The user gets
direct control over what feeds them:

- **Not interested** on any card or detail page. The title never appears in
  recommendation, random or "because" rows again. Settings lists these, and
  each one can be undone.
- **Don't count this** on anything watched. It stops feeding "Because you
  watched", for when someone else used your profile, or you watched with
  the kids. Available on the item and as a filter in Recently watched.
- **Muted genres** (`rec-muted-genres`, a list): never recommended, still
  in the library. Different from a content rating ceiling, which hides.
- **What counts**: finished titles only, or also titles you abandoned
  (`rec-sources`); whether rewatches count.
- **New or forgotten** (`rec-bias`): favour recent additions, favour the
  back catalogue, or neither.
- **Rotation** (`rec-rotation`): the same picks until you act on them, or a
  fresh shuffle each day.
- **Which automatic rows exist**: a library's Recommended tab shows the
  types the user has switched on, in their order. The home screen uses its
  own layout.
- **Every recommendation says why**, on the card ("Because you watched
  Heat", "Unseen, rated 8.1", "Added this week"), and Settings has "Why am I
  seeing this?" for any title.

Stored as preferences (`rec-*`) plus `user_item_flags (user_id, item_id,
flag)`, where flag is `not-interested`, `exclude-history` or `watchlist`.
Under federation these move to content keys with watch state.

## Continue Watching and Next Up

All of these are preferences, so they can be set per user, defaulted by the
admin, and set per series:

- **Merge** Continue Watching and Next Up into one row, or keep them apart.
- **Drop stale entries** after N weeks without progress (`continue-stale-weeks`,
  default off). The item stays half-watched, it just leaves the row.
- **Remove from Continue Watching** on any card. This clears the resume
  point, or with "Keep my place", only hides the card.
- **Include rewatches** in Next Up once a series is finished.
- **Thresholds**: below N% a title counts as not started, above M% as
  finished. The admin's default today is about 90%. A user may change it,
  within a range the admin allows.
- **New seasons**: a series you finished returns to Next Up when a new
  season arrives (on by default), or does not.

## Playback

Every item below is a preference, and the ones marked *item* can also be
set per series, film or library.

- Audio language, subtitle mode and subtitle language (exist; *item*).
- Subtitle appearance: size, background box, outline, position. Colours
  come from a short list of theme-safe choices, not free colour values.
- **Intros** (`skip-intro`): skip automatically, show a button, or do
  nothing (*item*).
- **Credits** (`skip-credits`): go to the next episode at the credits, show
  the up-next card, or play to the end (*item*).
- Autoplay the next episode (exists; *item*), and the countdown length.
- Playback speed for spoken-word content, remembered per audiobook and per
  podcast (*item*; milestones 7-8).
- Quality on wifi and on mobile data (device). Volume stays device-only.
- Resume behaviour: resume silently, ask, or always start from the
  beginning (*item*).

## Display and libraries

- **Spoiler protection** (`spoilers`): blur stills and hide synopses of
  unwatched episodes, hide episode titles beyond the next one, and hide
  the running time remaining in a series. Off, light (stills only) or full.
  *item*.
- **Density**: comfortable or compact cards; titles under posters or not.
- **Per library**: default tab (Recommended, Library, Genres), default sort
  and filter, grid or list. Stored as item overrides on the library.
- **Sidebar**: hide libraries you never open, reorder them, pin saved views
  and collections.
- **Titles**: show original titles, show ratings, which rating source.
- **Theme** (exists).
- **Start page**: Home, a library, a saved view, or the music player. A
  child's profile can start on their own library.

## Intro and credits detection

### What exists

Detection (`internal/segments`) compares each episode's opening and closing
audio with its neighbours in the season. An admin can set times by hand
and lock them. Detection runs from a per-library queue, under the
background schedule, read-speed cap and playback priority.

Stage 3 below is built: detection at every scope with a priority queue,
film credits from chapter names, the editor for films, reports of wrong
times and trusted editors. Where it differs from the design is under "As
built" at the end of this section.

### Detection at any scope

Re-detect becomes available on every level of the tree, and on a
selection:

| Target | What is reopened | Compared with |
|---|---|---|
| Episode | that episode only (exists) | its season, as now. The others are read as partners only, not analysed again, so this is cheap. |
| Season | every unlocked episode (exists) | each other |
| Series | every unlocked episode in every season (exists) | each season within itself, lone episodes across seasons (exists) |
| Library | every unlocked episode and film in the library (exists) | as above |
| Selection | the episodes in a Manage-list selection (exists) | their seasons |
| Film | that film's credits (below) (exists) | itself |

- `POST /items/{id}/segments/redetect` accepts any episode, season, series
  or film, and reopens exactly the target, no longer the whole season for
  an episode. `POST /libraries/{id}/segments/redetect` covers a library,
  and `POST /segments/redetect` with `{ items: [...] }` covers a selection.
  `?kinds=intro,credits` limits it to one kind.
- **Targeted work goes first.** A request names items, not a library, so
  the detector keeps a priority queue of seasons ahead of its library
  queue. Pressing "Detect" on one series analyses that series now, not
  after the 3,000 episodes the library still has due. It still gives way
  to playback, scans and a manual pause, as all background work does. The
  schedule does not apply to targeted work, as "Analyse now" already
  behaves.
- **Hand-set times stay locked** unless the request says
  `includeLocked: true`, which the UI asks about in words ("Also replace 3
  episodes you set by hand?").
- Progress for a targeted run appears where it was started: on the series
  page, the Manage list row, and the Activity page.

Where it appears in the UI: the episode, season and series menus ("Detect
intro & credits"), the film menu ("Detect credits"), the library's Manage
list as a bulk action on selected rows, and a library-level button in the
admin's Libraries page.

### Films

A film has no neighbour, so the audio comparison cannot work. In order of
cost:

1. **Chapter names.** Most ripped and many retail files name their
   chapters, and the play handler already reads them. A final chapter named
   "End Credits", "Credits", "End Titles", "Closing Credits" (and the
   common translations) gives the credits start for free. This runs during
   the scan with no extra read, and is the first thing to build.
2. **Versions of the same film.** Two cuts or encodes of one film share
   their credits audio. Where a film has several versions (version
   grouping exists), compare their closing stretches as episodes are
   compared.
3. **Visual detection**: dark frames carrying text (already in BACKLOG,
   under intro detection, for the shows audio cannot reach). The same
   detector serves films and those shows.

Film intros (studio logos) are not worth detecting. Hand-set times work
for films as for episodes, and the editor gains a film entry point.

### What users control

Detection reads from the shared storage, so starting it stays an admin
action. What a user controls:

- How intros and credits behave for them, everywhere or per series
  (`skip-intro`, `skip-credits` above).
- **Report wrong times** from the player's menu ("The intro skip is
  wrong"): it records the item, the position and an optional note, and the
  admin sees reports on the Activity page with the episode editor one click
  away. Three reports on one season prompts the admin to re-detect it.
- **Trusted editors.** An admin can grant a user the `segments` permission,
  as request permissions are granted today. That user gets the "Intro &
  credits..." editor and the Detect actions, without becoming an admin.
  The audit log names who changed what.

### As built

- **Scopes and the queue.** `POST /items/{id}/segments/redetect` takes an
  episode, season, series or film; `POST /segments/redetect` a selection of
  up to 1000; both accept `kinds` and `includeLocked` in the body or the
  query, and answer with how many items were in scope, how many analyses
  were reopened and how many hand-set items were kept, which is what the
  UIs ask about ("Also replace 3 episodes you set by hand?"). The reopened
  seasons and films go into the detector's priority queue
  (`Detector.Prioritise`), worked through before each library season and
  film; they ignore the schedule (`Gate.WaitNow`) but not a pause,
  playback or a scan. A library run held only by the schedule is stopped
  at once when targeted work arrives, and does that season again later; a
  run that is reading finishes its season first. The status lists the
  queue (`targets`), which the client's series, season and film pages and
  the admin's Activity page show.
- **A whole library** (`POST /libraries/{id}/segments/redetect`, admin
  only) goes through the library queue with "Analyse now" rather than
  ahead of it: it is too big to put in front of everyone else's requests.
- **An episode** has no fingerprint cache to reuse: its neighbours are
  decoded again as partners, but neither analysed nor saved again.
- **Films' credits from chapters** are recorded during the film scan from
  the probe it already makes, and the detector's pass over a film library
  covers films scanned before (or whose probe failed) by opening each
  file's header once. The chapter must be the last, or the one before a
  last "post-credits" chapter, start in the second half of the film, and
  last between 15 seconds and 30 minutes; its name must be one of the
  phrases outright (in English, German, French, Spanish, Portuguese,
  Italian, Dutch, the Scandinavian languages, Finnish, Polish, Czech,
  Russian and Japanese), after a chapter number is stripped. Both follow
  `libraries.intro_detection`.
- **Skip credits on a film** ends it as reaching the end would; when a
  chapter follows the credits, it skips to that instead.
- **Reports** (`segment_reports`, migration 0038): one open report per
  user, item and kind, so reporting again updates it and one person cannot
  make a season look crowded. The admin's Activity page lists them with
  the season's count, offers "Detect season again" at three, and links to
  the client's editor at `/items/{id}/segments`.
- **Trusted editors** are per user (`users.segment_editor`), as request
  levels are; groups do not carry permissions yet, and will with the
  admin's permissions in stage 8. Editors can also read
  `GET /segments/status`, for the progress of what they started, but not
  the reports list.

## Admin controls

Everything the admin controls, in one place in the admin UI:

- **Defaults** (built): the server's value for every preference, and each
  group's, each with a lock toggle. Shown as one table per Settings section
  with a column for the server and one per group, and on a phone as a card
  per preference. The groups' priority order is set on the same page with
  move buttons.
- **Home layouts**: the server's and each group's, per surface, edited with
  the same Edit home mode the client uses (the admin UI embeds it with a
  "previewing as" selector). Pinned rows with date and time windows.
- **Content rating ceiling** per user and per group (`max-rating`, in the
  `metadata.cert_region` board's terms, with an "unrated" policy: allow,
  hide). This hides titles everywhere: rows, search, libraries, person
  pages. It is the main thing a lock exists for: Kids get it locked, and
  can still arrange their own home, choose their own theme and set their
  own subtitles.
- **Permissions** per user and group: edit own home layout (default on),
  item overrides (on), saved views (on), trusted segment editor (off),
  requests (exists).
- **Shared saved views and rows**: an admin's saved view can be offered to
  a group, and appears in their Add row sheet.
- **Branding**: server name, sign-in and profile-picker backdrop (a chosen
  title, or the newest arrivals), and a short welcome or notice line.
- **"Previewing as"**: the admin can open the client as any user or group,
  read-only, to see the result of their defaults and locks.

## Clients

The design is written so the planned clients get the whole of it by
implementing three things: draw a row by its style, draw Settings from the
registry, and send the surface.

- **Web (phone, tablet, desktop)**: surface chosen by viewport, overridable
  per device. Edit home in place.
- **TV apps**: surface `tv`. Rows are focus-navigated, so `hero` and
  `landscape` styles get large focus targets. Editing works with a remote:
  long-press a row to open its menu. Typing is avoided: Add row offers the
  catalogue and existing saved views, and making a new saved view is
  something the phone or web client does.
- **Phone apps**: surface `phone`. The device-scoped settings (mobile data
  quality, downloads) live here, and so does offline sync (milestone 11),
  which follows the same registry.
- **Desktop apps**: surface `desktop`, plus whatever device-scoped
  preferences a desktop player needs (output device, exclusive mode).

## Staging

Each stage stands on its own and is worth shipping.

1. **Registry and layers** (built). `internal/prefs`, `GET`/`PUT
   /auth/me/prefs`, server and group defaults with locks, group order,
   device prefs, `skip-intro` and `skip-credits`. The web client's Settings
   page is drawn from the registry. Existing keys keep their names and
   values.
2. **Item overrides** for audio, subtitles, autoplay, intro and credits
   behaviour, and spoiler protection. The player and series menus gain
   "for this show".
3. **Targeted detection**: episode, series, library and selection scopes,
   the priority queue, film credits from chapter names, and trusted
   editors. Also "Report wrong times".
4. **Home layouts**: the row model, `/home/rows`, the catalogue of the row
   types that exist today plus `on-deck`, `collection`, `person` and
   `random`, Edit home, surfaces, server and group layouts.
5. **Saved views**: server-side filtering on `/libraries/{id}/items`,
   `saved_views`, the `saved-view` row, sidebar pins.
6. **Recommendation controls** and `user_item_flags`: not interested,
   don't count, muted genres, bias, rotation, watchlist.
7. **Seasonal rows**: TMDB keywords stored at scan time (and back-filled by
   a metadata refresh), the occasion calendar, the `seasonal` row and its
   automatic placement, Christmas specials found among episodes, "Add to /
   Not a Christmas film", admin occasions and additions. Halloween and
   Christmas first; the rest are entries in the calendar.
8. **Admin**: content rating ceilings, pinned rows with windows, previewing
   as a user, branding.
9. **Film credits from versions**, and visual detection, which is already
   listed under intro detection.

## Open questions

- Whether a group's layout should update users who have already customised
  theirs: pinned rows do; should anything else?
- How many rows a TV layout may hold before the server pages them (eight is
  a guess).
- Whether a content rating ceiling should also apply to music (explicit
  lyrics flag) once music metadata carries it.
- Whether item overrides on a library should be inherited by its items or
  stay library-level settings only (default sort, default tab).
