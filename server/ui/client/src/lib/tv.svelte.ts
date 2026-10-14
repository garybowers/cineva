// TV mode: the same client, shown for a screen across the room and worked
// with a remote (see docs/tv.md). The device's "interface" preference
// chooses it: automatic (a television's own browser), desktop or TV. In
// TV mode <html> carries the class "tv", the sizes grow, and the arrow
// keys move focus between whatever is on screen in the direction pressed:
// spatial navigation, which a remote's D-pad needs and a page's tab order
// cannot give.

import { onPrefs, pref } from './prefs'

export const tv = $state<{
  on: boolean
  /** The title the focus is on, for the Home banner to show. */
  focused: unknown | null
}>({ on: false, focused: null })

/** Browsers that are a television's own: Fire TV, Android TV and Google
    TV, LG webOS, Samsung Tizen, Sony Bravia, and the generic "SmartTV".
    The admin's main.ts keeps the same list, to stay off TVs. */
const TV_BROWSER = /AFT[A-Z]|Android TV|GoogleTV|BRAVIA|Web0S|webOS|Tizen|SMART-TV|SmartTV|HbbTV|CrKey|AppleTV/i

export function isTVBrowser(ua = navigator.userAgent): boolean {
  return TV_BROWSER.test(ua)
}

/** Switch to the interface the preference asks for. */
export function applyInterface(value = pref('interface', 'auto')) {
  const on = value === 'tv' || (value !== 'desktop' && isTVBrowser())
  tv.on = on
  document.documentElement.classList.toggle('tv', on)
  setScale()
}

/** How much larger TV mode draws than desktop: its root size over 16px.
    Icons are drawn at fixed pixel sizes, so they are zoomed by it to stay
    in proportion with the text around them. */
function setScale() {
  const px = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16
  document.documentElement.style.setProperty('--tv-scale', String(px / 16))
}

// ---- Spatial navigation ----

type Dir = 'up' | 'down' | 'left' | 'right'

const FOCUSABLE = 'button, a[href], input, select, textarea, [tabindex]'

/**
 * A screen's own keys come first: the player wants the arrows to seek while
 * its controls are hidden. Set while it is showing; return true for a key
 * it dealt with.
 */
let owner: ((e: KeyboardEvent) => boolean) | null = null
export function ownKeys(fn: ((e: KeyboardEvent) => boolean) | null) {
  owner = fn
}

/** Where focus may go: the topmost open dialogue, else the page. The full
    player counts as one (data-nav-scope): the page it covers is still laid
    out underneath, and the arrows must not wander off onto it. */
function scope(): ParentNode {
  const dialogs = [...document.querySelectorAll<HTMLElement>('[role="dialog"], [aria-modal="true"], dialog[open], [data-nav-scope]')]
    .filter(visible)
  return dialogs.at(-1) ?? document
}

function visible(el: HTMLElement): boolean {
  if (el.closest('[inert], [hidden], [aria-hidden="true"]')) return false
  const r = el.getBoundingClientRect()
  if (r.width === 0 && r.height === 0) return false
  const st = getComputedStyle(el)
  return st.visibility !== 'hidden' && st.display !== 'none' && Number(st.opacity) > 0.05
}

function candidates(root: ParentNode): HTMLElement[] {
  return [...root.querySelectorAll<HTMLElement>(FOCUSABLE)].filter((el) =>
    el.tabIndex >= 0 && !(el as HTMLButtonElement).disabled && visible(el))
}

/** The best element to go to from rect in a direction: nearest along the
    direction, strongly preferring ones in line with it (the same row when
    going sideways, the same column going up or down). */
function best(from: DOMRect, dir: Dir, els: HTMLElement[]): HTMLElement | null {
  let pick: HTMLElement | null = null
  let score = Infinity
  for (const el of els) {
    const r = el.getBoundingClientRect()
    let main: number, gap: number
    switch (dir) {
      case 'right': main = r.left - from.right; gap = spanGap(from.top, from.bottom, r.top, r.bottom); break
      case 'left': main = from.left - r.right; gap = spanGap(from.top, from.bottom, r.top, r.bottom); break
      case 'down': main = r.top - from.bottom; gap = spanGap(from.left, from.right, r.left, r.right); break
      case 'up': main = from.top - r.bottom; gap = spanGap(from.left, from.right, r.left, r.right); break
    }
    if (main < -2) continue // not in that direction
    const s = Math.max(main, 0) + gap * 3
    if (s < score) {
      score = s
      pick = el
    }
  }
  return pick
}

/** How far apart two spans are along one axis; 0 when they overlap. */
function spanGap(a1: number, a2: number, b1: number, b2: number): number {
  if (b2 < a1) return a1 - b2
  if (b1 > a2) return b1 - a2
  return 0
}

/** A row remembers where in it focus last was, so going down and back up
    returns to the same poster rather than the nearest one. */
const remembered = new WeakMap<Element, HTMLElement>()
const groupOf = (el: Element) => el.closest('[data-nav-group]')

/** The title last focused on each page, by address, so Back lands on the
    poster it left from. */
const lastOnPage = new Map<string, string>()

export function focusEl(el: HTMLElement) {
  const id = el.closest<HTMLElement>('[data-nav-id]')?.dataset.navId
  if (id) lastOnPage.set(location.pathname, id)
  el.focus({ preventScroll: true })
  // "nearest" with the scroll margins TV mode sets keeps the focus clear of
  // the edges and of the Home banner, without jumping when it is already in
  // view.
  el.scrollIntoView({ block: 'nearest', inline: 'nearest', behavior: 'smooth' })
  const g = groupOf(el)
  if (g) remembered.set(g, el)
}

export function move(dir: Dir): boolean {
  const root = scope()
  const els = candidates(root)
  if (els.length === 0) return false
  const cur = document.activeElement as HTMLElement | null
  if (!cur || cur === document.body || !els.includes(cur)) {
    focusEl(primary(els))
    return true
  }
  const from = cur.getBoundingClientRect()
  let next = best(from, dir, els.filter((el) => el !== cur))
  if (!next) return false
  // Entering another group: a row going up or down comes back to where it
  // was left; a menu going sideways (the rail) opens on the page it is on.
  const g = groupOf(next)
  if (g && g !== groupOf(cur)) {
    const here = g.querySelector<HTMLElement>('[aria-current="page"], .active')
    const back = remembered.get(g)
    const want = dir === 'left' || dir === 'right' ? (here ?? back) : back
    if (want && els.includes(want)) next = want
  }
  focusEl(next)
  return true
}

function inView(el: HTMLElement): boolean {
  const r = el.getBoundingClientRect()
  return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth
}

/** Where focus starts on a page: the title it was left from, if coming
    back; else the page's main action (Play); else the first poster in
    view; else the first thing in the page. */
function primary(els: HTMLElement[]): HTMLElement {
  const back = lastOnPage.get(location.pathname)
  if (back) {
    const el = els.find((e) => e.closest<HTMLElement>('[data-nav-id]')?.dataset.navId === back)
    if (el) return el
  }
  const main = els.filter((el) => el.closest('main'))
  const order = (list: HTMLElement[]) => list.filter(inView).sort((a, b) => {
    const ra = a.getBoundingClientRect(), rb = b.getBoundingClientRect()
    return ra.top - rb.top || ra.left - rb.left
  })
  // The page's main action wins even scrolled out of view: focusing it
  // brings it back.
  return main.find((el) => el.matches('.actions .play')) ??
    order(main.filter((el) => el.matches('.cell')))[0] ??
    order(main)[0] ?? main[0] ?? els[0]
}

/** After the page changes, put the focus somewhere: a remote has no other
    way to find it. Left alone when something on the page already has it. */
function settle() {
  setTimeout(() => {
    if (!tv.on) return
    const cur = document.activeElement as HTMLElement | null
    if (cur && cur !== document.body && cur.isConnected && visible(cur)) return
    const els = candidates(scope())
    if (!els.length) return
    const el = primary(els)
    // A page opened on its main action opens at its top, title and all.
    if (el.matches('.actions .play') && scope() === document) {
      window.scrollTo({ top: 0 })
      el.focus({ preventScroll: true })
      return
    }
    focusEl(el)
  }, 400)
}

const ARROWS: Record<string, Dir> = { ArrowUp: 'up', ArrowDown: 'down', ArrowLeft: 'left', ArrowRight: 'right' }
const BACK = new Set(['Backspace', 'GoBack', 'BrowserBack'])

function typing(el: Element | null): boolean {
  if (!el) return false
  if (el instanceof HTMLTextAreaElement) return true
  if (el instanceof HTMLInputElement) return !['button', 'checkbox', 'radio', 'range', 'submit'].includes(el.type)
  return (el as HTMLElement).isContentEditable
}

function onKey(e: KeyboardEvent) {
  if (!tv.on || e.defaultPrevented || e.altKey || e.ctrlKey || e.metaKey) return
  if (owner?.(e)) {
    // Dealt with: nothing else (the page's own key handlers) acts on it.
    e.preventDefault()
    e.stopImmediatePropagation()
    return
  }
  const active = document.activeElement
  const dir = ARROWS[e.key]
  if (dir) {
    // In a text field the sideways arrows move the caret; up and down
    // leave it. A range input keeps its own arrows.
    if (typing(active) && (dir === 'left' || dir === 'right')) return
    if (active instanceof HTMLInputElement && active.type === 'range' && (dir === 'left' || dir === 'right')) return
    if (active instanceof HTMLSelectElement) return
    if (move(dir)) e.preventDefault()
    return
  }
  if (BACK.has(e.key) && !typing(active)) {
    e.preventDefault()
    back()
  }
}

/** Back: close the dialogue on top, else go back a page. */
export function back() {
  if (scope() !== document) {
    // Dialogues close on Escape; send them one. It bubbles from the
    // focused element up to the window, where most of them listen.
    const target = (document.activeElement as HTMLElement | null) ?? document.body
    target.dispatchEvent(new KeyboardEvent('keydown', { key: 'Escape', bubbles: true }))
    return
  }
  history.back()
}

let installed = false
/** Start listening, and follow the preference as it changes (in
    Settings here, or for this device from another); harmless to call
    again. */
export function installSpatial() {
  if (installed) return
  installed = true
  window.addEventListener('keydown', onKey, true)
  window.addEventListener('resize', setScale)
  onPrefs(() => applyInterface())
  // Page changes: the client's own (pushState) and Back (popstate).
  window.addEventListener('popstate', settle)
  const push = history.pushState.bind(history)
  history.pushState = (...args: Parameters<History['pushState']>) => {
    push(...args)
    settle()
  }
}
