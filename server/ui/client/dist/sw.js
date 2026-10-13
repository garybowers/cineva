// Cineva service worker.
//
// It exists for two reasons: a browser will not offer to install an app
// without one, and an installed app that opens to a connection error when
// the server is off is worse than a bookmark. It caches the app shell and
// the build's own assets, and nothing else.
//
// It deliberately does not cache media, artwork or any API response. Watch
// state and library contents have to be live, range requests have to reach
// the server for seeking and downloads to work at all, and artwork is large
// enough to evict the shell it is meant to protect. Offline *media* is a
// different feature with its own storage budget (milestone 11).

const VERSION = 'v1'
const SHELL = `cineva-shell-${VERSION}`

// Only the entry points. Hashed asset names change every build, so listing
// them here would mean regenerating this file on each one; they are cached
// on first use instead.
const PRECACHE = ['/', '/favicon.svg', '/manifest.webmanifest']

self.addEventListener('install', (e) => {
  e.waitUntil(
    caches
      .open(SHELL)
      // One failed entry must not fail the install, or a single 404 leaves
      // the app permanently uninstallable.
      .then((c) => Promise.allSettled(PRECACHE.map((u) => c.add(u))))
      .then(() => self.skipWaiting()),
  )
})

self.addEventListener('activate', (e) => {
  e.waitUntil(
    caches
      .keys()
      .then((keys) => Promise.all(keys.filter((k) => k !== SHELL).map((k) => caches.delete(k))))
      .then(() => self.clients.claim()),
  )
})

self.addEventListener('fetch', (e) => {
  const req = e.request
  if (req.method !== 'GET') return

  const url = new URL(req.url)
  if (url.origin !== location.origin) return

  // Live-only paths: the API (state, playback, downloads) and artwork.
  if (url.pathname.startsWith('/api/') || url.pathname.startsWith('/artwork/')) return

  // A navigation goes to the network first, so a reachable server always
  // wins and the viewer never sees a stale page. Only when that fails does
  // the cached shell answer, which is what makes an installed app open at
  // all without a server.
  if (req.mode === 'navigate') {
    e.respondWith(
      fetch(req).catch(() =>
        caches.match('/').then((hit) => hit || new Response('Offline', { status: 503 })),
      ),
    )
    return
  }

  // Build assets carry a content hash in the name, so a hit is never stale.
  e.respondWith(
    caches.match(req).then(
      (hit) =>
        hit ||
        fetch(req).then((res) => {
          if (res.ok && res.type === 'basic') {
            const copy = res.clone()
            caches.open(SHELL).then((c) => c.put(req, copy))
          }
          return res
        }),
    ),
  )
})
