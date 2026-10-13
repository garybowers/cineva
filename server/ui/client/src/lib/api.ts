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

export const authStatus = () => get<{ setupRequired: boolean; loginMode: 'picker' | 'form'; user?: User }>('/auth/status')
export const login = (username: string, password: string) => post<User>('/auth/login', { username, password })
export const setupAdmin = (username: string, password: string, displayName: string) =>
  post<User>('/auth/setup', { username, password, displayName })
export const logout = () => post<void>('/auth/logout')
export const authProfiles = () => get<Profile[]>('/auth/profiles')
export const pinLogin = (userId: string, pin: string) => post<User>('/auth/pin', { userId, pin })
export const setPIN = (password: string, pin: string) => post<void>('/auth/pin/set', { password, pin })
export const changePassword = (current: string, next: string) => post<void>('/auth/password', { current, new: next })
export const listAPIKeys = () => get<APIKey[]>('/auth/keys')
export const createAPIKey = (name: string) => post<APIKey & { key: string }>('/auth/keys', { name })
export const deleteAPIKey = (id: string) => req<void>(`/auth/keys/${id}`, { method: 'DELETE' })

export const uploadAvatar = (file: File) => {
  const form = new FormData()
  form.append('image', file)
  return req<User>('/auth/avatar', { method: 'POST', body: form })
}
export const removeAvatar = () => req<User>('/auth/avatar', { method: 'DELETE' })
export const setAvatarColour = (colour: AvatarColour | '') =>
  req<User>('/auth/avatar/colour', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ colour }) })

export const listDevices = () => get<Device[]>('/auth/sessions')
export const nameDevice = (name: string) =>
  req<void>('/auth/sessions/current', { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ name }) })
export const revokeDevice = (id: string) => req<void>(`/auth/sessions/${id}`, { method: 'DELETE' })
export const revokeOtherDevices = () => post<{ revoked: number }>('/auth/sessions/revoke-others')
/** Records this device's queue so another can take it over. */
export const saveDeviceQueue = (itemIds: string[], index: number) =>
  req<void>('/auth/sessions/current/queue', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ itemIds, index }) })
export const deviceQueue = (id: string) => get<HandoffQueue>(`/auth/sessions/${id}/queue`)
export const playOnDevice = (id: string, itemIds: string[], index: number) =>
  post<void>(`/auth/sessions/${id}/play`, { itemIds, index })

const get = <T>(path: string) => req<T>(path)
const post = <T>(path: string, body?: unknown) =>
  req<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })

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
