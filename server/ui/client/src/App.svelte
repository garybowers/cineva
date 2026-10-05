<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, libraryItems, libraryItemsSince, libraryGenres, type GenreCount,
    libraryCollections, collectionDetail, type CollectionInfo,
    libraryRecommended, type RecRow,
    personDetail, type PersonProfile, type PersonCredit, type Person as PersonT, itemDetail, itemSubtitles, playItem, continueWatching, downloadURL, searchItems, subscribeEvents, setWatched,
    authStatus, login, setupAdmin, logout, changePassword, listAPIKeys, createAPIKey, deleteAPIKey, UNAUTHORISED, authProfiles, pinLogin, setPIN, type User as UserT, type APIKey, type Profile,
    type Health, type Library, type Item, type ItemDetail, type PlayInfo, type ResumeItem, type PlayQuality, type SubtitleTrack,
  } from './lib/api'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'
  import Player from './Player.svelte'
  import MusicPlayer from './MusicPlayer.svelte'
  import {
    House, Library as LibraryIcon, Settings as SettingsIcon, User, Clapperboard, Tv,
    Music as MusicIcon, Headphones, Podcast, BookOpen, Play, Shuffle, Download,
    RotateCcw, AudioLines, LogOut, KeyRound, Search, Eye, EyeOff, X, Check,
  } from '@lucide/svelte'

  type Tab = 'home' | 'libraries' | 'settings'
  const tabs = [
    { id: 'home' as Tab, label: 'Home', icon: House },
    { id: 'libraries' as Tab, label: 'Libraries', icon: LibraryIcon },
    { id: 'settings' as Tab, label: 'Settings', icon: SettingsIcon },
  ]

  // ---- Accounts: who is signed in, and the sign-in / first-run gate ----
  let me: UserT | null = $state(null)
  let gate: 'checking' | 'setup' | 'login' | 'in' = $state('checking')
  let authUser = $state('')
  let authPass = $state('')
  let authName = $state('')
  let authErr = $state('')
  let authBusy = $state(false)
  let showUserMenu = $state(false)
  // Household profile picker: users with a PIN get a name tile and a
  // numeric pad; anyone can switch to username + password.
  let profiles: Profile[] = $state([])
  let picked: Profile | null = $state(null)
  let usePassword = $state(false)
  let loginMode: 'picker' | 'form' = $state('picker')
  let pin = $state('')
  const loadProfiles = () => authProfiles().then((p) => (profiles = p)).catch(() => (profiles = []))
  function pickProfile(p: Profile) {
    picked = p
    pin = ''
    authPass = ''
    authUser = p.username
    authErr = ''
    setTimeout(() => (document.querySelector('.pinpad input') as HTMLInputElement | null)?.focus(), 30)
  }
  // A picked profile without a PIN signs in with its password.
  async function submitPickedPassword(e: Event) {
    e.preventDefault()
    if (!picked) return
    authBusy = true
    authErr = ''
    try {
      me = await login(picked.username, authPass)
      gate = 'in'
      authPass = ''
      boot()
    } catch (err) {
      authErr = err instanceof Error ? err.message : String(err)
    } finally {
      authBusy = false
    }
  }
  async function submitPIN(e: Event) {
    e.preventDefault()
    if (!picked) return
    authBusy = true
    authErr = ''
    try {
      me = await pinLogin(picked.id, pin)
      gate = 'in'
      pin = ''
      boot()
    } catch (err) {
      authErr = err instanceof Error ? err.message : String(err)
      pin = ''
    } finally {
      authBusy = false
    }
  }
  // Own PIN (Settings)
  let pinAuth = $state(''), pinNew = $state(''), pinMsg = $state('')
  async function submitPIN2(e: Event, clear = false) {
    e.preventDefault()
    pinMsg = ''
    try {
      await setPIN(pinAuth, clear ? '' : pinNew)
      pinMsg = clear ? 'PIN removed.' : 'PIN set.'
      pinAuth = pinNew = ''
      if (me) me = { ...me, hasPin: !clear }
    } catch (err) {
      pinMsg = err instanceof Error ? err.message : String(err)
    }
  }

  async function checkAuth() {
    try {
      const st = await authStatus()
      loginMode = st.loginMode ?? 'picker'
      if (st.setupRequired) gate = 'setup'
      else if (st.user) { me = st.user; gate = 'in' }
      else { gate = 'login'; if (loginMode === 'picker') await loadProfiles() }
    } catch {
      gate = 'login'
      offline = true
    }
  }
  async function submitAuth(e: Event) {
    e.preventDefault()
    authBusy = true
    authErr = ''
    try {
      me = gate === 'setup' ? await setupAdmin(authUser, authPass, authName) : await login(authUser, authPass)
      gate = 'in'
      authPass = ''
      boot()
    } catch (err) {
      authErr = err instanceof Error ? err.message : String(err)
    } finally {
      authBusy = false
    }
  }
  async function signOut() {
    showUserMenu = false
    try { await logout() } catch { /* already gone */ }
    me = null
    playing = null
    music = null
    picked = null
    gate = 'login'
    loadProfiles()
  }

  // Change password + API keys (Settings page)
  let pwCurrent = $state(''), pwNew = $state(''), pwMsg = $state('')
  async function submitPassword(e: Event) {
    e.preventDefault()
    pwMsg = ''
    try {
      await changePassword(pwCurrent, pwNew)
      pwCurrent = pwNew = ''
      pwMsg = 'Password changed.'
    } catch (err) {
      pwMsg = err instanceof Error ? err.message : String(err)
    }
  }
  let apiKeys: APIKey[] = $state([])
  let newKeyName = $state('')
  let revealedKey = $state('')
  const loadKeys = () => listAPIKeys().then((k) => (apiKeys = k)).catch(() => {})
  async function addKey(e: Event) {
    e.preventDefault()
    try {
      const k = await createAPIKey(newKeyName || 'API key')
      revealedKey = k.key
      newKeyName = ''
      apiKeys = [...apiKeys, k]
    } catch (err) {
      pwMsg = err instanceof Error ? err.message : String(err)
    }
  }

  // Revoking drops the row immediately; a failure restores it and says why.
  async function revokeKey(id: string) {
    const before = apiKeys
    apiKeys = apiKeys.filter((k) => k.id !== id)
    try {
      await deleteAPIKey(id)
    } catch (err) {
      apiKeys = before
      pwMsg = err instanceof Error ? err.message : String(err)
    }
  }

  let tab: Tab = $state('home')
  let health: Health | null = $state(null)
  let offline = $state(false)
  let routing = false // true while applyRoute drives state (suppresses pushState)

  // ---- URL routing: /libraries/<Library>/<view>/<Title>/... (names, not ids) ----
  //
  // `genres`, `collections` and `library` are reserved as the first segment
  // after a library name, so a view is linkable and survives a reload. A
  // film actually titled one of those three is shadowed by the view — a
  // trade worth making for routes that read the way the breadcrumb does.
  const libViewSegments = ['genres', 'collections', 'library']

  function viewPath(): string {
    if (libTab === 'genres') return '/genres' + (genre ? '/' + encodeURIComponent(genre) : '')
    if (libTab === 'collections') return '/collections' + (collection ? '/' + encodeURIComponent(collection.name) : '')
    if (libTab === 'library') return '/library'
    return '' // recommended is the default view and needs no segment
  }

  // The library view as breadcrumb steps: "Collections › Scream Collection".
  // Derived from the same state the route is, so the two cannot disagree.
  type ViewCrumb = { label: string; go: () => void }
  const viewCrumbs = $derived.by<ViewCrumb[]>(() => {
    const out: ViewCrumb[] = []
    if (libTab === 'genres') {
      out.push({ label: 'Genres', go: () => { detail = null; trail = []; genre = null; syncURL() } })
      if (genre) out.push({ label: genre, go: () => { detail = null; trail = []; syncURL() } })
    } else if (libTab === 'collections') {
      out.push({ label: 'Collections', go: () => { detail = null; trail = []; collection = null; syncURL() } })
      if (collection) out.push({ label: collection.name, go: () => { detail = null; trail = []; syncURL() } })
    }
    return out
  })

  function buildPath(): string {
    if (listening && music) return '/listening'
    if (tab === 'home') return '/'
    if (tab === 'settings') return '/settings'
    let p = '/libraries'
    if (currentLib) {
      p += '/' + encodeURIComponent(currentLib.name)
      // Kept on item URLs too, so opening a film from inside a collection
      // and reloading still shows where you came from.
      p += viewPath()
    }
    if (detail) {
      for (const a of trail) p += '/' + encodeURIComponent(a.title)
      p += '/' + encodeURIComponent(detail.item.title)
    }
    return p
  }

  function syncURL() {
    if (routing) return
    const p = buildPath()
    if (p !== location.pathname) history.pushState({}, '', p)
  }

  function setTab(t: Tab) {
    tab = t
    listening = false
    syncURL()
  }

  async function applyRoute(rawPath: string) {
    routing = true
    try {
      const parts = rawPath.split('/').filter(Boolean).map(decodeURIComponent)
      if (parts.length === 0) {
        tab = 'home'
        return
      }
      if (parts[0] === 'settings') {
        tab = 'settings'
        return
      }
      if (parts[0] === 'listening') {
        listening = !!music
        if (!music) tab = 'home'
        return
      }
      if (parts[0] !== 'libraries') {
        tab = 'home'
        return
      }
      tab = 'libraries'
      if (libraries.length === 0) {
        try {
          libraries = await listLibraries()
        } catch {
          return
        }
      }
      const lib = parts[1] ? libraries.find((l) => l.name === parts[1]) : undefined
      if (!lib) {
        currentLib = null
        detail = null
        trail = []
        return
      }
      await openLibrary(lib)
      let segs = parts.slice(2)

      // A reserved view segment, and then the genre or collection name —
      // which is unambiguous here because we are already inside that view.
      switch (segs[0]) {
        case 'genres':
          libTab = 'genres'
          segs = segs.slice(1)
          if (segs.length > 0) {
            await openGenre(segs[0])
            segs = segs.slice(1)
          }
          break
        case 'collections':
          libTab = 'collections'
          segs = segs.slice(1)
          if (segs.length > 0) {
            if (collections.length === 0) await loadCollections(lib)
            const c = collections.find((x) => x.name === segs[0])
            if (c) await openCollection(c)
            segs = segs.slice(1)
          }
          break
        case 'library':
          libTab = 'library'
          segs = segs.slice(1)
          break
      }
      if (segs.length === 0) return

      // Resolve title segments against the library's item tree.
      const items = await libraryItems(lib.id)
      let target: Item | null = null
      const ancestors: Item[] = []
      for (const seg of segs) {
        const found = items.find((i) =>
          i.title === seg &&
          (target ? i.parentId === target.id : !i.parentId || i.kind === 'movie' || i.kind === 'series' || i.kind === 'artist'),
        )
        if (!found) break
        if (target) ancestors.push(target)
        target = found
      }
      if (!target) return
      // Seasons are the last navigation level: an episode URL resolves to
      // its season with the episode selected in-page.
      let selectEp: Item | null = null
      if (target.kind === 'episode' && target.parentId) {
        selectEp = target
        target = items.find((i) => i.id === selectEp!.parentId) ?? target
        ancestors.pop()
      }
      detail = await itemDetail(target.id)
      trail = ancestors
      epDetail = null
      detailSubs = []
      if (detail.item.kind === 'movie') {
        itemSubtitles(target.id).then((s) => (detailSubs = s)).catch(() => {})
      }
      if (selectEp) selectEpisode(selectEp)
    } finally {
      routing = false
    }
  }

  const typeLabels: Record<string, { label: string; icon: typeof House }> = {
    movies: { label: 'Movies', icon: Clapperboard },
    tv: { label: 'TV Shows', icon: Tv },
    music: { label: 'Music', icon: MusicIcon },
    audiobooks: { label: 'Audiobooks', icon: Headphones },
    podcasts: { label: 'Podcasts', icon: Podcast },
    books: { label: 'Books', icon: BookOpen },
  }
  const libraryTree = $derived(
    Object.entries(typeLabels)
      .map(([type, meta]) => ({ type, ...meta, libs: libraries.filter((l) => l.mediaType === type) }))
      .filter((g) => g.libs.length > 0),
  )
  let themes: ThemeInfo[] = $state([])
  let theme = $state(currentTheme())

  // Libraries browse state: a simple drill-down stack.
  let libraries: Library[] = $state([])
  let currentLib: Library | null = $state(null)
  let gridItems: Item[] = $state([])
  let detail: ItemDetail | null = $state(null)
  let detailSubs: SubtitleTrack[] = $state([])
  let trail: Item[] = $state([]) // breadcrumb ancestors of the current detail
  let resume: ResumeItem[] = $state([])
  let playing: { item: Item; info: PlayInfo; quality: PlayQuality; gen: number } | null = $state(null)
  let music: { queue: Item[]; startIndex: number; shuffle: boolean; gen: number } | null = $state(null)
  let autoplay = $state(readAutoplay())
  // Music streaming quality: original file, or a server-side Opus transcode
  // for slow connections. Takes effect from the next track.
  type MusicQuality = 'original' | 'opus-128' | 'opus-64'
  const musicQualities: { id: MusicQuality; label: string }[] = [
    { id: 'original', label: 'Original' }, { id: 'opus-128', label: 'High (Opus 128k)' }, { id: 'opus-64', label: 'Data saver (Opus 64k)' },
  ]
  let musicQuality: MusicQuality = $state((() => { try { return (localStorage.getItem('cineva-music-quality') as MusicQuality) || 'original' } catch { return 'original' } })())
  function setMusicQuality(q: MusicQuality) {
    musicQuality = q
    try { localStorage.setItem('cineva-music-quality', q) } catch { /* private browsing */ }
  }
  let playGen = 0 // bumps on quality change so the player remounts
  let playingPos = $state(0) // live playhead seconds, reported by the Player

  // ---- Download manager: dispatches each file as its own browser download ----
  type DownloadEntry = { id: string; title: string; status: 'queued' | 'sent' }
  let downloads: DownloadEntry[] = $state([])
  let showDownloads = $state(false)
  let dispatching = false

  function queueDownloads(items: Item[]) {
    const fresh = items
      .filter((i) => !downloads.some((d) => d.id === i.id))
      .map((i) => ({
        id: i.id,
        title:
          i.kind === 'episode'
            ? `S${String(i.seasonNum).padStart(2, '0')}E${String(i.episodeNum).padStart(2, '0')} ${i.title}`
            : i.kind === 'track' && i.artist
              ? `${i.artist} — ${i.title}`
              : i.title,
        status: 'queued' as const,
      }))
    downloads = [...downloads, ...fresh]
    showDownloads = true
    void dispatchQueue()
  }

  async function dispatchQueue() {
    if (dispatching) return
    dispatching = true
    try {
      for (;;) {
        const next = downloads.find((d) => d.status === 'queued')
        if (!next) break
        const a = document.createElement('a')
        a.href = downloadURL(next.id)
        a.download = ''
        document.body.appendChild(a)
        a.click()
        a.remove()
        downloads = downloads.map((d) => (d.id === next.id ? { ...d, status: 'sent' } : d))
        await new Promise((r) => setTimeout(r, 900)) // stagger so browsers accept the batch
      }
    } finally {
      dispatching = false
    }
  }

  // Trigger a download in the chosen format ("original" | "mp3-320" | ...).
  function dlNavigate(id: string, v: string) {
    if (!v) return
    const [format, q] = v.split('-')
    const url = format === 'original' ? downloadURL(id) : `${downloadURL(id)}?format=${format}&quality=${q}`
    const a = document.createElement('a')
    a.href = url
    a.download = ''
    document.body.appendChild(a)
    a.click()
    a.remove()
  }

  // All playable descendants of a season or series, in order.
  async function collectEpisodes(root: Item): Promise<Item[]> {
    if (!detail) return []
    if (root.kind === 'season') return detail.children.filter((c) => c.kind === 'episode')
    if (root.kind === 'series' && currentLib) {
      const items = await libraryItems(currentLib.id)
      const seasonIds = new Set(items.filter((i) => i.parentId === root.id).map((i) => i.id))
      return items
        .filter((i) => i.kind === 'episode' && i.parentId && seasonIds.has(i.parentId))
        .sort((a, b) => (a.seasonNum ?? 0) - (b.seasonNum ?? 0) || (a.episodeNum ?? 0) - (b.episodeNum ?? 0))
    }
    return []
  }

  async function downloadAll(root: Item) {
    queueDownloads(await collectEpisodes(root))
  }

  // Video and music playback are mutually exclusive.
  function startMusic(tracks: Item[], startIndex: number, shuffleAll = false) {
    playing = null
    music = { queue: tracks, startIndex, shuffle: shuffleAll, gen: ++playGen }
  }

  // ---- Listening session: full-page album view over the playing queue ----
  let listening = $state(false)
  let musicNow: { track: Item; index: number } | null = $state(null)
  let musicPos = $state(0)
  let sessionAlbums: Map<string, Item> = $state(new Map())
  let mpRef: { jumpTo: (i: number) => void } | undefined = $state()

  function onTrackChange(track: Item, index: number) {
    musicNow = { track, index }
    musicPos = 0
    const albumId = track.parentId
    if (albumId && !sessionAlbums.has(albumId)) {
      itemDetail(albumId)
        .then((d) => (sessionAlbums = new Map(sessionAlbums).set(albumId, d.item)))
        .catch(() => {})
    }
  }

  function openListening() {
    listening = true
    syncURL()
  }

  function closeListening() {
    listening = false
    syncURL()
  }

  // "Pop the CD in": start the queue and open the session view.
  function startSession(tracks: Item[], startIndex = 0, shuffleAll = false) {
    startMusic(tracks, startIndex, shuffleAll)
    openListening()
  }

  async function leaveSessionToLibrary(lib: Library) {
    listening = false
    tab = 'libraries'
    await openLibrary(lib)
  }

  async function leaveSessionToArtist(lib: Library, album: Item) {
    listening = false
    tab = 'libraries'
    if (currentLib?.id !== lib.id || musicAll.length === 0) await openLibrary(lib)
    if (album.parentId) selectArtist(album.parentId)
    syncURL()
  }

  // Open the album containing a track (from the music bar or search).
  async function openAlbumOf(track: Item) {
    if (!track.parentId) return
    tab = 'libraries'
    const lib = libraries.find((l) => l.id === track.libraryId)
    if (lib && lib.id !== currentLib?.id) {
      currentLib = lib
      libraryItems(lib.id).then((items) => {
        gridItems = items.filter(gridFilter(lib))
      })
    }
    await openItem(track.parentId)
  }

  // Jump to a track's artist in the music view. Tracks hang off albums, so
  // the artist id comes from the album's parent.
  async function goToArtistOf(item: Item) {
    let artistId = item.kind === 'album' ? (item.parentId ?? '') : ''
    if (!artistId && item.parentId) {
      try {
        artistId = (await itemDetail(item.parentId)).item.parentId ?? ''
      } catch {
        /* album gone — fall through */
      }
    }
    const lib = libraries.find((l) => l.id === item.libraryId)
    if (!lib || !artistId) return
    tab = 'libraries'
    listening = false
    detail = null
    if (currentLib?.id !== lib.id || musicAll.length === 0) await openLibrary(lib)
    selectArtist(artistId)
    syncURL()
  }

  // A search hit on a song plays it in album context: queue the whole album
  // starting at that track.
  async function playTrackResult(t: Item) {
    let queue = [t]
    let start = 0
    if (t.parentId) {
      try {
        const tracks = (await itemDetail(t.parentId)).children.filter((c) => c.kind === 'track')
        if (tracks.length > 0) {
          queue = tracks
          start = Math.max(0, tracks.findIndex((c) => c.id === t.id))
        }
      } catch {
        /* album gone — play the bare track */
      }
    }
    startMusic(queue, start)
  }

  // "Go to song": the album page with the track picked out.
  let hlTrackId: string | null = $state(null)
  async function goToSong(t: Item) {
    hlTrackId = t.id
    await openAlbumOf(t)
    setTimeout(() => {
      document.querySelector('.children li.hl')?.scrollIntoView({ block: 'center', behavior: 'smooth' })
    }, 50)
    setTimeout(() => (hlTrackId = null), 5000)
  }

  const gridFilter = (lib: Library) => (i: Item) =>
    lib.mediaType === 'music' ? i.kind === 'album' : i.kind === 'movie' || i.kind === 'series'

  // ---- Library view: sub-tabs, a text filter and a sort order ----
  type LibTab = 'recommended' | 'library' | 'genres' | 'collections'
  let libTab: LibTab = $state('recommended')
  let gridQuery = $state('')
  // Date added, rating and runtime are all already on the item, so these
  // cost nothing beyond the comparator.
  type GridSort = 'title' | 'year' | 'added' | 'rating' | 'runtime' | 'watched'
  let gridSort: GridSort = $state('title')
  // Narrowing to what is still unseen is the usual way of picking something
  // to watch, so it is a first-class control rather than a search term.
  let gridWatch: 'all' | 'unwatched' | 'watched' = $state('all')
  // Minimum provider score. "Something good tonight" is a real way of
  // choosing, and the rating is already on every matched item.
  let gridMinRating = $state(0)
  let activeLetter = $state('')

  // Titles are sorted and bucketed on a leading-article-stripped key, so
  // "The Matrix" files under M where a viewer looks for it.
  const sortKey = (t: string) => t.replace(/^(the|a|an)\s+/i, '').toLocaleLowerCase()
  const initialOf = (t: string) => {
    const c = sortKey(t).charAt(0).toUpperCase()
    return c >= 'A' && c <= 'Z' ? c : '#'
  }

  /** How far through the viewer is, 0 when unstarted or finished. */
  const progressPct = (i: Item) =>
    !i.watched && i.positionSecs && i.runtimeSecs
      ? Math.min(100, Math.round((i.positionSecs / i.runtimeSecs) * 100))
      : 0

  const byTitle = (a: Item, b: Item) => sortKey(a.title).localeCompare(sortKey(b.title))
  // Every comparator falls back to title, so equal keys (no rating, same
  // year, missing runtime) still come out in a stable, meaningful order
  // rather than whatever the query happened to return.
  const comparators: Record<GridSort, (a: Item, b: Item) => number> = {
    title: byTitle,
    year: (a, b) => (b.year ?? 0) - (a.year ?? 0) || byTitle(a, b),
    added: (a, b) => (b.addedAt ?? '').localeCompare(a.addedAt ?? '') || byTitle(a, b),
    rating: (a, b) => (b.rating ?? 0) - (a.rating ?? 0) || byTitle(a, b),
    runtime: (a, b) => (b.runtimeSecs ?? 0) - (a.runtimeSecs ?? 0) || byTitle(a, b),
    // Never-touched films sort last rather than first: an empty timestamp
    // would otherwise lead the list under a plain string comparison.
    watched: (a, b) => (b.watchedAt ?? '').localeCompare(a.watchedAt ?? '') || byTitle(a, b),
  }

  const visibleItems = $derived.by(() => {
    const q = gridQuery.trim().toLocaleLowerCase()
    const out = gridItems.filter((i) => {
      if (gridWatch === 'unwatched' && i.watched) return false
      if (gridWatch === 'watched' && !i.watched) return false
      // An unrated film is excluded by any minimum rather than treated as
      // zero: "7+" means "known to be good", not "not known to be bad".
      if (gridMinRating > 0 && (i.rating ?? 0) < gridMinRating) return false
      if (!q) return true
      // Matched across the fields the item carries. Cast is searched through
      // the server's /search, which can reach the credits tables; this box
      // filters the list already in hand.
      return (
        i.title.toLocaleLowerCase().includes(q) ||
        (i.artist?.toLocaleLowerCase().includes(q) ?? false) ||
        (i.overview?.toLocaleLowerCase().includes(q) ?? false) ||
        String(i.year ?? '').includes(q)
      )
    })
    return out.sort(comparators[gridSort])
  })

  // A–Z sections, and the rail that jumps between them. Sorting by year
  // makes an alphabetical index meaningless, so both disappear.
  const sections = $derived.by(() => {
    if (gridSort !== 'title') return []
    const by = new Map<string, Item[]>()
    for (const i of visibleItems) {
      const k = initialOf(i.title)
      const list = by.get(k)
      if (list) list.push(i)
      else by.set(k, [i])
    }
    const letters = [...by.keys()].filter((c) => c !== '#').sort()
    if (by.has('#')) letters.push('#')
    return letters.map((letter) => ({ letter, items: by.get(letter)! }))
  })

  // Height of everything pinned above the grid, so a jump lands the heading
  // just below it instead of under it.
  // The sticky header (tabs + filter bar) and the top bar together eat a
  // real slice of a short laptop screen in landscape, so the header slides
  // away while scrolling down through the grid and comes back the moment
  // the viewer scrolls up — the gesture people already make when they want
  // the controls.
  let headHidden = $state(false)

  function stuckOffset(): number {
    const bar = document.querySelector('.topbar') as HTMLElement | null
    const head = document.querySelector('.libhead') as HTMLElement | null
    // A hidden header is not occupying space at the top of the viewport, so
    // a jump must not reserve room for it or every landing is short.
    const headH = headHidden ? 0 : (head?.offsetHeight ?? 0)
    return (bar?.offsetHeight ?? 0) + headH
  }

  // The rail is one tab stop, not one per letter: a roving tabindex. Twenty-
  // six stops between the filter box and the grid would be worse for a
  // keyboard user than the rail is worth, and arrow keys are what the WAI
  // pattern for a toolbar expects anyway.
  let railFocus = $state(0)

  function railKey(e: KeyboardEvent) {
    const last = sections.length - 1
    if (last < 0) return
    let next = railFocus
    switch (e.key) {
      case 'ArrowDown':
      case 'ArrowRight':
        next = Math.min(railFocus + 1, last)
        break
      case 'ArrowUp':
      case 'ArrowLeft':
        next = Math.max(railFocus - 1, 0)
        break
      case 'Home':
        next = 0
        break
      case 'End':
        next = last
        break
      default:
        return // leave Enter/Space to the button itself
    }
    e.preventDefault()
    railFocus = next
    // Move focus with the roving index, then jump — so the letter under the
    // cursor is both focused and shown.
    const el = railEl?.querySelectorAll('.azl')[next] as HTMLElement | undefined
    el?.focus()
    jumpTo(sections[next].letter)
  }

  // Keep the tab stop on the letter the viewer is actually looking at, so
  // tabbing into the rail after scrolling lands where they are.
  $effect(() => {
    const i = sections.findIndex((sec) => sec.letter === activeLetter)
    if (i >= 0) railFocus = i
  })

  function jumpTo(letter: string) {
    const el = document.getElementById('az-' + letter)
    if (!el) return
    // Using the rail means the viewer is navigating, so bring the header
    // back rather than landing under a hidden one.
    headHidden = false
    const y = el.getBoundingClientRect().top + window.scrollY - stuckOffset() - 8
    window.scrollTo({ top: Math.max(0, y) })
    activeLetter = letter
  }

  // Dragging a finger down the rail tracks through the letters, so the whole
  // alphabet is reachable in one gesture at any height.
  let railEl: HTMLElement | null = $state(null)
  let railDragging = false
  function railAt(clientY: number) {
    if (!railEl || sections.length === 0) return
    const r = railEl.getBoundingClientRect()
    const i = Math.floor(((clientY - r.top) / r.height) * sections.length)
    const s = sections[Math.min(sections.length - 1, Math.max(0, i))]
    if (s && s.letter !== activeLetter) jumpTo(s.letter)
  }
  function railDown(e: PointerEvent) {
    railDragging = true
    railEl?.setPointerCapture(e.pointerId)
    railAt(e.clientY)
  }
  function railMove(e: PointerEvent) {
    if (railDragging) railAt(e.clientY)
  }
  function railUp(e: PointerEvent) {
    railDragging = false
    railEl?.releasePointerCapture(e.pointerId)
  }

  // Scroll spy: the active letter is the last section whose top has passed
  // the top of the viewport — read straight off the layout in document
  // order. An IntersectionObserver reports only the sections whose state
  // changed, in no particular order, which highlighted B while A filled the
  // screen.
  $effect(() => {
    if (sections.length === 0) return
    const letters = sections.map((sec) => sec.letter)
    let queued = false
    let lastY = window.scrollY
    const sync = () => {
      queued = false

      // Collapse on the way down, reveal on the way up. The threshold stops
      // the header flickering on the small scroll jitter a trackpad makes,
      // and it always returns near the top so the controls are never
      // stranded out of reach.
      const y = window.scrollY
      const delta = y - lastY
      if (Math.abs(delta) > 6) {
        headHidden = delta > 0 && y > stuckOffset() * 2
        lastY = y
      }

      let current = letters[0]
      for (const l of letters) {
        const el = document.getElementById('az-' + l)
        if (el && el.getBoundingClientRect().top <= stuckOffset() + 16) current = l
        else break
      }
      activeLetter = current
    }
    const onScroll = () => {
      if (queued) return
      queued = true
      requestAnimationFrame(sync)
    }
    sync()
    window.addEventListener('scroll', onScroll, { passive: true })
    window.addEventListener('resize', onScroll, { passive: true })
    return () => {
      window.removeEventListener('scroll', onScroll)
      window.removeEventListener('resize', onScroll)
    }
  })

  // ---- People ----
  // Opening a cast member shows everything else in the library they are in,
  // which turns the cast list into a way through the collection rather than
  // a list of names.
  let person: PersonProfile | null = $state(null)

  /**
   * A filmography reads by what someone did, not by a flat list of titles:
   * acting roles first (the usual reason to tap a cast member), then the
   * crew work, each newest first — which is the order the server returns.
   */
  const personActing = $derived(person?.credits.filter((c) => c.creditKind === 'cast') ?? [])
  const personCrew = $derived(person?.credits.filter((c) => c.creditKind === 'crew') ?? [])

  /** "Director, Writer · 1994–2024 · 12 titles" */
  const personSummary = $derived.by(() => {
    if (!person) return ''
    const parts: string[] = []
    if (person.jobs?.length) parts.push(person.jobs.slice(0, 3).join(', '))
    if (person.firstYear) {
      parts.push(person.firstYear === person.lastYear ? `${person.firstYear}` : `${person.firstYear}–${person.lastYear}`)
    }
    const n = person.credits.length
    parts.push(`${n} ${n === 1 ? 'title' : 'titles'} here`)
    return parts.join(' · ')
  })

  async function openPerson(id: string) {
    try {
      person = await personDetail(id)
      syncURL()
    } catch (err) {
      console.error('person lookup failed', err)
    }
  }

  const closePerson = () => (person = null)

  // ---- Genres ----
  // The list is whatever this library's films carry, so it reflects the
  // collection rather than a fixed vocabulary.
  let genres: GenreCount[] = $state([])
  let genre: string | null = $state(null)
  let genreItems: Item[] = $state([])

  async function loadGenres(lib: Library) {
    try {
      genres = await libraryGenres(lib.id)
    } catch {
      genres = []
    }
  }

  // Collections come from TMDB's belongs_to_collection — Harry Potter,
  // James Bond, The Lord of the Rings and so on are all detected there, so
  // this needs no franchise list of its own. The server only reports a
  // collection once the library holds more than one of its films.
  let collections: CollectionInfo[] = $state([])
  let collection: CollectionInfo | null = $state(null)
  let collItems: Item[] = $state([])

  // Recommended shelves, decided server-side. Reloaded when watch state
  // changes, since every row is "things you have not seen".
  let recRows: RecRow[] = $state([])

  async function loadRecommended(lib: Library) {
    try {
      recRows = await libraryRecommended(lib.id)
    } catch {
      recRows = []
    }
  }

  async function loadCollections(lib: Library) {
    try {
      collections = await libraryCollections(lib.id)
    } catch {
      collections = []
    }
  }

  async function openCollection(c: CollectionInfo) {
    collection = c
    collItems = []
    syncURL()
    if (!currentLib) return
    try {
      const d = await collectionDetail(currentLib.id, c.key)
      // The server's summary is authoritative — it knows the viewer's
      // progress and which film is next; the tile we were clicked from may
      // be a cached copy.
      collection = d.collection
      collItems = d.items
    } catch {
      collItems = []
    }
  }

  /**
   * Play a collection from where the viewer left off: the queue is the whole
   * set in its own order, starting at the server's next-up. Watching through
   * a series is then one click rather than one per film.
   */
  function playCollection() {
    const id = collection?.nextUpId
    const from = id ? collItems.findIndex((i) => i.id === id) : 0
    playList(collItems, from < 0 ? 0 : from)
  }

  async function openGenre(name: string) {
    genre = name
    genreItems = []
    syncURL()
    if (!currentLib) return
    try {
      genreItems = await libraryItems(currentLib.id, name)
    } catch {
      genreItems = []
    }
  }

  // ---- Recommended ----
  const resumeHere = $derived(resume.filter((r) => r.libraryId === currentLib?.id))
  const recentlyAdded = $derived(
    gridItems
      .filter((i) => i.addedAt)
      .sort((a, b) => (b.addedAt ?? '').localeCompare(a.addedAt ?? ''))
      .slice(0, 12),
  )

  // ---- Music view: artist rail + album shelves ----
  let musicAll: Item[] = $state([])
  let selArtist = $state('ALL') // artist item id | 'ALL' | 'COMP'
  let selAlbumId: string | null = $state(null) // expanded album panel

  const mArtists = $derived(
    musicAll.filter((i) => i.kind === 'artist').sort((a, b) => a.title.localeCompare(b.title)),
  )
  const mAlbums = $derived(musicAll.filter((i) => i.kind === 'album'))
  const mTracks = $derived(musicAll.filter((i) => i.kind === 'track'))
  const albumById = $derived(new Map(mAlbums.map((a) => [a.id, a])))
  // Compilations: tagged Various Artists, or tracks by 2+ different artists.
  const compAlbumIds = $derived.by(() => {
    const artistsPer = new Map<string, Set<string>>()
    for (const t of mTracks) {
      if (!t.parentId) continue
      const s = artistsPer.get(t.parentId) ?? new Set<string>()
      s.add(t.artist ?? '')
      artistsPer.set(t.parentId, s)
    }
    const ids = new Set<string>()
    for (const a of mAlbums) {
      if ((a.artist ?? '').toLowerCase().startsWith('various') || (artistsPer.get(a.id)?.size ?? 0) > 1) {
        ids.add(a.id)
      }
    }
    return ids
  })
  const byYear = (a: Item, b: Item) => (a.year ?? 0) - (b.year ?? 0) || a.title.localeCompare(b.title)

  // Shelf groups: one per artist for "All", a single group otherwise.
  const shelfGroups = $derived.by(() => {
    if (selArtist === 'COMP') {
      return [{ id: 'COMP', title: 'Compilations', albums: mAlbums.filter((a) => compAlbumIds.has(a.id)).sort(byYear) }]
    }
    if (selArtist !== 'ALL') {
      const a = mArtists.find((x) => x.id === selArtist)
      return [{ id: selArtist, title: a?.title ?? '', albums: mAlbums.filter((al) => al.parentId === selArtist).sort(byYear) }]
    }
    return mArtists
      .map((a) => ({ id: a.id, title: a.title, albums: mAlbums.filter((al) => al.parentId === a.id).sort(byYear) }))
      .filter((g) => g.albums.length > 0)
  })
  const shelfAlbumCount = $derived(shelfGroups.reduce((n, g) => n + g.albums.length, 0))

  // Tracks grouped by album, computed once per library load — the filter
  // and shelf code call this per album, so it must be O(1), not a scan.
  const tracksByAlbum = $derived.by(() => {
    const m = new Map<string, Item[]>()
    for (const t of mTracks) {
      if (!t.parentId) continue
      const arr = m.get(t.parentId)
      if (arr) arr.push(t)
      else m.set(t.parentId, [t])
    }
    for (const arr of m.values()) {
      arr.sort((x, y) => (x.discNum ?? 0) - (y.discNum ?? 0) || (x.trackNum ?? 0) - (y.trackNum ?? 0))
    }
    return m
  })
  const tracksOf = (albumId: string) => tracksByAlbum.get(albumId) ?? []

  const shelfQueue = () => shelfGroups.flatMap((g) => g.albums.flatMap((a) => tracksOf(a.id)))

  function selectArtist(id: string) {
    selArtist = id
    selAlbumId = null
    musicFilter = ''
  }

  // Artist rail filter (name narrowing for very large libraries).
  let railFilter = $state('')
  const railArtists = $derived.by(() => {
    const q = railFilter.trim().toLowerCase()
    return q ? mArtists.filter((a) => a.title.toLowerCase().includes(q)) : mArtists
  })

  // Filter within the current artist/album scope: albums by title or by
  // containing a matching song; matching songs listed for direct play.
  let musicFilter = $state('')
  let mfTimer: ReturnType<typeof setTimeout> | undefined
  function onMusicFilterInput(e: Event) {
    const v = (e.target as HTMLInputElement).value
    clearTimeout(mfTimer)
    mfTimer = setTimeout(() => (musicFilter = v), 200)
  }
  const filterQ = $derived(musicFilter.trim().toLowerCase())
  const filteredShelfGroups = $derived.by(() => {
    if (!filterQ) return shelfGroups
    return shelfGroups
      .map((g) => ({
        ...g,
        albums: g.albums.filter(
          (a) =>
            a.title.toLowerCase().includes(filterQ) ||
            tracksOf(a.id).some((t) => t.title.toLowerCase().includes(filterQ)),
        ),
      }))
      .filter((g) => g.albums.length > 0)
  })
  const matchedSongs = $derived.by(() => {
    if (!filterQ) return []
    const out: { t: Item; album: Item; idx: number; tracks: Item[] }[] = []
    outer: for (const g of shelfGroups) {
      for (const a of g.albums) {
        const ts = tracksOf(a.id)
        for (let i = 0; i < ts.length; i++) {
          if (ts[i].title.toLowerCase().includes(filterQ)) {
            out.push({ t: ts[i], album: a, idx: i, tracks: ts })
            if (out.length >= 60) break outer
          }
        }
      }
    }
    return out
  })

  const trackTime = (secs?: number) =>
    secs ? `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}` : ''

  // Search
  let searchQ = $state('')
  let searchResults: Item[] = $state([])
  let searchTimer: ReturnType<typeof setTimeout> | undefined

  function onSearchInput() {
    clearTimeout(searchTimer)
    const q = searchQ.trim()
    if (q.length < 2) {
      searchResults = []
      return
    }
    searchTimer = setTimeout(async () => {
      try {
        searchResults = await searchItems(q)
      } catch {
        searchResults = []
      }
    }, 250)
  }

  function clearSearch() {
    searchQ = ''
    searchResults = []
  }

  // The full player shows on the playing item's own page; anywhere else it
  // collapses to the bottom now-playing bar (Plex-style).
  const playerCompact = $derived(
    !!playing &&
      !(tab === 'libraries' && detail &&
        (detail.item.id === playing.item.id || detail.item.id === playing.item.parentId)),
  )

  // Jump to an item's detail page, restoring library context.
  async function showItem(target: Item) {
    if (target.kind === 'track') return openAlbumOf(target)
    tab = 'libraries'
    const lib = libraries.find((l) => l.id === target.libraryId)
    if (lib && lib.id !== currentLib?.id) {
      currentLib = lib
      libraryItems(lib.id).then((items) => {
        gridItems = items.filter(gridFilter(lib))
      })
    }
    await openItem(target.id, false, target)
  }

  function readQuality(): PlayQuality {
    try {
      return (localStorage.getItem('cineva-quality') as PlayQuality) || 'auto'
    } catch {
      return 'auto'
    }
  }

  function readAutoplay(): boolean {
    try {
      return localStorage.getItem('cineva-autoplay') !== 'off'
    } catch {
      return true
    }
  }

  function setAutoplay(on: boolean) {
    autoplay = on
    try {
      localStorage.setItem('cineva-autoplay', on ? 'on' : 'off')
    } catch {
      /* private browsing */
    }
  }

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  async function poll() {
    try {
      const [h, libs, res] = await Promise.all([getHealth(), listLibraries(), continueWatching()])
      health = h
      libraries = libs
      resume = res
      offline = false
      // The bus drops events for a subscriber whose buffer is full, so the
      // grid cannot rely on hints alone to be current. Its own failure is
      // not a connectivity verdict, so it never flips the offline flag.
      if (currentLib) refreshLibraryItems().catch(() => {})
    } catch {
      health = null
      offline = true
    }
  }

  // ---- Play queue ----
  //
  // Playing a single film replaces the queue with just that film, so the
  // default stays "play this and stop". Playing a collection loads the set,
  // and the player rolls through it.
  let queue: Item[] = $state([])
  let queueIndex = $state(0)
  const upNext = $derived(queue.slice(queueIndex + 1))

  /** Queue a collection (or any list) and start at the given position. */
  function playList(items: Item[], startIndex = 0) {
    if (items.length === 0) return
    const at = Math.max(0, Math.min(startIndex, items.length - 1))
    queue = items
    queueIndex = at
    startPlay(items[at], readQuality(), false, undefined, true)
  }

  function enqueue(item: Item) {
    // Appending to an empty queue while nothing plays would silently do
    // nothing visible, so it starts playing instead.
    if (queue.length === 0 && !playing) {
      playList([item])
      return
    }
    queue = [...queue, item]
  }

  const dequeue = (index: number) => {
    const absolute = queueIndex + 1 + index
    queue = queue.filter((_, i) => i !== absolute)
  }

  const clearQueue = () => {
    queue = playing ? [playing.item] : []
    queueIndex = 0
  }

  /** Called when a film ends with no next episode to roll into. */
  function queueAdvance() {
    const next = queue[queueIndex + 1]
    if (!next) {
      closePlayer()
      return
    }
    queueIndex += 1
    playing = null
    startPlay(next, readQuality(), false, undefined, true)
  }

  async function startPlay(item: Item, quality: PlayQuality = readQuality(), fromStart = false, audioTrack?: number, keepQueue = false) {
    // Anything the viewer starts directly is a fresh queue of one; only the
    // queue itself plays on without resetting it.
    if (!keepQueue) {
      queue = [item]
      queueIndex = 0
    }
    try {
      const info = await playItem(item.id, quality, fromStart, audioTrack)
      music = null // video and music are mutually exclusive
      playingPos = info.positionSecs || 0
      playing = { item, info, quality, gen: ++playGen }
      const inContext = detail && (detail.item.id === item.id || detail.item.id === item.parentId)
      if (!inContext) showItem(item) // full player lives on the item's (or its season's) page
    } catch (e) {
      console.error('play failed', e)
    }
  }

  function closePlayer() {
    playing = null
    poll() // refresh continue-watching
  }

  function playNext(next: Item) {
    playing = null
    startPlay(next, readQuality(), false, undefined, true)
  }

  function changeQuality(q: PlayQuality) {
    try {
      localStorage.setItem('cineva-quality', q)
    } catch {
      /* private browsing */
    }
    if (playing) startPlay(playing.item, q, false, undefined, true)
  }

  // Switching audio track restarts the session with that stream, the same
  // way a quality change does — the transcoder picks one track per session.
  function changeAudioTrack(streamIndex: number) {
    if (playing) startPlay(playing.item, playing.quality, false, streamIndex, true)
  }

  async function openLibrary(lib: Library) {
    currentLib = lib
    detail = null
    trail = []
    gridItems = [] // never show the previous library's tiles under the new name
    musicAll = []
    libTab = 'recommended'
    gridQuery = ''
    activeLetter = ''
    genre = null
    genreItems = []
    itemsMark = ''
    loadGenres(lib)
    loadCollections(lib)
    loadRecommended(lib)
    const items = await libraryItems(lib.id)
    if (lib.mediaType === 'music') {
      musicAll = items
      selArtist = 'ALL'
      selAlbumId = null
      gridItems = []
    } else {
      gridItems = items.filter(gridFilter(lib))
    }
    syncURL()
  }

  // fromChild: opened via the current detail's children list (drill-down);
  // otherwise the trail is rebuilt by walking the parent chain, so direct
  // jumps (resume, search) still show the full path.
  // known: the Item the caller already has (a grid tile, a children list).
  // Passing it paints the page on the click instead of after the fetch.
  async function openItem(id: string, fromChild = false, known?: Item) {
    const parent = fromChild && detail ? detail.item : null
    const baseTrail = trail // captured: the instant paint below may extend it

    // Paint from what we already know so the click lands at once; children,
    // play state and breadcrumbs fill in when the fetch returns. Episodes are
    // excluded — they redirect to their season, so painting the episode first
    // would flash a page we are about to leave.
    if (known && known.kind !== 'episode') {
      detail = { item: known, children: [] }
      epDetail = null
      detailSubs = []
      if (parent) trail = [...baseTrail, parent]
      syncURL()
    }

    let d = await itemDetail(id)
    // Seasons are the last navigation level: opening an episode opens its
    // season with the episode selected in-page.
    let selectEp: Item | null = null
    if (d.item.kind === 'episode' && d.item.parentId) {
      selectEp = d.item
      d = await itemDetail(d.item.parentId)
    }
    detail = d
    epDetail = null
    if (parent && !selectEp) {
      trail = [...baseTrail, parent]
    } else {
      // Each parent id comes from the item before it, so this walk cannot be
      // parallelised — but the page is already painted, so it only delays the
      // breadcrumbs rather than the content.
      const ancestors: Item[] = []
      let parentId = detail.item.parentId
      while (parentId && ancestors.length < 4) {
        try {
          const p = (await itemDetail(parentId)).item
          ancestors.unshift(p)
          parentId = p.parentId
        } catch {
          break
        }
      }
      trail = ancestors
    }
    detailSubs = []
    if (detail.item.kind === 'movie') {
      itemSubtitles(detail.item.id).then((s) => (detailSubs = s)).catch(() => {})
    }
    if (selectEp) selectEpisode(selectEp)
    syncURL()
  }

  // ---- Episode selection within a season page (accordion panel) ----
  let epDetail: ItemDetail | null = $state(null)
  let epSubs: SubtitleTrack[] = $state([])

  async function selectEpisode(ep: Item) {
    if (epDetail?.item.id === ep.id) {
      epDetail = null // toggle off
      return
    }
    epSubs = []
    epDetail = await itemDetail(ep.id)
    itemSubtitles(ep.id).then((s) => (epSubs = s)).catch(() => {})
  }

  const canResume = (d: ItemDetail | null) =>
    !!d?.playState && d.playState.positionSecs > 10 && !d.playState.watched
  const minsLeft = (d: ItemDetail | null) =>
    d && d.playState && d.item.runtimeSecs
      ? Math.max(1, Math.round((d.item.runtimeSecs - d.playState.positionSecs) / 60))
      : 0

  const resumable = $derived(canResume(detail))
  const minutesLeft = $derived(minsLeft(detail))

  function toLibraries() {
    currentLib = null
    detail = null
    epDetail = null
    trail = []
    gridItems = []
    tab = 'libraries'
    syncURL()
  }

  // Clicking the library crumb returns to the library itself, so it also
  // leaves whichever view (genre, collection) you had drilled into —
  // otherwise the crumb would be a no-op from inside one.
  function toGrid() {
    detail = null
    epDetail = null
    trail = []
    libTab = 'recommended'
    genre = null
    collection = null
    syncURL()
  }

  async function toCrumb(index: number) {
    const target = trail[index]
    detail = await itemDetail(target.id)
    trail = trail.slice(0, index)
    epDetail = null
    syncURL()
  }

  function runtime(secs?: number): string {
    if (!secs) return ''
    const m = Math.round(secs / 60)
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`
  }

  // Grouped by role — "Director: Phil Lord, Christopher Miller" — rather
  // than one entry per credit, which repeats the role for every co-director
  // and reads as noise. Producer credits are dropped: a big film has a
  // dozen and nobody scans a detail page for them.
  const CREW_ROLES = ['Director', 'Screenplay', 'Writer', 'Story', 'Novel', 'Director of Photography', 'Original Music Composer']

  function crewByRole(crew: PersonT[]): { role: string; people: PersonT[] }[] {
    const groups = new Map<string, PersonT[]>()
    for (const c of crew) {
      if (!c.role || !CREW_ROLES.includes(c.role)) continue
      const list = groups.get(c.role) ?? []
      if (!list.some((p) => p.id === c.id)) list.push(c)
      groups.set(c.role, list)
    }
    return CREW_ROLES.filter((r) => groups.has(r)).map((r) => ({ role: r, people: groups.get(r)! }))
  }

  // "2160p HEVC · 28.4 GB" — enough to tell two copies apart at a glance.
  function versionLabel(v: Item): string {
    const parts: string[] = []
    if (v.height) parts.push(`${v.height}p`)
    if (v.videoCodec) parts.push(v.videoCodec.toUpperCase())
    if (v.sizeBytes) parts.push(fmtSize(v.sizeBytes))
    return parts.join(' · ') || 'Version'
  }

  const fmtSize = (b: number) =>
    b >= 1e9 ? `${(b / 1e9).toFixed(1)} GB` : `${Math.round(b / 1e6)} MB`

  // Marking watched is optimistic: the flag flips at once and the server
  // confirms. Both directions clear the resume position, so the Resume
  // button and "min left" follow from the same state.
  async function toggleWatched() {
    if (!detail) return
    const item = detail.item
    const before = detail
    const next = !detail.playState?.watched
    detail = { ...detail, playState: { itemId: item.id, positionSecs: 0, watched: next } }
    try {
      await setWatched(item.id, next)
      continueWatching().then((r) => (resume = r)).catch(() => {})
    } catch (err) {
      detail = before
      console.error('mark watched failed', err)
    }
  }

  /**
   * Toggle watched straight from a grid tile, so marking a stack of films
   * seen does not mean opening each one. Optimistic: the tile flips at once
   * and reverts if the server refuses.
   */
  async function toggleWatchedOn(item: Item, e: Event) {
    // The tile itself is a button that opens the film; this sits inside it.
    e.stopPropagation()
    e.preventDefault()
    const next = !item.watched
    const before = gridItems
    gridItems = gridItems.map((i) => (i.id === item.id ? { ...i, watched: next, positionSecs: 0 } : i))
    // The genre, collection and person lists hold their own copies.
    const patch = (list: Item[]) => list.map((i) => (i.id === item.id ? { ...i, watched: next, positionSecs: 0 } : i))
    genreItems = patch(genreItems)
    collItems = patch(collItems)
    try {
      await setWatched(item.id, next)
      continueWatching().then((r) => (resume = r)).catch(() => {})
    } catch (err) {
      gridItems = before
      console.error('mark watched failed', err)
    }
  }

  // Refetch the open library's items in place, without disturbing the view
  // (selected artist, expanded album, breadcrumbs stay put). The grid is
  // keyed by item id, so a scan's new items appear without re-rendering the
  // tiles already on screen.
  //
  // A running scan fires refetch hints about once a second. Only one request
  // is ever in flight: a hint arriving during one sets refreshPending and is
  // served by a single trailing refetch, so a big library that takes longer
  // than a second to fetch self-throttles instead of queueing up requests.
  let refreshing = false
  let refreshPending = false
  // The high-water mark of the last items response, so a refetch during a
  // scan asks only for what changed. Reset whenever the library changes.
  let itemsMark = $state('')

  async function refreshLibraryItems(full = false) {
    if (!currentLib) return
    if (refreshing) {
      refreshPending = true
      return
    }
    refreshing = true
    const lib = currentLib
    try {
      const { items, mark } = await libraryItemsSince(lib.id, full ? undefined : itemsMark || undefined)
      // The library may have been switched while the request was in flight;
      // applying another library's items would blank the grid.
      if (currentLib?.id !== lib.id) return
      itemsMark = mark || itemsMark

      const merge = (prev: Item[], incoming: Item[]) => {
        if (incoming.length === 0) return prev
        const byID = new Map(prev.map((i) => [i.id, i]))
        for (const i of incoming) byID.set(i.id, i)
        return [...byID.values()]
      }
      // A delta is merged into what is on screen; a full fetch replaces it,
      // which is also how rows that were pruned or folded into a version
      // finally disappear.
      const next = full || !itemsMark ? items : merge(lib.mediaType === 'music' ? musicAll : gridItems, items)
      if (lib.mediaType === 'music') musicAll = next
      else gridItems = next.filter(gridFilter(lib))
    } finally {
      refreshing = false
      if (refreshPending) {
        refreshPending = false
        refreshLibraryItems().catch(() => {})
      }
    }
  }

  // boot runs once signed in: data loads, routing, the live event stream.
  let booted = false
  let teardown: (() => void) | null = null
  function boot() {
    if (booted) return
    booted = true
    poll()
    applyRoute(location.pathname)
    if (tab === 'settings') loadKeys()
    const onPop = () => applyRoute(location.pathname)
    window.addEventListener('popstate', onPop)
    // Live updates over SSE; the slow poll below is only the fallback for
    // anything a dropped event missed.
    const closeEvents = subscribeEvents(
      {
        libraries: () => {
          listLibraries().then((l) => (libraries = l)).catch(() => {})
          continueWatching().then((r) => (resume = r)).catch(() => {})
          // Scan end: pruning and version grouping have run, and neither is
          // expressible as a delta (a removed row simply stops existing), so
          // this is the one refetch that must be a full one.
          if (currentLib) refreshLibraryItems(true).catch(() => {})
        },
        // Fired throughout a scan as items land, so the grid fills in live.
        items: (data) => {
          const e = data as { libraryId?: string } | null
          if (e?.libraryId && currentLib?.id === e.libraryId) {
            refreshLibraryItems().catch(() => {})
          }
        },
      },
      (connected) => {
        if (connected && offline) poll()
      },
    )
    const id = setInterval(poll, 30000)
    teardown = () => {
      clearInterval(id)
      closeEvents()
      window.removeEventListener('popstate', onPop)
    }
  }

  onMount(() => {
    listThemes().then((t) => (themes = t)).catch(() => {})
    checkAuth().then(() => { if (gate === 'in') boot() })
    // Any 401 from the API means the session ended: back to sign-in.
    const onUnauthorised = () => { if (gate === 'in') { me = null; picked = null; gate = 'login'; loadProfiles() } }
    window.addEventListener(UNAUTHORISED, onUnauthorised)
    return () => {
      teardown?.()
      window.removeEventListener(UNAUTHORISED, onUnauthorised)
    }
  })
</script>

{#snippet poster(item: Item)}
  {#if item.poster}
    <img class="poster" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'} src={item.poster} alt={item.title} loading="lazy" />
  {:else}
    <div class="poster placeholder" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'}>
      <span>
        {#if item.kind === 'album' || item.kind === 'track' || item.kind === 'artist'}
          <MusicIcon size={26} />
        {:else if item.kind === 'series' || item.kind === 'season' || item.kind === 'episode'}
          <Tv size={26} />
        {:else}
          <Clapperboard size={26} />
        {/if}
      </span>
    </div>
  {/if}
{/snippet}

{#snippet personPanel()}
  {#if person}
    <div class="pback" role="presentation" onclick={closePerson}></div>
    <div class="ppanel" role="dialog" aria-modal="true" aria-label={person.name}>
      <div class="phead">
        {#if person.profile}
          <img class="headshot lg" src={person.profile} alt="" />
        {:else}
          <span class="headshot lg ph"><User size={26} /></span>
        {/if}
        <div>
          <h2>{person.name}</h2>
          <p class="muted small">{personSummary}</p>
        </div>
        <button class="ctlx" onclick={closePerson} aria-label="Close"><X size={16} /></button>
      </div>
      {#if person.credits.length === 0}
        <p class="muted">Nothing else here with them yet.</p>
      {:else}
        {#if personActing.length > 0}
          <h3 class="psec">
            {personCrew.length > 0 ? 'Acting' : 'Films'}
            <span class="muted">{personActing.length}</span>
          </h3>
          {@render filmography(personActing)}
        {/if}
        {#if personCrew.length > 0}
          <h3 class="psec">Crew <span class="muted">{personCrew.length}</span></h3>
          {@render filmography(personCrew)}
        {/if}
      {/if}
    </div>
  {/if}
{/snippet}

<!-- A filmography row shows the role, which is the point of a person page:
     "Ryland Grace" or "Director" is what distinguishes one credit from
     another, and a bare poster grid loses it. -->
{#snippet filmography(credits: PersonCredit[])}
  <div class="grid">
    {#each credits as c (c.id + c.creditKind)}
      <button class="cell" onclick={() => { closePerson(); openItem(c.id, false, c) }}>
        <span class="posterwrap">
          {@render poster(c)}
          {#if c.watched}<span class="seen" title="Watched"><Check size={13} /></span>{/if}
        </span>
        <span class="cell-title">{c.title}</span>
        <span class="cell-sub">
          {c.year ?? ''}{#if c.role}<span class="prole">{c.role}</span>{/if}
        </span>
      </button>
    {/each}
  </div>
{/snippet}

{#snippet cell(item: Item)}
  <button class="cell" onclick={() => openItem(item.id, false, item)}>
    <span class="posterwrap">
      {@render poster(item)}
      {#if item.kind === 'movie' || item.kind === 'episode'}
        <!-- Always in the DOM so it is reachable by keyboard and on touch,
             where there is no hover to reveal it; CSS fades it in on hover
             for pointer devices. -->
        <span
          class="seentoggle"
          class:on={item.watched}
          role="button"
          tabindex="0"
          aria-label={item.watched ? `Mark ${item.title} unwatched` : `Mark ${item.title} watched`}
          title={item.watched ? 'Watched — click to unmark' : 'Mark watched'}
          onclick={(e) => toggleWatchedOn(item, e)}
          onkeydown={(e) => { if (e.key === 'Enter' || e.key === ' ') toggleWatchedOn(item, e) }}
        >
          <Check size={13} />
        </span>
      {:else if item.watched}
        <span class="seen" title="Watched"><Check size={13} /></span>
      {/if}
      {#if progressPct(item) > 0}
        <span class="resume" title="{progressPct(item)}% watched"><span style="width: {progressPct(item)}%"></span></span>
      {/if}
    </span>
    <span class="cell-title">{item.title}</span>
    <span class="cell-sub">
      {#if item.kind === 'album'}{item.artist}{#if item.year} · {item.year}{/if}{:else if item.year}{item.year}{/if}
    </span>
  </button>
{/snippet}

{#if gate !== 'in'}
  <div class="gate">
    {#if gate === 'checking'}
      <span class="muted">Connecting…</span>
    {:else if gate === 'login' && loginMode === 'picker' && profiles.length > 0 && !usePassword && !picked}
      <div class="picker">
        <h1>Who's watching?</h1>
        <div class="tiles">
          {#each profiles as p (p.id)}
            <button class="tile" onclick={() => pickProfile(p)}>
              <span class="tile-avatar">{p.displayName.slice(0, 1).toUpperCase()}</span>
              <span>{p.displayName}</span>
            </button>
          {/each}
        </div>
        <button class="link" onclick={() => (usePassword = true)}>Sign in with a username and password</button>
      </div>
    {:else if gate === 'login' && picked}
      <form class="pinpad" onsubmit={picked.hasPin ? submitPIN : submitPickedPassword}>
        <span class="tile-avatar big">{picked.displayName.slice(0, 1).toUpperCase()}</span>
        <h1>{picked.displayName}</h1>
        {#if picked.hasPin}
          <p>Enter your PIN</p>
          <input type="password" inputmode="numeric" pattern="[0-9]*" maxlength="6" autocomplete="one-time-code" bind:value={pin} required aria-label="PIN" />
        {:else}
          <p>Enter your password</p>
          <input type="password" class="pw" autocomplete="current-password" bind:value={authPass} required aria-label="Password" />
        {/if}
        {#if authErr}<span class="err">{authErr}</span>{/if}
        <button class="go" type="submit" disabled={authBusy || (picked.hasPin ? pin.length < 4 : !authPass)}>{authBusy ? '…' : 'Sign in'}</button>
        <button class="link" type="button" onclick={() => { picked = null; authErr = '' }}>Not you? Choose another profile</button>
        {#if picked.hasPin && picked.hasPassword}<button class="link" type="button" onclick={() => { authUser = picked?.username ?? ''; picked = null; usePassword = true }}>Use my password instead</button>{/if}
      </form>
    {:else}
      <form onsubmit={submitAuth}>
        <h1>Cineva</h1>
        {#if gate === 'setup'}
          <p>Welcome. Create the first account — it will be the administrator.</p>
          <input placeholder="Display name" bind:value={authName} autocomplete="name" />
        {:else}
          <p>Sign in to continue.</p>
        {/if}
        <input placeholder="Username" bind:value={authUser} autocomplete="username" required />
        <input type="password" placeholder={gate === 'setup' ? 'Password (8+ characters)' : 'Password or PIN'} bind:value={authPass} autocomplete={gate === 'setup' ? 'new-password' : 'current-password'} minlength={gate === 'setup' ? 8 : undefined} required />
        {#if authErr}<span class="err">{authErr}</span>{/if}
        <button class="go" type="submit" disabled={authBusy}>{authBusy ? '…' : gate === 'setup' ? 'Create admin account' : 'Sign in'}</button>
        {#if gate === 'login' && loginMode === 'picker' && profiles.length > 0}<button class="link" type="button" onclick={() => { usePassword = false; authErr = '' }}>Back to profiles</button>{/if}
      </form>
    {/if}
  </div>
{/if}

<div class="shell">
  <div class="topbar">
    <button class="logo" onclick={() => setTab('home')}>Cineva</button>
    <div class="search">
      <input
        type="search"
        placeholder="Search…"
        bind:value={searchQ}
        oninput={onSearchInput}
        onkeydown={(e) => e.key === 'Escape' && clearSearch()}
      />
      {#if searchResults.length > 0}
        <div class="search-results">
          {#each searchResults as r (r.id)}
            {@const open = () => { if (r.kind === 'track') playTrackResult(r); else showItem(r); clearSearch() }}
            <div class="sr" role="button" tabindex="0" onclick={open} onkeydown={(e) => e.key === 'Enter' && open()}>
              {#if r.poster}
                <img src={r.poster} alt="" />
              {:else}
                <span class="sr-ph">{r.title.slice(0, 1)}</span>
              {/if}
              <span class="sr-text">
                <span class="sr-title">{r.title}</span>
                <span class="sr-sub">
                  {r.kind}{r.year ? ` · ${r.year}` : ''}{r.kind === 'episode' ? ` · S${String(r.seasonNum).padStart(2, '0')}E${String(r.episodeNum).padStart(2, '0')}` : ''}{r.kind === 'track' && r.runtimeSecs ? ` · ${trackTime(r.runtimeSecs)}` : ''}
                </span>
              </span>
              {#if r.kind === 'track'}
                <span class="sr-actions">
                  <button class="sr-act" onclick={(e) => { e.stopPropagation(); goToSong(r); clearSearch() }}>Song</button>
                  <button class="sr-act" onclick={(e) => { e.stopPropagation(); openAlbumOf(r); clearSearch() }}>Album</button>
                  <button class="sr-act" onclick={(e) => { e.stopPropagation(); goToArtistOf(r); clearSearch() }}>Artist</button>
                </span>
              {:else if r.kind === 'album'}
                <span class="sr-actions">
                  <button class="sr-act" onclick={(e) => { e.stopPropagation(); goToArtistOf(r); clearSearch() }}>Artist</button>
                </span>
              {/if}
            </div>
          {/each}
        </div>
      {/if}
    </div>
    {#if downloads.length > 0}
      <div class="dlwrap">
        <button class="topbtn" class:active={showDownloads} onclick={() => (showDownloads = !showDownloads)} title="Downloads">
          <Download size={16} />
          <span class="dlbadge">{downloads.length}</span>
        </button>
        {#if showDownloads}
          <div class="dlpanel">
            <div class="dlhead">
              <span>Downloads</span>
              <button class="dlclear" onclick={() => { downloads = []; showDownloads = false }}>Clear</button>
            </div>
            <p class="dlnote muted">Files are handed to your browser — see its download list for progress.</p>
            {#each downloads as d (d.id)}
              <div class="dlrow">
                <span class="dltitle">{d.title}</span>
                <span class="muted">{d.status === 'sent' ? 'sent to browser' : 'queued…'}</span>
              </div>
            {/each}
          </div>
        {/if}
      </div>
    {/if}
    <span class="status" class:offline title={offline ? 'Server unreachable' : `Connected · ${health?.version ?? ''}`}></span>
    <button class="topbtn" class:active={tab === 'settings'} onclick={() => setTab('settings')} title="Settings"><SettingsIcon size={17} /></button>
    <div class="userwrap">
      <button class="avatar" title={me ? `${me.displayName} (${me.username})` : 'Account'} onclick={() => (showUserMenu = !showUserMenu)}>
        {#if me}<span class="initial">{me.displayName.slice(0, 1).toUpperCase()}</span>{:else}<User size={17} />{/if}
      </button>
      {#if showUserMenu && me}
        <div class="usermenu">
          <div class="um-name">{me.displayName}</div>
          <div class="um-sub">{me.username} · {me.role}</div>
          {#if me.role === 'admin'}<a class="um-item" href="/admin/">Admin</a>{/if}
          <button class="um-item" onclick={() => { showUserMenu = false; setTab('settings'); loadKeys() }}><KeyRound size={14} /> Account settings</button>
          <button class="um-item" onclick={signOut}><LogOut size={14} /> Sign out</button>
        </div>
      {/if}
    </div>
  </div>

  <div class="body">
  <aside class="side">
    <button class="side-item" class:active={tab === 'home'} onclick={() => setTab('home')}><House size={16} /> Home</button>
    <div class="tree">
      {#each libraryTree as group (group.type)}
        {@const TypeIcon = group.icon}
        <div class="type-label"><TypeIcon size={15} /> {group.label}</div>
        {#each group.libs as lib (lib.id)}
          <button
            class="side-item leaf"
            class:active={tab === 'libraries' && currentLib?.id === lib.id}
            onclick={() => { tab = 'libraries'; openLibrary(lib) }}
          >{lib.name}</button>
        {/each}
      {/each}
      {#if libraries.length === 0}
        <div class="type-label">No libraries yet</div>
      {/if}
    </div>
    <div class="side-status side-bottom">
      <!-- Connection state only. The build version is a maintainer's
           detail, not something a viewer needs on every screen; it is still
           on the settings page and at /api/v1/health. -->
      <span class="status" class:offline title={offline ? 'Server unreachable' : 'Connected'}></span>
      {#if offline}<span>offline</span>{/if}
    </div>
  </aside>

  <div class="content">
  <main class:withbar={(playing && playerCompact) || !!music}>
    {#if tab === 'libraries' && currentLib && !listening}
      <nav class="crumbs" aria-label="Breadcrumb">
        <button class="crumb" onclick={toLibraries}>Libraries</button>
        <span class="sep">›</span>
        {#if detail}
          <button class="crumb" onclick={toGrid}>{currentLib.name}</button>
          {#each viewCrumbs as c (c.label)}
            <span class="sep">›</span>
            <button class="crumb" onclick={c.go}>{c.label}</button>
          {/each}
          {#each trail as ancestor, i (ancestor.id)}
            <span class="sep">›</span>
            <button class="crumb" onclick={() => toCrumb(i)}>{ancestor.title}</button>
          {/each}
          <span class="sep">›</span>
          <span class="crumb-current">{detail.item.title}</span>
        {:else if viewCrumbs.length > 0}
          <button class="crumb" onclick={toGrid}>{currentLib.name}</button>
          {#each viewCrumbs as c, i (c.label)}
            <span class="sep">›</span>
            {#if i === viewCrumbs.length - 1}
              <span class="crumb-current">{c.label}</span>
            {:else}
              <button class="crumb" onclick={c.go}>{c.label}</button>
            {/if}
          {/each}
        {:else}
          <span class="crumb-current">{currentLib.name}</span>
        {/if}
      </nav>
    {/if}
    {#if music}
      {#key music.gen}
        <MusicPlayer
          bind:this={mpRef}
          queue={music.queue}
          startIndex={music.startIndex}
          startShuffle={music.shuffle}
          onclose={() => { music = null; musicNow = null; if (listening) closeListening() }}
          onlocate={openAlbumOf}
          onexpand={openListening}
          onchange={onTrackChange}
          ontime={(t) => (musicPos = t)}
        />
      {/key}
    {/if}
    {#if playing}
      {#key playing.gen}
        <Player
          item={playing.item}
          info={playing.info}
          quality={playing.quality}
          compact={playerCompact}
          onclose={closePlayer}
          onnext={playNext}
          onended={queueAdvance}
          onquality={changeQuality}
          onaudiotrack={changeAudioTrack}
          onexpand={() => playing && showItem(playing.item)}
          ontime={(t) => (playingPos = t)}
        />
      {/key}
      {#if upNext.length > 0 && !playerCompact}
        <section class="queue" aria-label="Play queue">
          <header>
            <strong>Up next</strong>
            <span class="muted small">{upNext.length} queued</span>
            <span class="qspacer"></span>
            <button class="linkish" onclick={clearQueue}>Clear</button>
          </header>
          <ol>
            {#each upNext as q, i (q.id)}
              <li>
                <span class="qpos muted">{i + 1}</span>
                <button class="qtitle" onclick={() => playList(queue, queueIndex + 1 + i)}>
                  {q.title}{#if q.year}<span class="qyear muted">{q.year}</span>{/if}
                </button>
                <button class="qdrop" onclick={() => dequeue(i)} aria-label="Remove {q.title} from the queue">
                  <X size={14} />
                </button>
              </li>
            {/each}
          </ol>
        </section>
      {/if}
    {/if}
    {#if listening && music && musicNow}
      {@const albumId = musicNow.track.parentId ?? ''}
      {@const albumTracks = music.queue.filter((t) => t.parentId === albumId)}
      {@const album = sessionAlbums.get(albumId)}
      {@const queueAlbums = [...new Map(music.queue.map((t) => [t.parentId ?? '', t])).values()]}
      {@const slib = libraries.find((l) => l.id === musicNow.track.libraryId)}
      <section class="session">
        <nav class="crumbs" aria-label="Breadcrumb">
          <button class="crumb" onclick={() => { listening = false; toLibraries() }}>Libraries</button>
          {#if slib}
            <span class="sep">›</span>
            <button class="crumb" onclick={() => slib && leaveSessionToLibrary(slib)}>{slib.name}</button>
          {/if}
          {#if album?.parentId && album.artist}
            <span class="sep">›</span>
            <button class="crumb" onclick={() => slib && album && leaveSessionToArtist(slib, album)}>{album.artist}</button>
          {/if}
          <span class="sep">›</span>
          <span class="crumb-current">{album?.title ?? musicNow.track.title}</span>
        </nav>
        <div class="session-body">
          <div class="session-side">
            {#if musicNow.track.poster}
              <img class="session-cover" src={musicNow.track.poster} alt="" />
            {:else}
              <div class="session-cover ph"><MusicIcon size={64} /></div>
            {/if}
            <h2 class="session-album">{album?.title ?? ''}</h2>
            <p class="muted">{musicNow.track.artist}{album?.year ? ` · ${album.year}` : ''}</p>
            {#if queueAlbums.length > 1}
              <p class="muted small">Album {queueAlbums.findIndex((t) => (t.parentId ?? '') === albumId) + 1} of {queueAlbums.length} in this session</p>
            {/if}
          </div>
          <ul class="session-tracks">
            {#each albumTracks as t (t.id)}
              {@const qi = music.queue.findIndex((q) => q.id === t.id)}
              <li>
                <button class="strack" class:current={t.id === musicNow.track.id} onclick={() => mpRef?.jumpTo(qi)}>
                  <span class="tno">{t.trackNum || ''}</span>
                  <span class="tname">{t.title}</span>
                  <span class="ttm muted">{trackTime(t.runtimeSecs)}</span>
                  {#if t.id === musicNow.track.id && t.runtimeSecs}
                    <span class="strack-progress"><span style="width: {Math.min(100, (musicPos / t.runtimeSecs) * 100)}%"></span></span>
                  {/if}
                </button>
              </li>
            {/each}
          </ul>
        </div>
        {#if queueAlbums.length > 1}
          <div class="session-shelf">
            {#each queueAlbums as qa (qa.parentId ?? qa.id)}
              {@const firstIdx = music.queue.findIndex((q) => q.parentId === qa.parentId)}
              <button class="session-disc" class:cur={(qa.parentId ?? '') === albumId} onclick={() => mpRef?.jumpTo(firstIdx)} title={sessionAlbums.get(qa.parentId ?? '')?.title ?? ''}>
                {#if qa.poster}<img src={qa.poster} alt="" />{:else}<span class="ph"><MusicIcon size={20} /></span>{/if}
              </button>
            {/each}
          </div>
        {/if}
      </section>
    {:else if tab === 'home'}
      <h1>Home</h1>
      <section>
        <h2>Continue Watching</h2>
        {#if resume.length === 0}
          <div class="empty">Nothing in progress — start something from your libraries.</div>
        {:else}
          <div class="grid">
            {#each resume as r (r.id)}
              <button class="cell" onclick={() => startPlay(r)}>
                {@render poster(r)}
                <div class="progress"><div class="progress-fill" style="width: {Math.min(100, (r.positionSecs / (r.runtimeSecs || 1)) * 100)}%"></div></div>
                <span class="cell-title">{r.title}</span>
                {#if r.kind === 'episode'}<span class="cell-sub">S{String(r.seasonNum).padStart(2, '0')}E{String(r.episodeNum).padStart(2, '0')}</span>{/if}
              </button>
            {/each}
          </div>
        {/if}
      </section>
      <section>
        <h2>Libraries</h2>
        {#if libraries.length === 0}
          <div class="empty">No libraries yet — create one in the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="librow">
            {#each libraries as lib (lib.id)}
              <button class="libcard" onclick={() => { tab = 'libraries'; openLibrary(lib) }}>
                <strong>{lib.name}</strong>
                <span>{lib.itemCount} items</span>
              </button>
            {/each}
          </div>
        {/if}
      </section>
    {:else if tab === 'libraries'}
      {#if detail}
        {#if detail.item.backdrop || detail.item.poster}
          <!-- The film's own artwork behind the page. The backdrop is the
               wide still meant for this; the poster is a fallback and is
               blurred harder, since cropping a 2:3 image to a wide band
               magnifies it. -->
          <div
            class="pagebg"
            class:from-poster={!detail.item.backdrop}
            style="background-image: url({detail.item.backdrop || detail.item.poster})"
            aria-hidden="true"
          ></div>
        {/if}
        <div class="detail">
          {@render poster(detail.item)}
          <div class="detail-body">
            <h1>
              {detail.item.title}
              {#if detail.item.year}<span class="muted">({detail.item.year})</span>{/if}
            </h1>
            {#if detail.item.kind === 'season' || detail.item.kind === 'series'}
              <div class="actions">
                <button class="dl" onclick={() => detail && downloadAll(detail.item)}>
                  <Download size={14} /> Download {detail.item.kind === 'season' ? 'season' : 'show'}
                </button>
              </div>
            {/if}
            {#if detail.item.kind === 'album'}
              <p class="meta">{detail.item.artist} · {detail.children.length} tracks</p>
              <div class="actions">
                <button class="play" onclick={() => detail && startSession(detail.children, 0)}><Play size={15} /> Listen</button>
                <button class="dl" onclick={() => detail && startMusic(detail.children, 0)}>Queue</button>
                <button class="dl" onclick={() => detail && startMusic(detail.children, 0, true)}><Shuffle size={14} /> Shuffle</button>
                <select class="dlsel" title="Download album (ZIP)" onchange={(e) => { detail && dlNavigate(detail.item.id, (e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = '' }}>
                  <option value="">Download…</option>
                  <option value="original">Original (ZIP)</option>
                  <option value="mp3-320">MP3 320 (ZIP)</option>
                  <option value="mp3-192">MP3 192 (ZIP)</option>
                  <option value="ogg-192">OGG 192 (ZIP)</option>
                </select>
              </div>
            {/if}
            <p class="meta">
              {#if detail.item.kind === 'episode'}S{String(detail.item.seasonNum).padStart(2, '0')}E{String(detail.item.episodeNum).padStart(2, '0')} · {/if}
              {runtime(detail.item.runtimeSecs)}
              {#if detail.item.width}· {detail.item.width}x{detail.item.height} {detail.item.videoCodec}{/if}
            </p>
            {#if detail.item.overview}<p class="overview">{detail.item.overview}</p>{/if}
            {#if detail.item.kind === 'movie' || detail.item.kind === 'episode'}
              <div class="actions">
                <button class="play" onclick={() => detail && startPlay(detail.item)}>
                  <Play size={15} /> {resumable ? 'Resume' : 'Play'}
                </button>
                {#if resumable}
                  <span class="muted">{minutesLeft} min left</span>
                  <button class="dl" onclick={() => detail && startPlay(detail.item, undefined, true)}>
                    <RotateCcw size={14} /> Start over
                  </button>
                {/if}
                <button class="dl" onclick={toggleWatched} title={detail.playState?.watched ? 'Mark as unwatched' : 'Mark as watched'}>
                  {#if detail.playState?.watched}
                    <EyeOff size={14} /> Mark unwatched
                  {:else}
                    <Eye size={14} /> Mark watched
                  {/if}
                </button>
                {#if detail.item.kind === 'movie'}
                  <button class="dl" onclick={() => detail && enqueue(detail.item)} title="Play after what is queued">
                    Add to queue
                  </button>
                {/if}
                <a class="dl" href={downloadURL(detail.item.id)} download><Download size={14} /> Download</a>
              </div>
              {#if detail.versions && detail.versions.length > 1}
                <div class="versions">
                  <span class="muted small">This film is held {detail.versions.length} times:</span>
                  <div class="vchips">
                    {#each detail.versions as v (v.id)}
                      <button
                        class="chip" class:sel={v.id === detail.item.id}
                        onclick={() => openItem(v.id, false, v)}
                        title={v.path}>{versionLabel(v)}</button>
                    {/each}
                  </div>
                </div>
              {/if}
              {#if detail.item.tagline}
                <p class="tagline">{detail.item.tagline}</p>
              {/if}
              {#if detail.item.rating || detail.item.certification || detail.item.genres?.length}
                <p class="facts">
                  {#if detail.item.rating}
                    <span class="score" title="{detail.item.voteCount} votes on TMDB">★ {detail.item.rating.toFixed(1)}</span>
                  {/if}
                  {#if detail.item.certification}
                    <span class="cert" title="Content rating">{detail.item.certification}</span>
                  {/if}
                  {#if detail.item.genres?.length}
                    <span class="muted">{detail.item.genres.join(' · ')}</span>
                  {/if}
                  {#if detail.item.collectionName}
                    <span class="muted">· {detail.item.collectionName}</span>
                  {/if}
                </p>
              {/if}
              {#if detail.crew?.length}
                <dl class="crew">
                  {#each crewByRole(detail.crew) as g (g.role)}
                    <div>
                      <dt>{g.role}</dt>
                      <dd>
                        {#each g.people as c, i (c.id)}
                          {#if i > 0}<span class="muted">, </span>{/if}
                          <button class="linkish" onclick={() => openPerson(c.id)}>{c.name}</button>
                        {/each}
                      </dd>
                    </div>
                  {/each}
                </dl>
              {/if}
              <dl class="tech">
                <!-- An item that has not been probed has no height; "p (h264)"
                     reads as a glitch, so show just the codec. -->
                <div><dt>Video</dt><dd>{detail.item.height ? `${detail.item.height}p ` : ''}({detail.item.videoCodec})</dd></div>
                <div><dt>Audio</dt><dd>{(detail.item.audioCodec ?? '').toUpperCase()}</dd></div>
                <div>
                  <dt>Subtitles</dt>
                  <dd>{detailSubs.length ? detailSubs.map((s) => s.title || s.language || s.codec).join(' · ') : 'None'}</dd>
                </div>
              </dl>
            {/if}
          </div>
        </div>
        {#if detail.cast?.length}
          <section class="castsec">
            <h2>Cast</h2>
            <div class="castrow">
              {#each detail.cast as c (c.id)}
                <button class="castcard" onclick={() => openPerson(c.id)} title="More with {c.name}">
                  {#if c.profile}
                    <img class="headshot" src={c.profile} alt="" loading="lazy" />
                  {:else}
                    <span class="headshot ph"><User size={20} /></span>
                  {/if}
                  <span class="castname">{c.name}</span>
                  {#if c.role}<span class="castrole muted">{c.role}</span>{/if}
                </button>
              {/each}
            </div>
          </section>
        {/if}
        {#if detail.children.length > 0}
          {#if detail.item.kind === 'series'}
            <!-- Seasons as a poster grid -->
            <div class="grid seasons">
              {#each detail.children as child (child.id)}
                <button class="cell" onclick={() => openItem(child.id, true, child)}>
                  {@render poster(child)}
                  <span class="cell-title">{child.title}</span>
                </button>
              {/each}
            </div>
          {:else if detail.item.kind === 'season'}
            <!-- Episodes: terminal level — rows select an inline panel -->
            <ul class="eplist">
              {#each detail.children as child (child.id)}
                <li>
                  <button class="eprow" class:sel={epDetail?.item.id === child.id} class:nowplaying={playing?.item.id === child.id} onclick={() => selectEpisode(child)}>
                    {#if child.poster}
                      <img class="epthumb" src={child.poster} alt="" loading="lazy" />
                    {:else}
                      <span class="epthumb ph"><Tv size={20} /></span>
                    {/if}
                    <span class="epmain">
                      <span><span class="epnum">{child.episodeNum}.</span> {child.title}</span>
                    </span>
                    {#if playing?.item.id === child.id}
                      <span class="ep-live" title="Now playing"><AudioLines size={16} /></span>
                    {/if}
                    <span class="muted">{runtime(child.runtimeSecs)}</span>
                    {#if playing?.item.id === child.id && child.runtimeSecs}
                      <span class="ep-progress"><span style="width: {Math.min(100, (playingPos / child.runtimeSecs) * 100)}%"></span></span>
                    {/if}
                  </button>
                  {#if epDetail && epDetail.item.id === child.id}
                    <div class="eppanel">
                      <p class="meta">
                        S{String(epDetail.item.seasonNum).padStart(2, '0')}E{String(epDetail.item.episodeNum).padStart(2, '0')}
                        · {runtime(epDetail.item.runtimeSecs)}
                        {#if epDetail.item.width}· {epDetail.item.width}x{epDetail.item.height} {epDetail.item.videoCodec}{/if}
                        {#if epSubs.length}· CC: {epSubs.map((s) => s.language || s.codec).join(', ')}{/if}
                      </p>
                      {#if epDetail.item.overview}<p class="overview">{epDetail.item.overview}</p>{/if}
                      <div class="actions">
                        <button class="play" onclick={() => epDetail && startPlay(epDetail.item)}>
                          <Play size={15} /> {canResume(epDetail) ? 'Resume' : 'Play'}
                        </button>
                        {#if canResume(epDetail)}
                          <span class="muted">{minsLeft(epDetail)} min left</span>
                          <button class="dl" onclick={() => epDetail && startPlay(epDetail.item, undefined, true)}>
                            <RotateCcw size={14} /> Start over
                          </button>
                        {/if}
                        <a class="dl" href={downloadURL(epDetail.item.id)} download><Download size={14} /> Download</a>
                      </div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {:else}
            <ul class="children">
              {#each detail.children as child, ci (child.id)}
                <li class:hl={child.id === hlTrackId}>
                  {#if child.kind === 'track'}
                    <button onclick={() => detail && startMusic(detail.children, ci)}>
                      <span>
                        <span class="epnum">{child.trackNum || ci + 1}.</span>
                        {child.title}
                        {#if child.artist && child.artist !== detail.item.artist}<span class="muted"> — {child.artist}</span>{/if}
                      </span>
                      <span class="muted">{child.runtimeSecs ? `${Math.floor(child.runtimeSecs / 60)}:${String(child.runtimeSecs % 60).padStart(2, '0')}` : ''}</span>
                    </button>
                  {:else}
                    <button onclick={() => openItem(child.id, true, child)}>
                      <span>
                        {child.title}
                        {#if child.kind === 'album' && child.year}<span class="muted"> ({child.year})</span>{/if}
                      </span>
                      <span class="muted">{runtime(child.runtimeSecs)}</span>
                    </button>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
      {:else if currentLib && currentLib.mediaType === 'music'}
        <h1>{currentLib.name}</h1>
        {#if musicAll.length === 0}
          <div class="empty">Nothing here yet — run a scan from the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="music">
            <aside class="mrail">
              <div class="rail-top">
                <input
                  class="rail-filter"
                  type="search"
                  placeholder="Filter artists…"
                  bind:value={railFilter}
                  onkeydown={(e) => e.key === 'Escape' && (railFilter = '')}
                />
                <button class="rail-item" class:sel={selArtist === 'ALL'} onclick={() => selectArtist('ALL')}>
                  All Artists <span class="rail-n">{mArtists.length}</span>
                </button>
                {#if compAlbumIds.size > 0}
                  <button class="rail-item" class:sel={selArtist === 'COMP'} onclick={() => selectArtist('COMP')}>
                    Compilations <span class="rail-n">{compAlbumIds.size}</span>
                  </button>
                {/if}
                <div class="rail-div"></div>
              </div>
              {#each railArtists as a (a.id)}
                <button class="rail-item" class:sel={selArtist === a.id} onclick={() => selectArtist(a.id)}>{a.title}</button>
              {/each}
              {#if railArtists.length === 0}
                <div class="rail-empty muted">No artists match</div>
              {/if}
            </aside>

            <section class="mcontent">
              <header class="mhead">
                <div>
                  <h2 class="mtitle">
                    {selArtist === 'ALL' ? 'All Artists' : selArtist === 'COMP' ? 'Compilations' : shelfGroups[0]?.title}
                  </h2>
                  <span class="muted">{shelfAlbumCount} albums</span>
                </div>
                <div class="mactions">
                  <input
                    class="mfilter"
                    type="search"
                    placeholder="Filter songs & albums…"
                    oninput={onMusicFilterInput}
                    onkeydown={(e) => { if (e.key === 'Escape') { musicFilter = ''; (e.target as HTMLInputElement).value = '' } }}
                  />
                  <button class="play" onclick={() => startSession(shelfQueue(), 0)}><Play size={15} /> Listen</button>
                  <button class="dl" onclick={() => startMusic(shelfQueue(), 0)}>Queue</button>
                  <button class="dl" onclick={() => startMusic(shelfQueue(), 0, true)}><Shuffle size={14} /> Shuffle</button>
                  {#if selArtist !== 'ALL' && selArtist !== 'COMP'}
                    <select class="dlsel" title="Download artist (ZIP)" onchange={(e) => { dlNavigate(selArtist, (e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = '' }}>
                      <option value="">Download…</option>
                      <option value="original">Original (ZIP)</option>
                      <option value="mp3-320">MP3 320 (ZIP)</option>
                      <option value="mp3-192">MP3 192 (ZIP)</option>
                      <option value="ogg-192">OGG 192 (ZIP)</option>
                    </select>
                  {:else}
                    <button class="dl" onclick={() => queueDownloads(shelfQueue())} title="Download all shown"><Download size={14} /></button>
                  {/if}
                </div>
              </header>

              {#if filterQ && matchedSongs.length > 0}
                <div class="msongs">
                  <div class="msongs-h">Songs</div>
                  {#each matchedSongs as m (m.t.id)}
                    <button class="msong" onclick={() => startMusic(m.tracks, m.idx)}>
                      <span class="tname">{m.t.title}</span>
                      <span class="muted">{m.album.artist} — {m.album.title}</span>
                      <span class="muted ttm">{trackTime(m.t.runtimeSecs)}</span>
                    </button>
                  {/each}
                </div>
              {:else if filterQ && filteredShelfGroups.length === 0}
                <div class="empty">No matches for “{musicFilter}”.</div>
              {/if}

              {#each filteredShelfGroups as group (group.id)}
                {#if selArtist === 'ALL'}
                  <button class="shelf-h" onclick={() => selectArtist(group.id)}>{group.title} <span class="muted">· {group.albums.length} albums</span></button>
                {/if}
                <div class="shelf">
                  {#each group.albums as album (album.id)}
                    <button class="acard" class:sel={selAlbumId === album.id} onclick={() => (selAlbumId = selAlbumId === album.id ? null : album.id)}>
                      {#if album.poster}
                        <img class="acover" src={album.poster} alt={album.title} loading="lazy" />
                      {:else}
                        <span class="acover ph"><MusicIcon size={30} /></span>
                      {/if}
                      <span class="aname">{album.title}</span>
                      <span class="asub">{album.year || ''}</span>
                    </button>
                  {/each}
                </div>
                {#if selAlbumId && group.albums.some((a) => a.id === selAlbumId)}
                  {@const album = albumById.get(selAlbumId)}
                  {@const tracks = tracksOf(selAlbumId)}
                  {#if album}
                    <div class="apanel">
                      {#if album.poster}
                        <img class="apanel-cover" src={album.poster} alt="" />
                      {:else}
                        <span class="apanel-cover ph"><MusicIcon size={40} /></span>
                      {/if}
                      <div class="apanel-body">
                        <div class="apanel-head">
                          <div>
                            <strong>{album.title}</strong>
                            <span class="muted"> — {album.artist}{album.year ? ` · ${album.year}` : ''} · {tracks.length} tracks</span>
                          </div>
                          <div class="mactions">
                            <button class="play" onclick={() => startSession(tracks, 0)}><Play size={15} /> Listen</button>
                            <button class="dl" onclick={() => startMusic(tracks, 0)}>Queue</button>
                            <button class="dl" onclick={() => startMusic(tracks, 0, true)}><Shuffle size={14} /></button>
                            <select class="dlsel" title="Download album (ZIP)" onchange={(e) => { selAlbumId && dlNavigate(selAlbumId, (e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = '' }}>
                              <option value="">Download…</option>
                              <option value="original">Original (ZIP)</option>
                              <option value="mp3-320">MP3 320 (ZIP)</option>
                              <option value="mp3-192">MP3 192 (ZIP)</option>
                              <option value="ogg-192">OGG 192 (ZIP)</option>
                            </select>
                          </div>
                        </div>
                        <ul class="tlist">
                          {#each tracks as t, ti (t.id)}
                            <li>
                              <button onclick={() => startMusic(tracks, ti)}>
                                <span class="tno">{t.trackNum || ti + 1}</span>
                                <span class="tname">{t.title}{#if t.artist && t.artist !== album.artist}<span class="muted"> — {t.artist}</span>{/if}</span>
                                <span class="muted ttm">{trackTime(t.runtimeSecs)}</span>
                              </button>
                            </li>
                          {/each}
                        </ul>
                      </div>
                    </div>
                  {/if}
                {/if}
              {/each}
            </section>
          </div>
        {/if}
      {:else if currentLib}
        <h1>{currentLib.name}</h1>
        {#if gridItems.length === 0}
          <div class="empty">Nothing here yet — run a scan from the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="libhead" class:hidden={headHidden}>
          <nav class="subtabs" aria-label="{currentLib.name} views">
            {#each [['recommended', 'Recommended'], ['library', 'Library'], ['genres', 'Genres'], ['collections', 'Collections']] as [id, label] (id)}
              <button
                class="subtab"
                class:on={libTab === id}
                aria-current={libTab === id ? 'page' : undefined}
                onclick={() => { libTab = id as LibTab; genre = null; collection = null; syncURL() }}>{label}</button>
            {/each}
          </nav>
          {#if libTab === 'library'}
            <div class="filters">
              <div class="fbar">
                <label class="ffield">
                  <Search size={15} />
                  <input
                    type="search"
                    placeholder="Filter by title, year or synopsis…"
                    bind:value={gridQuery}
                    aria-label="Filter {currentLib.name}"
                  />
                </label>
                <select class="fsel" bind:value={gridSort} aria-label="Sort order">
                  <option value="title">A–Z</option>
                  <option value="year">Newest first</option>
                  <option value="added">Recently added</option>
                  <option value="rating">Highest rated</option>
                  <option value="runtime">Longest first</option>
                  <option value="watched">Recently watched</option>
                </select>
                <select class="fsel" bind:value={gridWatch} aria-label="Watched filter">
                  <option value="all">All</option>
                  <option value="unwatched">Unwatched</option>
                  <option value="watched">Watched</option>
                </select>
                <select class="fsel" bind:value={gridMinRating} aria-label="Minimum rating">
                  <option value={0}>Any rating</option>
                  <option value={6}>★ 6+</option>
                  <option value={7}>★ 7+</option>
                  <option value={8}>★ 8+</option>
                </select>
              </div>
              <div class="fcount muted">
                {visibleItems.length}
                {visibleItems.length === 1 ? 'title' : 'titles'}
                {#if visibleItems.length !== gridItems.length}of {gridItems.length}{/if}
              </div>
            </div>
          {/if}
          </div>

          {#if libTab === 'recommended'}
            {#if resumeHere.length > 0}
              <section>
                <h2>Continue Watching</h2>
                <div class="grid">
                  {#each resumeHere as r (r.id)}
                    <button class="cell" onclick={() => startPlay(r)}>
                      {@render poster(r)}
                      <div class="progress"><div class="progress-fill" style="width: {Math.min(100, (r.positionSecs / (r.runtimeSecs || 1)) * 100)}%"></div></div>
                      <span class="cell-title">{r.title}</span>
                      {#if r.kind === 'episode'}<span class="cell-sub">S{String(r.seasonNum).padStart(2, '0')}E{String(r.episodeNum).padStart(2, '0')}</span>{/if}
                    </button>
                  {/each}
                </div>
              </section>
            {/if}
            <section>
              <h2>Recently Added</h2>
              <div class="grid">
                {#each recentlyAdded as item (item.id)}
                  {@render cell(item)}
                {/each}
              </div>
            </section>
            {#each recRows as row (row.kind + row.title)}
              <section>
                <h2>
                  {#if row.genre}
                    <button class="h2link" onclick={() => { libTab = 'genres'; openGenre(row.genre!) }}>
                      {row.title} <span class="muted">→</span>
                    </button>
                  {:else}
                    {row.title}
                  {/if}
                </h2>
                <div class="grid">
                  {#each row.items as item (item.id)}
                    {@render cell(item)}
                  {/each}
                </div>
              </section>
            {/each}
            <section>
              <h2>Browse</h2>
              <p class="muted">
                {gridItems.length} titles in {currentLib.name}.
                <button class="linkish" onclick={() => (libTab = 'library')}>Open the full library →</button>
              </p>
            </section>

          {:else if libTab === 'collections'}
            {#if collections.length === 0}
              <div class="empty tall">
                <p><strong>No collections yet.</strong></p>
                <p>
                  Collections come from the metadata provider — Harry Potter,
                  James Bond, The Lord of the Rings and the rest are grouped
                  automatically once their films are matched. A set appears here
                  when this library holds more than one of its titles.
                </p>
              </div>
            {:else if !collection}
              <div class="grid">
                {#each collections as c (c.key)}
                  <button class="cell" onclick={() => openCollection(c)}>
                    <span class="posterwrap">
                      {#if c.poster}
                        <img class="poster" src={c.poster} alt={c.name} loading="lazy" />
                      {:else}
                        <div class="poster placeholder"><span><Clapperboard size={26} /></span></div>
                      {/if}
                      {#if c.watched >= c.count}
                        <span class="seen" title="Collection complete"><Check size={13} /></span>
                      {:else}
                        <span class="collcount">{c.watched}/{c.count}</span>
                      {/if}
                      {#if c.watched > 0 && c.watched < c.count}
                        <span class="resume" title="{c.watched} of {c.count} watched">
                          <span style="width: {(c.watched / c.count) * 100}%"></span>
                        </span>
                      {/if}
                    </span>
                    <span class="cell-title">{c.name}</span>
                    <span class="cell-sub">
                      {#if c.watched >= c.count}Watched{:else if c.watched > 0}{c.count - c.watched} left{:else}{c.count} films{/if}
                    </span>
                  </button>
                {/each}
              </div>
            {:else}
              <div class="collhead">
                <p class="gcount muted">
                  {collItems.length} {collItems.length === 1 ? 'title' : 'titles'}
                  {#if collection.count > 0}
                    · {collection.watched} watched
                  {/if}
                </p>
                <div class="collacts">
                  {#if collection.nextUpId}
                    <button class="play" onclick={playCollection}>
                      <Play size={15} />
                      {collection.watched > 0 ? 'Continue' : 'Play'} collection
                      <span class="nextname">{collection.nextUpName}</span>
                    </button>
                  {:else}
                    <button class="play" onclick={() => playList(collItems, 0)}>
                      <Play size={15} /> Play from the start
                    </button>
                  {/if}
                  <button class="dl" onclick={() => collItems.forEach(enqueue)}>Add all to queue</button>
                </div>
              </div>
              {#if collItems.length === 0}
                <div class="empty">Nothing in this collection.</div>
              {:else}
                <div class="grid">
                  {#each collItems as item (item.id)}
                    {@render cell(item)}
                  {/each}
                </div>
              {/if}
            {/if}

          {:else if libTab === 'genres'}
            {#if genres.length === 0}
              <div class="empty tall">
                <p><strong>No genres yet.</strong></p>
                <p>Genres arrive with metadata — scan this library once a provider is configured.</p>
              </div>
            {:else if !genre}
              <div class="genres">
                {#each genres as g (g.genre)}
                  <button class="genre" onclick={() => openGenre(g.genre)}>
                    <span class="genre-name">{g.genre}</span>
                    <span class="genre-n muted">{g.count}</span>
                  </button>
                {/each}
              </div>
            {:else}
              <p class="gcount muted">{genreItems.length} {genreItems.length === 1 ? 'title' : 'titles'}</p>
              {#if genreItems.length === 0}
                <div class="empty">Nothing in this genre.</div>
              {:else}
                <div class="grid">
                  {#each genreItems as item (item.id)}
                    {@render cell(item)}
                  {/each}
                </div>
              {/if}
            {/if}

          {:else}
            {#if visibleItems.length === 0}
              <div class="empty">
                Nothing matches that filter.
                <button class="linkish" onclick={() => (gridQuery = '')}>Clear filter</button>
              </div>
            {:else if sections.length > 0}
              <div class="azwrap">
                <div class="azcol">
                  {#each sections as sec (sec.letter)}
                    <section class="azsec" id="az-{sec.letter}" data-letter={sec.letter}>
                      <h2 class="azhead">{sec.letter === '#' ? '0–9' : sec.letter}</h2>
                      <div class="grid">
                        {#each sec.items as item (item.id)}
                          {@render cell(item)}
                        {/each}
                      </div>
                    </section>
                  {/each}
                </div>
                <nav
                  class="azrail"
                  bind:this={railEl}
                  aria-label="Jump to initial"
                  onpointerdown={railDown}
                  onpointermove={railMove}
                  onpointerup={railUp}
                  onpointercancel={railUp}
                  onkeydown={railKey}
                >
                  {#each sections as sec, i (sec.letter)}
                    <button
                      class="azl"
                      class:on={activeLetter === sec.letter}
                      tabindex={i === railFocus ? 0 : -1}
                      aria-label="Jump to {sec.letter === '#' ? '0 to 9' : sec.letter}"
                      aria-current={activeLetter === sec.letter ? 'true' : undefined}
                      onfocus={() => (railFocus = i)}
                      onclick={() => jumpTo(sec.letter)}>{sec.letter === '#' ? '#' : sec.letter}</button>
                  {/each}
                </nav>
              </div>
            {:else}
              <div class="grid">
                {#each visibleItems as item (item.id)}
                  {@render cell(item)}
                {/each}
              </div>
            {/if}
          {/if}
        {/if}
      {:else}
        <h1>Libraries</h1>
        {#if libraries.length === 0}
          <div class="empty tall">
            <p><strong>No libraries yet.</strong></p>
            <p>Libraries are created in the <a href="/admin/">admin UI</a> — one media type each.</p>
          </div>
        {:else}
          <div class="librow">
            {#each libraries as lib (lib.id)}
              <button class="libcard" onclick={() => openLibrary(lib)}>
                <strong>{lib.name}</strong>
                <span>{lib.mediaType} · {lib.itemCount} items</span>
              </button>
            {/each}
          </div>
        {/if}
      {/if}
    {:else}
      <h1>Settings</h1>
      <section class="card">
        <div class="row">
          <span>Theme</span>
          <div class="theme-picker">
            {#each themes as t (t.id)}
              <button class="chip" class:sel={theme === t.id} onclick={() => setTheme(t.id)}>{t.name}</button>
            {/each}
          </div>
        </div>
        <div class="row">
          <span>Server</span>
          <span class="muted">{offline ? 'unreachable' : `${health?.service} ${health?.version}`}</span>
        </div>
        <div class="row col">
          <span>Password</span>
          <form class="inline" onsubmit={submitPassword}>
            <input type="password" placeholder="Current password" bind:value={pwCurrent} autocomplete="current-password" required />
            <input type="password" placeholder="New password (8+ characters)" bind:value={pwNew} autocomplete="new-password" minlength="8" required />
            <button class="chip" type="submit">Change</button>
          </form>
          {#if pwMsg}<span class="muted small">{pwMsg}</span>{/if}
        </div>
        <div class="row col">
          <span>Quick sign-in PIN <span class="muted small">— {me?.hasPin ? 'set: tap your name on the sign-in screen and enter it' : 'not set: 4–6 digits for the profile picker'}</span></span>
          <form class="inline" onsubmit={submitPIN2}>
            <input type="password" placeholder={me?.hasPassword ? 'Current password' : 'Current PIN'} bind:value={pinAuth} autocomplete="current-password" required />
            <input type="password" inputmode="numeric" pattern="[0-9]{4,6}" maxlength="6" placeholder="New PIN (4–6 digits)" bind:value={pinNew} required />
            <button class="chip" type="submit">{me?.hasPin ? 'Change PIN' : 'Set PIN'}</button>
            {#if me?.hasPin && me?.hasPassword}<button class="chip" type="button" onclick={(e) => submitPIN2(e, true)}>Remove PIN</button>{/if}
          </form>
          {#if pinMsg}<span class="muted small">{pinMsg}</span>{/if}
        </div>
        <div class="row col">
          <span>API keys <span class="muted small">— for apps and scripts; send as <code>Authorization: Bearer</code></span></span>
          {#if revealedKey}
            <div class="keyreveal">
              <span>New key — copy it now, it will not be shown again:</span>
              <code>{revealedKey}</code>
              <button class="chip" onclick={() => (revealedKey = '')}>Done</button>
            </div>
          {/if}
          {#each apiKeys as k (k.id)}
            <div class="keyrow">
              <span>{k.name}</span>
              <span class="muted small">created {k.createdAt.slice(0, 10)}{k.lastUsedAt ? ` · last used ${k.lastUsedAt.slice(0, 16)}` : ' · never used'}</span>
              <button class="chip" onclick={() => revokeKey(k.id)}>Revoke</button>
            </div>
          {/each}
          <form class="inline" onsubmit={addKey}>
            <input placeholder="Key name (e.g. Phone)" bind:value={newKeyName} />
            <button class="chip" type="submit">Create key</button>
          </form>
        </div>
        <div class="row">
          <span>Music streaming quality</span>
          <div class="theme-picker">
            {#each musicQualities as q (q.id)}
              <button class="chip" class:sel={musicQuality === q.id} onclick={() => setMusicQuality(q.id)}>{q.label}</button>
            {/each}
          </div>
        </div>
        <div class="row">
          <span>Next-episode autoplay</span>
          <div class="theme-picker">
            <button class="chip" class:sel={autoplay} onclick={() => setAutoplay(true)}>On</button>
            <button class="chip" class:sel={!autoplay} onclick={() => setAutoplay(false)}>Off</button>
          </div>
        </div>
      </section>

      <!-- Required by TMDB's terms wherever their data is used. -->
      <section class="card">
        <div class="row">
          <span>
            Metadata
            <span class="muted small">
              — film and programme details, artwork and cast come from
              <a href="https://www.themoviedb.org/" target="_blank" rel="noreferrer noopener">TMDB</a>.
            </span>
          </span>
        </div>
        <p class="muted small attrib">
          This product uses the TMDB API but is not endorsed or certified by TMDB.
        </p>
      </section>
    {/if}
  </main>

  {@render personPanel()}

  <nav class="tabbar">
    {#each tabs as t (t.id)}
      {@const TabIcon = t.icon}
      <button class:active={tab === t.id} onclick={() => setTab(t.id)}>
        <TabIcon size={19} />
        <span>{t.label}</span>
      </button>
    {/each}
  </nav>
  </div>
  </div>
</div>


<style>
  .shell { min-height: 100vh; display: flex; flex-direction: column; }
  .body { flex: 1; display: flex; }
  .content { flex: 1; min-width: 0; display: flex; flex-direction: column; }

  /* ---- Sign-in / first-run gate ---- */
  .gate { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; background: var(--bg); padding: 1rem; }
  .gate form {
    width: min(22rem, 100%); background: var(--panel); border: 1px solid var(--border); border-radius: 14px;
    padding: 1.6rem; display: flex; flex-direction: column; gap: 0.7rem;
  }
  .gate h1 { font-size: 1.6rem; margin-bottom: 0.2rem; background: linear-gradient(120deg, var(--accent), var(--accent-2)); -webkit-background-clip: text; background-clip: text; color: transparent; }
  .gate p { color: var(--muted); font-size: 0.85rem; margin-bottom: 0.4rem; }
  .gate input { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 0.6rem 0.75rem; font: inherit; }
  .gate input:focus { outline: none; border-color: var(--accent); }
  .gate .go { background: var(--accent); color: white; border-radius: 8px; padding: 0.65rem; font-weight: 600; margin-top: 0.3rem; }
  .gate .go:disabled { opacity: 0.5; }
  .gate .err { color: var(--err); font-size: 0.85rem; }
  .gate .link { color: var(--muted); font-size: 0.85rem; padding: 0.3rem; }
  .gate .link:hover { color: var(--text); }
  .picker { display: flex; flex-direction: column; align-items: center; gap: 1.5rem; text-align: center; }
  .picker h1 { font-size: 1.8rem; }
  .tiles { display: flex; flex-wrap: wrap; justify-content: center; gap: 1.2rem; max-width: 40rem; }
  .tile { display: flex; flex-direction: column; align-items: center; gap: 0.6rem; padding: 0.6rem; border-radius: 12px; color: var(--muted); font-size: 0.95rem; }
  .tile:hover { color: var(--text); background: var(--panel); }
  .tile-avatar {
    width: 5.5rem; height: 5.5rem; border-radius: 16px; display: grid; place-items: center;
    font-size: 2.2rem; font-weight: 600; color: white;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
  }
  .tile-avatar.big { align-self: center; }
  .pinpad { align-items: center; text-align: center; }
  .pinpad input { text-align: center; font-size: 1.6rem; letter-spacing: 0.6rem; width: 12rem; }
  .pinpad input.pw { font-size: 1rem; letter-spacing: normal; width: 16rem; }
  .userwrap { position: relative; }
  .avatar .initial { font-weight: 600; font-size: 0.85rem; }
  .usermenu {
    position: absolute; right: 0; top: calc(100% + 0.4rem); min-width: 12rem; z-index: 60;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.4rem;
    display: flex; flex-direction: column; box-shadow: 0 8px 24px color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .um-name { font-weight: 600; padding: 0.4rem 0.6rem 0; }
  .um-sub { color: var(--muted); font-size: 0.78rem; padding: 0 0.6rem 0.4rem; border-bottom: 1px solid var(--border); margin-bottom: 0.3rem; }
  .um-item { display: flex; align-items: center; gap: 0.5rem; padding: 0.45rem 0.6rem; border-radius: 6px; color: var(--text); text-align: left; text-decoration: none; font-size: 0.9rem; }
  .um-item:hover { background: var(--bg); }
  .row.col { flex-direction: column; align-items: stretch; gap: 0.5rem; }
  .inline { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .inline input { flex: 1; min-width: 10rem; background: var(--bg); border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 0.45rem 0.6rem; font: inherit; }
  .keyrow { display: flex; gap: 0.75rem; align-items: center; flex-wrap: wrap; }
  .keyrow span:first-child { font-weight: 500; }
  .keyreveal { display: flex; flex-direction: column; gap: 0.4rem; background: color-mix(in srgb, var(--accent) 12%, transparent); border-radius: 8px; padding: 0.6rem 0.8rem; }
  .keyreveal code { user-select: all; word-break: break-all; color: var(--accent-2); }

  /* ---- Persistent top bar: logo, universal search, profile ---- */
  :root { --topbar-h: 3.4rem; }
  .topbar {
    position: sticky;
    top: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 0.9rem;
    height: var(--topbar-h);
    padding: 0 1rem;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
  }
  .topbar .logo { flex: none; }
  .topbar .status { margin-left: auto; } /* pushes status + profile to the right edge */
  .search { position: relative; flex: 1; max-width: 30rem; }
  .search input {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    font: inherit;
    font-size: 0.88rem;
    padding: 0.45rem 1rem;
  }
  .search input:focus { outline: none; border-color: var(--accent); }
  .search-results {
    position: absolute;
    top: calc(100% + 0.4rem);
    left: 0;
    right: 0;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4);
    overflow: hidden;
    max-height: 60vh;
    overflow-y: auto;
  }
  .sr {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    width: 100%;
    padding: 0.5rem 0.8rem;
    text-align: left;
    cursor: pointer;
  }
  .sr:hover { background: var(--bg); }
  .sr-actions { margin-left: auto; display: flex; gap: 0.35rem; flex: none; }
  .sr-act {
    font-size: 0.72rem;
    color: var(--muted);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.15rem 0.55rem;
    white-space: nowrap;
  }
  .sr-act:hover { color: var(--text); border-color: var(--accent); }
  .sr img, .sr-ph {
    width: 2rem;
    height: 3rem;
    border-radius: 4px;
    object-fit: cover;
    flex: none;
  }
  .sr-ph {
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--muted);
    font-weight: 700;
  }
  .sr-text { display: flex; flex-direction: column; min-width: 0; }
  .sr-title { font-size: 0.9rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sr-sub { font-size: 0.76rem; color: var(--muted); }
  .dlwrap { position: relative; margin-left: auto; }
  .dlwrap + .status { margin-left: 0; }
  .dlbadge {
    position: absolute;
    top: -0.3rem;
    right: -0.3rem;
    background: var(--accent);
    color: #fff;
    border-radius: 999px;
    font-size: 0.62rem;
    min-width: 1rem;
    height: 1rem;
    display: grid;
    place-items: center;
    padding: 0 0.2rem;
  }
  .dlpanel {
    position: absolute;
    top: calc(100% + 0.5rem);
    right: 0;
    width: 21rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4);
    padding: 0.6rem 0.8rem;
    max-height: 60vh;
    overflow-y: auto;
    z-index: 60;
  }
  .dlhead { display: flex; justify-content: space-between; align-items: center; font-weight: 600; font-size: 0.9rem; }
  .dlclear { color: var(--accent-2); font-size: 0.8rem; }
  .dlnote { font-size: 0.75rem; margin: 0.25rem 0 0.5rem; }
  .dlrow { display: flex; justify-content: space-between; gap: 0.8rem; padding: 0.3rem 0; font-size: 0.85rem; border-top: 1px solid var(--border); }
  .dltitle { white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }

  .avatar, .topbtn {
    position: relative;
    width: 2.1rem;
    height: 2.1rem;
    border-radius: 50%;
    background: var(--bg);
    border: 1px solid var(--border);
    display: grid;
    place-items: center;
    font-size: 1rem;
    flex: none;
    color: var(--muted);
  }
  .avatar:hover, .topbtn:hover { border-color: var(--accent); color: var(--text); }
  .topbtn.active { border-color: var(--accent); color: var(--accent-2); }

  /* ---- Sidebar: desktop only; mobile keeps the bottom tab bar ---- */
  .side { display: none; }
  @media (min-width: 768px) {
    .side {
      display: flex;
      flex-direction: column;
      width: 15rem;
      flex: none;
      position: sticky;
      top: var(--topbar-h);
      height: calc(100vh - var(--topbar-h));
      overflow-y: auto;
      background: var(--panel);
      border-right: 1px solid var(--border);
      padding: 0.9rem 0.8rem;
      gap: 0.15rem;
    }
    .tabbar { display: none !important; }
  }
  main.withbar { padding-bottom: 6.5rem; }
  .side-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-align: left;
    padding: 0.5rem 0.65rem;
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.92rem;
  }
  .side-item:hover { background: var(--bg); color: var(--text); }
  .side-item.active { background: var(--bg); color: var(--accent-2); }
  .tree { display: flex; flex-direction: column; gap: 0.1rem; margin-top: 0.75rem; flex: 1; }
  .type-label {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    color: var(--muted);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.7rem 0.65rem 0.25rem;
  }
  .type-icon { margin-right: 0.35rem; font-size: 1.15rem; vertical-align: -0.2rem; }
  .side-item.leaf { padding-left: 1.9rem; }
  .side-bottom { margin-top: auto; }
  .side-status {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.6rem 0.65rem 0;
    color: var(--muted);
    font-size: 0.78rem;
  }
  .logo {
    font-size: 1.35rem;
    font-weight: 700;
    letter-spacing: -0.02em;
    background: linear-gradient(120deg, var(--accent), var(--accent-2));
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }
  .status {
    width: 10px; height: 10px; border-radius: 50%;
    background: var(--ok);
    box-shadow: 0 0 8px var(--ok);
  }
  .status.offline { background: var(--err); box-shadow: 0 0 8px var(--err); }

  main {
    position: relative; /* the detail backdrop positions against this */
    /* Makes this a stacking context so the backdrop's negative z-index is
       trapped here and lands above main's own background but below every
       descendant, rather than escaping behind the page. */
    isolation: isolate;
    /* The backdrop deliberately bleeds past this padding and is scaled a
       little, which would otherwise widen the page and give every detail
       view a horizontal scrollbar. `clip` trims it without creating a
       scroll container, so the sticky library header still sticks —
       `hidden` would force overflow-y to auto and break it. */
    overflow-x: clip;
    flex: 1;
    padding: 1.25rem 1.5rem;
    padding-bottom: 5rem;
    width: 100%;
  }
  /* Bleeds past main's padding to the content edges, fades out before the
     page does, and sits under everything. The scrim is --bg, so it works
     in a light theme as well as a dark one without a second rule. */
  .pagebg {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 32rem;
    margin: -1.25rem -1.5rem 0;
    background-size: cover;
    background-position: center 25%;
    /* Strong enough to read as the film's own artwork, weak enough that
       the synopsis over it keeps its contrast in a light theme as well as
       a dark one. */
    opacity: 0.3;
    filter: saturate(0.7) blur(2px);
    transform: scale(1.03); /* hides the blur's soft edges */
    pointer-events: none;
    /* Negative, so it paints under all in-flow content — including the
       player, which is not positioned and would otherwise be painted over
       by any positioned sibling no matter the DOM order. */
    z-index: -1;
    -webkit-mask-image: linear-gradient(to bottom, rgb(0 0 0 / 1) 0%, rgb(0 0 0 / 0.6) 40%, rgb(0 0 0 / 0.2) 72%, rgb(0 0 0 / 0) 100%);
    mask-image: linear-gradient(to bottom, rgb(0 0 0 / 1) 0%, rgb(0 0 0 / 0.6) 40%, rgb(0 0 0 / 0.2) 72%, rgb(0 0 0 / 0) 100%);
  }
  .pagebg.from-poster { filter: saturate(0.7) blur(16px); opacity: 0.24; }

  @media (prefers-reduced-motion: reduce) {
    .pagebg { transform: none; }
  }
  h1 { font-size: 1.4rem; margin-bottom: 1rem; }
  h2 { font-size: 1rem; color: var(--muted); margin: 1.25rem 0 0.5rem; }
  .muted { color: var(--muted); }

  .empty {
    background: var(--panel);
    border: 1px dashed var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    color: var(--muted);
    font-size: 0.95rem;
  }
  .empty.tall { padding: 2.5rem 1.5rem; text-align: center; }
  .empty p + p { margin-top: 0.5rem; }
  .empty a, a { color: var(--accent-2); }

  .librow { display: grid; grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr)); gap: 0.75rem; }
  .libcard {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.1rem 1.2rem;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    text-align: left;
  }
  .libcard span { color: var(--muted); font-size: 0.82rem; }
  .libcard:hover { border-color: var(--accent); }

  .crumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem;
    margin-bottom: 0.9rem;
    font-size: 0.88rem;
  }
  .crumb { color: var(--accent-2); padding: 0.15rem 0.1rem; }
  .crumb:hover { text-decoration: underline; }
  .crumb-current { color: var(--muted); padding: 0.15rem 0.1rem; }
  .sep { color: var(--muted); user-select: none; }

  /* ---- Library sub-tabs ---- */
  .subtabs {
    display: flex;
    gap: 0.25rem;
    border-bottom: 1px solid var(--border);
    overflow-x: auto;
    scrollbar-width: none;
  }
  .subtab {
    flex: none;
    padding: 0.5rem 0.85rem;
    border: 0;
    border-bottom: 2px solid transparent;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 0.92rem;
    cursor: pointer;
  }
  .subtab:hover { color: var(--text); }
  .subtab.on { color: var(--text); border-bottom-color: var(--accent); }

  /* ---- A–Z sections and the index rail ---- */
  /* The rail is fixed to the viewport, so it is out of flow — the grid
     reserves the gutter itself. */
  .azwrap { padding-right: 2rem; }
  .azsec { scroll-margin-top: 0.5rem; }
  /* Not sticky: the filter bar above it already holds the top of the
     viewport, and the rail shows which letter you are in. Two stacked
     sticky bars would just eat the grid. */
  .azhead {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    margin: 0 0 0.7rem;
    padding: 0.45rem 0 0.35rem;
    font-size: 1.05rem;
    font-weight: 700;
    color: var(--text);
    background: var(--bg);
  }
  /* A rule running out from the letter marks the band without a heavy bar. */
  .azhead::after {
    content: '';
    flex: 1;
    height: 1px;
    background: var(--border);
  }
  .azsec + .azsec { margin-top: 1.5rem; }
  /* Sticks beside the grid and spans the viewport, so a drag covers the
     whole alphabet whatever the library size. touch-action keeps that drag
     from scrolling the page instead. */
  /* Pinned to the viewport between the top bar and the bottom edge, so it
     spans the full height and stays put while the grid scrolls under it. */
  .azrail {
    position: fixed;
    right: 0.3rem;
    top: var(--topbar-h);
    bottom: 0;
    z-index: 5;
    width: 1.6rem;
    display: flex;
    flex-direction: column;
    justify-content: space-evenly;
    align-items: center;
    padding: 0.6rem 0;
    touch-action: none;
    user-select: none;
  }
  /* Clears the playback bar when one is up. */
  :global(main.withbar) .azrail { bottom: 6.5rem; }
  .azl {
    display: grid;
    place-items: center;
    width: 1.5rem;
    height: 1.5rem;
    flex: none;
    border: 0;
    background: none;
    color: var(--muted);
    font: inherit;
    font-size: 0.72rem;
    font-weight: 600;
    line-height: 1;
    padding: 0;
    cursor: pointer;
    border-radius: 999px;
  }
  .azl.on { color: var(--bg); background: var(--accent); }
  /* The rail is a single tab stop with arrow-key movement, so the focus ring
     is the only thing telling a keyboard user which letter is live. The
     letters are small and sit over the grid, hence an offset outline rather
     than a border that would shift the layout. */
  .azl:focus-visible {
    outline: 2px solid var(--accent-2);
    outline-offset: 1px;
    color: var(--text);
  }
  @media (hover: hover) {
    .azl:hover { color: var(--text); }
  }
  /* Phones get a slimmer gutter so the grid keeps its width. */
  @media (max-width: 34rem) {
    .azwrap { padding-right: 1.5rem; }
    .azrail { width: 1.25rem; right: 0.1rem; }
    .azl { width: 1.2rem; height: 1.2rem; font-size: 0.62rem; }
  }

  /* ---- Library filter bar ---- */
  .libhead {
    position: sticky;
    top: var(--topbar-h);
    z-index: 4;
    background: var(--bg);
    padding: 0 2rem 0.6rem 0;
    margin-bottom: 0.5rem;
    transition: transform 0.2s ease, opacity 0.2s ease;
  }
  /* Slid up behind the top bar rather than removed from the flow: taking it
     out would reflow the grid under the pointer mid-scroll. */
  .libhead.hidden {
    transform: translateY(calc(-100% - var(--topbar-h)));
    opacity: 0;
    pointer-events: none;
  }
  @media (prefers-reduced-motion: reduce) {
    .libhead { transition: none; }
  }
  .filters { display: flex; flex-direction: column; gap: 0.5rem; padding-top: 0.7rem; }
  .fbar { display: flex; flex-wrap: wrap; gap: 0.5rem; align-items: center; }
  .ffield {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    /* Keeps a usable width, then takes its own row and lets the two pickers
       share the next one. */
    flex: 1 1 11rem;
    min-width: 0;
    padding: 0.45rem 0.7rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--panel);
    color: var(--muted);
  }
  .ffield input {
    flex: 1;
    min-width: 0;
    border: 0;
    background: none;
    color: var(--text);
    font: inherit;
    outline: none;
  }
  .ffield input::-webkit-search-cancel-button { filter: invert(0.5); }
  .fsel {
    padding: 0.45rem 0.6rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--panel);
    color: var(--text);
    font: inherit;
    flex: none;
  }
  .fcount { font-size: 0.8rem; }
  .linkish {
    border: 0;
    background: none;
    color: var(--accent);
    font: inherit;
    cursor: pointer;
    padding: 0;
    margin-left: 0.4rem;
  }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr)); gap: 0.9rem; }
  .cell { display: flex; flex-direction: column; gap: 0.35rem; text-align: left; min-width: 0; }
  /* Carries the watched tick and the resume bar over the artwork. */
  .posterwrap { position: relative; display: block; }
  .seen {
    position: absolute;
    top: 0.35rem;
    right: 0.35rem;
    display: grid;
    place-items: center;
    width: 1.35rem;
    height: 1.35rem;
    border-radius: 50%;
    background: var(--accent);
    color: #fff;
    /* The tick sits on artwork of any colour, so it carries its own edge
       rather than relying on contrast with the poster behind it. */
    box-shadow: 0 0 0 2px var(--bg);
  }
  .resume {
    position: absolute;
    left: 0.4rem;
    right: 0.4rem;
    bottom: 0.4rem;
    height: 3px;
    border-radius: 2px;
    background: rgb(0 0 0 / 0.55);
    overflow: hidden;
  }
  .resume span { display: block; height: 100%; background: var(--accent); }

  /* The tile's watched control. Visible when set, or on hover/focus — an
     always-on empty circle over every unwatched poster would be noise. */
  .seentoggle {
    position: absolute;
    top: 0.35rem;
    right: 0.35rem;
    display: grid;
    place-items: center;
    width: 1.35rem;
    height: 1.35rem;
    border-radius: 50%;
    cursor: pointer;
    background: rgb(0 0 0 / 0.6);
    color: rgb(255 255 255 / 0.85);
    box-shadow: 0 0 0 2px var(--bg);
    opacity: 0;
    transition: opacity 0.12s ease, background 0.12s ease;
  }
  .seentoggle.on {
    opacity: 1;
    background: var(--accent);
    color: #fff;
  }
  .cell:hover .seentoggle,
  .seentoggle:focus-visible {
    opacity: 1;
  }
  .seentoggle:focus-visible { outline: 2px solid var(--accent-2); outline-offset: 2px; }
  .seentoggle:hover { background: var(--accent); color: #fff; }
  /* No hover on touch, so the control is always visible there. */
  @media (hover: none) {
    .seentoggle { opacity: 0.85; }
  }
  /* How many of the set this library holds, over the stand-in poster. */
  .collcount {
    position: absolute;
    top: 0.35rem;
    right: 0.35rem;
    min-width: 1.35rem;
    height: 1.35rem;
    padding: 0 0.35rem;
    display: grid;
    place-items: center;
    border-radius: 999px;
    background: rgb(0 0 0 / 0.72);
    color: #fff;
    font-size: 0.72rem;
    font-weight: 600;
    box-shadow: 0 0 0 1px rgb(255 255 255 / 0.18);
  }
  .poster {
    width: 100%;
    aspect-ratio: 2 / 3;
    object-fit: cover;
    border-radius: 10px;
    border: 1px solid var(--border);
    background: var(--panel);
  }
  .poster.placeholder {
    display: grid;
    place-items: center;
    color: var(--muted);
    opacity: 0.45;
  }
  .poster.square { aspect-ratio: 1 / 1; }

  /* ---- Listening session ---- */
  .session { padding-top: 0.5rem; }
  .session .crumbs { margin-bottom: 1.4rem; }
  .session-body { display: flex; gap: 3rem; align-items: flex-start; justify-content: center; }
  .session-side { flex: none; width: min(26rem, 40vw); text-align: center; position: sticky; top: calc(var(--topbar-h, 3.4rem) + 1.5rem); }
  .session-cover {
    width: 100%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
    border-radius: 18px;
    border: 1px solid var(--border);
    box-shadow: 0 24px 70px rgba(0, 0, 0, 0.45);
    margin-bottom: 1.2rem;
  }
  .session-cover.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); }
  .session-album { font-size: 1.5rem; margin: 0 0 0.3rem; line-height: 1.2; }
  .small { font-size: 0.8rem; }
  .session-tracks { list-style: none; flex: 1; min-width: 0; max-width: 36rem; padding-top: 0.5rem; }
  .strack {
    position: relative;
    display: flex;
    align-items: baseline;
    gap: 0.9rem;
    width: 100%;
    text-align: left;
    padding: 0.6rem 0.9rem;
    border-radius: 10px;
    font-size: 0.96rem;
  }
  .strack:hover { background: var(--panel); color: var(--accent-2); }
  .strack.current { background: var(--panel); color: var(--accent-2); font-weight: 600; }
  .strack .tno { width: 1.6rem; }
  .strack-progress {
    position: absolute;
    left: 0.9rem;
    right: 0.9rem;
    bottom: 3px;
    height: 3px;
    border-radius: 2px;
    background: var(--border);
    overflow: hidden;
  }
  .strack-progress span { display: block; height: 100%; background: var(--accent); }
  .session-shelf { display: flex; gap: 0.6rem; margin-top: 2.2rem; flex-wrap: wrap; justify-content: center; }
  .session-disc { border-radius: 8px; opacity: 0.5; transition: opacity 0.15s, transform 0.15s; padding: 0; }
  .session-disc img, .session-disc .ph {
    width: 4.4rem;
    height: 4.4rem;
    object-fit: cover;
    border-radius: 8px;
    border: 1px solid var(--border);
    display: grid;
    place-items: center;
    color: var(--muted);
    background: var(--panel);
  }
  .session-disc:hover { opacity: 1; transform: translateY(-2px); }
  .session-disc.cur { opacity: 1; outline: 2px solid var(--accent); outline-offset: 2px; }
  @media (max-width: 767px) {
    .session-body { flex-direction: column; align-items: stretch; }
    .session-side { width: 100%; position: static; }
  }

  /* ---- Music view: artist rail + album shelves ---- */
  .music { display: flex; gap: 1.25rem; align-items: flex-start; }
  .mrail {
    flex: none;
    width: 12.5rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.5rem;
    position: sticky;
    top: calc(var(--topbar-h, 3.4rem) + 1rem);
    max-height: calc(100vh - var(--topbar-h, 3.4rem) - 2rem);
    overflow-y: auto;
  }
  .rail-top {
    position: sticky;
    top: 0;
    z-index: 1;
    background: var(--panel);
    padding-bottom: 0.15rem;
    margin-bottom: 0.15rem;
  }
  .rail-filter {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    font: inherit;
    font-size: 0.82rem;
    padding: 0.35rem 0.8rem;
    margin-bottom: 0.4rem;
  }
  .rail-filter:focus { outline: none; border-color: var(--accent); }
  .rail-empty { padding: 0.5rem 0.7rem; font-size: 0.85rem; }
  .rail-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    text-align: left;
    padding: 0.45rem 0.7rem;
    border-radius: 8px;
    font-size: 0.9rem;
    color: var(--text);
  }
  .rail-item:hover { background: var(--bg); }
  .rail-item.sel { background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent-2); font-weight: 600; }
  .rail-n { color: var(--muted); font-size: 0.75rem; font-weight: 400; }
  .rail-div { height: 1px; background: var(--border); margin: 0.4rem 0.3rem; }

  .mcontent { flex: 1; min-width: 0; }
  .mhead {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
    flex-wrap: wrap;
  }
  .mtitle { font-size: 1.6rem; margin: 0; }
  .mactions { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
  .mfilter {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    font: inherit;
    font-size: 0.86rem;
    padding: 0.4rem 0.9rem;
    width: 15rem;
  }
  .mfilter:focus { outline: none; border-color: var(--accent); }

  .dlsel {
    background: var(--panel);
    color: var(--muted);
    border: 1px solid var(--border);
    border-radius: 8px;
    font: inherit;
    font-size: 0.82rem;
    padding: 0.3rem 0.45rem;
    max-width: 8rem;
  }
  .dlsel:hover { color: var(--text); border-color: var(--accent); }

  .msongs {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.5rem;
    margin-bottom: 1rem;
  }
  .msongs-h {
    color: var(--muted);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.3rem 0.6rem;
  }
  .msong {
    display: flex;
    align-items: baseline;
    gap: 0.8rem;
    width: 100%;
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-radius: 8px;
    font-size: 0.88rem;
  }
  .msong:hover { background: var(--bg); color: var(--accent-2); }
  .msong .tname { flex: none; max-width: 40%; }

  .shelf-h {
    display: block;
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text);
    padding: 0;
    margin: 1.4rem 0 0.6rem;
  }
  .shelf-h:hover { color: var(--accent-2); }
  .shelf {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
    gap: 0.9rem;
    margin-bottom: 0.75rem;
  }
  .acard { display: flex; flex-direction: column; gap: 0.3rem; text-align: left; border-radius: 10px; }
  .acover {
    width: 100%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
    border-radius: 10px;
    border: 1px solid var(--border);
    background: var(--panel);
    transition: transform 0.12s ease;
  }
  .acover.ph { display: grid; place-items: center; font-size: 2rem; color: var(--muted); }
  .acard:hover .acover { transform: scale(1.03); border-color: var(--accent); }
  .acard.sel .acover { outline: 2px solid var(--accent); outline-offset: 2px; }
  .aname { font-size: 0.85rem; line-height: 1.25; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .asub { font-size: 0.75rem; color: var(--muted); }

  .apanel {
    display: flex;
    gap: 1.1rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.1rem;
    margin: 0.25rem 0 1.25rem;
  }
  .apanel-cover {
    width: 11rem;
    height: 11rem;
    border-radius: 10px;
    object-fit: cover;
    flex: none;
    border: 1px solid var(--border);
  }
  .apanel-cover.ph { display: grid; place-items: center; font-size: 3rem; color: var(--muted); background: var(--bg); }
  .apanel-body { flex: 1; min-width: 0; }
  .apanel-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    margin-bottom: 0.6rem;
    flex-wrap: wrap;
  }
  .tlist { list-style: none; columns: 2; column-gap: 2rem; }
  .tlist li { break-inside: avoid; }
  .tlist button {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
    width: 100%;
    text-align: left;
    padding: 0.32rem 0.5rem;
    border-radius: 6px;
    font-size: 0.88rem;
  }
  .tlist button:hover { background: var(--bg); color: var(--accent-2); }
  .tno { color: var(--muted); font-size: 0.78rem; width: 1.3rem; flex: none; font-variant-numeric: tabular-nums; }
  .tname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttm { flex: none; font-size: 0.8rem; font-variant-numeric: tabular-nums; }

  @media (max-width: 767px) {
    .music { flex-direction: column; }
    .mrail {
      position: static;
      width: 100%;
      max-height: none;
      display: flex;
      overflow-x: auto;
      gap: 0.25rem;
      padding: 0.4rem;
    }
    .rail-item { width: auto; white-space: nowrap; }
    .rail-div { display: none; }
    .tlist { columns: 1; }
    .apanel { flex-direction: column; }
    .apanel-cover { width: 8rem; height: 8rem; }
  }
  .cell:hover .poster { border-color: var(--accent); }
  /* Titles can be long and, for an unmatched release name, a single
     unbreakable word — break anywhere and clamp so one cell cannot push its
     neighbours out of line. */
  .attrib { margin: 0.4rem 0 0; }
  /* ---- Cast ---- */
  .castsec { margin-top: 1.5rem; }
  /* Wraps into as many rows as the width allows, like the poster grid, so
     the whole cast is visible at once rather than hidden behind a sideways
     scroll. Narrower tracks than posters because a headshot needs less. */
  .castrow {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(5.5rem, 1fr));
    gap: 0.9rem 0.75rem;
  }
  .castcard {
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    min-width: 0;
    text-align: left;
    background: none;
    border: 0;
    padding: 0;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .headshot {
    width: 100%;
    aspect-ratio: 2 / 3;
    object-fit: cover;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--panel);
    display: block;
  }
  .headshot.ph { display: grid; place-items: center; color: var(--muted); opacity: 0.5; }
  .headshot.lg { width: 4.5rem; aspect-ratio: 2 / 3; }
  /* Long names break rather than pushing their track wide; two lines each
     keeps rows aligned. */
  .castname {
    font-size: 0.82rem;
    font-weight: 600;
    line-height: 1.25;
    overflow-wrap: anywhere;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .castrole {
    font-size: 0.75rem;
    line-height: 1.2;
    overflow-wrap: anywhere;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .castcard:hover .castname { color: var(--accent); }
  /* Same two-column shape as the technical details below it, so the page
     reads as one block rather than two unrelated lists. */
  .crew { margin: 0 0 0.7rem; font-size: 0.88rem; }
  .crew div { display: flex; gap: 0.6rem; }
  .crew dt { color: var(--muted); min-width: 10rem; flex: none; }
  .crew dd { margin: 0; }

  /* Person overlay */
  .pback { position: fixed; inset: 0; background: rgb(0 0 0 / 0.6); z-index: 40; }
  .ppanel {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(52rem, calc(100vw - 2rem));
    max-height: min(44rem, calc(100vh - 3rem));
    overflow-y: auto;
    padding: 1.1rem 1.25rem;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    z-index: 41;
  }
  .phead { display: flex; align-items: flex-start; gap: 0.9rem; margin-bottom: 1rem; }
  .phead h2 { margin: 0; font-size: 1.1rem; }
  .ctlx { margin-left: auto; border: 0; background: none; color: var(--muted); cursor: pointer; }
  .ctlx:hover { color: var(--text); }

  .tagline { font-style: italic; color: var(--muted); margin: 0.2rem 0 0.5rem; }
  /* Boxed like a classification card so it reads as a certificate rather
     than another piece of prose. */
  .cert {
    border: 1px solid var(--border);
    border-radius: 4px;
    padding: 0 0.35rem;
    font-size: 0.78rem;
    font-weight: 600;
    color: var(--muted);
    letter-spacing: 0.02em;
  }
  .facts { display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem; margin: 0 0 0.6rem; font-size: 0.88rem; }
  .score { color: var(--ok); font-weight: 600; }
  /* Genre tiles: a wrapping set of chips sized to their label. */
  .genres { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  .genre {
    display: flex;
    align-items: baseline;
    gap: 0.5rem;
    padding: 0.55rem 0.9rem;
    border: 1px solid var(--border);
    border-radius: 10px;
    background: var(--panel);
    color: var(--text);
    font: inherit;
    cursor: pointer;
  }
  .genre:hover { border-color: var(--accent); }
  .genre-name { font-weight: 600; }
  .genre-n { font-size: 0.82rem; }
  /* ---- Play queue ---- */
  .queue {
    margin: -0.5rem 0 1.5rem;
    border: 1px solid var(--border);
    border-top: 0;
    border-radius: 0 0 12px 12px;
    background: var(--panel);
    padding: 0.6rem 0.9rem 0.7rem;
  }
  .queue header {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.4rem;
  }
  .qspacer { flex: 1; }
  .queue ol {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.1rem;
    /* Long queues scroll rather than pushing the page down. */
    max-height: 12rem;
    overflow-y: auto;
  }
  .queue li {
    display: flex;
    align-items: center;
    gap: 0.6rem;
    padding: 0.25rem 0.3rem;
    border-radius: 6px;
  }
  .queue li:hover { background: var(--bg); }
  .qpos { font-variant-numeric: tabular-nums; min-width: 1.2rem; }
  .qtitle {
    flex: 1;
    min-width: 0;
    text-align: left;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .qtitle:hover { color: var(--accent-2); }
  /* A margin rather than a literal space: whitespace between Svelte
     elements is not reliably preserved. */
  .qyear { margin-left: 0.4rem; }
  .qdrop { color: var(--muted); display: flex; flex: none; }
  .qdrop:hover { color: var(--err); }

  .collacts { display: flex; gap: 0.6rem; align-items: center; flex-wrap: wrap; }

  /* A row heading that is also a link into the full listing. Inherits the
     h2's size rather than looking like a button in a heading. */
  .h2link { font: inherit; color: inherit; padding: 0; }
  .h2link:hover { color: var(--accent-2); }

  /* ---- Person page ---- */
  .psec {
    font-size: 0.85rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: var(--muted);
    margin: 1.25rem 0 0.6rem;
    display: flex;
    gap: 0.5rem;
    align-items: baseline;
  }
  .psec:first-of-type { margin-top: 0.5rem; }
  /* The role sits under the year on its own line: character names run long
     and would otherwise push the year out of the tile. */
  .prole {
    display: block;
    color: var(--text);
    opacity: 0.8;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }

  /* The breadcrumb names where you are now, so this is just the count. */
  .gcount { font-size: 0.85rem; }
  .collhead {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    flex-wrap: wrap;
    margin-bottom: 0.9rem;
  }
  /* .play already supplies the accent fill and flex layout. */
  /* The film's name is context for the button, not its label. */
  .nextname { opacity: 0.75; font-weight: 400; }
  @media (max-width: 30rem) {
    .nextname { display: none; }
  }

  .versions { margin: 0.6rem 0 0.9rem; }
  .vchips { display: flex; flex-wrap: wrap; gap: 0.4rem; margin-top: 0.35rem; }

  .cell-title {
    font-size: 0.86rem;
    line-height: 1.25;
    /* Always two lines tall: without this a one-line title sits its year
       higher than its neighbours and every row reads as ragged. */
    min-height: 2.5em;
    overflow-wrap: anywhere;
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }
  .cell-sub {
    font-size: 0.75rem;
    color: var(--muted);
    overflow-wrap: anywhere;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }

  .detail { display: flex; gap: 1.25rem; align-items: flex-start; }
  .detail .poster { width: 9rem; flex: none; }
  .detail-body { flex: 1; min-width: 0; }
  .meta { color: var(--muted); font-size: 0.88rem; margin: 0.4rem 0 0.8rem; }
  .overview { font-size: 0.95rem; line-height: 1.5; margin-bottom: 1rem; }
  .actions { display: flex; gap: 0.6rem; align-items: center; }
  .play {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    background: var(--accent);
    color: white;
    padding: 0.55rem 1.4rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .dl {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--accent-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.55rem 1rem;
    text-decoration: none;
    font-size: 0.9rem;
  }
  .progress {
    height: 4px;
    background: var(--border);
    border-radius: 2px;
    overflow: hidden;
    margin-top: -0.1rem;
  }
  .progress-fill { height: 100%; background: var(--accent); }
  .tech {
    margin-top: 1.1rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-size: 0.88rem;
  }
  .tech div { display: flex; gap: 0.75rem; }
  .tech dt { color: var(--muted); width: 5.5rem; flex: none; }

  .grid.seasons { margin-top: 1.25rem; grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr)); }

  /* ---- Episode list (season page, terminal level) ---- */
  .eplist { list-style: none; margin-top: 1.25rem; }
  .eplist li { border-bottom: 1px solid var(--border); }
  .eplist li:last-child { border-bottom: none; }
  .eprow {
    position: relative;
    display: flex;
    align-items: center;
    gap: 0.9rem;
    width: 100%;
    text-align: left;
    padding: 0.5rem 0.4rem;
    border-radius: 10px;
  }
  .eprow:hover { background: var(--panel); }
  .eprow.sel { background: var(--panel); }
  .epthumb {
    width: 7.5rem;
    aspect-ratio: 16 / 9;
    object-fit: cover;
    border-radius: 8px;
    border: 1px solid var(--border);
    flex: none;
  }
  .epthumb.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); }
  .epmain { flex: 1; min-width: 0; }
  .eprow.nowplaying .epmain { color: var(--accent-2); }
  .ep-live { color: var(--accent-2); flex: none; display: grid; place-items: center; }
  .ep-progress {
    position: absolute;
    left: 0.4rem;
    right: 0.4rem;
    bottom: 0;
    height: 3px;
    border-radius: 2px;
    background: var(--border);
    overflow: hidden;
  }
  .ep-progress span { display: block; height: 100%; background: var(--accent); }
  .eppanel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem 1.2rem;
    margin: 0.35rem 0 0.85rem;
  }
  .eppanel .overview { margin: 0.5rem 0 0.9rem; }
  .eppanel .meta { margin: 0; }

  .children { list-style: none; margin-top: 1.25rem; }
  .children li { border-bottom: 1px solid var(--border); }
  .children li:last-child { border-bottom: none; }
  .children li.hl {
    background: color-mix(in srgb, var(--accent) 14%, transparent);
    border-radius: 8px;
  }
  .children button {
    width: 100%;
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.75rem 0.25rem;
    text-align: left;
  }
  .children button:hover { color: var(--accent-2); }
  .epnum { color: var(--muted); }

  .card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.25rem 1.25rem;
  }
  .row {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.9rem 0;
    border-bottom: 1px solid var(--border);
  }
  .row:last-child { border-bottom: none; }
  .theme-picker { display: flex; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end; }
  .chip {
    padding: 0.3rem 0.75rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.85rem;
  }
  .chip.sel {
    border-color: var(--accent);
    color: var(--text);
    background: color-mix(in srgb, var(--accent) 18%, transparent);
  }

  .tabbar {
    position: fixed;
    bottom: 0; left: 0; right: 0;
    display: flex;
    background: var(--panel);
    border-top: 1px solid var(--border);
  }
  .tabbar button {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.15rem;
    padding: 0.6rem 0 0.7rem;
    color: var(--muted);
    font-size: 0.75rem;
  }
  .tabbar button .icon { font-size: 1.15rem; }
  .tabbar button.active { color: var(--accent-2); }

  @media (min-width: 768px) {
    main { padding-bottom: 1.25rem; }
  }
</style>
