# Scanning in passes

Status: designed and built (October 2026); "As built" at the end. A first scan of a big
library (11,000 episodes and the films beside them) took hours before a
single poster appeared, because every new title's metadata lookup and
artwork download ran inside the scan loop. Music already scans in
passes since the music overhaul's stage 1; this brings film and TV in
line.

## The three passes

1. **Register.** The walk, the change test, the parallel probe, and the
   rows: a film from its file name and year, a series, its seasons and
   episodes from the folder and the episode numbers, all with a title the
   path gives, no provider id, no artwork. Everything is in the database
   and on every screen within minutes, with placeholders where the
   posters will be. The scan's progress is files registered of files
   found, as now.
2. **Enrich.** A background job, one per library, started twenty seconds
   after the scan decides to defer and run alongside the register pass:
   every title the pass has left unmatched, newest first, goes through
   the lookup the retry pass already has (`retryOne`: the provider
   match, the poster and backdrop, a series' seasons and episodes with
   their titles, overviews, stills and air dates), four lookups at a
   time, each worker paced for the provider, an `items` event per title
   so tiles fill in as it runs, and progress ("Fetching details, 120 of
   480 titles") on the scan's row in the admin and on the library's
   page. The job works in rounds, asking for the unmatched titles again
   when a round ends, until the pass has finished and a round finds
   nothing. A title the provider does not know keeps the retry pass's
   slow cadence after that first attempt, as now. A rescan while the job
   runs leaves it running, and if it defers too the job takes it on.
3. **Read.** Intro and credits detection and the seek-bar previews, which
   already run in the background after a scan through the gate, start
   when the enrichment ends rather than racing it, so the first hours of
   a new library go to showing it rather than reading it.

## When the lookup stays inline

An incremental scan that finds a handful of new titles (a new episode
from Sonarr, one film) looks them up inline as it always did: the title
appears with its metadata in one go, seconds later. The scan defers to
the enrichment job when it registers more than `enrichThreshold` new
films or series (25) in one run, which is what a first scan or a bulk
import looks like. The threshold is a constant, not a setting.

## What the admin sees

- The scan row gains a phase: `registering` (with `enriched` of
  `toEnrich` filling in once the job is running alongside), `enriching`
  once the pass has ended and the job is still going, and `done`; the
  `scan` event carries it. The Libraries page shows "Scanning files" with
  its bar and "Fetching details" with its own, for as long as each runs.
- The library page in the client says "Details are still arriving" with
  a count while the job runs, for everyone.
- Cancelling a scan cancels the register pass and the job with it; once
  the phase is enriching the same button stops the job alone.

## Storage

`scans` gains `phase`, `enriched` and `to_enrich`; nothing else. The
unmatched set is what it is today: `tmdb_id = 0` with `match_tried_at`
unset or due.

## Not changed

Music keeps its own background enrichment (MusicBrainz, `music.md`), the
retry cadence for titles the provider does not know stays, and the
scheduled rescan is unaffected.

## As built

- The register pass is the scanners with no provider for that run: a scan
  that finds more than 25 new films or series (counted from the changed
  files before the loop: new file paths for films, new series folders for
  TV) saves the same rows the no-provider path always produced, and the
  scan's post-pass retry of unmatched titles is skipped for that run.
- The pass is quick because a file is opened once and its rows are
  written together. `media.Probe` reads the subtitle and audio streams
  with the rest (the pass used to open every file three times, for the
  technical info, the subtitles and the audio tracks, and on a real
  episode each open costs 30 ms), and each probe batch of 32 files is
  prepared in full, every lookup, download and probe done, then written
  in one transaction: `store.SaveFilms` for a batch of films,
  `store.SaveSeries` for each series' share of a batch (its row when
  new, its seasons, its episodes, each with genres, credits, streams and
  the failed-match record), so the write lock is held for the writes
  alone. The store opens SQLite with `synchronous(NORMAL)`, which in WAL
  mode risks only the last transactions on a power cut, never the
  database, and with `_txlock=immediate`, so a transaction waits for the
  write lock rather than failing at its first write. Measured on the 39
  test episodes on NVMe with no provider: 1.8 s before, 0.62 s after,
  nearly all of it the probes; the pragma on its own changed nothing,
  the rows were not the cost.
- The job (`internal/library/enrich.go`) starts `enrichStartDelay` (20 s)
  after the scan decides to defer, while the pass is still running, and
  works in rounds: `UnmatchedItemsNewest`, the titles due a try, through
  a pool of `enrichWorkers` (4) workers, each with the retry pass's 80 ms
  between its own lookups, so at most about fifty a second against
  TMDB's limit of about fifty. `retryOne` matches a film or a series
  and, for a series, fills its seasons and episodes from the provider.
  A round that finds nothing waits five seconds and asks again while the
  pass is registering, wakes as soon as the pass ends, and ends the job
  once the pass has ended. The TV pass marks the series whose episodes it
  is still writing (the last series of each batch), and the job leaves
  that series for a later round, so a series is matched only once all of
  its episodes are there for `fillSeriesChildren` to fill. The job takes
  no scan lock: it reads the database, the provider and the artwork
  directory, and its writes queue behind the pass's transactions. A
  title the provider does not know is recorded as a failed match and
  joins the slow retry cadence. A provider that cannot be reached is
  waited for a minute, three times, then the job gives up and the retry
  pass takes the rest on later scans. The scan row's `phase`,
  `enriched` and `toEnrich` are written per title (`registering` while
  the pass runs, `enriching` after); a `scan` event (admins) and an
  `items` event carrying the counts (everyone) go out per title, and the
  pass's own progress events carry the job's counts too.
- `OnScanDone`, which starts detection and the previews, fires once: when
  the job ends after the pass, or at the end of the scan when the job
  ended during it (stopped, or the provider unreachable) or there was
  none. A cancelled or failed pass starts no readers. The CLI's `cineva
  scan` has no event bus, so it runs the job in line after the pass and
  still returns with the details fetched.
- Cancelling a running scan stops the pass and the job; cancel on a scan
  whose phase is enriching stops the job alone. A rescan while the job
  runs leaves it to finish and does not start another; a rescan that
  defers as well hands its titles to the running job, which reports on
  the newer scan's row from then on.
- An `.nfo` TMDB id is not applied on a deferred run (the scan does not
  read the provider then, and the job matches by title and year, or by an
  id in the file name); see BACKLOG.
