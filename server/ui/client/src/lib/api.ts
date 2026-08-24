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

async function get<T>(path: string): Promise<T> {
  const res = await fetch(`/api/v1${path}`)
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<T>
}

export const getHealth = () => get<Health>('/health')
export const listLibraries = () => get<Library[]>('/libraries')
export const libraryItems = (id: string) => get<Item[]>(`/libraries/${id}/items`)
export const itemDetail = (id: string) => get<ItemDetail>(`/items/${id}`)
