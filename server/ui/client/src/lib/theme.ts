// Theme loading + persistence. Built-in themes are folders served by the Go
// binary at /themes/<id>/theme.css; custom ones, made in Settings, are
// stylesheets the server writes at /api/v1/themes/<id>/theme.css. Each
// defines the full design-token set from app.css, and may add a background
// picture (--bg-image, --bg-dim). See docs/themes.md.

import { pref, setPref } from './prefs'

export interface ThemeInfo {
  id: string
  name: string
  appearance: 'dark' | 'light'
  /** The theme's design tokens by name without the dashes ("bg",
   *  "on-accent"), for drawing a preview without switching to it. */
  tokens?: Record<string, string>
  /** Its background picture's URL, and how strongly --bg covers it (%). */
  background?: string
  dim?: number
  /** The animated touch it shows. */
  effect?: Effect
  /** The holiday a built-in theme is for. */
  occasion?: string
  /** Made in Settings: by whom, shared with everyone, by the caller. */
  custom?: boolean
  ownerId?: string
  household?: boolean
  mine?: boolean
  /** The stylesheet to load. */
  css: string
}

export type Effect = '' | 'snow' | 'leaves' | 'blossom'

/** The holiday on now, and the theme for it. */
export interface Occasion {
  id: string
  name: string
  /** The last day of its window, YYYY-MM-DD. */
  until: string
  themeId: string
}

export const DEFAULT_THEME = 'midnight'
export const CUSTOM_PREFIX = 'c-'

export function currentTheme(): string {
  return pref('theme', DEFAULT_THEME)
}

/** The stylesheet for a theme known only by id. */
export function themeCSS(id: string): string {
  return id.startsWith(CUSTOM_PREFIX) ? `/api/v1/themes/${id}/theme.css` : `/themes/${id}/theme.css`
}

// What was painted last, so a page load paints the same (a holiday's theme
// included) before the theme list and the calendar have been fetched.
const PAINTED = 'cineva-painted-theme'

export function paintedTheme(): { id: string; css: string } | null {
  try {
    const v = JSON.parse(localStorage.getItem(PAINTED) ?? 'null')
    return v && typeof v.id === 'string' && typeof v.css === 'string' ? v : null
  } catch {
    return null
  }
}

export function applyTheme(id: string, css = themeCSS(id)): void {
  let link = document.getElementById('theme-css') as HTMLLinkElement | null
  if (!link) {
    link = document.createElement('link')
    link.id = 'theme-css'
    link.rel = 'stylesheet'
    document.head.appendChild(link)
  }
  if (link.getAttribute('href') !== css) link.href = css
  link.dataset.theme = id
  try {
    localStorage.setItem(PAINTED, JSON.stringify({ id, css }))
  } catch {
    /* private window: the next load paints the chosen theme first */
  }
}

/** The theme on screen now. */
export function onScreen(): string | undefined {
  return (document.getElementById('theme-css') as HTMLLinkElement | null)?.dataset.theme
}

/**
 * Remember a theme as this user's choice.
 *
 * Deliberately separate from applyTheme, which only touches the DOM.
 * Startup paints the current theme before the user's preferences have been
 * fetched; if that paint also wrote, loading the page would overwrite the
 * stored choice with whatever this browser happened to have — which is
 * exactly what it did until these were split.
 */
export function chooseTheme(id: string): void {
  setPref('theme', id)
}

/** The theme to paint: the holiday's while one is on and the user has
    holiday themes on, otherwise their own choice (or the default when that
    has gone). */
export function effectiveTheme(list: ThemeInfo[], chosen: string, occasion: Occasion | null): ThemeInfo | undefined {
  if (occasion?.themeId && pref('holiday-themes', 'on') === 'on' && pref('holiday-skip') !== holidayKey(occasion)) {
    const holiday = list.find((t) => t.id === occasion.themeId)
    if (holiday) return holiday
  }
  return list.find((t) => t.id === chosen) ?? list.find((t) => t.id === DEFAULT_THEME)
}

/** This year's run of a holiday, as holiday-skip stores it: "halloween-2026".
    Christmas's window ends in January, so its year is the one it ends in
    less one when that is January. */
export function holidayKey(o: Occasion): string {
  const [y, m] = o.until.split('-').map(Number)
  return `${o.id}-${m === 1 ? y - 1 : y}`
}

export async function listThemes(): Promise<ThemeInfo[]> {
  const res = await fetch('/api/v1/themes')
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<ThemeInfo[]>
}

export async function getOccasion(): Promise<Occasion | null> {
  const res = await fetch('/api/v1/themes/occasion')
  if (res.status === 204 || !res.ok) return null
  return res.json() as Promise<Occasion>
}

// ---- Making themes ----

/** The tokens a custom theme sets, in the order the editor offers them. */
export const TOKENS: { key: string; label: string; more?: boolean }[] = [
  { key: 'bg', label: 'Background' },
  { key: 'panel', label: 'Panels' },
  { key: 'border', label: 'Lines' },
  { key: 'text', label: 'Text' },
  { key: 'muted', label: 'Quieter text' },
  { key: 'accent', label: 'Accent' },
  { key: 'on-accent', label: 'Text on the accent' },
  { key: 'accent-2', label: 'Links and focus' },
  { key: 'ok', label: 'Good news', more: true },
  { key: 'err', label: 'Problems', more: true },
  { key: 'chart-1', label: 'Chart, first series', more: true },
  { key: 'chart-2', label: 'Chart, second series', more: true },
  { key: 'chart-3', label: 'Chart, third series', more: true },
]

export interface ThemeDraft {
  name: string
  tokens: Record<string, string>
  dim: number
  effect: Effect
  household: boolean
}

async function send(method: string, path: string, body?: unknown): Promise<ThemeInfo> {
  const res = await fetch(`/api/v1${path}`, {
    method,
    headers: body ? { 'Content-Type': 'application/json' } : undefined,
    body: body ? JSON.stringify(body) : undefined,
  })
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const b = await res.json()
      if (b?.message) msg = String(b.message)
    } catch { /* not JSON */ }
    throw new Error(msg)
  }
  return res.status === 204 ? (undefined as unknown as ThemeInfo) : res.json()
}

export const createTheme = (d: ThemeDraft) => send('POST', '/themes', d)
export const updateTheme = (id: string, d: Partial<ThemeDraft>) => send('PATCH', `/themes/${id}`, d)
export const deleteTheme = (id: string) => send('DELETE', `/themes/${id}`)
export const removeThemeBackground = (id: string) => send('DELETE', `/themes/${id}/background`)

export async function uploadThemeBackground(id: string, file: File): Promise<ThemeInfo> {
  const form = new FormData()
  form.append('image', file)
  const res = await fetch(`/api/v1/themes/${id}/background`, { method: 'PUT', body: form })
  if (!res.ok) {
    let msg = `${res.status} ${res.statusText}`
    try {
      const b = await res.json()
      if (b?.message) msg = String(b.message)
    } catch { /* not JSON */ }
    throw new Error(msg)
  }
  return res.json()
}

/** WCAG contrast ratio of two #rrggbb colours, 1 to 21. */
export function contrast(a: string, b: string): number {
  const lum = (hex: string) => {
    const n = parseInt(hex.slice(1), 16)
    const ch = [(n >> 16) & 255, (n >> 8) & 255, n & 255].map((c) => {
      const v = c / 255
      return v <= 0.03928 ? v / 12.92 : ((v + 0.055) / 1.055) ** 2.4
    })
    return 0.2126 * ch[0] + 0.7152 * ch[1] + 0.0722 * ch[2]
  }
  const [hi, lo] = [lum(a), lum(b)].sort((x, y) => y - x)
  return (hi + 0.05) / (lo + 0.05)
}
