// What the client looks like right now: the themes on offer, the user's own
// choice, and the holiday on now. The theme painted is the holiday's while
// one is on (and Holiday themes is on), otherwise the user's choice; it is
// repainted whenever any of the three, or that preference, changes.

import { onPrefs, pref } from './prefs'
import { applyTheme, currentTheme, effectiveTheme, getOccasion, listThemes, type Occasion, type ThemeInfo } from './theme'

export const looks = $state<{ list: ThemeInfo[]; occasion: Occasion | null; chosen: string; effects: boolean; painted: string }>({
  list: [],
  occasion: null,
  chosen: currentTheme(),
  effects: pref('theme-effects', 'on') === 'on',
  // What paint() last put on screen: the one thing that follows the
  // holiday preferences too, which are not reactive themselves.
  painted: '',
})

/** The theme on screen: what paint() applies. */
export function activeTheme(): ThemeInfo | undefined {
  return effectiveTheme(looks.list, looks.chosen, looks.occasion)
}

/** Apply the theme that should be on screen. Before the list has loaded
    the chosen theme is painted by id, as at startup. */
export function paint(): void {
  const t = activeTheme()
  if (t) applyTheme(t.id, t.css)
  else if (looks.list.length === 0) applyTheme(looks.chosen)
  looks.painted = t?.id ?? looks.chosen
}

/** Fetch the themes and the calendar again, then repaint: at sign-in, when
    a theme is made or changed, and as a day goes by. */
export async function refreshLooks(): Promise<void> {
  const [list, occasion] = await Promise.all([listThemes(), getOccasion().catch(() => null)])
  looks.list = list
  looks.occasion = occasion
  paint()
}

let installed = false
/** Repaint when preferences change (Holiday themes switched off, a theme
    chosen on another device), and look at the calendar again every few
    hours, so a screen left on starts Christmas on 1 December. */
export function installLooks(): void {
  if (installed) return
  installed = true
  onPrefs(() => {
    looks.chosen = currentTheme()
    looks.effects = pref('theme-effects', 'on') === 'on'
    paint()
  })
  setInterval(() => refreshLooks().catch(() => {}), 3 * 60 * 60 * 1000)
}
