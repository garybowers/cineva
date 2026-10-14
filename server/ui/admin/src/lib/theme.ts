// Theme loading + persistence. Themes are folders served by the Go binary at
// /themes/<id>/theme.css; each defines the full design-token set from app.css.

export interface ThemeInfo {
  id: string
  name: string
  appearance: 'dark' | 'light'
  /** The theme's design tokens by name without the dashes ("bg",
   *  "on-accent"), for drawing a preview without switching to it. */
  tokens?: Record<string, string>
}

const STORAGE_KEY = 'cineva-theme'
export const DEFAULT_THEME = 'midnight'

export function currentTheme(): string {
  try {
    return localStorage.getItem(STORAGE_KEY) ?? DEFAULT_THEME
  } catch {
    return DEFAULT_THEME
  }
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
  try {
    localStorage.setItem(STORAGE_KEY, id)
  } catch {
    /* private browsing — theme just won't persist */
  }
}

export async function listThemes(): Promise<ThemeInfo[]> {
  const res = await fetch('/api/v1/themes')
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`)
  return res.json() as Promise<ThemeInfo[]>
}
