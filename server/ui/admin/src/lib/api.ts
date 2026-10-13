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
  source: string
  paths: string[]
  itemCount: number
  createdAt: string
  /** Seen only by the users and groups in its access; otherwise by everyone. */
  restricted: boolean
  /** Whether its folders can be reached, from the server's 30-second check. */
  storage?: { available: boolean; since?: string; path?: string; error?: string }
}

export interface Scan {
  id: string
  libraryId: string
  status: 'running' | 'completed' | 'failed'
  filesSeen: number
  itemsCreated: number
  error?: string
  startedAt: string
  finishedAt?: string
}

export interface User {
  id: string
  username: string
  displayName: string
  role: 'admin' | 'user'
  disabled: boolean
  hasPassword: boolean
  hasPin: boolean
  createdAt: string
  /** Every library this user can see, computed from library access. */
  libraryIds: string[]
}
export interface UserRequest {
  username?: string
  displayName?: string
  password?: string
  role?: 'admin' | 'user'
  disabled?: boolean
  pin?: string // set, or '' to clear
}
export const UNAUTHORISED = 'cineva:unauthorised'
export const authStatus = () => req<{ setupRequired: boolean; user?: User }>('/auth/status')
export const login = (username: string, password: string) =>
  req<User>('/auth/login', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ username, password }) })
export const logout = () => req<void>('/auth/logout', { method: 'POST' })
export const listUsers = () => req<User[]>('/users')
export const createUser = (u: UserRequest) =>
  req<User>('/users', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(u) })
export const updateUser = (id: string, u: UserRequest) =>
  req<User>(`/users/${id}`, { method: 'PATCH', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(u) })
export const deleteUser = (id: string) => req<void>(`/users/${id}`, { method: 'DELETE' })

// Library access: open to everyone unless restricted to users and groups.
export interface UserGroup {
  id: string
  name: string
  memberIds: string[]
  createdAt: string
}
export interface LibraryAccess {
  restricted: boolean
  userIds: string[]
  groupIds: string[]
}
const json = (method: string, body: unknown): RequestInit => ({
  method, headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(body),
})
export const listGroups = () => req<UserGroup[]>('/groups')
export const createGroup = (name: string, memberIds: string[]) => req<UserGroup>('/groups', json('POST', { name, memberIds }))
export const updateGroup = (id: string, name: string, memberIds: string[]) =>
  req<UserGroup>(`/groups/${id}`, json('PUT', { name, memberIds }))
export const deleteGroup = (id: string) => req<void>(`/groups/${id}`, { method: 'DELETE' })

// Sonarr and Radarr. Cineva registers a webhook in each, so what they import
// is scanned in within seconds rather than at the next scheduled rescan.
export type IntegrationKind = 'sonarr' | 'radarr'
export interface Integration {
  id: string
  kind: IntegrationKind
  name: string
  url: string
  /** Cineva's address as the instance reaches it. */
  callbackUrl: string
  /** The webhook's id inside Sonarr/Radarr; 0 if not registered. */
  notificationId: number
  /** The instance's version when the connection was last saved. */
  version: string
  createdAt: string
  updatedAt: string
}
export interface IntegrationRequest {
  kind?: IntegrationKind
  name?: string
  url?: string
  /** Blank on an update keeps the current key. */
  apiKey?: string
  callbackUrl?: string
}
export const listIntegrations = () => req<Integration[]>('/integrations')
export const createIntegration = (r: IntegrationRequest) => req<Integration>('/integrations', json('POST', r))
export const updateIntegration = (id: string, r: IntegrationRequest) =>
  req<Integration>(`/integrations/${id}`, json('PATCH', r))
/** Resolves with a warning when the webhook could not be removed from the instance. */
export const deleteIntegration = (id: string) =>
  req<{ warning?: string } | undefined>(`/integrations/${id}`, { method: 'DELETE' })
export const getLibraryAccess = (id: string) => req<LibraryAccess>(`/libraries/${id}/access`)
export const setLibraryAccess = (id: string, a: LibraryAccess) => req<LibraryAccess>(`/libraries/${id}/access`, json('PUT', a))
export interface ServerSettings {
  loginMode: 'picker' | 'form'
  hwAccel: boolean
  /** Read-only: the backend detected at startup, or "none". */
  hwAccelStatus: string
  /** ISO 3166-1 country whose content rating is stored and shown. */
  certRegion: string
  /** Read-only: the regions the server offers. */
  certRegions: string[]
  /** Intro detection's read cap in MB/s; 0 is no cap. */
  detectionRateMB: number
  /** Read-only: the caps offered. */
  detectionRateOptions: number[]
}
export const getSettings = () => req<ServerSettings>('/settings')
export const putSettings = (s: Partial<ServerSettings>) =>
  req<ServerSettings>('/settings', { method: 'PUT', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify(s) })

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

export const getHealth = () => req<Health>('/health')
export const listLibraries = () => req<Library[]>('/libraries')
/** Rename a library or change which paths it covers. Media type is immutable. */
export const updateLibrary = (id: string, patch: { name?: string; paths?: string[] }) =>
  req<Library>(`/libraries/${id}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(patch),
  })

export const createLibrary = (name: string, mediaType: string, paths: string[]) =>
  req<Library>('/libraries', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, mediaType, paths }),
  })
export const deleteLibrary = (id: string) => req<void>(`/libraries/${id}`, { method: 'DELETE' })
// Incremental unless full: only files changed since the last scan are read.
export const scanLibrary = (id: string, full = false) =>
  req<Scan>(`/libraries/${id}/scan${full ? '?full=true' : ''}`, { method: 'POST' })
export const listScans = () => req<Scan[]>('/scans')

/** Background intro detection; also pushed over /events as "segments". */
export interface IntroStatus {
  enabled?: boolean
  running: boolean
  libraryId?: string
  startedAt?: string
  seasonsDone: number
  seasonsTotal: number
  episodesDone: number
  episodesTotal: number
  introsFound: number
  creditsFound: number
  unreadable: number
  /** Waiting: "manual" (paused here), or giving way to "playing" / "scanning". */
  paused?: boolean
  pauseReason?: 'manual' | 'playing' | 'scanning'
  current?: { series: string; season: number; kind: 'intro' | 'credits'; phase: 'reading' | 'comparing' | 'saving'; done: number; total: number }
  recent?: IntroResult[]
  last?: {
    libraryId: string
    finishedAt: string
    seconds: number
    seasons: number
    episodes: number
    introsFound: number
    creditsFound: number
    unreadable: number
    cancelled?: boolean
  }
}
export interface IntroResult {
  itemId: string
  kind: 'intro' | 'credits'
  series: string
  season: number
  episode: number
  found: boolean
  startSecs?: number
  endSecs?: number
}
export const segmentStatus = () => req<IntroStatus>('/segments/status')
export const pauseSegments = () => req<IntroStatus>('/segments/pause', { method: 'POST' })
export const resumeSegments = () => req<IntroStatus>('/segments/resume', { method: 'POST' })
export const cancelScan = (id: string) => req<void>(`/scans/${id}/cancel`, { method: 'POST' })

export interface PlaySession {
  id: string
  userId: string
  itemId: string
  title: string
  subtitle?: string
  kind: string
  mode: 'direct' | 'hls'
  quality?: string
  sessionId?: string
  client: { ip: string; browser: string; os: string; userAgent: string }
  positionSecs: number
  durationSecs: number
  paused: boolean
  startedAt: string
  lastSeenAt: string
  transcode?: { id: string; segmentSecs: number; numSegments: number; quality: { label: string } }
}

// ---- Library contents and metadata management ----
export interface Item {
  id: string
  libraryId: string
  kind: string
  title: string
  year?: number
  path: string
  sizeBytes?: number
  width?: number
  height?: number
  videoCodec?: string
  tmdbId?: number
  overview?: string
  poster?: string
  versionOf?: string
  tagline?: string
  backdrop?: string
  rating?: number
  certification?: string
  collectionName?: string
  genres?: string[]
  /** Fields an operator has edited; automatic updates leave these alone. */
  lockedFields?: string[]
}

export interface MatchCandidate {
  tmdbId: number
  title: string
  year?: number
  overview?: string
  poster?: string
}

export const libraryItems = (libraryId: string) => req<Item[]>(`/libraries/${libraryId}/items`)

/** Candidate matches for an item; q and year default to the item's own. */
export const itemMatches = (id: string, q?: string, year?: number) => {
  const p = new URLSearchParams()
  if (q) p.set('q', q)
  if (year !== undefined) p.set('year', String(year))
  const qs = p.toString()
  return req<{ query: string; year: number; results: MatchCandidate[] }>(
    `/items/${id}/matches${qs ? `?${qs}` : ''}`,
  )
}

export const applyMatch = (id: string, tmdbId: number) =>
  req<Item>(`/items/${id}/match`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tmdbId }),
  })

export const refreshItem = (id: string) => req<Item>(`/items/${id}/refresh`, { method: 'POST' })

export interface RefreshProgress {
  libraryId: string
  done: number
  total: number
  matched: number
  failed: number
  running: boolean
}

/** itemIds limits the run to those items; omit for the whole library. */
export const refreshLibrary = (id: string, itemIds?: string[]) =>
  req<RefreshProgress>(`/libraries/${id}/refresh`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ itemIds: itemIds ?? [] }),
  })
export const cancelRefresh = (id: string) =>
  req<void>(`/libraries/${id}/refresh`, { method: 'DELETE' })
export const refreshStatus = (id: string) => req<RefreshProgress>(`/libraries/${id}/refresh`)

export const listSessions = () => req<PlaySession[]>('/play/sessions')
export const stopSession = (id: string) => req<void>(`/play/${id}`, { method: 'DELETE' })

// Ends a playback whether or not it is transcoding. A direct play has no
// session to kill — the server is only answering range requests — so it is
// dropped from the activity list and the client is told to stop on its next
// progress heartbeat, within about 15 seconds.
export const stopPlayback = (userId: string, itemId: string) =>
  req<void>(`/playbacks/${userId}/${itemId}`, { method: 'DELETE' })

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

// Folder picker: the server lists subdirectories of an absolute path
// (directories only) for the add-library form.
export interface DirListing {
  path: string
  parent?: string
  dirs: { name: string; path: string }[]
}
export const browseDirs = (path: string, hidden = false) =>
  req<DirListing>(`/fs/dirs?path=${encodeURIComponent(path)}${hidden ? '&hidden=1' : ''}`)

// Admin dashboard stats.
export interface SystemSample {
  at: string
  cpuPct: number
  memUsed: number
  memTotal: number
  procRss: number
  numCpu: number
}
export interface PlayDay {
  date: string
  movies: number
  episodes: number
  tracks: number
}
export const systemStats = (range: '1h' | '24h') => req<SystemSample[]>(`/stats/system?range=${range}`)
export const playStats = (days: number) => req<PlayDay[]>(`/stats/plays?days=${days}`)

// Watch totals over the same window as playStats. Separate endpoint because
// /stats/plays returns a bare array and its shape is fixed for this version.
export type PlayTotals = { plays: number; completed: number; secs: number }
export const playTotals = (days: number) => req<PlayTotals>(`/stats/plays/totals?days=${days}`)

// Storage and audit trail (admin dashboard / Logs page).
export interface DiskVolume {
  paths: string[]
  libraries: string[]
  totalBytes: number
  freeBytes: number
  usedBytes: number
  error?: string
}
export interface AuditEntry {
  id: number
  at: string
  userId: string
  action: string
  targetKind?: string
  targetId?: string
  targetTitle?: string
  detail?: string
  clientIp?: string
  client?: string
}
export const diskStats = () => req<DiskVolume[]>('/stats/disks')
export const auditLog = (limit = 100, offset = 0, action = '') =>
  req<AuditEntry[]>(`/logs?limit=${limit}&offset=${offset}&action=${encodeURIComponent(action)}`)

// ---- Collections -----------------------------------------------------
/**
 * A set of films presented as one. `key` is "tmdb:<id>" while the provider's
 * grouping is in use, or a row id once an operator has edited it — editing
 * any provider collection adopts it, copying the current membership and
 * order in first.
 */
export interface Collection {
  key: string
  tmdbId?: number
  name: string
  count: number
  poster?: string
  /** True once edited: membership and order then come from the database. */
  managed: boolean
  watched: number
  nextUpId?: string
  nextUpName?: string
}
export interface CollectionDetail {
  collection: Collection
  items: Item[]
}
export const libraryCollections = (libraryId: string) =>
  req<Collection[]>(`/libraries/${libraryId}/collections`)
export const collectionDetail = (libraryId: string, key: string) =>
  req<CollectionDetail>(`/libraries/${libraryId}/collections/${encodeURIComponent(key)}`)
export const createCollection = (libraryId: string, name: string, itemIds: string[] = []) =>
  req<Collection>(`/libraries/${libraryId}/collections`, {
    method: 'POST', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, itemIds }),
  })
export const renameCollection = (libraryId: string, key: string, name: string) =>
  req<CollectionDetail>(`/libraries/${libraryId}/collections/${encodeURIComponent(key)}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  })
/** The list sent is the membership *and* the order — add, remove and reorder in one call. */
export const setCollectionItems = (libraryId: string, key: string, itemIds: string[]) =>
  req<CollectionDetail>(`/libraries/${libraryId}/collections/${encodeURIComponent(key)}/items`, {
    method: 'PUT', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ itemIds }),
  })
/** Deleting an override restores the provider's own grouping. */
export const deleteCollection = (libraryId: string, key: string) =>
  req<void>(`/libraries/${libraryId}/collections/${encodeURIComponent(key)}`, { method: 'DELETE' })

// ---- Manual metadata edits -------------------------------------------
/** Only the fields sent are changed, and each one sent is locked against automatic updates. */
export interface ItemEdit {
  title?: string
  year?: number
  overview?: string
  tagline?: string
  certification?: string
  rating?: number
  poster?: string
  backdrop?: string
  genres?: string[]
}
export const updateItem = (id: string, edit: ItemEdit) =>
  req<Item>(`/items/${id}`, {
    method: 'PATCH', headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(edit),
  })
/** Hand the item back to the provider; values stay until the next refresh. */
export const clearItemLocks = (id: string) => req<void>(`/items/${id}/locks`, { method: 'DELETE' })
