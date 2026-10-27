/**
 * A paged listing as a grid scrolls: the first page replaces what was
 * there, the next pages append, and a page that comes back for a request
 * that has been superseded (the sort changed meanwhile) is dropped.
 */

import type { Item, MusicPage } from '../../lib/api'

export const PAGE_SIZE = 60

export function createPager() {
  let items = $state<Item[]>([])
  let total = $state(0)
  let index = $state<Record<string, number> | undefined>(undefined)
  let loading = $state(false)
  let error = $state('')
  let page = 0
  let gen = 0
  let fetcher: ((page: number) => Promise<MusicPage>) | null = null

  async function fetchPage(p: number, g: number) {
    if (!fetcher) return
    loading = true
    try {
      const res = await fetcher(p)
      if (g !== gen) return
      items = p === 1 ? res.items : [...items, ...res.items]
      total = res.total
      if (res.index) index = res.index
      else if (p === 1) index = undefined
      page = p
      error = ''
    } catch (err) {
      if (g === gen) error = err instanceof Error ? err.message : String(err)
    } finally {
      if (g === gen) loading = false
    }
  }

  return {
    get items() { return items },
    get total() { return total },
    get index() { return index },
    get loading() { return loading },
    get error() { return error },
    get done() { return items.length >= total },
    /** A new listing: the first page replaces everything. */
    start(f: (page: number) => Promise<MusicPage>) {
      fetcher = f
      gen++
      page = 0
      index = undefined
      void fetchPage(1, gen)
    },
    /** The next page, if there is one and none is on its way. */
    more() {
      if (loading || !fetcher || items.length >= total) return
      void fetchPage(page + 1, gen)
    },
    /** Fetch the listing again from the top, keeping what shows until it lands. */
    refresh() {
      if (!fetcher) return
      gen++
      void fetchPage(1, gen)
    },
    /** Something on screen changed (a rating): patch it in place. */
    patch(f: (it: Item) => Item) {
      items = items.map(f)
    },
  }
}

/** An element near the end of a grid: when it comes into view, ask for more. */
export function nearEnd(el: HTMLElement, more: () => void) {
  const io = new IntersectionObserver((entries) => {
    if (entries.some((e) => e.isIntersecting)) more()
  }, { rootMargin: '800px' })
  io.observe(el)
  return {
    update(next: () => void) { more = next },
    destroy() { io.disconnect() },
  }
}
