// Single typed API module — all server calls go through here (see CLAUDE.md).

export interface Health {
  status: string
  service: string
  version: string
}

export interface Library {
  id: string
  name: string
  mediaType: string
  itemCount: number
  /** A scan is running. One bit, with none of the admin-only progress detail. */
  scanning?: boolean
  /** Whether the library's folders can be reached right now. */
  storage?: StorageStatus
}

export interface StorageStatus {
  available: boolean
  /** When it last changed. */
  since?: string
  /** The folder that failed, and how. */
  path?: string
  error?: string
}

export interface Item {
  id: string
  libraryId: string
  kind: 'movie' | 'series' | 'season' | 'episode' | 'artist' | 'album' | 'track'
  artist?: string
  trackNum?: number
  discNum?: number
  parentId?: string
  title: string
  year?: number
  seasonNum?: number
  episodeNum?: number
  runtimeSecs?: number
  sizeBytes?: number
  rating?: number
  voteCount?: number
  tagline?: string
  backdrop?: string
  collectionName?: string
  /** Content rating for the server's configured region ("15", "PG-13"). */
  certification?: string
  /** Series only: "Ended", "Returning Series", "Canceled" — TMDB's wording. */
  status?: string
  /** Series only: the broadcaster it began on. */
  network?: string
  /** Series only: when it last aired, and when the next episode is due. */
  lastAirDate?: string
  nextAirDate?: string
  /** The name in the work's own language, present only when it differs. */
  originalTitle?: string
  /** ISO 3166-1 country of origin. */
  country?: string
  /** Broadcast date of an episode, or a season's premiere ("2008-01-20"). */
  airDate?: string
  /** What the provider says the series has, which is not what is on disk. */
  seasonCount?: number
  episodeCount?: number
  genres?: string[]
  /** On a series in a recently added row, when an episode of it arrived in
   *  the last six weeks: the newest one, to label the tile with what is new. */
  latestEpisode?: EpisodeBrief
  videoCodec?: string
  audioCodec?: string
  width?: number
  height?: number
  overview?: string
  poster?: string
  addedAt?: string
  /** The viewer's own state, stamped on by the library listing. */
  watched?: boolean
  positionSecs?: number
  /** When this viewer last touched it, for a "recently watched" ordering. */
  watchedAt?: string
  /** The viewer's counted plays (an album's: its tracks' summed) and the last one. */
  playCount?: number
  lastPlayedAt?: string
  /** The viewer's own stars, 1 to 5; absent when not rated. (rating above
      is the provider's score.) */
  userRating?: number
}

export interface ItemDetail {
  item: Item
  children: Item[]
  playState?: { itemId: string; positionSecs: number; watched: boolean }
  /** The same film held more than once — primary first. Absent when single. */
  versions?: Item[]
  cast?: Person[]
  crew?: Person[]
}

export interface SubtitleTrack {
  id: string
  language?: string
  /** The language in English ("German" for "ger"), when the server knows it. */
  languageName?: string
  title?: string
  codec: string
  forced?: boolean
  /** Set only for text tracks, which the player overlays as WebVTT. */
  url?: string
  /**
   * PGS/VobSub: a bitmap with no text to hand the player, so it can only be
   * shown by painting it into the picture. Selecting one restarts playback
   * as a re-encode.
   */
  burnIn?: boolean
  /** Identifies the track to the transcoder when burning in. */
  streamIndex?: number
}

export interface PlayState {
  itemId: string
  positionSecs: number
  watched: boolean
  updatedAt: string
}

export interface PlayInfo {
  mode: 'direct' | 'hls'
  url: string
  sessionId?: string
  durationSecs: number
  positionSecs: number
  reason?: string
  subtitles: SubtitleTrack[]
  audio: AudioTrack[]
  audioTrack: number
  /** The subtitle the user's preferences switched on, to show from the start. */
  subtitle?: string
  /** Stream index of the image subtitle burned into the picture, if any. */
  burnedSubtitle?: number
  /** The file's own chapter marks, in order. Absent for files with none. */
  chapters?: Chapter[]
  /** Parts the player can offer to skip, such as a detected intro. */
  segments?: Segment[]
  source: PlaySource
}

/** A part of an item that is not the item itself, in seconds from the start. */
export interface Segment {
  kind: 'intro' | 'credits'
  startSecs: number
  endSecs: number
}

export interface Chapter {
  startSecs: number
  endSecs: number
  /** Often absent — a disc rip usually just numbers them. */
  title?: string
}

export type PlayQuality = 'auto' | 'original' | '1080p' | '720p' | '480p'
export const QUALITIES: { id: PlayQuality; label: string }[] = [
  { id: 'auto', label: 'Auto' },
  { id: 'original', label: 'Original' },
  { id: '1080p', label: '1080p · 8 Mbps' },
  { id: '720p', label: '720p · 4 Mbps' },
  { id: '480p', label: '480p · 2.5 Mbps' },
]

// What this device can decode, detected once via canPlayType. Sent with play
// requests so the server only transcodes what the device actually lacks.
function detectCaps() {
  const v = document.createElement('video')
  const can = (t: string) => v.canPlayType(t) !== ''
  const containers: string[] = []
  if (can('video/mp4; codecs="avc1.640029, mp4a.40.2"')) containers.push('mp4', 'm4v', 'mov')
  if (can('video/x-matroska; codecs="avc1.640029, mp4a.40.2"')) containers.push('mkv')
  if (can('video/webm; codecs="vp9, opus"')) containers.push('webm')
  const videoCodecs: string[] = []
  if (can('video/mp4; codecs="avc1.640029"')) videoCodecs.push('h264')
  if (can('video/mp4; codecs="hvc1.1.6.L123.B0"')) videoCodecs.push('hevc')
  if (can('video/webm; codecs="vp9"')) videoCodecs.push('vp9')
  if (can('video/mp4; codecs="av01.0.05M.08"')) videoCodecs.push('av1')
  const audioCodecs: string[] = []
  if (can('audio/mp4; codecs="mp4a.40.2"')) audioCodecs.push('aac')
  if (can('audio/mpeg')) audioCodecs.push('mp3')
  if (can('audio/mp4; codecs="ac-3"')) audioCodecs.push('ac3')
  if (can('audio/mp4; codecs="ec-3"')) audioCodecs.push('eac3')
  if (can('audio/webm; codecs="opus"')) audioCodecs.push('opus', 'vorbis')
  if (can('audio/flac')) audioCodecs.push('flac')
  return { containers, videoCodecs, audioCodecs }
}
const caps = detectCaps()

/** Enough of an episode to label something else with it. */
export interface EpisodeBrief {
  id: string
  title: string
  seasonNum: number
  episodeNum: number
  addedAt: string
  airDate?: string
}

export interface ResumeItem extends Item {
  positionSecs: number
  /** An episode's series, since its own title does not say which show. Its
   *  artwork comes too: an episode's own image is a landscape still. */
  seriesId?: string
  seriesTitle?: string
  seriesPoster?: string
  seriesBackdrop?: string
}

/** One row on the home screen, from one library. */
export interface HomeShelf {
  libraryId: string
  libraryName: string
  /** "recent", or a recommendation row's kind. */
  kind: string
  title: string
  genre?: string
  items: Item[]
}
export interface HomeFeed {
  continueWatching: ResumeItem[]
  /** The next unstarted episode of each series being watched. */
  nextUp: ResumeItem[]
  /** Episodes that aired lately across the TV libraries, newest first. */
  newEpisodes: ResumeItem[]
  shelves: HomeShelf[]
}
export const homeFeed = () => get<HomeFeed>('/home')

// Accounts (milestone 4). A 401 anywhere means the session is gone: the
// app listens for this event and shows the sign-in screen.
export interface User {
  id: string
  username: string
  displayName: string
  role: 'admin' | 'user'
  disabled: boolean
  hasPassword: boolean
  hasPin: boolean
  /** An uploaded picture's /artwork/ path. */
  avatar?: string
  /** The theme token the initial is drawn on; absent for the default. */
  avatarColour?: AvatarColour
  libraryIds: string[]
  /** What happens to this user's requests (admins are always auto). */
  requestLevel?: 'none' | 'approval' | 'auto'
  /** A trusted editor: may correct intro and credits times and start
      their detection without being an admin. */
  segmentEditor?: boolean
}
export interface Profile {
  id: string
  username: string
  displayName: string
  hasPassword: boolean
  hasPin: boolean
  avatar?: string
  avatarColour?: AvatarColour
}
/** Theme tokens, not colours, so every theme draws an avatar in its own palette. */
export const AVATAR_COLOURS = ['accent-2', 'chart-1', 'chart-2', 'chart-3', 'ok', 'err'] as const
export type AvatarColour = (typeof AVATAR_COLOURS)[number]

/** A signed-in browser: listed, named, signed out, and handed queues. */
export interface Device {
  id: string
  name: string
  /** Browser and system from the User-Agent: "Chrome · Android". */
  client: string
  clientIp: string
  createdAt: string
  lastSeenAt: string
  /** The device making the request. */
  current: boolean
  /** Has the app open now, so can be sent a queue. */
  online: boolean
  queue?: { count: number; index: number; current?: Item; seriesTitle?: string; updatedAt: string }
  /** What it is playing now, as its last heartbeat said. */
  playing?: { item: Item; seriesTitle?: string; positionSecs: number; durationSecs: number; paused: boolean; reportedAt: string }
  /** The Home Assistant area it is in (its room); "" for none. */
  areaId?: string
}
/** A queue on its way between devices. */
export interface HandoffQueue {
  items: Item[]
  index: number
}
export interface APIKey {
  id: string
  name: string
  createdAt: string
  lastUsedAt?: string
}
export const UNAUTHORISED = 'cineva:unauthorised'

// Thrown by postProgress when the server answers 409: an administrator ended
// this playback. The heartbeat is the only channel for this — a direct play
// is the server handing out byte ranges, so there is nothing it can cut off
// at its end, and the event stream is broadcast to every client, which would
// tell everyone who was watching what.
export const PLAYBACK_STOPPED = 'cineva:playbackStopped'

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, init)
  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORISED))
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const body = await res.json()
      if (body?.message) msg = String(body.message)
    } catch { /* not JSON */ }
    throw new Error(msg)
  }
  if (res.status === 204) return undefined as T
  return res.json() as Promise<T>
}

/** autoUser is who this device signs in as when nobody is signed in on it
    (an admin's choice), for "Back to ..." on the profile picker. Asking
    signs such a device in as them when nobody is. */
export const authStatus = () =>
  get<{ setupRequired: boolean; loginMode: 'picker' | 'form'; user?: User; autoUser?: { id: string; displayName: string } }>('/auth/status')
export const login = (username: string, password: string) => post<User>('/auth/login', { username, password })
export const setupAdmin = (username: string, password: string, displayName: string) =>
  post<User>('/auth/setup', { username, password, displayName })
export const logout = () => post<void>('/auth/logout')
export const authProfiles = () => get<Profile[]>('/auth/profiles')
export const pinLogin = (userId: string, pin: string) => post<User>('/auth/pin', { userId, pin })
export const updateMe = (displayName: string) => req<User>('/auth/me', {
  method: 'PATCH',
  headers: { 'Content-Type': 'application/json' },
  body: JSON.stringify({ displayName }),
})
export const setPIN = (password: string, pin: string) => post<void>('/auth/pin/set', { password, pin })
export const changePassword = (current: string, next: string) => post<void>('/auth/password', { current, new: next })
export const listAPIKeys = () => get<APIKey[]>('/auth/keys')
export const createAPIKey = (name: string) => post<APIKey & { key: string }>('/auth/keys', { name })
export const deleteAPIKey = (id: string) => req<void>(`/auth/keys/${id}`, { method: 'DELETE' })

/** A photo as chosen, or as framed in the crop dialogue (a JPEG Blob). */
export const uploadAvatar = (file: Blob) => {
  const form = new FormData()
  form.append('image', file, file instanceof File ? file.name : 'avatar.jpg')
  return req<User>('/auth/avatar', { method: 'POST', body: form })
}
export const removeAvatar = () => req<User>('/auth/avatar', { method: 'DELETE' })
export const setAvatarColour = (colour: AvatarColour | '') =>
  req<User>('/auth/avatar/colour', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ colour }) })

export const listDevices = () => get<Device[]>('/auth/sessions')
/** Name this device, or put it in a Home Assistant room ("" for none).
    Fields left out are kept, so send only what changes. */
export const nameDevice = (change: { name?: string; areaId?: string }) =>
  req<void>('/auth/sessions/current', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(change) })
export const revokeDevice = (id: string) => req<void>(`/auth/sessions/${id}`, { method: 'DELETE' })
/** Forget a device: its name, room and settings go, and it is signed out. */
export const forgetDevice = (id: string) => req<void>(`/auth/sessions/${id}?forget=true`, { method: 'DELETE' })
export const revokeOtherDevices = () => post<{ revoked: number }>('/auth/sessions/revoke-others')
/** Records this device's queue so another can take it over. */
export const saveDeviceQueue = (itemIds: string[], index: number) =>
  req<void>('/auth/sessions/current/queue', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ itemIds, index }) })
export const deviceQueue = (id: string) => get<HandoffQueue>(`/auth/sessions/${id}/queue`)
export const playOnDevice = (id: string, itemIds: string[], index: number) =>
  post<void>(`/auth/sessions/${id}/play`, { itemIds, index })
/** Continue here what another device plays: it pauses, and this is what
    to play from where it stopped (positionSecs for a track, which keeps no
    resume state). */
export const takeOverDevice = (id: string) =>
  post<HandoffQueue & { positionSecs: number; from: string }>(`/auth/sessions/${id}/takeover`, {})
/** Said to the players when another device takes over what this one plays:
    the one playing itemId pauses and reports where it stopped. */
export const TAKEN_OVER = 'cineva:taken-over'

const get = <T>(path: string) => req<T>(path)
const post = <T>(path: string, body?: unknown) =>
  req<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })

// ---- Intro and credits, corrected by an administrator or trusted editor ----
export type SegmentKind = 'intro' | 'credits'
export interface SegmentState {
  kind: SegmentKind
  /** The item has this segment; startSecs and endSecs are its bounds. */
  found: boolean
  startSecs?: number
  endSecs?: number
  /** Detection has looked at the current file, or it was set by hand. */
  analysed: boolean
  /** Set by hand (a segment, or that there is none): detection leaves it alone. */
  locked: boolean
}
export interface ItemSegments {
  /** Intro detection is on, so the item can be re-detected. A film has credits only. */
  detection: boolean
  runtimeSecs: number
  segments: SegmentState[]
}
export const itemSegments = (id: string) => get<ItemSegments>(`/items/${id}/segments`)
/** Set by hand, or "none": locked against re-detection until cleared. */
export const setItemSegment = (id: string, kind: SegmentKind, body: { startSecs: number; endSecs: number } | { none: true }) =>
  req<ItemSegments>(`/items/${id}/segments/${kind}`, { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
/** Hand a hand-set segment back to detection. */
export const clearItemSegment = (id: string, kind: SegmentKind) =>
  req<ItemSegments>(`/items/${id}/segments/${kind}`, { method: 'DELETE' })
/** What a re-detection covered. */
export interface RedetectResult {
  /** Episodes and films in scope. */
  items: number
  /** Analyses sent back to detection. */
  reopened: number
  /** Items whose hand-set times were kept; ask before replacing them. */
  locked: number
}
export interface RedetectOptions {
  kinds?: SegmentKind[]
  includeLocked?: boolean
}
/** Detect an episode (alone), season, series or film again, ahead of the
    library queue. */
export const redetectItem = (id: string, opts: RedetectOptions = {}) =>
  post<RedetectResult>(`/items/${id}/segments/redetect`, opts)

/** A season or film waiting for detection, or being worked on. */
export interface DetectionTarget {
  itemId: string
  kind: 'season' | 'film'
  libraryId?: string
  seriesId?: string
  title: string
  season?: number
  working: boolean
}
/** As much of detection's status as the client shows: what it was asked
    to do, and whether it is waiting. */
export interface DetectionStatus {
  enabled: boolean
  paused?: boolean
  pauseReason?: string
  targets?: DetectionTarget[]
}
export const detectionStatus = () => get<DetectionStatus>('/segments/status')

/** Tell the administrator an intro or credits skip is in the wrong place. */
export const reportSegment = (id: string, body: { kind: SegmentKind; positionSecs: number; note?: string }) =>
  post<unknown>(`/items/${id}/segments/reports`, body)

export const getHealth = () => get<Health>('/health')
export const listLibraries = () => get<Library[]>('/libraries')
export interface Person {
  id: string
  tmdbId: number
  name: string
  profile?: string
  /** Character for cast, job for crew. */
  role?: string
  kind?: 'cast' | 'crew'
}

/** One film in a person's filmography, with what they did on it. */
export interface PersonCredit extends Item {
  /** "cast" or "crew" — distinct from Item.kind, which is the media kind. */
  creditKind: 'cast' | 'crew'
  /** Character for cast, job for crew; several joined with " · ". */
  role?: string
}

/** A person, their filmography here, and the shape of it. */
export interface PersonProfile extends Person {
  credits: PersonCredit[]
  castCount: number
  crewCount: number
  firstYear?: number
  lastYear?: number
  /** Distinct crew jobs, most frequent first — "Director, Writer". */
  jobs?: string[]
}

export const personDetail = (id: string) => get<PersonProfile>(`/people/${id}`)

export interface GenreCount { genre: string; count: number }

/** One shelf on the Recommended tab, decided server-side. */
export interface RecRow {
  kind: 'recent' | 'because' | 'toprated' | 'genre' | 'unwatched'
  title: string
  /** Set on a genre row, so its heading can open the full genre listing. */
  genre?: string
  items: Item[]
}
export const libraryRecommended = (id: string) => get<RecRow[]>(`/libraries/${id}/recommended`)
/** A TV library's New episodes row: what aired lately, newest first. */
export const libraryNewEpisodes = (id: string) => get<ResumeItem[]>(`/libraries/${id}/new-episodes`)

/** The genres this library actually holds, most common first. */
export const libraryGenres = (id: string) => req<GenreCount[]>(`/libraries/${id}/genres`)

export const libraryItems = (id: string, genre?: string) =>
  get<Item[]>(`/libraries/${id}/items${genre ? `?genre=${encodeURIComponent(genre)}` : ''}`)

/**
 * The library's items plus the mark to resume from. Passing a previous mark
 * returns only what has changed since — a scan rewrites every item it sees,
 * so polling during one otherwise refetches the whole list (~200 KB a second
 * at four thousand items) for a handful of new tiles.
 *
 * Deletions are not expressible: a pruned row simply stops existing. Those
 * are reconciled by the full refetch on the "libraries" event at scan end,
 * which is when pruning and version grouping happen.
 */
export async function libraryItemsSince(
  id: string,
  since?: string,
): Promise<{ items: Item[]; mark: string }> {
  const q = since ? `?since=${encodeURIComponent(since)}` : ''
  const res = await fetch(`/api/v1/libraries/${id}/items${q}`)
  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORISED))
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return { items: (await res.json()) as Item[], mark: res.headers.get('X-Items-Mark') ?? '' }
}
export const itemDetail = (id: string) => get<ItemDetail>(`/items/${id}`)
export const itemSubtitles = (id: string) => get<SubtitleTrack[]>(`/items/${id}/subtitles`)
export const searchItems = (q: string) => get<Item[]>(`/search?q=${encodeURIComponent(q)}`)
export interface AudioTrack {
  id: string
  streamIndex: number
  codec: string
  language?: string
  /** The language in English ("French" for "fre"), when the server knows it. */
  languageName?: string
  title?: string
  channels?: number
  bitRate?: number
  default?: boolean
}

/** What the server is actually sending, for the player's info panel. */
export interface PlaySource {
  /** The transcode path built — a device name, or "none" for software. */
  accel?: string
  /** True when an HDR source is being mapped to SDR for this stream. */
  hdrToneMapped?: boolean
  container?: string
  videoCodec?: string
  width?: number
  height?: number
  hdrFormat?: string
  dvProfile?: number
  video: 'direct' | 'remux' | 'transcode'
  outHeight?: number
  audio: 'direct' | 'transcode'
  audioCodec?: string
}

export const playItem = (
  id: string,
  quality: PlayQuality = 'auto',
  fromStart = false,
  audioTrack?: number,
  burnSubtitle?: number,
) => post<PlayInfo>(`/items/${id}/play`, { ...caps, quality, fromStart, audioTrack, burnSubtitle })
/** Mark an item watched or unwatched by hand; both directions clear resume. */
export const setWatched = (id: string, watched: boolean) =>
  req<PlayState>(`/items/${id}/watched`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ watched }),
  })
export async function postProgress(id: string, positionSecs: number, paused = false): Promise<void> {
  const res = await fetch(`/api/v1/items/${id}/progress`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ positionSecs, paused }),
  })
  if (res.status === 401) window.dispatchEvent(new Event(UNAUTHORISED))
  if (res.status === 409) throw new Error(PLAYBACK_STOPPED)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
}
// Tells the server this client has stopped playing the item (drops it from
// the admin Activity page straight away instead of after the idle timeout).
export const stopPlayback = (id: string) => fetch(`/api/v1/items/${id}/playback`, { method: 'DELETE' })
// Same, for page unload: a beacon survives the tab closing where fetch won't.
export const beaconStop = (id: string) => navigator.sendBeacon?.(`/api/v1/items/${id}/playback/stop`)
export const continueWatching = () => get<ResumeItem[]>('/continue-watching')
export const stopSession = (sid: string) => fetch(`/api/v1/play/${sid}`, { method: 'DELETE' })
export const downloadURL = (id: string) => `/api/v1/items/${id}/download`

/** A fresh check of the storage behind an item, with the sentence to show
    when it is unavailable. For a player whose stream has stopped arriving. */
export interface ItemStorage extends StorageStatus {
  library: string
  message?: string
}
export const itemStorage = (id: string) => get<ItemStorage>(`/items/${id}/storage`)

/** The server's reason a stream URL is failing, or null when it answers.
    A transcode session ended by the server (its storage stopped answering)
    says why on its playlist, and hls.js keeps only the status code. */
export async function streamProblem(url: string): Promise<string | null> {
  const res = await fetch(url, { cache: 'no-store' })
  if (res.ok) {
    await res.body?.cancel()
    return null
  }
  try {
    const body = await res.json()
    if (body?.message) return String(body.message)
  } catch { /* not JSON */ }
  return `${res.status} ${res.statusText}`
}

export async function prevEpisode(id: string): Promise<Item | null> {
  const res = await fetch(`/api/v1/items/${id}/prev`)
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<Item>
}

export async function nextUp(id: string): Promise<Item | null> {
  const res = await fetch(`/api/v1/items/${id}/next`)
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<Item>
}

/**
 * Where to carry on in a series or season.
 *
 * `reason` says which promise the button should make: "resume" carries on
 * part-way through, "next" starts one never begun, "restart" offers the
 * first episode again because everything is watched.
 */
export type NextUpInfo = { item: Item; positionSecs: number; reason: 'resume' | 'next' | 'restart' }

export async function seriesNextUp(id: string): Promise<NextUpInfo | null> {
  const res = await fetch(`/api/v1/items/${id}/next-up`)
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<NextUpInfo>
}

// Live event stream: the server pushes state changes (scan progress, library
// changes) over SSE so the UI updates without refreshes. Events are hints —
// handlers refetch whatever they display. EventSource reconnects on its own;
// onState reports connectivity (true on open, false while retrying).
export function subscribeEvents(
  handlers: Record<string, (data: unknown) => void>,
  onState?: (connected: boolean) => void,
): () => void {
  const es = new EventSource('/api/v1/events')
  for (const [type, fn] of Object.entries(handlers)) {
    es.addEventListener(type, (e) => {
      try {
        fn(JSON.parse((e as MessageEvent).data || 'null'))
      } catch {
        fn(null)
      }
    })
  }
  es.onopen = () => onState?.(true)
  es.onerror = () => onState?.(false)
  return () => es.close()
}
/** A TMDB collection ("The Matrix Collection") the library holds 2+ films from. */
export interface CollectionInfo {
  /** "tmdb:<id>" while the provider's grouping is in use, else a row id. */
  key: string
  tmdbId?: number
  name: string
  count: number
  poster?: string
  /** True once an operator has edited membership or order. */
  managed: boolean
  /** How many of the held films this viewer has finished. */
  watched: number
  /** The first film they have not, in the collection's order — absent once complete. */
  nextUpId?: string
  nextUpName?: string
}
export interface CollectionDetail {
  collection: CollectionInfo
  /** In release order, which is how a series is watched. */
  items: Item[]
}
export const libraryCollections = (id: string) =>
  req<CollectionInfo[]>(`/libraries/${id}/collections`)
export const collectionDetail = (id: string, key: string) =>
  req<CollectionDetail>(`/libraries/${id}/collections/${encodeURIComponent(key)}`)


// ---- Requests: titles outside the library, from TMDB, sent to Sonarr/Radarr ----
export type RequestState = 'requested' | 'approved' | 'downloading' | 'available' | 'declined'
/** Whether request screens are worth showing, and for which kinds. */
export interface RequestStatus {
  level: 'none' | 'approval' | 'auto'
  movies: boolean
  tv: boolean
  tmdb: boolean
  enabled: boolean
}
export interface EpisodeRef {
  season: number
  episode: number
}
export interface MediaRequest {
  id: string
  userId: string
  userName: string
  mediaType: 'movie' | 'tv'
  tmdbId: number
  title: string
  year?: number
  poster?: string
  backdrop?: string
  seasons: number[]
  episodes: EpisodeRef[]
  status: RequestState
  /** The admin's word on approving or declining. */
  note?: string
  /** Why it could not be sent; it waits for an admin meanwhile. */
  error?: string
  createdAt: string
  updatedAt: string
}
/** A search result outside the library. */
export interface DiscoverResult {
  mediaType: 'movie' | 'tv'
  tmdbId: number
  title: string
  year?: number
  overview?: string
  poster?: string
  backdrop?: string
  /** Status of the newest open request for it. */
  request?: RequestState
}
export interface DiscoverCredit {
  tmdbId: number
  name: string
  role?: string
  profile?: string
}
export interface DiscoverSeason {
  seasonNumber: number
  name: string
  episodeCount: number
  airDate?: string
  poster?: string
  /** Episodes of it on disk. */
  held: number
  request?: RequestState
}
/** A film or series page driven by TMDB. */
export interface DiscoverTitle {
  mediaType: 'movie' | 'tv'
  tmdbId: number
  title: string
  originalTitle?: string
  year?: number
  overview?: string
  tagline?: string
  poster?: string
  backdrop?: string
  genres: string[]
  rating?: number
  voteCount?: number
  runtimeMins?: number
  certification?: string
  status?: string
  network?: string
  cast: DiscoverCredit[]
  crew: DiscoverCredit[]
  seasons?: DiscoverSeason[]
  /** The library item, when this viewer can see one. */
  itemId?: string
  requests: MediaRequest[]
  canRequest: boolean
}
export interface MissingEpisode {
  season: number
  episode: number
  title: string
  airDate?: string
  status: 'missing' | 'upcoming'
  monitored: boolean
}
export interface MissingSeason {
  seasonNumber: number
  held: number
  total: number
  episodes: MissingEpisode[]
}
/** What a series is missing by Sonarr's account. */
export interface MissingInfo {
  /** The series' TMDB id, which a request for its gaps names. */
  tmdbId: number
  managed: boolean
  connection?: string
  seasons: MissingSeason[]
  requests: MediaRequest[]
}
export const requestStatus = () => get<RequestStatus>('/requests/status')
export const discoverSearch = (q: string) => get<DiscoverResult[]>(`/discover/search?q=${encodeURIComponent(q)}`)
export const discoverTitle = (type: 'movie' | 'tv', tmdbId: number) => get<DiscoverTitle>(`/discover/${type}/${tmdbId}`)
export const listRequests = (all = false) => get<MediaRequest[]>(`/requests${all ? '?all=1' : ''}`)
export const createRequest = (r: { mediaType: 'movie' | 'tv'; tmdbId: number; seasons?: number[]; episodes?: EpisodeRef[] }) =>
  post<MediaRequest>('/requests', r)
export const withdrawRequest = (id: string) => req<void>(`/requests/${id}`, { method: 'DELETE' })
export const itemMissing = (id: string) => get<MissingInfo>(`/items/${id}/missing`)
export const searchMissing = (id: string, seasons: number[], episodes: EpisodeRef[]) =>
  post<void>(`/items/${id}/missing/search`, { seasons, episodes })

// ---- Music: artist pages, listening rows, playlists ----

export interface ArtistPage {
  artist: Item
  /** Their own albums, oldest first. */
  albums: Item[]
  /** Other artists' albums with a track of theirs: compilations, soundtracks. */
  appearsOn: Item[]
  /** Ranked by the viewer's plays, then the household's; unplayed left out. */
  topTracks: Item[]
  stats: { tracks: number; secs: number; plays: number }
  /** The Wikipedia article the biography (artist.overview) is from. */
  bioUrl?: string
  /** A picture and biography are being looked up; ask again shortly. */
  lookingUp?: boolean
}
export const artistPage = (id: string) => get<ArtistPage>(`/items/${id}/artist`)

export interface Listening {
  recentlyPlayed: Item[]
  mostPlayed: Item[]
  mostPlayedAlbums: Item[]
}
export const libraryListening = (id: string) => get<Listening>(`/libraries/${id}/listening`)

export type SmartField = 'artist' | 'album' | 'title' | 'genre' | 'year' | 'format' | 'added' | 'plays' | 'lastPlayed' | 'rating'
export type SmartOp = 'is' | 'isNot' | 'contains' | 'gt' | 'lt' | 'inLast' | 'notInLast' | 'atLeast' | 'atMost'
export type SmartSort = 'random' | 'title' | 'artist' | 'album' | 'year' | 'added' | 'plays' | 'lastPlayed' | 'leastRecent' | 'rating'
export interface SmartRule { field: SmartField; op: SmartOp; value: string }
export interface SmartRules { match: 'all' | 'any'; rules: SmartRule[]; sort: SmartSort; limit: number }

export interface Playlist {
  id: string
  name: string
  /** Present on a smart playlist: its rules, evaluated each time it is read. */
  smart?: SmartRules
  trackCount: number
  durationSecs: number
  /** Up to four distinct album covers, for a mosaic. */
  covers: string[]
  createdAt?: string
  updatedAt?: string
}
export interface PlaylistDetail { playlist: Playlist; tracks: Item[] }

const send = <T>(method: string, path: string, body: unknown) =>
  req<T>(path, { method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body) })

/** Rate an item 1 to 5 stars; 0 clears the rating. */
export const setRating = (id: string, stars: number) => send<void>('PUT', `/items/${id}/rating`, { stars })

export const listPlaylists = () => get<Playlist[]>('/playlists')
export const createPlaylist = (name: string, smart?: SmartRules) => post<Playlist>('/playlists', { name, smart })
export const playlistDetail = (id: string) => get<PlaylistDetail>(`/playlists/${id}`)
export const updatePlaylist = (id: string, change: { name?: string; smart?: SmartRules }) =>
  send<PlaylistDetail>('PATCH', `/playlists/${id}`, change)
export const deletePlaylist = (id: string) => req<void>(`/playlists/${id}`, { method: 'DELETE' })
/** Tracks, albums or artists, added to the end in listening order. */
export const addToPlaylist = (id: string, itemIds: string[]) => post<PlaylistDetail>(`/playlists/${id}/items`, { itemIds })
/** The playlist's tracks, replaced with these in this order. */
export const setPlaylistTracks = (id: string, itemIds: string[]) =>
  send<PlaylistDetail>('PUT', `/playlists/${id}/items`, { itemIds })

// ---- Preferences ----
// The server's registry says what each preference is; the resolved view
// says what applies to this viewer on this device, and why.

export type PrefSource = 'device' | 'user' | 'group' | 'server' | 'builtin'
export interface PrefOption { value: string; label: string; description?: string }
export interface PrefEntry {
  key: string
  type: 'bool' | 'enum' | 'int' | 'string' | 'list'
  default: string
  /** person: follows you everywhere; device: stays with this screen. */
  scope: 'person' | 'device'
  lockable: boolean
  section: string
  /** Kept by the client for itself; not a row in Settings or Defaults. */
  hidden?: boolean
  label: string
  description: string
  options?: PrefOption[]
  min?: number
  max?: number
  /** Hidden while another preference has this value. */
  hideWhen?: { key: string; value: string }
}
export interface PrefOrigin { value: string; source: PrefSource; groupId?: string; groupName?: string }
export interface ResolvedPref extends PrefOrigin {
  /** Set by an administrator; source and groupName say whose lock. */
  locked: boolean
  /** What applies without this viewer's own choices: what Reset goes back to. */
  default?: PrefOrigin
}
export interface MyPrefs {
  device?: string
  sections: { id: string; label: string }[]
  registry: PrefEntry[]
  prefs: Record<string, ResolvedPref>
}
/** A value sets a key; null hands it back to the default. */
export type PrefChanges = Record<string, string | null>

export const getMyPrefs = () => get<MyPrefs>('/auth/me/prefs')
export const putMyPrefs = (changes: PrefChanges) => send<MyPrefs>('PUT', '/auth/me/prefs', changes)
/** Device preferences on this browser's own session. */
export const putDevicePrefs = (changes: PrefChanges) => send<MyPrefs>('PUT', '/auth/sessions/current/prefs', changes)

// ---- Home Assistant: the rooms, controls, cameras and modes this user may see ----
// Browsers only ever talk to Cineva, which holds the Home Assistant
// credential; camera pictures and live video are proxied through it too.

export type HADomain =
  'light' | 'switch' | 'fan' | 'input_boolean' | 'cover' | 'media_player' | 'scene' | 'script' | 'camera'

/** An entity's state, normalised. Fields that do not apply to its domain are left out. */
export interface HAEntity {
  entityId: string
  domain: HADomain
  /** Its friendly name. */
  name: string
  /** Its area, or its device's; "" for none. */
  areaId: string
  /** Home Assistant's own state: "on", "off", "playing", "open", ... */
  state: string
  /** False when Home Assistant reports it unavailable or unknown. */
  available: boolean
  /** Lights, switches, fans, input booleans, media players. */
  on?: boolean
  /** A light that takes brightnessPct. */
  dimmable?: boolean
  brightnessPct?: number
  /** A media player's volume, 0 to 1. */
  volume?: number
  muted?: boolean
  /** A media player's input, and the inputs it offers. */
  source?: string
  sources?: string[]
  /** What a media player says is playing. */
  mediaTitle?: string
  /** A cover's position, 100 open. */
  position?: number
  /** A camera that can stream live. */
  stream?: boolean
}

export interface HAArea {
  /** Home Assistant's area id; "" for entities with no area. */
  id: string
  name: string
  /** Cameras are among them. */
  entities: HAEntity[]
}

export interface HARooms {
  configured: boolean
  /** False while Home Assistant is unreachable; the last known states are still listed. */
  connected: boolean
  /** By name: areas holding at least one exposed entity this user may see. */
  areas: HAArea[]
  /** Every area in Home Assistant, for choosing this device's room. */
  allAreas?: { id: string; name: string }[]
  /** This device's room; "" for none. */
  currentAreaId: string
  /** This device, to tell whether a mode played here or sent the title elsewhere. */
  currentDeviceId?: string
}

/** One allowed service on an entity, with the fields its domain takes. */
export interface HAAction {
  service: string
  brightnessPct?: number
  volume?: number
  muted?: boolean
  source?: string
}

export interface HAModeStep {
  type: 'action' | 'wait' | 'play'
  entityId?: string
  service?: string
  brightnessPct?: number
  volume?: number
  muted?: boolean
  source?: string
  seconds?: number
  deviceId?: string
  itemId?: string
}

/** A named list of steps an admin builds and users run: "Movie mode". */
export interface HAMode {
  id: string
  name: string
  steps: HAModeStep[]
  /** It plays the title it is started from, so running it needs an itemId. */
  needsTitle: boolean
  /** The room its play step's device is in; "" when it plays nothing or the device has no room. */
  areaId?: string
}

/** A mode run's progress, sent over the event stream to whoever started it. */
export interface HAModeRun {
  runId: string
  modeId: string
  modeName: string
  status: 'running' | 'done' | 'failed'
  step: number
  steps: number
  error?: string
  /** The device the title was sent to, once the play step has run; empty when it was already playing it. */
  playedOn?: string
  playedOnName?: string
}

export const haRooms = () => get<HARooms>('/homeassistant/rooms')
/** Accepted when it answers; the new state arrives as a "homeassistant" event. */
export const haEntityAction = (entityId: string, action: HAAction) =>
  post<void>(`/homeassistant/entities/${encodeURIComponent(entityId)}/action`, action)
/** A camera's current picture. Add a query parameter to fetch a fresh one. */
export const haSnapshotURL = (entityId: string) =>
  `/api/v1/homeassistant/cameras/${encodeURIComponent(entityId)}/snapshot`
/** A camera's live view, for hls.js; its relative paths lead to the rest. */
export const haStreamURL = (entityId: string) =>
  `/api/v1/homeassistant/cameras/${encodeURIComponent(entityId)}/hls/master_playlist.m3u8`
export const listHAModes = () => get<HAMode[]>('/homeassistant/modes')
/** Starts it in the background; mode-run events report each step. */
export const runHAMode = (id: string, itemId?: string) =>
  post<{ runId: string }>(`/homeassistant/modes/${encodeURIComponent(id)}/run`, itemId ? { itemId } : {})

// ---- Notifications: the caller's own destinations ----
// Personal events (your requests, new episodes of what you watch) go only
// to destinations you set up yourself. The kinds say which settings each
// needs, so the form is drawn from them.

export interface NotifyField {
  name: string
  label: string
  secret?: boolean
  required?: boolean
  hint?: string
}
export interface NotifyKind {
  id: string
  label: string
  personal: boolean
  fields: NotifyField[]
}
export interface NotifyEvent {
  id: string
  label: string
  scope: 'household' | 'personal'
}
export interface NotifyDestination {
  id: string
  ownerId?: string
  kind: string
  name: string
  /** The kind's settings; secrets masked to their last four characters ("••••abcd"). */
  config: Record<string, string>
  events: string[]
  libraryIds: string[]
  enabled: boolean
  lastSentAt?: string
  lastError?: string
  lastErrorAt?: string
  createdAt?: string
  updatedAt?: string
}
/** On create kind, name and config are needed; kind cannot change. A secret
    left blank on an update keeps its saved value. */
export interface NotifyDestinationRequest {
  kind?: string
  name?: string
  config?: Record<string, string>
  events?: string[]
  enabled?: boolean
}
export interface MyNotifications {
  destinations: NotifyDestination[]
  events: NotifyEvent[]
  kinds: NotifyKind[]
}

export const myNotifications = () => get<MyNotifications>('/auth/me/notifications')
export const createMyDestination = (d: NotifyDestinationRequest) => post<NotifyDestination>('/auth/me/notifications', d)
export const updateMyDestination = (id: string, change: NotifyDestinationRequest) =>
  send<NotifyDestination>('PATCH', `/auth/me/notifications/${encodeURIComponent(id)}`, change)
export const deleteMyDestination = (id: string) =>
  req<void>(`/auth/me/notifications/${encodeURIComponent(id)}`, { method: 'DELETE' })
/** Sends a test message now; throws with the reason when it was not delivered. */
export const testMyDestination = (id: string) => post<void>(`/auth/me/notifications/${encodeURIComponent(id)}/test`)
