/**
 * Preferences, resolved on the server.
 *
 * These used to live in localStorage, which made them per-browser rather
 * than per-person: the same account on a phone and a laptop disagreed, a
 * cleared cache lost them, and a native client could never see them. Now
 * the server resolves each one through its layers (this device's own value,
 * then the user's, their groups' defaults, the server's, the built-in one)
 * and says where each value came from and whether an admin has locked it.
 *
 * localStorage is still written, as a mirror rather than the source of
 * truth. A preference has to be readable *synchronously* at first paint —
 * the theme especially, or every load flashes the default while a fetch is
 * in flight — so the local copy supplies the initial value and the server's
 * answer corrects it a moment later.
 *
 * Volume is deliberately not here: it belongs to the browser, not even to
 * the device's server-side preferences. Headphones and living-room speakers
 * want different values, and syncing them would be a regression dressed as
 * a feature.
 */

import { getMyPrefs, putDevicePrefs, putMyPrefs, type MyPrefs, type PrefChanges } from './api'

const PREFIX = 'cineva-'

let cache: Record<string, string> = {}
let state: MyPrefs | null = null
let loaded = false
const listeners = new Set<(s: MyPrefs) => void>()

/** Read a preference synchronously, for first paint. */
export function pref(key: string, fallback = ''): string {
  if (loaded && key in cache) return cache[key]
  try {
    return localStorage.getItem(PREFIX + key) ?? fallback
  } catch {
    return fallback // private browsing, or storage disabled
  }
}

function adopt(s: MyPrefs): Record<string, string> {
  state = s
  cache = Object.fromEntries(Object.entries(s.prefs).map(([k, r]) => [k, r.value]))
  loaded = true
  // Mirror locally so the next load paints correctly before this fetch
  // has even been made.
  for (const [k, v] of Object.entries(cache)) {
    try { localStorage.setItem(PREFIX + k, v) } catch { /* ignore */ }
  }
  for (const fn of listeners) fn(s)
  return cache
}

/** Fetch the resolved preferences and adopt them. Call once the user is known. */
export async function loadPrefs(): Promise<Record<string, string>> {
  try {
    return adopt(await getMyPrefs())
  } catch {
    return cache // offline: the local mirror is what we have
  }
}

/** The last resolved preferences with their registry, or null before the first load. */
export function prefState(): MyPrefs | null {
  return state
}

/** Called with the new state whenever preferences are loaded or changed. */
export function onPrefs(fn: (s: MyPrefs) => void): () => void {
  listeners.add(fn)
  return () => {
    listeners.delete(fn)
  }
}

// A device preference is written to this browser's own session, so the
// living-room TV and a phone keep their own; a person preference to the
// user, so it follows them everywhere.
function write(changes: PrefChanges, device: boolean) {
  void (device ? putDevicePrefs(changes) : putMyPrefs(changes)).then(adopt).catch(() => {
    /* a failed write just means the other devices find out later */
  })
}

const onDevice = (key: string) =>
  !!state?.device && state.registry.some((p) => p.key === key && p.scope === 'device')

/**
 * Set a preference. Applied locally at once and pushed to the server in the
 * background — a preference change should never feel like it is waiting on
 * the network. A locked preference is left alone: the controls for it are
 * disabled, and the server would refuse it anyway.
 */
export function setPref(key: string, value: string): void {
  const current = state?.prefs[key]
  if (current?.locked) return
  cache[key] = value
  try { localStorage.setItem(PREFIX + key, value) } catch { /* ignore */ }
  const device = onDevice(key)
  if (state) {
    state = {
      ...state,
      prefs: { ...state.prefs, [key]: { ...current, value, source: device ? 'device' : 'user', locked: false } },
    }
    for (const fn of listeners) fn(state)
  }
  write({ [key]: value }, device)
}

/** Hand a preference back to its default, clearing the layer that set it. */
export function resetPref(key: string): void {
  const current = state?.prefs[key]
  if (!current || current.locked) return
  write({ [key]: null }, current.source === 'device')
}

/** Forget the in-memory copy on sign-out, so the next user starts clean. */
export function clearPrefs(): void {
  cache = {}
  state = null
  loaded = false
}
