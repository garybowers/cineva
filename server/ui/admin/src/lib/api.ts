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

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, init)
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const body = await res.json()
      if (body.message) msg = body.message
    } catch {
      /* not json */
    }
    throw new Error(msg)
  }
  return res.status === 204 ? (undefined as T) : (res.json() as Promise<T>)
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
  itemId: string
  title: string
  segmentSecs: number
  numSegments: number
}

export const listSessions = () => req<PlaySession[]>('/play/sessions')
export const stopSession = (id: string) => req<void>(`/play/${id}`, { method: 'DELETE' })
