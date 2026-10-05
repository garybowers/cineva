/**
 * Per-user UI preferences, stored on the server.
 *
 * These used to live in localStorage, which made them per-browser rather
 * than per-person: the same account on a phone and a laptop disagreed, a
 * cleared cache lost them, and a native client could never see them.
 *
 * localStorage is still written, as a mirror rather than the source of
 * truth. A preference has to be readable *synchronously* at first paint —
 * the theme especially, or every load flashes the default while a fetch is
 * in flight — so the local copy supplies the initial value and the server's
 * answer corrects it a moment later.
 *
 * Volume is deliberately not here: it belongs to the device, not the person.
 * Headphones and living-room speakers want different values, and syncing
 * them would be a regression dressed as a feature.
 */

const PREFIX = 'cineva-'

let cache: Record<string, string> = {}
let loaded = false

/** Read a preference synchronously, for first paint. */
export function pref(key: string, fallback = ''): string {
  if (loaded && key in cache) return cache[key]
  try {
    return localStorage.getItem(PREFIX + key) ?? fallback
  } catch {
    return fallback // private browsing, or storage disabled
  }
}

/** Fetch the server's copy and adopt it. Call once the user is known. */
export async function loadPrefs(): Promise<Record<string, string>> {
  try {
    const res = await fetch('/api/v1/auth/prefs')
    if (!res.ok) return cache
    cache = (await res.json()) as Record<string, string>
    loaded = true
    // Mirror locally so the next load paints correctly before this fetch
    // has even been made.
    for (const [k, v] of Object.entries(cache)) {
      try { localStorage.setItem(PREFIX + k, v) } catch { /* ignore */ }
    }
  } catch {
    /* offline: the local mirror is what we have */
  }
  return cache
}

/**
 * Set a preference. Applied locally at once and pushed to the server in the
 * background — a preference change should never feel like it is waiting on
 * the network, and a failed write just means the other devices find out
 * later.
 */
export function setPref(key: string, value: string): void {
  cache[key] = value
  try { localStorage.setItem(PREFIX + key, value) } catch { /* ignore */ }
  void fetch('/api/v1/auth/prefs', {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ [key]: value }),
  }).catch(() => {})
}

/** Forget the in-memory copy on sign-out, so the next user starts clean. */
export function clearPrefs(): void {
  cache = {}
  loaded = false
}
