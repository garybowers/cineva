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
  libraryIds: string[]
}
export interface UserRequest {
  username?: string
  displayName?: string
  password?: string
  role?: 'admin' | 'user'
  disabled?: boolean
  libraryIds?: string[]
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
export interface ServerSettings {
  loginMode: 'picker' | 'form'
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
export const createLibrary = (name: string, mediaType: string, paths: string[]) =>
  req<Library>('/libraries', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name, mediaType, paths }),
  })
export const deleteLibrary = (id: string) => req<void>(`/libraries/${id}`, { method: 'DELETE' })
export const scanLibrary = (id: string) => req<Scan>(`/libraries/${id}/scan`, { method: 'POST' })
export const listScans = () => req<Scan[]>('/scans')
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

export const listSessions = () => req<PlaySession[]>('/play/sessions')
export const stopSession = (id: string) => req<void>(`/play/${id}`, { method: 'DELETE' })

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
