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
}

export interface Item {
  id: string
  libraryId: string
  kind: 'movie' | 'series' | 'season' | 'episode'
  parentId?: string
  title: string
  year?: number
  seasonNum?: number
  episodeNum?: number
  runtimeSecs?: number
  videoCodec?: string
  width?: number
  height?: number
  overview?: string
  poster?: string
}

export interface ItemDetail {
  item: Item
  children: Item[]
}

export interface PlayInfo {
  mode: 'direct' | 'hls'
  url: string
  sessionId?: string
  durationSecs: number
  positionSecs: number
}

export interface ResumeItem extends Item {
  positionSecs: number
}

async function req<T>(path: string, init?: RequestInit): Promise<T> {
  const res = await fetch(`/api/v1${path}`, init)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

const get = <T>(path: string) => req<T>(path)
const post = <T>(path: string, body?: unknown) =>
  req<T>(path, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: body === undefined ? undefined : JSON.stringify(body),
  })

export const getHealth = () => get<Health>('/health')
export const listLibraries = () => get<Library[]>('/libraries')
export const libraryItems = (id: string) => get<Item[]>(`/libraries/${id}/items`)
export const itemDetail = (id: string) => get<ItemDetail>(`/items/${id}`)
export const playItem = (id: string) => post<PlayInfo>(`/items/${id}/play`)
export const postProgress = (id: string, positionSecs: number) =>
  post<void>(`/items/${id}/progress`, { positionSecs })
export const continueWatching = () => get<ResumeItem[]>('/continue-watching')
export const stopSession = (sid: string) => fetch(`/api/v1/play/${sid}`, { method: 'DELETE' })
export const downloadURL = (id: string) => `/api/v1/items/${id}/download`

export async function nextUp(id: string): Promise<Item | null> {
  const res = await fetch(`/api/v1/items/${id}/next`)
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<Item>
}
