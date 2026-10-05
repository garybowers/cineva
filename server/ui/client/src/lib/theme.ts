// Theme loading + persistence. Themes are folders served by the Go binary at
// /themes/<id>/theme.css; each defines the full design-token set from app.css.

import { pref, setPref } from './prefs'

export interface ThemeInfo {
  id: string
  name: string
  appearance: 'dark' | 'light'
}

export const DEFAULT_THEME = 'midnight'

export function currentTheme(): string {
  return pref('theme', DEFAULT_THEME)
}

export function applyTheme(id: string): void {
  let link = document.getElementById('theme-css') as HTMLLinkElement | null
  if (!link) {
    link = document.createElement('link')
    link.id = 'theme-css'
    link.rel = 'stylesheet'
    document.head.appendChild(link)
  }
  link.href = `/themes/${id}/theme.css`
}

/**
 * Apply a theme *and* remember it as this user's choice.
 *
 * Deliberately separate from applyTheme, which only touches the DOM.
 * Startup paints the current theme before the user's preferences have been
 * fetched; if that paint also wrote, loading the page would overwrite the
 * stored choice with whatever this browser happened to have — which is
 * exactly what it did until these were split.
 */
export function chooseTheme(id: string): void {
  applyTheme(id)
  setPref('theme', id)
}

export async function listThemes(): Promise<ThemeInfo[]> {
  const res = await fetch('/api/v1/themes')
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<ThemeInfo[]>
}
