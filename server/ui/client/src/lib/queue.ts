/**
 * The play queue as it is kept: one record for this device, whichever kind
 * it holds — a run of films and episodes, or a run of tracks — mirrored in
 * localStorage for first paint and saved to the server (the session's
 * queue, server/docs/music.md part 2 and docs/devices.md) so another of the
 * user's devices can take it over and a reload carries on where it was.
 *
 * The items are stored whole in the mirror rather than as ids: restoring
 * from ids would mean waiting for a request, and the queue has to be on
 * screen at first paint or a reload looks like it lost everything. The
 * server keeps ids, with each entry's place in the unshuffled order,
 * shuffle and repeat, where in the current entry, and what the queue was
 * made from.
 */

import { saveDeviceQueue, type HandoffQueue, type Item, type QueueMix, type QueueRecord, type QueueSource } from './api'

const KEY = 'cineva-queue'

/** A queue big enough to be worth keeping, small enough not to abuse storage. */
export const MAX_ITEMS = 1000

export type QueueKind = 'video' | 'music'
export type Repeat = 'off' | 'all' | 'one'

export interface StoredQueue {
  kind: QueueKind
  /** In play order. */
  items: Item[]
  /** Each item's place in the unshuffled order, parallel to items. */
  ords: number[]
  index: number
  shuffle: boolean
  repeat: Repeat
  positionSecs: number
  source: QueueSource | null
  /** The queue is, or ends in, a mix (server/docs/mix.md). */
  mix: QueueMix | null
}

export const emptyQueue = (kind: QueueKind = 'video'): StoredQueue =>
  ({ kind, items: [], ords: [], index: 0, shuffle: false, repeat: 'off', positionSecs: 0, source: null, mix: null })

const mixOf = (m: unknown): QueueMix | null =>
  m && typeof m === 'object' && typeof (m as QueueMix).seedId === 'string' ? (m as QueueMix) : null

const kindOf = (items: Item[]): QueueKind => (items[0]?.kind === 'track' ? 'music' : 'video')

export function loadQueue(): StoredQueue {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return emptyQueue()
    const parsed = JSON.parse(raw) as Partial<StoredQueue>
    // Anything could be in storage: a half-written value, or something an
    // older build wrote ({items, index} only). A queue that cannot be
    // trusted is dropped rather than handed to the player.
    if (!Array.isArray(parsed?.items)) return emptyQueue()
    const items = parsed.items.filter((i): i is Item => !!i && typeof i.id === 'string')
    if (items.length === 0) return emptyQueue()
    const index = Number.isInteger(parsed.index) ? Math.max(0, Math.min(parsed.index!, items.length - 1)) : 0
    const ords = Array.isArray(parsed.ords) && parsed.ords.length === items.length
      ? parsed.ords.map((o) => Number(o) || 0)
      : items.map((_, i) => i)
    return {
      kind: parsed.kind === 'music' || parsed.kind === 'video' ? parsed.kind : kindOf(items),
      items, ords, index,
      shuffle: !!parsed.shuffle,
      repeat: parsed.repeat === 'all' || parsed.repeat === 'one' ? parsed.repeat : 'off',
      positionSecs: Number(parsed.positionSecs) || 0,
      source: parsed.source && typeof parsed.source === 'object' && typeof parsed.source.kind === 'string' ? parsed.source : null,
      mix: mixOf(parsed.mix),
    }
  } catch {
    return emptyQueue() // private browsing, disabled storage, bad JSON
  }
}

/** The server's shape of a queue. */
export function recordOf(q: StoredQueue): QueueRecord {
  return {
    entries: q.items.slice(0, MAX_ITEMS).map((it, i) => ({ id: it.id, ord: q.ords[i] ?? i })),
    index: q.index,
    shuffle: q.shuffle,
    repeat: q.repeat,
    positionSecs: q.positionSecs,
    source: q.source,
    mix: q.mix,
  }
}

/** A queue arriving from another device, or this one's own saved copy. */
export function fromHandoff(h: HandoffQueue): StoredQueue {
  const items = h.items ?? []
  return {
    kind: kindOf(items),
    items,
    ords: items.map((_, i) => i),
    index: Math.max(0, Math.min(h.index ?? 0, Math.max(0, items.length - 1))),
    shuffle: !!h.shuffle,
    repeat: h.repeat === 'all' || h.repeat === 'one' ? h.repeat : 'off',
    positionSecs: h.positionSecs ?? 0,
    source: h.source ?? null,
    mix: mixOf(h.mix),
  }
}

let remote = false
/** Whether saves reach the server: only once signed in. */
export function setQueueRemote(on: boolean) {
  remote = on
}

let timer: ReturnType<typeof setTimeout> | undefined

/**
 * Keep the queue: the mirror at once, the server a second later (a run of
 * changes is one request). An empty queue clears both.
 */
export function saveQueue(q: StoredQueue): void {
  try {
    if (q.items.length === 0) localStorage.removeItem(KEY)
    else localStorage.setItem(KEY, JSON.stringify({ ...q, items: q.items.slice(0, MAX_ITEMS) }))
  } catch {
    /* Storage full or unavailable: the queue still works for this session. */
  }
  if (!remote) return
  clearTimeout(timer)
  // Checked again when it fires: a sign-out in the meantime must not send
  // the request and be answered with a sign-in prompt.
  timer = setTimeout(() => { if (remote) saveDeviceQueue(recordOf(q)).catch(() => {}) }, 1000)
}

/** Note where the current entry has got to, in the mirror only: the
    playback heartbeat tells the server. */
export function noteQueuePosition(secs: number): void {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return
    const parsed = JSON.parse(raw) as StoredQueue
    parsed.positionSecs = secs
    localStorage.setItem(KEY, JSON.stringify(parsed))
  } catch {
    /* as above */
  }
}
