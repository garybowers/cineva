/**
 * The music queue: what is playing and what follows, where in it, shuffle
 * and repeat, and what it was made from. One store, read by the engine
 * (MusicPlayer), the mini bar, the now-playing screen and every Play, Play
 * next and Add to queue button; kept through lib/queue.ts, so a reload
 * carries on and another device can take it over.
 *
 * The engine owns sound; this owns order. The engine moves to the next
 * entry by calling advanced(); everything else changes the store and, when
 * playback must (re)start, bumps gen, which the engine watches.
 */

import { itemMix, type Item, type MixResult, type QueueMix, type QueueSource } from '../../lib/api'
import { pref } from '../../lib/prefs'
import { emptyQueue, loadQueue, noteQueuePosition, saveQueue, type Repeat, type StoredQueue } from '../../lib/queue'

/** What the engine lets the screens do. */
export interface Controls {
  toggle(): void
  next(): void
  prev(): void
  seek(secs: number): void
  setVolume(v: number): void
}

export const mq = $state({
  /** In play order. */
  items: [] as Item[],
  /** Each item's place in the unshuffled order, parallel to items. */
  ords: [] as number[],
  index: 0,
  shuffle: false,
  repeat: 'off' as Repeat,
  source: null as QueueSource | null,
  /** The queue is, or ends in, a mix: what it grew from and where it has
      got to (server/docs/mix.md). Entries from mix.from on are the mix's. */
  mix: null as QueueMix | null,
  /** A batch of the mix is being fetched. */
  mixLoading: false,
  /** A queue exists: the engine is mounted and the bar shows. */
  active: false,
  /** Bumped when the engine must start the entry at index, from offset. */
  gen: 0,
  offset: 0,
  /** False when restored after a reload: the engine loads but stays paused. */
  autoplay: true,
  /** Bumped when the order changed under the engine, so its pre-scheduled
      next track is checked again. */
  orderGen: 0,
  // Mirrored by the engine for the screens.
  paused: true,
  loading: false,
  positionSecs: 0,
  duration: 0,
  /** What is playing, for the badge: "FLAC 24/96", "Opus 128k". */
  quality: '',
  /** 0 to 1; this device's, kept by the engine. */
  volume: 1,
  controls: null as Controls | null,
})

export const current = (): Item | undefined => mq.items[mq.index]
export const upNext = (): Item[] => mq.items.slice(mq.index + 1)

export function snapshot(): StoredQueue {
  return {
    kind: 'music', items: mq.items, ords: mq.ords, index: mq.index,
    shuffle: mq.shuffle, repeat: mq.repeat, positionSecs: mq.positionSecs, source: mq.source, mix: mq.mix,
  }
}

function persist() {
  saveQueue(mq.active ? snapshot() : emptyQueue('music'))
}

function shuffled<T>(list: T[]): T[] {
  const out = [...list]
  for (let i = out.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[out[i], out[j]] = [out[j], out[i]]
  }
  return out
}

/** Items and ords move together. */
function pairs(): { it: Item; ord: number }[] {
  return mq.items.map((it, i) => ({ it, ord: mq.ords[i] ?? i }))
}
function setPairs(list: { it: Item; ord: number }[]) {
  mq.items = list.map((p) => p.it)
  mq.ords = list.map((p) => p.ord)
}

/**
 * Replace the queue and play. With shuffle, the list is shuffled and
 * begins anywhere, unless startHere keeps the entry at index first (a
 * track chosen while shuffle is on).
 */
export function play(items: Item[], index = 0, opts: { shuffle?: boolean; startHere?: boolean; source?: QueueSource | null; offset?: number; autoplay?: boolean; repeat?: Repeat; mix?: QueueMix | null } = {}) {
  if (items.length === 0) return
  let list = items.map((it, i) => ({ it, ord: i }))
  let at = Math.max(0, Math.min(index, items.length - 1))
  if (opts.shuffle) {
    list = opts.startHere ? [list[at], ...shuffled(list.filter((_, i) => i !== at))] : shuffled(list)
    at = 0
  }
  setPairs(list)
  mq.index = at
  mq.shuffle = !!opts.shuffle
  mq.repeat = opts.repeat ?? 'off'
  mq.source = opts.source ?? null
  mq.mix = opts.mix ?? null
  mq.offset = opts.offset ?? 0
  mq.autoplay = opts.autoplay ?? true
  mq.positionSecs = mq.offset
  mq.active = true
  mq.gen++
  persist()
  void maybeExtend()
}

/** Put these after the current entry; starts them if nothing is queued. */
export function playNext(items: Item[]) {
  if (items.length === 0) return
  if (!mq.active) return play(items, 0)
  const list = pairs()
  const top = Math.max(...list.map((p) => p.ord), -1) + 1
  const add = items.map((it, i) => ({ it, ord: top + i }))
  setPairs([...list.slice(0, mq.index + 1), ...add, ...list.slice(mq.index + 1)])
  mq.orderGen++
  persist()
}

/** Add to the end; starts them if nothing is queued. */
export function append(items: Item[]) {
  if (items.length === 0) return
  if (!mq.active) return play(items, 0)
  const list = pairs()
  const top = Math.max(...list.map((p) => p.ord), -1) + 1
  setPairs([...list, ...items.map((it, i) => ({ it, ord: top + i }))])
  mq.orderGen++
  persist()
}

/** Play the entry at i now. */
export function jump(i: number) {
  if (i < 0 || i >= mq.items.length) return
  mq.index = i
  mq.offset = 0
  mq.autoplay = true
  mq.gen++
  persist()
  void maybeExtend()
}

/** The engine moved to i by itself (the previous entry ended). */
export function advanced(i: number) {
  mq.index = Math.max(0, Math.min(i, mq.items.length - 1))
  mq.positionSecs = 0
  persist()
  void maybeExtend()
}

export function remove(i: number) {
  if (i < 0 || i >= mq.items.length) return
  const list = pairs()
  list.splice(i, 1)
  if (list.length === 0) return clear()
  setPairs(list)
  if (mq.mix && i < (mq.mix.from ?? 0)) mq.mix = { ...mq.mix, from: (mq.mix.from ?? 0) - 1 }
  if (i < mq.index) mq.index--
  else if (i === mq.index) {
    // The playing entry went: what followed it plays.
    mq.index = Math.min(mq.index, list.length - 1)
    mq.offset = 0
    mq.gen++
  }
  mq.orderGen++
  persist()
}

/** Move the entry at from to sit at to. Whatever is playing keeps playing. */
export function move(from: number, to: number) {
  if (from === to || from < 0 || to < 0 || from >= mq.items.length || to >= mq.items.length) return
  const list = pairs()
  const [p] = list.splice(from, 1)
  list.splice(to, 0, p)
  setPairs(list)
  if (from === mq.index) mq.index = to
  else if (from < mq.index && to >= mq.index) mq.index--
  else if (from > mq.index && to <= mq.index) mq.index++
  mq.orderGen++
  persist()
}

/** Shuffle what has not played yet, or put the queue back in order. */
export function setShuffle(on: boolean) {
  if (on === mq.shuffle) return
  const list = pairs()
  const cur = list[mq.index]
  if (on) {
    setPairs([...list.slice(0, mq.index + 1), ...shuffled(list.slice(mq.index + 1))])
  } else {
    const sorted = [...list].sort((a, b) => a.ord - b.ord)
    setPairs(sorted)
    mq.index = Math.max(0, sorted.indexOf(cur))
  }
  mq.shuffle = on
  mq.orderGen++
  persist()
}

export function setRepeat(r: Repeat) {
  mq.repeat = r
  mq.orderGen++
  persist()
}
export function cycleRepeat() {
  setRepeat(mq.repeat === 'off' ? 'all' : mq.repeat === 'all' ? 'one' : 'off')
}

/** Everything but the current entry goes. */
export function clearUpcoming() {
  const cur = current()
  if (!cur) return
  setPairs([{ it: cur, ord: 0 }])
  mq.index = 0
  mq.mix = null
  mq.orderGen++
  persist()
}

/** Stop and forget the queue. */
export function clear() {
  mq.items = []
  mq.ords = []
  mq.index = 0
  mq.active = false
  mq.source = null
  mq.mix = null
  mq.paused = true
  mq.positionSecs = 0
  mq.duration = 0
  persist()
}

// ---- Mixes (server/docs/mix.md) ----

/** Words for a batch's basis: "Similar artists and what you play". */
export function basisWords(basis: string[] | undefined): string {
  const words: Record<string, string> = {
    'similar-artists': 'similar artists', 'top-songs': 'best-known songs', household: 'what you play',
    'co-listening': 'what you play alongside it', genre: 'the same genre',
  }
  const parts = (basis ?? []).map((b) => words[b]).filter((w): w is string => !!w)
  if (parts.length === 0) return 'From your library'
  const text = parts.length === 1 ? parts[0] : `${parts.slice(0, -1).join(', ')} and ${parts[parts.length - 1]}`
  return text.charAt(0).toUpperCase() + text.slice(1)
}

/** The entries from the mix on, and those before it. */
export function mixFrom(): number {
  return mq.mix ? Math.max(0, Math.min(mq.mix.from ?? 0, mq.items.length)) : mq.items.length
}

/** Start a mix on purpose: the batch replaces the queue and plays. */
export function startMix(res: MixResult, seed: Item) {
  if (res.tracks.length === 0) return
  play(res.tracks, 0, {
    source: { kind: 'mix', id: seed.id, title: seed.title },
    mix: { seedId: seed.id, kind: seed.kind as QueueMix['kind'], title: seed.title, after: res.tracks[res.tracks.length - 1].id, from: 0, basis: res.basis },
  })
}

/** Drop the mix's entries still to come and forget it; what is playing
    keeps playing. */
export function stopMix() {
  if (!mq.mix) return
  const from = Math.max(mixFrom(), mq.index + 1)
  const list = pairs().slice(0, from)
  setPairs(list)
  mq.index = Math.min(mq.index, list.length - 1)
  mq.mix = null
  mq.orderGen++
  persist()
}

const EXTEND_WHEN = 2 // tracks still to come when the next batch is asked for
let extending = false
let exhaustedFor = '' // the mix seed and after that returned nothing, so it is not asked again

/**
 * Keep a mix fed, and carry an ordinary queue on when it is ending and the
 * person wants that: as the last tracks come up, the next batch is asked
 * for and added under the mix's heading, so what is coming is visible
 * before it plays. Repeat does not run on: the queue is going round.
 */
export async function maybeExtend() {
  if (!mq.active || extending || mq.repeat !== 'off') return
  const remaining = mq.items.length - mq.index - 1
  if (remaining > EXTEND_WHEN) return
  const last = mq.items[mq.items.length - 1]
  if (!last || last.kind !== 'track') return
  let seedId: string
  let seedKind: QueueMix['kind']
  let title: string | undefined
  if (mq.mix) {
    seedId = mq.mix.seedId
    seedKind = mq.mix.kind
    title = mq.mix.title
  } else {
    if (pref('music-autoplay', 'on') === 'off') return
    seedId = last.id
    seedKind = 'track'
    title = last.title
  }
  if (exhaustedFor === `${seedId}/${last.id}`) return
  extending = true
  mq.mixLoading = true
  try {
    const res = await itemMix(seedId, { after: last.id, exclude: mq.items.map((t) => t.id), size: 20 })
    if (!mq.active || mq.items[mq.items.length - 1]?.id !== last.id) return // the queue changed meanwhile
    if (res.tracks.length === 0) {
      exhaustedFor = `${seedId}/${last.id}`
      return
    }
    const from = mq.mix ? (mq.mix.from ?? 0) : mq.items.length
    const list = pairs()
    const top = Math.max(...list.map((p) => p.ord), -1) + 1
    setPairs([...list, ...res.tracks.map((it, i) => ({ it, ord: top + i }))])
    mq.mix = { seedId, kind: seedKind, title, after: res.tracks[res.tracks.length - 1].id, from, basis: res.basis }
    mq.orderGen++
    persist()
  } catch {
    exhaustedFor = `${seedId}/${last.id}` // not again until the queue moves on
  } finally {
    extending = false
    mq.mixLoading = false
  }
}

/** The position, from the engine's heartbeat: the mirror only; the
    heartbeat itself tells the server. */
export function notePosition(secs: number) {
  mq.positionSecs = secs
  noteQueuePosition(secs)
}

/** Take a queue as it stands (restored after a reload, or from another
    device), paused where it was unless told to play. */
export function adopt(q: StoredQueue, opts: { autoplay: boolean }) {
  if (q.items.length === 0) return
  mq.items = q.items
  mq.ords = q.ords.length === q.items.length ? q.ords : q.items.map((_, i) => i)
  mq.index = Math.max(0, Math.min(q.index, q.items.length - 1))
  mq.shuffle = q.shuffle
  mq.repeat = q.repeat
  mq.source = q.source
  mq.mix = q.mix
  mq.offset = q.positionSecs
  mq.positionSecs = q.positionSecs
  mq.autoplay = opts.autoplay
  mq.active = true
  mq.gen++
  persist()
  if (opts.autoplay) void maybeExtend()
}

/** After a reload: the queue the mirror holds, if it is a music one. */
export function restore(): boolean {
  const q = loadQueue()
  if (q.kind !== 'music' || q.items.length === 0) return false
  adopt(q, { autoplay: false })
  return true
}
