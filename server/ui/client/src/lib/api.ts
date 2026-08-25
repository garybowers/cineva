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
  videoCodec?: string
  width?: number
  height?: number
  overview?: string
  poster?: string
}

export interface ItemDetail {
  item: Item
  children: Item[]
  playState?: { itemId: string; positionSecs: number; watched: boolean }
}

export interface SubtitleTrack {
  id: string
  language?: string
  title?: string
  codec: string
  forced?: boolean
  url?: string // absent for image-based tracks (burn-in not yet available)
}

export interface PlayInfo {
  mode: 'direct' | 'hls'
  url: string
  sessionId?: string
  durationSecs: number
  positionSecs: number
  reason?: string
  subtitles: SubtitleTrack[]
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
export const itemSubtitles = (id: string) => get<SubtitleTrack[]>(`/items/${id}/subtitles`)
export const searchItems = (q: string) => get<Item[]>(`/search?q=${encodeURIComponent(q)}`)
export const playItem = (id: string, quality: PlayQuality = 'auto', fromStart = false) =>
  post<PlayInfo>(`/items/${id}/play`, { ...caps, quality, fromStart })
export const postProgress = (id: string, positionSecs: number, paused = false) =>
  post<void>(`/items/${id}/progress`, { positionSecs, paused })
// Tells the server this client has stopped playing the item (drops it from
// the admin Activity page straight away instead of after the idle timeout).
export const stopPlayback = (id: string) => fetch(`/api/v1/items/${id}/playback`, { method: 'DELETE' })
// Same, for page unload: a beacon survives the tab closing where fetch won't.
export const beaconStop = (id: string) => navigator.sendBeacon?.(`/api/v1/items/${id}/playback/stop`)
export const continueWatching = () => get<ResumeItem[]>('/continue-watching')
export const stopSession = (sid: string) => fetch(`/api/v1/play/${sid}`, { method: 'DELETE' })
export const downloadURL = (id: string) => `/api/v1/items/${id}/download`

export async function nextUp(id: string): Promise<Item | null> {
  const res = await fetch(`/api/v1/items/${id}/next`)
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<Item>
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
