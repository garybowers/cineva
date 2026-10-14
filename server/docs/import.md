# Import from Plex and Jellyfin

Status: agreed design, being built (October 2026). Moving to Cineva should
not cost anyone what they have watched: an admin points Cineva at the old
Plex or Jellyfin server once, maps its users to Cineva's, chooses what to
bring, and runs it.

## What can come across

The admin chooses, per import:

| Option | Brings | From |
|---|---|---|
| **Watch state** (on by default) | Watched flags, resume positions, when last played | Jellyfin: every user. Plex: every account's watched history; resume positions only for the account whose token is used (Plex does not hand other accounts' positions to a server token). |
| **Create users** | A Cineva account for each source user mapped to "Create", with a temporary password shown once to the admin | Both |
| **Music playlists and favourites** | Each user's music playlists as Cineva playlists, and Jellyfin's favourite tracks as a playlist called "Favourites" | Jellyfin: playlists and favourites. Plex: the token account's music playlists. |

Video playlists and video favourites have nowhere to go: Cineva's
playlists are music, and it has no favourites. The dry run says how many
were left behind.

## Matching titles

In order, stopping at the first that finds the title:

1. **Provider id.** Films and series by TMDB id; an episode by its series'
   TMDB id with its season and episode numbers.
2. **File path.** When both servers read the same files, the last parts of
   the path match: the file name with its folder (`Season 1/Show
   S01E01.mkv`), compared without regard to case. Different mount points
   (`/data/tv` against `/mnt/media/TV`) do not matter.
3. **Music** by artist, album and title, ignoring case, when the path does
   not match.

Anything unmatched is listed in the dry run and skipped by the import.

## Never losing Cineva's own state

An imported state is written only where Cineva has none for that user and
title, or has an older one: a film watched in Cineva last week is not
marked unwatched by a year-old Plex position. Watched titles keep their
last-played date, so Continue Watching and "recently watched" stay in a
sensible order. Running the same import twice changes nothing the second
time.

## The flow

From the admin's Users page, "Import from Plex or Jellyfin":

1. **Connect**: the kind, the server's address and a token (Jellyfin: an
   API key from its dashboard; Plex: the server owner's `X-Plex-Token`).
   Cineva lists the source's users.
2. **Map**: each source user to a Cineva user (matched by name where they
   agree), to "Create a user", or to "Skip". Tick what to bring.
3. **Dry run**: per user, what would come across (watched, in progress,
   playlists) and what did not match, without writing anything.
4. **Import**: runs in the background with progress; the result repeats the
   dry run's numbers as done, plus the temporary passwords of created users.

The token is held in memory for the run only and never stored. One import
runs at a time.

## API (admin)

| Method and path | Does |
|---|---|
| `POST /import/check` | Connect to a source: its name, version and users, and any caveat (Plex positions) |
| `POST /import/runs` | Start a dry run or an import: source, mapping, options |
| `GET /import/runs/current` | The current or last run: status, progress, and per-user results |

Progress also arrives as `import` events on the event stream.

## Building it

`internal/importer`: one source per kind (Jellyfin, Plex) giving a common
model of users, watch states, tracks and playlists; the matcher over
Cineva's library; and the runner. Storage gains a conditional play-state
write (`ImportPlayState`). The admin UI gains the import dialogue on the
Users page.
