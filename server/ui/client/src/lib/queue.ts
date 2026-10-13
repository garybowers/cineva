/**
 * The play queue, kept across reloads.
 *
 * Stored in localStorage rather than on the server, for the same reason
 * volume is (see prefs.ts): a queue is something you line up on the device
 * in front of you, not a property of the account. The phone's queue and the
 * laptop's queue being different is correct, not a bug.
 *
 * The items are stored whole rather than as ids. Restoring from ids would
 * mean either a request per entry — there is no bulk item endpoint — or
 * waiting for the library list to load, and the queue has to be on screen
 * at first paint or a reload looks like it lost everything. The cost is a
 * title that stays stale until the item is queued again, which is a far
 * smaller problem than an empty queue.
 */

import type { Item } from './api'

const KEY = 'cineva-queue'

/** A queue big enough to be worth keeping, small enough not to abuse storage. */
const MAX_ITEMS = 500

export type StoredQueue = { items: Item[]; index: number }

export function loadQueue(): StoredQueue {
  try {
    const raw = localStorage.getItem(KEY)
    if (!raw) return { items: [], index: 0 }
    const parsed = JSON.parse(raw) as Partial<StoredQueue>
    // Anything could be in storage: a half-written value, or something an
    // older build wrote. A queue that cannot be trusted is dropped rather
    // than handed to the player.
    if (!Array.isArray(parsed?.items)) return { items: [], index: 0 }
    const items = parsed.items.filter((i): i is Item => !!i && typeof i.id === 'string')
    const index = Number.isInteger(parsed.index) ? Math.max(0, Math.min(parsed.index!, items.length - 1)) : 0
    return { items, index: items.length ? index : 0 }
  } catch {
    return { items: [], index: 0 } // private browsing, disabled storage, bad JSON
  }
}

export function saveQueue(items: Item[], index: number): void {
  try {
    if (items.length === 0) {
      localStorage.removeItem(KEY)
      return
    }
    localStorage.setItem(KEY, JSON.stringify({ items: items.slice(0, MAX_ITEMS), index }))
  } catch {
    /* Storage full or unavailable: the queue still works for this session. */
  }
}
