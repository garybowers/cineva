<script lang="ts">
  import { onMount, tick, untrack } from 'svelte'
  import {
    getHealth, listLibraries, libraryItems, libraryItemsSince, libraryGenres, type GenreCount,
    libraryCollections, collectionDetail, type CollectionInfo,
    libraryRecommended, libraryNewEpisodes, type RecRow, homeFeed, type HomeFeed,
    personDetail, type PersonProfile, type PersonCredit, type Person as PersonT, itemDetail, itemSubtitles, playItem, continueWatching, downloadURL, searchItems, subscribeEvents, setWatched,
    itemTracks, librarySearch, myQueue, type QueueSource,
    seriesNextUp, type NextUpInfo,
    authStatus, login, setupAdmin, logout, UNAUTHORISED, authProfiles, pinLogin, type User as UserT, type Profile,
    uploadAvatar,
    listDevices, deviceQueue, playOnDevice, takeOverDevice, TAKEN_OVER, type Device,
    type Health, type Library, type Item, type ItemDetail, type PlayInfo, type ResumeItem, type PlayQuality, type SubtitleTrack, type HandoffQueue,
    requestStatus, discoverSearch, discoverTitle, listRequests, createRequest, withdrawRequest, itemMissing, searchMissing,
    type RequestStatus, type RequestState, type DiscoverResult, type DiscoverTitle, type MediaRequest, type MissingInfo,
    itemMix,
  } from './lib/api'
  import { chooseTheme, holidayKey } from './lib/theme'
  import { activeTheme, installLooks, looks, paint, refreshLooks } from './lib/looks.svelte'
  import ThemeEffect from './ThemeEffect.svelte'
  import { tv } from './lib/tv.svelte'
  import { pref, setPref, loadPrefs, clearPrefs, onPrefs } from './lib/prefs'
  import { onDismiss } from './lib/dismiss'
  import { loadQueue, saveQueue, fromHandoff, recordOf, setQueueRemote, emptyQueue as blankQueue, type StoredQueue } from './lib/queue'
  import Player from './Player.svelte'
  import MusicPlayer from './music/player/MusicPlayer.svelte'
  import MiniBar from './music/player/MiniBar.svelte'
  import NowPlaying from './music/player/NowPlaying.svelte'
  import {
    mq, play as playMusic, playNext as playNextMusic, append as appendMusic, clear as clearMusic,
    adopt as adoptMusicQueue, restore as restoreMusicQueue, snapshot as musicSnapshot, startMix,
  } from './music/player/queue.svelte'
  import MusicLibrary, { type MusicTab } from './music/library/MusicLibrary.svelte'
  import ArtistView from './music/ArtistView.svelte'
  import AlbumPage from './music/AlbumPage.svelte'
  import PlaylistsView from './music/PlaylistsView.svelte'
  import AddToPlaylist from './music/AddToPlaylist.svelte'
  import TrackMenu from './music/TrackMenu.svelte'
  import Stars from './music/Stars.svelte'
  import { setMusicActions, addToPlaylist, plays as playsLabel, ratedElsewhere, trackTime, type ItemRef, type NoticeAction } from './music/actions.svelte'
  import AvatarCrop from './AvatarCrop.svelte'
  import SegmentEditor from './SegmentEditor.svelte'
  import DetectMenu from './DetectMenu.svelte'
  import { setDetection, loadDetection, canEditSegments, detect } from './lib/detection.svelte'
  import Settings from './Settings.svelte'
  import RoomsPage from './ha/RoomsPage.svelte'
  import PlayInMode from './ha/PlayInMode.svelte'
  import { loadHA, clearHA, haVisible, onHAEvent, onModeRun } from './lib/homeassistant.svelte'
  import {
    House, Library as LibraryIcon, Settings as SettingsIcon, User, Clapperboard, Tv,
    Music as MusicIcon, Headphones, Podcast, BookOpen, Play, Shuffle, Download,
    RotateCcw, AudioLines, LogOut, Users, Delete, KeyRound, Search, Eye, EyeOff, X, Check,
    ListVideo, ChevronUp, ChevronDown, ChevronLeft, ChevronRight, Ellipsis, TriangleAlert,
    Pin, PinOff, PanelLeftOpen, ZoomIn, ZoomOut, Cast, MonitorSmartphone,
    Inbox, Plus, Clock, CalendarClock, ListMusic, ScanSearch, Lamp,
  } from '@lucide/svelte'

  // 'requests' and 'discover' (a title outside the library) are reached from
  // the sidebar and from search; neither is one of the always-there tabs.
  type Tab = 'home' | 'libraries' | 'queue' | 'settings' | 'requests' | 'discover' | 'playlists' | 'rooms'
  const tabs = [
    { id: 'home' as Tab, label: 'Home', icon: House },
    { id: 'libraries' as Tab, label: 'Libraries', icon: LibraryIcon },
    // The queue needs a home of its own. It used to appear only inside an
    // open player, so "Add all to queue" looked like it had done nothing and
    // there was no way to see or change the queue before starting something.
    { id: 'queue' as Tab, label: 'Queue', icon: ListVideo },
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

  // Every floating panel closes on a click away or Escape; see onDismiss.
  $effect(() => {
    if (!showUserMenu) return
    return onDismiss('.userwrap', () => (showUserMenu = false))
  })

  // The sidebar floats over the content as a rail of icons and expands on
  // hover; pinned, it stays open and the content makes room for it. Pinning
  // belongs to the device — a laptop and a TV's browser want different
  // things — so it is kept in this browser rather than on the account.
  const SIDE_PINNED = 'cineva-side-pinned'
  let sidePinned = $state(readSidePinned())
  // Opened by its button rather than by hover: the way in on a touch screen.
  let sideOpen = $state(false)
  function readSidePinned(): boolean {
    try {
      return localStorage.getItem(SIDE_PINNED) === '1'
    } catch {
      return false
    }
  }
  function setSidePinned(on: boolean) {
    sidePinned = on
    sideOpen = false
    try {
      localStorage.setItem(SIDE_PINNED, on ? '1' : '0')
    } catch {
      /* storage blocked: pinned for this visit only */
    }
  }
  $effect(() => {
    if (!sideOpen) return
    return onDismiss('.side', () => (sideOpen = false))
  })
  // Hover opens it after a beat and closes it after a slightly longer one,
  // so the pointer can cross it on the way to a poster, or slip off its edge,
  // without it flapping. Mouse only: a tap on a touch screen also "enters",
  // and should simply follow the link it landed on.
  let sideHover = $state(false)
  let sideFocus = $state(false) // keyboard focus inside it
  let hoverTimer: ReturnType<typeof setTimeout> | undefined
  function sideHoverTo(on: boolean, e: PointerEvent) {
    if (e.pointerType !== 'mouse') return
    clearTimeout(hoverTimer)
    hoverTimer = setTimeout(() => (sideHover = on), on ? 80 : 200)
  }
  const sideExpanded = $derived(sidePinned || sideOpen || sideHover || sideFocus)

  // Poster size on desktop: four steps around today's size, one setting for
  // every film and series grid. Per device like the pin — a TV across the
  // room wants bigger posters than a laptop on a knee.
  const POSTER_ZOOM = 'cineva-poster-zoom'
  const posterScales = [0.8, 1, 1.3, 1.7]
  let posterZoom = $state(readPosterZoom())
  function readPosterZoom(): number {
    try {
      const raw = localStorage.getItem(POSTER_ZOOM)
      const n = raw === null ? NaN : Number(raw)
      return Number.isInteger(n) && n >= 0 && n < posterScales.length ? n : 1
    } catch {
      return 1
    }
  }
  $effect(() => {
    try {
      localStorage.setItem(POSTER_ZOOM, String(posterZoom))
    } catch {
      /* storage blocked: this visit only */
    }
  })
  // Going somewhere closes a sidebar opened by its button, as a menu would.
  $effect(() => {
    void tab
    void currentLib
    sideOpen = false
  })

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
    // On a TV the number pad takes the focus, so the remote can enter the
    // PIN without the television's own keyboard covering the screen.
    setTimeout(() => (document.querySelector(tv.on ? '.digits button' : '.pinpad input') as HTMLElement | null)?.focus(), 30)
  }
  // A digit from the on-screen pad; it signs in once the PIN is complete
  // only when OK is pressed, as typing does.
  function padDigit(d: string) {
    if (d === 'del') pin = pin.slice(0, -1)
    else if (pin.length < 6) pin += d
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
  // Who this device signs in as when nobody is signed in on it, when an
  // admin has set one (the living-room TV): its menu offers "Switch user"
  // rather than "Sign out", and the profile picker a way back to them.
  let autoUser: { id: string; displayName: string } | null = $state(null)
  async function checkAuth() {
    try {
      const st = await authStatus()
      autoUser = st.autoUser ?? null
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
  /** Sign this device back in as its own user (the status check does it). */
  async function backToAutoUser() {
    picked = null
    usePassword = false
    authErr = ''
    await checkAuth()
    if (gate === 'in') boot()
    else if (loginMode === 'picker') await loadProfiles()
  }
  async function signOut() {
    showUserMenu = false
    // The next person to sign in on this browser must not inherit these.
    clearPrefs()
    clearHA()
    try { await logout() } catch { /* already gone */ }
    me = null
    playing = null
    clearMusic()
    picked = null
    gate = 'login'
    loadProfiles()
  }

  const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

  // ---- Picture ----
  // An uploaded photo, or the initial on a colour of the user's choosing.
  let avatarMsg = $state('')
  let avatarBusy = $state(false)
  // The photo being framed before it goes up (AvatarCrop).
  let cropFile: File | null = $state(null)
  async function sendAvatar(picture: Blob) {
    avatarBusy = true
    avatarMsg = ''
    try {
      me = await uploadAvatar(picture)
      cropFile = null
    } catch (err) {
      avatarMsg = errText(err)
      cropFile = null
    } finally {
      avatarBusy = false
    }
  }
  // ---- Devices ----
  // Every signed-in browser, this one included: named, signed out from
  // here, and the targets and sources of a handed-over queue.
  let devices: Device[] = $state([])
  const deviceTitle = (d: Device) => d.name || d.client || 'Unknown browser'
  const otherDevices = $derived(devices.filter((d) => !d.current))
  async function loadDevices() {
    try {
      devices = await listDevices()
    } catch {
      /* the list stays as it was */
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
    if (currentLib?.mediaType === 'music') {
      if (detail) return out
      if (musicGenre) {
        out.push({ label: 'Genres', go: () => setMusicTab('genres') })
        out.push({ label: musicGenre, go: () => setMusicTab('albums', { genre: musicGenre }) })
      } else if (musicComp) {
        out.push({ label: 'Compilations', go: () => setMusicTab('albums', { compilations: true }) })
      } else if (musicTab !== 'listen') {
        const label = musicTab[0].toUpperCase() + musicTab.slice(1)
        out.push({ label, go: () => setMusicTab(musicTab) })
      }
      return out
    }
    if (libTab === 'genres') {
      out.push({ label: 'Genres', go: () => { detail = null; trail = []; genre = null; syncURL() } })
      if (genre) out.push({ label: genre, go: () => { detail = null; trail = []; syncURL() } })
    } else if (libTab === 'collections') {
      out.push({ label: 'Collections', go: () => { detail = null; trail = []; collection = null; syncURL() } })
      if (collection) out.push({ label: collection.name, go: () => { detail = null; trail = []; syncURL() } })
    }
    return out
  })

  // The music library's own views, as route segments (/libraries/Music/albums).
  function musicViewPath(): string {
    if (musicGenre) return '/genres/' + encodeURIComponent(musicGenre)
    if (musicComp) return '/albums/compilations'
    return musicTab === 'listen' ? '' : '/' + musicTab
  }

  function buildPath(): string {
    if (nowPlayingOpen && mq.active) return '/now-playing'
    if (tab === 'home') return '/'
    if (tab === 'queue') return '/queue'
    if (tab === 'settings') return '/settings' + (settingsSection ? '/' + settingsSection : '')
    if (tab === 'requests') return '/requests'
    if (tab === 'rooms') return '/rooms'
    if (tab === 'playlists') return '/playlists' + (playlistId ? '/' + playlistId : '')
    if (tab === 'discover' && discoverKey) return `/discover/${discoverKey.type}/${discoverKey.id}`
    let p = '/libraries'
    if (currentLib) {
      p += '/' + encodeURIComponent(currentLib.name)
      // Kept on item URLs too, so opening a film from inside a collection
      // and reloading still shows where you came from.
      p += currentLib.mediaType === 'music' ? (detail ? '' : musicViewPath()) : viewPath()
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

  // The open Settings section (/settings/devices); '' for the list.
  let settingsSection = $state('')
  function openSettings(id: string) {
    settingsSection = id
    syncURL()
  }

  function setTab(t: Tab) {
    tab = t
    if (t === 'settings') settingsSection = ''
    nowPlayingOpen = false
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
        settingsSection = parts[1] ?? ''
        return
      }
      if (parts[0] === 'queue') {
        tab = 'queue'
        return
      }
      // /items/<id>/segments: the intro and credits editor for an episode
      // or film, which the admin's reports link to. The page underneath is
      // the item's own (an episode's season), and the address becomes its.
      if (parts[0] === 'items' && parts[1]) {
        let target: Item
        try {
          if (libraries.length === 0) libraries = await listLibraries()
          target = (await itemDetail(parts[1])).item
          await showItem(target)
        } catch {
          tab = 'home' // gone, or not one this user can see
          return
        }
        history.replaceState({}, '', buildPath())
        if (parts[2] === 'segments' && canEditSegments(me)) segmentsFor = target
        return
      }
      if (parts[0] === 'playlists') {
        tab = 'playlists'
        playlistId = parts[1] ?? null
        return
      }
      if (parts[0] === 'requests') {
        tab = 'requests'
        loadMyRequests()
        return
      }
      if (parts[0] === 'rooms') {
        tab = 'rooms'
        return
      }
      if (parts[0] === 'discover' && (parts[1] === 'movie' || parts[1] === 'tv') && Number(parts[2]) > 0) {
        await openDiscover(parts[1], Number(parts[2]))
        return
      }
      if (parts[0] === 'listening' || parts[0] === 'now-playing') {
        nowPlayingOpen = mq.active
        if (!mq.active) tab = 'home'
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
      if (lib.mediaType === 'music') {
        await applyMusicRoute(lib, segs)
        return
      }

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
      selEpId = null
      detailSubs = []
      if (detail.item.kind === 'movie') {
        itemSubtitles(target.id).then((s) => (detailSubs = s)).catch(() => {})
      }
      if (selectEp) focusEpisode(selectEp.id)
    } finally {
      routing = false
    }
  }

  // /libraries/<Music>[/artists|albums|songs|genres[/<Genre>]|albums/compilations]
  // or /libraries/<Music>/<Artist>[/<Album>]: a tab, or a page found by
  // title through the library's search (nothing fetches the library whole).
  async function applyMusicRoute(lib: Library, segs: string[]) {
    const tabs: MusicTab[] = ['artists', 'albums', 'songs', 'genres']
    musicTab = 'listen'
    musicGenre = ''
    musicComp = false
    if (segs.length === 0) return
    if ((tabs as string[]).includes(segs[0])) {
      musicTab = segs[0] as MusicTab
      if (segs[0] === 'genres' && segs[1]) {
        musicTab = 'albums'
        musicGenre = segs[1]
      } else if (segs[0] === 'albums' && segs[1] === 'compilations') {
        musicComp = true
      }
      return
    }
    try {
      const found = await librarySearch(lib.id, segs[0])
      const artist = found.artists.find((a) => a.title === segs[0])
      if (!artist) return
      let target: Item = artist
      let ancestors: Item[] = []
      if (segs[1]) {
        const more = await librarySearch(lib.id, segs[1])
        const album = more.albums.find((a) => a.title === segs[1] && a.parentId === artist.id)
        if (album) {
          ancestors = [artist]
          target = album
        }
      }
      detail = await itemDetail(target.id)
      trail = ancestors
      selEpId = null
      detailSubs = []
    } catch {
      /* not found: the library's own page shows */
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
  // Themes: the list, the user's choice and the holiday on now live in
  // lib/looks.svelte.ts, which paints whichever should be on screen.
  // The effect (snow, leaves, blossom) waits while anything plays.
  const shownEffect = $derived.by(() => {
    const e = looks.list.find((t) => t.id === looks.painted)?.effect ?? ''
    return looks.effects && !playing && !mq.active ? e : ''
  })

  // Libraries browse state: a simple drill-down stack.
  let libraries: Library[] = $state([])
  let currentLib: Library | null = $state(null)
  let gridItems: Item[] = $state([])
  let detail: ItemDetail | null = $state(null)
  let detailSubs: SubtitleTrack[] = $state([])
  let trail: Item[] = $state([]) // breadcrumb ancestors of the current detail
  let resume: ResumeItem[] = $state([])
  let playing: { item: Item; info: PlayInfo; quality: PlayQuality; gen: number } | null = $state(null)
  let playGen = 0 // bumps on quality change so the player remounts
  let playingPos = $state(0) // live playhead seconds, reported by the Player

  // ---- Download manager: dispatches each file as its own browser download ----
  type DownloadEntry = { id: string; title: string; status: 'queued' | 'sent' }
  let downloads: DownloadEntry[] = $state([])
  let showDownloads = $state(false)
  $effect(() => {
    if (!showDownloads) return
    return onDismiss('.dlwrap', () => (showDownloads = false))
  })
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

  /**
   * Queue the whole series from one episode onward and start it.
   *
   * "Play" starts a single episode and stops; this rolls through the rest,
   * which is what someone settling in for an evening wants. The queue page
   * then shows exactly what is lined up.
   */
  async function playSeriesFrom(start: Item) {
    if (!detail) return
    const eps = await collectEpisodes(detail.item)
    const at = eps.findIndex((e) => e.id === start.id)
    if (at < 0) {
      startPlay(start)
      return
    }
    playList(eps, at)
  }

  async function downloadAll(root: Item) {
    queueDownloads(await collectEpisodes(root))
  }

  // Video and music playback are mutually exclusive: the music queue
  // (music/player/queue.svelte.ts) holds the music; starting a track stops
  // the video player, and starting a video (startPlay) clears the music.
  function startMusic(tracks: Item[], startIndex: number, shuffleAll = false, offset = 0, source: QueueSource | null = null) {
    playing = null
    playMusic(tracks, startIndex, { shuffle: shuffleAll, offset, source, startHere: true })
  }
  // The video player must stop when music starts, wherever it starts from.
  $effect(() => {
    void mq.gen
    if (mq.active && mq.autoplay) untrack(() => { if (playing) closePlayer() })
  })

  // ---- Now playing: the screen over the queue ----
  let nowPlayingOpen = $state(false)
  function openNowPlaying() {
    nowPlayingOpen = true
    syncURL()
  }
  function closeNowPlaying() {
    nowPlayingOpen = false
    if (location.pathname === '/now-playing') history.back()
    else syncURL()
  }
  $effect(() => {
    if (!mq.active && nowPlayingOpen) closeNowPlaying()
  })
  // The music-start preference: Play opens the screen, or just the bar.
  let lastStartGen = 0
  $effect(() => {
    const g = mq.gen
    if (g === lastStartGen || !mq.active || !mq.autoplay) return
    lastStartGen = g
    if (pref('music-start', 'bar') === 'now-playing') untrack(openNowPlaying)
  })
  // The engine stopped: an administrator ended playback, or another
  // device carried the track on ("Continued on ..." is said by the event).
  function onMusicStopped(why: 'admin' | 'takeover') {
    if (why === 'takeover') clearMusic()
    else notify('error', 'Playback was stopped by an administrator.')
  }

  // Open the album containing a track (from the music bar or search).
  // A track carries only its album's id, so the album is looked for in what
  // the client already holds — the playing queue's albums (fetched as each
  // track starts), the open music library, the grid, the search results —
  // and the page paints from it at once, as every other navigation does;
  // the tracks fill in when the fetch returns.
  async function openAlbumOf(track: Item) {
    const albumId = track.parentId
    if (!albumId) return
    nowPlayingOpen = false
    tab = 'libraries'
    switchLibraryTo(track.libraryId)
    const find = (i: Item) => i.id === albumId
    const album = gridItems.find(find) ?? searchResults.find(find)
    await openItem(albumId, false, album)
  }

  // Jump to a track's or album's artist page. Tracks hang off albums, so
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
    if (artistId) await openArtist({ id: artistId, libraryId: item.libraryId })
  }

  // An artist's page: the artist item's own detail route
  // (/libraries/<Music>/<Artist>), which renders as ArtistView.
  async function openArtist(a: Item | ItemRef) {
    nowPlayingOpen = false
    if ('kind' in a && a.title) return showItem(a)
    tab = 'libraries'
    switchLibraryTo(a.libraryId)
    await openItem(a.id)
  }

  // Makes a library current on the way to one of its items, loading what
  // its view needs: the shelves and rail of a music library, the grid of a
  // video one. Already current, or already loaded, costs nothing.
  function switchLibraryTo(libraryId: string) {
    const lib = libraries.find((l) => l.id === libraryId)
    if (!lib) return
    if (lib.mediaType === 'music') {
      // A music library's pages fetch what they show; nothing is loaded whole.
      if (lib.id !== currentLib?.id) {
        currentLib = lib
        musicTab = 'listen'
        musicGenre = ''
        musicComp = false
      }
      return
    }
    if (lib.id === currentLib?.id && gridItems.length > 0) return
    currentLib = lib
    libraryItems(lib.id).then((items) => {
      gridItems = items.filter(gridFilter(lib))
    })
  }

  // ---- Playlists (their own route, /playlists[/<id>]) ----
  let playlistId: string | null = $state(null)
  function openPlaylist(id: string | null) {
    nowPlayingOpen = false
    tab = 'playlists'
    playlistId = id
    syncURL()
  }

  setMusicActions({
    play: (tracks, index = 0, shuffle = false, source = null) => {
      playing = null
      playMusic(tracks, index, { shuffle, source, startHere: index > 0 })
    },
    playNext: playNextMusic,
    queue: appendMusic,
    openAlbum: (al) => {
      nowPlayingOpen = false
      if ('kind' in al && al.title) return void showItem(al)
      const lib = libraries.find((l) => l.id === al.libraryId)
      tab = 'libraries'
      if (lib && lib.id !== currentLib?.id) currentLib = lib
      void openItem(al.id)
    },
    openArtist: (a) => void openArtist(a),
    openPlaylist: (id) => openPlaylist(id),
    mix: (seed) => void startMixFrom(seed),
    notify: (kind, text, action) => notify(kind, text, undefined, action),
    download: (id, format) => dlNavigate(id, format),
  })

  // A mix started on purpose (server/docs/mix.md): the first batch opens
  // with the seed's best and replaces the queue.
  async function startMixFrom(seed: Item) {
    try {
      const res = await itemMix(seed.id, { start: true, size: 20 })
      if (res.tracks.length === 0) {
        notify('info', `Nothing to mix from ${seed.title} yet.`)
        return
      }
      playing = null
      startMix(res, seed)
    } catch (err) {
      notify('error', errText(err))
    }
  }

  // A search hit on a song plays it in album context: queue the whole album
  // starting at that track.
  async function playTrackResult(t: Item) {
    let queue = [t]
    let start = 0
    if (t.parentId) {
      try {
        const tracks = await itemTracks(t.parentId)
        if (tracks.length > 0) {
          queue = tracks
          start = Math.max(0, tracks.findIndex((c) => c.id === t.id))
        }
      } catch {
        /* album gone — play the bare track */
      }
    }
    startMusic(queue, start, false, 0, t.album ? { kind: 'album', id: t.parentId, title: t.album } : null)
  }

  // "Go to song": the album page with the track picked out.
  let hlTrackId: string | null = $state(null)
  async function goToSong(t: Item) {
    hlTrackId = t.id
    await openAlbumOf(t)
    setTimeout(() => {
      document.querySelector('.tl li.hl, .children li.hl')?.scrollIntoView({ block: 'center', behavior: 'smooth' })
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

  // ---- Home ----
  // One request for the whole screen. Refetched whenever the resume list is
  // (progress saved, something marked watched), since both move with it.
  let home = $state<HomeFeed | null>(null)
  $effect(() => {
    if (tab !== 'home') return
    void resume
    homeFeed().then((h) => (home = h)).catch(() => {})
  })
  // The banner: what was last in progress, or else the next episode due.
  const hero = $derived(home?.continueWatching[0] ?? home?.nextUp[0] ?? null)
  // What a title is, for the TV banner's preview of the focused one.
  const kindLabel = (k: string) =>
    ({ movie: 'Film', series: 'Series', season: 'Season', episode: 'Episode', album: 'Album', artist: 'Artist', track: 'Track' } as Record<string, string>)[k] ?? ''
  // The preview belongs to Home; elsewhere the banner is not shown.
  $effect(() => {
    void tab
    tv.focused = null
  })

  function scrollShelf(e: Event, dir: number) {
    const row = (e.currentTarget as HTMLElement).closest('.shelf-head')?.nextElementSibling as HTMLElement | null
    row?.scrollBy({ left: dir * row.clientWidth * 0.85, behavior: 'smooth' })
  }

  // ---- Recommended ----
  const resumeHere = $derived(resume.filter((r) => r.libraryId === currentLib?.id))

  // New episodes on a TV library's Recommended tab: what aired lately,
  // decided server-side. Refetched with the resume list, as home is, so a
  // tick appears once an episode is watched.
  let newEps: ResumeItem[] = $state([])
  $effect(() => {
    void resume
    const lib = currentLib
    if (!lib || lib.mediaType !== 'tv') {
      newEps = []
      return
    }
    if (tab !== 'libraries' || libTab !== 'recommended' || detail) return
    libraryNewEpisodes(lib.id)
      .then((r) => { if (currentLib?.id === lib.id) newEps = r })
      .catch(() => (newEps = []))
  })

  /** "S6 · E5", as an episode card or a series' newest arrival reads. */
  const epLabel = (season?: number, episode?: number) => `S${season ?? 0} · E${episode ?? 0}`

  // ---- Music library: which tab, and what the rail highlights ----
  let musicTab: MusicTab = $state('listen')
  let musicGenre = $state('') // the Albums and Songs tabs narrowed to a genre
  let musicComp = $state(false) // the Albums tab narrowed to compilations
  function setMusicTab(t: MusicTab, opts: { genre?: string; compilations?: boolean } = {}) {
    musicTab = t
    musicGenre = opts.genre ?? ''
    musicComp = opts.compilations ?? false
    if (detail) {
      detail = null
      trail = []
    }
    syncURL()
  }

  // An artist's or album's page from the current music library, shown
  // with the rail beside it.
  const musicDetail = $derived.by(() => {
    const d = detail
    return !!d && !!currentLib && d.item.libraryId === currentLib.id &&
      (d.item.kind === 'artist' || d.item.kind === 'album' || d.item.kind === 'track')
  })
  // What the rail highlights: the open artist (an album's is its parent),
  // else the chosen listing.
  const railCurrent = $derived.by(() => {
    const d = detail
    if (!d || !musicDetail) return musicComp ? 'COMP' : 'ALL'
    return d.item.kind === 'artist' ? d.item.id : (d.item.parentId ?? '')
  })

  // Search
  let searchQ = $state('')
  let searchResults: Item[] = $state([])
  let searchTimer: ReturnType<typeof setTimeout> | undefined

  function onSearchInput() {
    clearTimeout(searchTimer)
    const q = searchQ.trim()
    if (q.length < 2) {
      searchResults = []
      discoverResults = []
      return
    }
    searchTimer = setTimeout(async () => {
      // TMDB is slower than the library, so the library's own results are
      // not held back for it: the row below them fills in when it answers.
      if (reqStatus?.enabled) {
        discoverSearch(q)
          .then((r) => { if (searchQ.trim() === q) discoverResults = r })
          .catch(() => (discoverResults = []))
      } else {
        discoverResults = []
      }
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
    discoverResults = []
  }

  // Results hang over the page like any other popup, so they dismiss like
  // one. Only the list is cleared, not the query: clicking away should not
  // also wipe what was typed.
  $effect(() => {
    if (searchResults.length === 0 && discoverResults.length === 0) return
    return onDismiss('.search', () => {
      searchResults = []
      discoverResults = []
    })
  })

  // ---- Requests: asking for what the library does not have ----
  // Shown only to accounts that may request, and only for the kinds a
  // connected Sonarr (series) or Radarr (films) can fetch; the server
  // decides both.
  let reqStatus: RequestStatus | null = $state(null)
  const loadRequestStatus = () =>
    requestStatus().then((s) => (reqStatus = s)).catch(() => (reqStatus = null))
  // The phone's tab bar gains Requests for accounts that may request.
  // It gains Rooms too, when Home Assistant has something for this user.
  const navTabs = $derived.by(() => {
    const extra = [
      ...(reqStatus && reqStatus.level !== 'none' ? [{ id: 'requests' as Tab, label: 'Requests', icon: Inbox }] : []),
      ...(haVisible() ? [{ id: 'rooms' as Tab, label: 'Rooms', icon: Lamp }] : []),
    ]
    return extra.length ? [...tabs.slice(0, 3), ...extra, tabs[3]] : tabs
  })

  // ---- Home Assistant: rooms, controls and modes ----
  // Loaded for whoever is signed in, again when someone else signs in on
  // this browser; the event stream keeps it current after that.
  const haUser = $derived.by(() => (gate === 'in' ? (me?.id ?? '') : ''))
  $effect(() => {
    if (haUser) untrack(() => void loadHA())
  })
  // "Not in your library": search results from TMDB.
  let discoverResults: DiscoverResult[] = $state([])
  const requestLabel: Record<RequestState, string> = {
    requested: 'Requested', approved: 'Approved', downloading: 'Downloading', available: 'Available', declined: 'Declined',
  }

  // A title page from TMDB, for something the library does not have.
  let discover: DiscoverTitle | null = $state(null)
  let discoverKey: { type: 'movie' | 'tv'; id: number } | null = $state(null)
  let discoverErr = $state('')
  let choosingSeasons = $state(false)
  let pickedSeasons: number[] = $state([])
  let requestBusy = $state(false)
  let requestMsg = $state('')
  async function openDiscover(type: 'movie' | 'tv', id: number) {
    tab = 'discover'
    nowPlayingOpen = false
    discoverKey = { type, id }
    discover = null
    discoverErr = ''
    choosingSeasons = false
    pickedSeasons = []
    requestMsg = ''
    syncURL()
    await loadDiscover()
  }
  async function loadDiscover() {
    const k = discoverKey
    if (!k) return
    try {
      const d = await discoverTitle(k.type, k.id)
      if (discoverKey === k) discover = d
    } catch (err) {
      if (discoverKey === k) discoverErr = errText(err)
    }
  }
  // The open request a season (or the whole series) is covered by.
  const seasonRequest = (n: number) => discover?.seasons?.find((s) => s.seasonNumber === n)?.request
  async function requestTitle(seasons?: number[]) {
    if (!discover) return
    requestBusy = true
    requestMsg = ''
    try {
      const r = await createRequest({ mediaType: discover.mediaType, tmdbId: discover.tmdbId, seasons })
      requestMsg = r.error
        ? `Saved, but it could not be sent yet (${r.error}). An admin will see it.`
        : r.status === 'requested'
          ? 'Requested. It will be sent once an admin approves it.'
          : 'Requested. It is on its way.'
      choosingSeasons = false
      pickedSeasons = []
      await loadDiscover()
    } catch (err) {
      requestMsg = errText(err)
    } finally {
      requestBusy = false
    }
  }
  function toggleSeasonPick(n: number) {
    pickedSeasons = pickedSeasons.includes(n) ? pickedSeasons.filter((x) => x !== n) : [...pickedSeasons, n].sort((a, b) => a - b)
  }
  async function openLibraryItem(id: string) {
    try {
      const d = await itemDetail(id)
      await showItem(d.item)
    } catch (err) {
      notify('error', errText(err))
    }
  }
  function openFound(r: DiscoverResult) {
    clearSearch()
    void openDiscover(r.mediaType, r.tmdbId)
  }

  // The Requests page: your own, newest first (an admin sees everyone's).
  let myRequests: MediaRequest[] = $state([])
  let requestsErr = $state('')
  function loadMyRequests() {
    listRequests(me?.role === 'admin')
      .then((r) => {
        myRequests = r
        requestsErr = ''
      })
      .catch((err) => (requestsErr = errText(err)))
  }
  async function withdraw(r: MediaRequest) {
    const before = myRequests
    myRequests = myRequests.filter((x) => x.id !== r.id)
    try {
      await withdrawRequest(r.id)
    } catch (err) {
      myRequests = before
      requestsErr = errText(err)
    }
  }
  function requestScope(r: MediaRequest): string {
    if (r.mediaType === 'movie') return 'Film'
    const parts: string[] = []
    if (r.seasons.length) parts.push(`${r.seasons.length === 1 ? 'Season' : 'Seasons'} ${r.seasons.join(', ')}`)
    if (r.episodes.length) parts.push(r.episodes.map((e) => epCode(e.season, e.episode)).join(', '))
    return parts.length ? parts.join(' · ') : 'Whole series'
  }
  const epCode = (s: number, e: number) => `S${String(s).padStart(2, '0')}E${String(e).padStart(2, '0')}`
  // SQLite's datetime('now') is UTC without a zone; say so before parsing.
  const utcDate = (s: string) => airLabel(s.includes('T') ? s : s.replace(' ', 'T') + 'Z')

  // What a series held is missing, by Sonarr's account: seasons not on
  // disk on the series page, episodes on the season page, both greyed and
  // after what can be played.
  let missing: MissingInfo | null = $state(null)
  let missingFor = ''
  let missingBusy = $state('')
  // Closures rather than plain expressions: TypeScript would otherwise
  // narrow `detail` to its initial null here.
  const missingSeriesId = $derived.by(() => {
    const it = detail?.item
    return it?.kind === 'series' ? it.id : it?.kind === 'season' ? (it.parentId ?? '') : ''
  })
  function loadMissing(seriesId: string) {
    missingFor = seriesId
    itemMissing(seriesId)
      .then((m) => { if (missingFor === seriesId) missing = m })
      .catch(() => { if (missingFor === seriesId) missing = null })
  }
  $effect(() => {
    const id = missingSeriesId
    const on = !!reqStatus?.enabled && !!reqStatus.tv
    if (!id || !on) {
      missing = null
      missingFor = ''
      return
    }
    if (id === missingFor) return
    missing = null
    loadMissing(id)
  })
  const missingSeason = $derived.by(() => {
    const d = detail
    return d?.item.kind === 'season' ? missing?.seasons.find((s) => s.seasonNumber === d.item.seasonNum) : undefined
  })
  // Seasons Sonarr lists with nothing on disk, for the series page.
  const absentSeasons = $derived.by(() => {
    const d = detail
    if (d?.item.kind !== 'series' || !missing) return []
    return missing.seasons.filter((s) => !d.children.some((c) => c.kind === 'season' && c.seasonNum === s.seasonNumber))
  })
  // The open request covering a season or an episode of the series shown.
  function coveringRequest(season: number, episode?: number): MediaRequest | undefined {
    return missing?.requests.find((r) =>
      (r.seasons.length === 0 && r.episodes.length === 0) || r.seasons.includes(season) ||
      (episode !== undefined && r.episodes.some((e) => e.season === season && e.episode === episode)))
  }
  // Request it (users needing approval) or have Sonarr search now (admins
  // and users whose requests go straight through).
  async function askForMissing(season: number, episode?: number) {
    if (!missing || !reqStatus || !missingFor) return
    const key = episode === undefined ? `s${season}` : `s${season}e${episode}`
    const seasons = episode === undefined ? [season] : []
    const episodes = episode === undefined ? [] : [{ season, episode }]
    missingBusy = key
    try {
      if (reqStatus.level === 'auto') {
        await searchMissing(missingFor, seasons, episodes)
        notify('info', `Sonarr is searching for ${episode === undefined ? `season ${season}` : epCode(season, episode)}.`)
      } else {
        const r = await createRequest({ mediaType: 'tv', tmdbId: missing.tmdbId, seasons, episodes })
        notify('info', r.error ? `Requested; it could not be sent yet (${r.error}).` : 'Requested. An admin will take a look.')
      }
      loadMissing(missingFor)
    } catch (err) {
      notify('error', errText(err))
    } finally {
      missingBusy = ''
    }
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
    switchLibraryTo(target.libraryId)
    await openItem(target.id, false, target)
  }

  function readQuality(): PlayQuality {
    try {
      return (pref('quality', 'auto') as PlayQuality)
    } catch {
      return 'auto'
    }
  }

  function setTheme(id: string) {
    // Choosing a theme while a holiday's shows means "mine, for the rest of
    // this holiday"; choosing the holiday's own brings it back.
    if (looks.occasion) {
      if (id === looks.occasion.themeId) setPref('holiday-skip', '')
      else if (activeTheme()?.occasion) setPref('holiday-skip', holidayKey(looks.occasion))
    }
    looks.chosen = id
    chooseTheme(id) // a deliberate choice, so it is remembered
    paint()
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
  // Restored synchronously so a reload shows the queue at first paint
  // rather than blanking it and filling it in a moment later.
  // One record holds whichever queue this device has, films or tracks
  // (lib/queue.ts): the mirror says which kind it was; the music one is
  // restored by the queue store once signed in.
  const restoredQueue = loadQueue()
  const restoredVideo = restoredQueue.kind === 'video' ? restoredQueue : blankQueue()
  let queue: Item[] = $state(restoredVideo.items)
  let queueIndex = $state(restoredVideo.index)
  const upNext = $derived(queue.slice(queueIndex + 1))

  // Kept across reloads and told to the server, so another device can take
  // it over. Reading both values here is what subscribes this effect to
  // them; an empty queue that was empty already is not a change, so a
  // music queue in the record is left alone.
  let lastVideoLen = restoredVideo.items.length
  $effect(() => {
    const n = queue.length
    const at = queueIndex
    if (n === 0 && lastVideoLen === 0) return
    lastVideoLen = n
    saveQueue({ ...blankQueue('video'), items: queue, ords: queue.map((_, i) => i), index: at })
  })
  // Saves reach the server once signed in.
  $effect(() => {
    setQueueRemote(!!me)
  })
  let devMenu = $state<'send' | 'take' | null>(null)
  $effect(() => {
    if (!devMenu) return
    return onDismiss('.devwrap', () => (devMenu = null))
  })
  // Always wanted: the strip offering to continue what another device
  // plays (below) reads them on every page.
  $effect(() => {
    if (me) loadDevices()
  })
  const takeable = $derived(otherDevices.filter((d) => d.queue?.current))

  async function sendQueueTo(d: Device) {
    devMenu = null
    try {
      await sendTo(d)
    } catch {
      /* already said */
    }
  }
  // What "Play on…" sends: the queue from where it is. When the title
  // playing is not the queue's current entry — the next episode rolled on
  // by itself, or a title played on its own — it is that title and what is
  // queued after it.
  function handoffList(): StoredQueue {
    if (mq.active) return musicSnapshot()
    const now = playing?.item
    const items = !now || queue[queueIndex]?.id === now.id ? queue : [now, ...upNext]
    const index = !now || queue[queueIndex]?.id === now.id ? queueIndex : 0
    return { ...blankQueue('video'), items, ords: items.map((_, i) => i), index }
  }
  async function sendTo(d: Device) {
    const rec = recordOf(handoffList())
    try {
      await playOnDevice(d.id, rec)
    } catch (err) {
      notify('error', errText(err))
      throw err
    }
    if (playing) closePlayer()
    if (mq.active) clearMusic()
    notify('info', `Playing on ${deviceTitle(d)}.`)
  }
  async function takeQueueFrom(d: Device) {
    devMenu = null
    if (d.playing && d.online) return continueHere(d)
    try {
      const q = await deviceQueue(d.id)
      if (q.items.length === 0) {
        notify('info', `Nothing on ${deviceTitle(d)}'s queue can be played here.`)
        return
      }
      if (q.items[q.index]?.kind === 'track') {
        playing = null
        adoptMusicQueue(fromHandoff(q), { autoplay: true })
      } else playList(q.items, q.index)
    } catch (err) {
      notify('error', errText(err))
    }
  }
  // ---- Continue here what another device plays ----
  // While another of the user's devices is playing and this one is not, a
  // strip above the bottom of the screen offers to carry on here; that
  // device pauses (see the takeover event). A paused one is not offered,
  // so a tab left paused somewhere does not nag for ever; the Queue page's
  // "Continue from…" still reaches it.
  let devicesSoon: ReturnType<typeof setTimeout> | undefined
  const elsewhere = $derived(otherDevices.find((d) => d.online && d.playing && !d.playing.paused) ?? null)
  let clockNow = $state(Date.now())
  $effect(() => {
    if (!elsewhere) return
    const t = setInterval(() => (clockNow = Date.now()), 1000)
    return () => clearInterval(t)
  })
  // Where it has got to, moved on from its last heartbeat.
  function elsewhereAt(d: Device): number {
    const p = d.playing
    if (!p) return 0
    const since = p.paused ? 0 : Math.max(0, (clockNow - Date.parse(p.reportedAt)) / 1000)
    return Math.min(p.durationSecs || Infinity, p.positionSecs + since)
  }
  const clock = (secs: number) => {
    const s = Math.floor(secs)
    const h = Math.floor(s / 3600), m = Math.floor((s % 3600) / 60), r = String(s % 60).padStart(2, '0')
    return h ? `${h}:${String(m).padStart(2, '0')}:${r}` : `${m}:${r}`
  }
  let continuing = $state(false)
  async function continueHere(d: Device) {
    if (continuing) return
    continuing = true
    try {
      const t = await takeOverDevice(d.id)
      const cur = t.items[t.index]
      if (!cur) {
        notify('info', `Nothing ${deviceTitle(d)} is playing can be played here.`)
        return
      }
      if (cur.kind === 'track') {
        playing = null
        adoptMusicQueue(fromHandoff(t), { autoplay: true })
      } else playList(t.items, t.index)
    } catch (err) {
      notify('error', errText(err))
    } finally {
      continuing = false
      loadDevices()
    }
  }

  // Another device sent its queue here.
  function receiveHandoff(data: unknown) {
    const h = data as ({ from?: string } & HandoffQueue) | null
    if (!h?.items?.length) return
    notify('info', `Playing from ${h.from ?? 'another device'}.`)
    if (h.items[h.index ?? 0]?.kind === 'track') {
      playing = null
      adoptMusicQueue(fromHandoff(h), { autoplay: true })
    } else playList(h.items, h.index ?? 0)
  }

  /** Queue a collection (or any list) and start at the given position. */
  function playList(items: Item[], startIndex = 0) {
    if (items.length === 0) return
    const at = Math.max(0, Math.min(startIndex, items.length - 1))
    queue = items
    queueIndex = at
    startPlay(items[at], readQuality(), false, undefined, true)
  }

  /**
   * Add to the end of the queue without starting anything.
   *
   * It used to start playing when the queue was empty, on the grounds that
   * appending to an empty queue showed nothing. That is no longer true — the
   * queue has its own page and a count in the sidebar — and the old
   * behaviour made "Add to queue" start a film, which is not what it says.
   */
  function enqueue(item: Item) {
    queue = [...queue, item]
  }

  /** Add several at once, in order; used by "Add all to queue". */
  function enqueueAll(items: Item[]) {
    if (items.length === 0) return
    queue = [...queue, ...items]
  }

  const dequeue = (index: number) => removeFromQueue(queueIndex + 1 + index)

  /**
   * Remove one entry by its position in the whole queue.
   *
   * Taking out something already played shifts the pointer with it, so the
   * current film stays current — otherwise tidying up the history would
   * silently skip to the next title.
   */
  function removeFromQueue(absolute: number) {
    if (absolute < 0 || absolute >= queue.length) return
    queue = queue.filter((_, i) => i !== absolute)
    if (absolute < queueIndex) queueIndex -= 1
    else if (absolute === queueIndex) queueIndex = Math.min(queueIndex, Math.max(0, queue.length - 1))
  }

  /**
   * Move an entry one place earlier or later, by its position in the whole
   * queue. The pointer follows the same rule as removal: whatever is playing
   * stays playing.
   */
  function moveInQueue(absolute: number, delta: -1 | 1) {
    const to = absolute + delta
    if (absolute < 0 || absolute >= queue.length || to < 0 || to >= queue.length) return
    const next = [...queue]
    ;[next[absolute], next[to]] = [next[to], next[absolute]]
    queue = next
    if (absolute === queueIndex) queueIndex = to
    else if (to === queueIndex) queueIndex = absolute
  }

  /** Empty the queue completely, including anything playing. */
  const emptyQueue = () => {
    queue = []
    queueIndex = 0
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

  // The stream index of an image subtitle being painted into the picture,
  // carried across restarts so a quality change does not drop it. 0 leaves
  // the subtitle to the user's preferences; -1 is the viewer having chosen
  // none, which the server then respects rather than reapplying them.
  let burnSub = $state(0)
  // A text subtitle to show once a restart that dropped a burned one is up.
  let showAfterBurn = ''

  /** Restart playback with a bitmap subtitle burned in, or (-1) without one. */
  function burnSubtitle(streamIndex: number, thenShow = '') {
    burnSub = streamIndex
    showAfterBurn = thenShow
    if (playing) startPlay(playing.item, playing.quality, false, playing.info.audioTrack, true)
  }

  // ---- Notices ----
  // One line at the bottom of the screen for what the viewer asked for and
  // did not get — a title that would not start, and why. Errors stay until
  // dismissed or replaced; anything else fades on its own.
  let notice = $state<{ kind: 'info' | 'error'; text: string; action?: NoticeAction } | null>(null)
  let noticeTimer: ReturnType<typeof setTimeout> | undefined
  function notify(kind: 'info' | 'error', text: string, ms?: number, action?: NoticeAction) {
    clearTimeout(noticeTimer)
    notice = { kind, text, action }
    noticeTimer = setTimeout(() => (notice = null), ms ?? (kind === 'error' ? 15000 : 4000))
  }

  // The item being started, if any. Starting can take a few seconds (a
  // transcode spinning up, a NAS disk waking), and a second press used to
  // start a second session behind the first, until the browser ran out of
  // connections and nothing could start at all.
  let starting = $state<string | null>(null)

  async function startPlay(item: Item, quality: PlayQuality = readQuality(), fromStart = false, audioTrack?: number, keepQueue = false) {
    if (mq.active) clearMusic()
    if (starting === item.id) return
    // Anything the viewer starts directly is a fresh queue of one; only the
    // queue itself plays on without resetting it.
    if (!keepQueue) {
      queue = [item]
      queueIndex = 0
      burnSub = 0 // a new film starts without a burned-in track
    }
    starting = item.id
    // Said only if it is taking a moment; a quick start needs no message.
    const slow = setTimeout(() => notify('info', `Starting ${item.title}…`, 30000), 600)
    try {
      const info = await playItem(item.id, quality, fromStart, audioTrack, burnSub || undefined)
      if (showAfterBurn) {
        info.subtitle = showAfterBurn
        showAfterBurn = ''
      }
      clearTimeout(slow)
      if (notice?.kind === 'info') notice = null
      playingPos = info.positionSecs || 0
      playing = { item, info, quality, gen: ++playGen }
      const inContext = detail && (detail.item.id === item.id || detail.item.id === item.parentId)
      if (!inContext) showItem(item) // full player lives on the item's (or its season's) page
    } catch (e) {
      clearTimeout(slow)
      console.error('play failed', e)
      notify('error', `Couldn't play ${item.title}. ${e instanceof Error ? e.message : String(e)}`)
    } finally {
      starting = null
    }
  }

  const storageOf = (libraryId?: string) => libraries.find((l) => l.id === libraryId)?.storage

  function closePlayer() {
    playing = null
    // Fullscreen belongs to the page while playing; nothing to show in it now.
    if (document.fullscreenElement) document.exitFullscreen().catch(() => {})
    poll() // refresh continue-watching
  }

  // The next episode replaces this one when it is ready, rather than
  // clearing the player first: a gap would drop out of fullscreen and
  // flash the page between episodes.
  function playNext(next: Item) {
    startPlay(next, readQuality(), false, undefined, true)
  }

  function changeQuality(q: PlayQuality) {
    try {
      setPref('quality', q)
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
    libTab = 'recommended'
    gridQuery = ''
    activeLetter = ''
    genre = null
    genreItems = []
    itemsMark = ''
    if (lib.mediaType === 'music') {
      // Its tabs fetch a page at a time; nothing is loaded whole.
      musicTab = 'listen'
      musicGenre = ''
      musicComp = false
      syncURL()
      return
    }
    loadGenres(lib)
    loadCollections(lib)
    loadRecommended(lib)
    const items = await libraryItems(lib.id)
    gridItems = items.filter(gridFilter(lib))
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
      selEpId = null
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
    selEpId = null
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
    if (selectEp) focusEpisode(selectEp.id)
    syncURL()
  }

  // ---- Episode rows on a season page ----
  // An episode URL lands on its season with that row highlighted.
  let selEpId: string | null = $state(null)
  // Where to carry on in the series or season being viewed.
  let upNextHere: NextUpInfo | null = $state(null)
  // Where to carry on in this series. Decided by the server so the web
  // client and the native apps cannot disagree about where someone is up
  // to (see CLAUDE.md). Follows the page shown however it was reached: a
  // link, its address, Back or a breadcrumb.
  const nextUpFor = $derived.by(() => {
    const it = detail?.item
    return it?.kind === 'series' || it?.kind === 'season' ? it.id : ''
  })
  $effect(() => {
    const forID = nextUpFor
    upNextHere = null
    if (!forID) return
    seriesNextUp(forID)
      .then((u) => {
        if (detail?.item.id === forID) upNextHere = u
      })
      .catch(() => {})
  })
  // The row whose ⋯ menu is open, and the row showing its whole overview.
  let epMenu: string | null = $state(null)
  // The episode or film whose intro and credits an administrator or
  // trusted editor is correcting.
  let segmentsFor: Item | null = $state(null)
  // What came of "Detect intro & credits" on an episode row, shown in it.
  let epDetect: { id: string; text: string } | null = $state(null)
  // Where detection stands, for whoever can start it: a page opened while
  // it runs shows its progress straight away.
  $effect(() => {
    if (canEditSegments(me)) loadDetection()
  })
  async function detectEpisode(ep: Item) {
    epMenu = null
    epDetect = { id: ep.id, text: 'Sending to detection…' }
    try {
      epDetect = { id: ep.id, text: await detect(ep) }
    } catch (err) {
      epDetect = { id: ep.id, text: err instanceof Error ? err.message : String(err) }
    }
  }
  let epOpen: string | null = $state(null)

  $effect(() => {
    if (!epMenu) return
    return onDismiss('.epmenuwrap', () => (epMenu = null))
  })

  async function focusEpisode(id: string) {
    selEpId = id
    await tick()
    document.getElementById(`ep-${id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })
  }

  const epResumable = (ep: Item) => !ep.watched && (ep.positionSecs ?? 0) > 10
  // The playing episode follows the player live; the rest show where the
  // viewer left them.
  function epProgress(ep: Item): number {
    if (!ep.runtimeSecs) return 0
    const pos = playing?.item.id === ep.id ? playingPos : (ep.positionSecs ?? 0)
    return Math.min(100, (pos / ep.runtimeSecs) * 100)
  }

  // Optimistic like the grid's toggle. The season's next-up is refetched,
  // since marking an episode seen moves where the series carries on.
  async function toggleEpWatched(ep: Item) {
    epMenu = null
    if (!detail) return
    const before = detail
    const next = !ep.watched
    detail = {
      ...detail,
      children: detail.children.map((c) => (c.id === ep.id ? { ...c, watched: next, positionSecs: 0 } : c)),
    }
    try {
      await setWatched(ep.id, next)
      continueWatching().then((r) => (resume = r)).catch(() => {})
      const forID = detail.item.id
      seriesNextUp(forID)
        .then((u) => {
          if (detail?.item.id === forID) upNextHere = u
        })
        .catch(() => {})
    } catch (err) {
      detail = before
      console.error('mark watched failed', err)
    }
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
    selEpId = null
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
    selEpId = null
    trail = []
    libTab = 'recommended'
    genre = null
    collection = null
    // Reached from Home, search or the music bar, the library's own list
    // may never have been fetched; the crumb would then land on "no items".
    if (currentLib) switchLibraryTo(currentLib.id)
    syncURL()
  }

  async function toCrumb(index: number) {
    const target = trail[index]
    detail = await itemDetail(target.id)
    trail = trail.slice(0, index)
    selEpId = null
    syncURL()
  }

  function runtime(secs?: number): string {
    if (!secs) return ''
    const m = Math.round(secs / 60)
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`
  }

  // How many of the series' seasons are actually on disk, against what the
  // provider says exists — the gap is the point.
  const seasonsHeld = $derived(detail?.children.filter((c) => c.kind === 'season').length ?? 0)

  /** "12 Mar 2010" — a broadcast date, in the order British readers expect. */
  function airLabel(iso: string): string {
    const d = new Date(iso)
    if (Number.isNaN(d.getTime())) return ''
    return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'short', year: 'numeric' })
  }

  // h:mm:ss for a live playhead, where runtime()'s whole minutes would sit
  // still for a minute at a time and look stuck.
  function fmtClock(secs: number): string {
    const t = Math.max(0, Math.floor(secs))
    const h = Math.floor(t / 3600)
    const m = Math.floor((t % 3600) / 60)
    const sec = t % 60
    return h > 0
      ? `${h}:${String(m).padStart(2, '0')}:${String(sec).padStart(2, '0')}`
      : `${m}:${String(sec).padStart(2, '0')}`
  }

  // Grouped by role — "Director: Phil Lord, Christopher Miller" — rather
  // than one entry per credit, which repeats the role for every co-director
  // and reads as noise. Producer credits are dropped: a big film has a
  // dozen and nobody scans a detail page for them.
  // Creator leads: for a series it is the headline credit, the way Director
  // is for a film. TMDB keeps it outside `credits`, in `created_by`, which is
  // why it arrives as a crew job of our own naming.
  const CREW_ROLES = ['Creator', 'Director', 'Screenplay', 'Writer', 'Story', 'Novel', 'Director of Photography', 'Original Music Composer']

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

  // Whether the open library is being scanned. Seeded from the libraries
  // list (so a client loading mid-scan knows) and kept current by the
  // "items" event, which carries the same one bit for every user.
  let scanningNow = $state(false)
  const libraryScanning = $derived(scanningNow || (currentLib?.scanning ?? false))
  // The details pass after a big scan: titles are on screen, their posters
  // and synopses still arriving. Counts only, from the items event.
  let enriching = $state<{ done: number; total: number } | null>(null)

  async function refreshLibraryItems(full = false) {
    if (!currentLib || currentLib.mediaType === 'music') return
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
      const next = full || !itemsMark ? items : merge(gridItems, items)
      gridItems = next.filter(gridFilter(lib))
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
    // The account's own preferences, which may differ from this browser's
    // mirror — adopt them and re-apply anything already painted from the
    // local copy.
    // Playback choices are read through pref() each time they are needed,
    // so they pick the server's values up on their own; the theme was
    // already painted from the local mirror and needs re-applying.
    //
    // Adopting is not choosing: the stored choice is taken as it is, and
    // paint() decides what is on screen (a holiday's theme may be).
    const adoptPrefs = () => loadPrefs().then((p) => {
      if (p.theme) looks.chosen = p.theme
      paint()
    }).catch(() => {})
    adoptPrefs()
    // The music queue as it was: the mirror first, else the server's copy
    // (storage cleared, or another browser profile), paused where it was.
    if (!restoreMusicQueue()) {
      myQueue().then((q) => {
        if (q.items.length > 0 && q.items[q.index]?.kind === 'track' && !mq.active) adoptMusicQueue(fromHandoff(q), { autoplay: false })
      }).catch(() => {})
    }
    poll()
    loadRequestStatus()
    applyRoute(location.pathname)
    const onPop = () => applyRoute(location.pathname)
    window.addEventListener('popstate', onPop)
    // Live updates over SSE; the slow poll below is only the fallback for
    // anything a dropped event missed.
    const closeEvents = subscribeEvents(
      {
        // A custom theme was made, changed or deleted: the list (and what
        // is painted, if it was that one) catches up.
        themes: () => refreshLooks().catch(() => {}),
        // A track rated on another of this viewer's screens.
        rating: (data) => {
          const d = data as { itemId?: string; stars?: number } | null
          if (d?.itemId) ratedElsewhere(d.itemId, d.stars ?? 0)
        },
        libraries: () => {
          listLibraries().then((l) => (libraries = l)).catch(() => {})
          continueWatching().then((r) => (resume = r)).catch(() => {})
          // Scan end: pruning and version grouping have run, and neither is
          // expressible as a delta (a removed row simply stops existing), so
          // this is the one refetch that must be a full one.
          if (currentLib) {
            refreshLibraryItems(true).catch(() => {})
            // Recently added is ranked server-side, so new episodes only
            // move their series up once the rows are fetched again.
            loadRecommended(currentLib)
          }
        },
        // This user's devices changed: one signed in or out, opened or
        // closed the app, or saved a queue.
        devices: () => {
          clearTimeout(devicesSoon)
          devicesSoon = setTimeout(loadDevices, 250)
        },
        handoff: receiveHandoff,
        // Another of this user's devices is continuing what this one plays.
        takeover: (data) => {
          const t = data as { to?: string; itemId?: string } | null
          if (!t?.itemId) return
          window.dispatchEvent(new CustomEvent(TAKEN_OVER, { detail: { itemId: t.itemId } }))
          notify('info', `Continued on ${t.to ?? 'another device'}.`)
        },
        // Preferences changed on another device, or an admin changed a
        // default or a lock.
        prefs: adoptPrefs,
        // Intro and credits detection moved on: the pages it was asked
        // from show how it is getting on.
        segments: (data) => setDetection(data as Parameters<typeof setDetection>[0]),
        // This device was signed out from another: back to the sign-in screen.
        revoked: () => window.dispatchEvent(new Event(UNAUTHORISED)),
        // Home Assistant: changed states, or (no data) fetch everything again.
        homeassistant: onHAEvent,
        // A mode started from here moved on a step, finished or failed.
        'mode-run': (data) => {
          const n = onModeRun(data)
          if (n) notify(n.kind, n.text, n.ms)
        },
        // A request moved (made, approved, grabbed, found on disk): refresh
        // whatever shows one.
        requests: () => {
          loadRequestStatus()
          if (tab === 'requests') loadMyRequests()
          if (tab === 'discover') loadDiscover()
          if (missingFor) loadMissing(missingFor)
        },
        // The server's settings can change what may be requested.
        settings: loadRequestStatus,
        // Fired throughout a scan as items land, so the grid fills in live.
        items: (data) => {
          const e = data as { libraryId?: string; scanning?: boolean; enriched?: number; toEnrich?: number } | null
          if (e?.libraryId && currentLib?.id === e.libraryId) {
            scanningNow = e.scanning ?? false
            enriching = e.toEnrich ? { done: e.enriched ?? 0, total: e.toEnrich } : null
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

  // Where the music bar sits: the music-bar preference, read at first
  // paint from the local mirror and followed as the server's answer and
  // Settings change it.
  let musicBarAt = $state<'top' | 'bottom'>(pref('music-bar', 'top') === 'bottom' ? 'bottom' : 'top')
  $effect(() => onPrefs((s) => (musicBarAt = s.prefs['music-bar']?.value === 'bottom' ? 'bottom' : 'top')))

  // How big the sidebar is: the sidebar-size preference (a device's, like
  // the interface). "compact" is the size the sidebar had before the
  // preference existed; "regular" is about 15 percent larger; "large" is
  // for a desktop looked at from a distance. The scale drives the rail's
  // widths, type and spacing through --side-scale on .body; the icons take
  // their size as a number.
  type SidebarSize = 'compact' | 'regular' | 'large'
  const sidebarSizes: Record<SidebarSize, { scale: number; icon: number; small: number }> = {
    compact: { scale: 1, icon: 16, small: 14 },
    regular: { scale: 1.15, icon: 19, small: 16 },
    large: { scale: 1.35, icon: 22, small: 18 },
  }
  const asSidebarSize = (v: string): SidebarSize => (v === 'compact' || v === 'large' ? v : 'regular')
  let sidebarSize = $state<SidebarSize>(asSidebarSize(pref('sidebar-size', 'regular')))
  $effect(() => onPrefs((s) => (sidebarSize = asSidebarSize(s.prefs['sidebar-size']?.value ?? 'regular'))))
  const sideScale = $derived(sidebarSizes[sidebarSize].scale)
  const sideIcon = $derived(sidebarSizes[sidebarSize].icon)
  const sideIconSmall = $derived(sidebarSizes[sidebarSize].small)

  onMount(() => {
    installLooks()
    refreshLooks().catch(() => {})
    checkAuth().then(() => { if (gate === 'in') boot() })
    // Any 401 from the API means the session ended: back to sign-in.
    const onUnauthorised = () => {
      if (gate !== 'in') return
      me = null
      picked = null
      gate = 'login'
      // A device that signs in by itself goes back to its own user rather
      // than leaving the TV on a sign-in screen.
      if (autoUser) void backToAutoUser()
      else loadProfiles()
    }
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

<!-- Small artwork beside a title in a list (queue, up next, search). An
     episode shows its still, so it is landscape like the season's episode
     list; anything else keeps its poster's shape. -->
{#snippet thumb(item: Item)}
  {@const musical = item.kind === 'album' || item.kind === 'track' || item.kind === 'artist'}
  <span class="thumb" class:wide={item.kind === 'episode'} class:square={musical}>
    {#if item.poster}
      <img src={item.poster} alt="" loading="lazy" />
    {:else if musical}
      <MusicIcon size={16} />
    {:else if item.kind === 'series' || item.kind === 'season' || item.kind === 'episode'}
      <Tv size={16} />
    {:else}
      <Clapperboard size={16} />
    {/if}
  </span>
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

<!-- An empty library means one of two quite different things, and telling a
     viewer to go and start a scan while one is already running is the
     unhelpful one. -->
{#snippet emptyLibrary()}
  {#if libraryScanning}
    <div class="empty tall">
      <p><strong>Scanning…</strong></p>
      <p>Titles appear here as they are found.</p>
    </div>
  {:else if me?.role === 'admin'}
    <div class="empty">Nothing here yet — run a scan from {@render adminLink()}.</div>
  {:else}
    <div class="empty">Nothing here yet. Ask an administrator to scan this library.</div>
  {/if}
{/snippet}

<!-- Shown on a library, and on any title in it, while its storage cannot be
     reached: browsing still works from what was scanned, playing does not. -->
{#snippet storageWarning(libraryId: string)}
  {@const st = storageOf(libraryId)}
  {#if st && !st.available}
    <div class="storage-warn" role="status">
      <TriangleAlert size={16} />
      <span>This library's storage isn't reachable ({st.path}: {st.error}), so its titles can't be played until it's back. Check that the share is connected.</span>
    </div>
  {/if}
  {#if enriching && currentLib?.id === libraryId}
    <p class="enrich-note muted" role="status">Details are still arriving: {enriching.done} of {enriching.total} titles have their artwork and synopsis.</p>
  {/if}
{/snippet}

<!-- A home-screen row heading, with arrows for pointer users; touch scrolls
     the row directly, so the arrows are hidden where there is no hover. -->
{#snippet shelfHead(title: string, library: string)}
  <div class="shelf-head">
    <h2>{title}{#if library}<span class="shelf-lib">{' · '}{library}</span>{/if}</h2>
    <span class="shelf-nav">
      <button aria-label="Scroll {title} back" onclick={(e) => scrollShelf(e, -1)}><ChevronLeft size={18} /></button>
      <button aria-label="Scroll {title} on" onclick={(e) => scrollShelf(e, 1)}><ChevronRight size={18} /></button>
    </span>
  </div>
{/snippet}

<!-- A person's face: their picture, or their initial on the colour they
     chose (a theme token, so each theme draws it in its own palette). -->
{#snippet face(p: { displayName: string; avatar?: string; avatarColour?: string }, cls: string)}
  {#if p.avatar}
    <img class="{cls} face-img" src={p.avatar} alt="" />
  {:else}
    <span class={cls} class:tinted={!!p.avatarColour} style:--face={p.avatarColour ? `var(--${p.avatarColour})` : undefined}>{p.displayName.slice(0, 1).toUpperCase()}</span>
  {/if}
{/snippet}

<!-- Another device's queue, offered to take over: which device, and what
     it is on. -->
{#snippet takeRow(d: Device)}
  {@const cur = d.queue?.current}
  <button class="um-item takerow" role="menuitem" onclick={() => takeQueueFrom(d)}>
    {#if cur}{@render thumb(cur)}{/if}
    <span class="takebody">
      <span class="devmenu-name">{deviceTitle(d)}{#if d.online}<span class="tag on">open now</span>{/if}</span>
      <span class="muted small">
        {#if cur}{d.queue?.seriesTitle ? `${d.queue.seriesTitle} · ` : ''}{cur.kind === 'episode' ? `S${String(cur.seasonNum).padStart(2, '0')}E${String(cur.episodeNum).padStart(2, '0')} · ` : ''}{cur.title}{/if}
        {#if (d.queue?.count ?? 0) > 1} · {d.queue?.count} queued{/if}
      </span>
    </span>
  </button>
{/snippet}

<!-- An in-progress item: an episode leads with its series, since "Episode
     1" or "The Big Bran Hypothesis" alone does not say which show it is. -->
{#snippet resumeBody(r: ResumeItem)}
  {@render poster(r.seriesPoster ? { ...r, poster: r.seriesPoster } : r)}
  <div class="progress"><div class="progress-fill" style="width: {Math.min(100, (r.positionSecs / (r.runtimeSecs || 1)) * 100)}%"></div></div>
  {#if r.kind === 'episode'}
    <span class="cell-title">{r.seriesTitle || r.title}</span>
    <span class="cell-sub">S{String(r.seasonNum).padStart(2, '0')}E{String(r.episodeNum).padStart(2, '0')}{#if r.seriesTitle}{' · '}{r.title}{/if}</span>
  {:else}
    <span class="cell-title">{r.title}</span>
  {/if}
{/snippet}

<!-- A new episode: the series' poster and title lead, since the episode's
     own still is landscape and its title alone does not say which show. -->
{#snippet newEpisodeCell(r: ResumeItem, open: () => void)}
  <button class="cell" data-nav-id={r.id} onclick={open} onfocus={() => { if (tv.on) tv.focused = r }}>
    <span class="posterwrap">
      {@render poster(r.seriesPoster ? { ...r, poster: r.seriesPoster } : r)}
      {#if r.watched}<span class="seen" title="Watched"><Check size={13} /></span>{/if}
      {#if progressPct(r) > 0}
        <span class="resume" title="{progressPct(r)}% watched"><span style="width: {progressPct(r)}%"></span></span>
      {/if}
    </span>
    <span class="cell-title">{r.seriesTitle || r.title}</span>
    <span class="cell-sub">{r.title}</span>
    <span class="cell-sub">{epLabel(r.seasonNum, r.episodeNum)}</span>
  </button>
{/snippet}

<!-- Asking for something a series is missing: a request for users whose
     requests need approval, a search in Sonarr for admins and users whose
     requests go straight through, or the state of a request already made. -->
{#snippet askButton(season: number, episode: number | undefined, what: string = '')}
  {@const covered = coveringRequest(season, episode)}
  {@const key = episode === undefined ? `s${season}` : `s${season}e${episode}`}
  {#if covered}
    <span class="reqstate tiny" data-state={covered.status}>{requestLabel[covered.status]}</span>
  {:else if reqStatus && reqStatus.level !== 'none'}
    <button class="askbtn" disabled={missingBusy === key} onclick={() => askForMissing(season, episode)}>
      {#if reqStatus.level === 'auto'}
        <Search size={13} /> {missingBusy === key ? 'Searching…' : what ? `Search ${what}` : 'Search now'}
      {:else}
        <Plus size={13} /> {missingBusy === key ? 'Requesting…' : what ? `Request ${what}` : 'Request'}
      {/if}
    </button>
  {/if}
{/snippet}

<!-- open defaults to opening the item in place, inside its library; the
     home screen passes showItem instead, which switches to the library
     first. -->
<!-- The admin pages: a link on a computer or phone; on a TV, where they
     are not offered, just where to find them. -->
{#snippet adminLink(href = '/admin/')}
  {#if tv.on}the admin pages on a computer or phone{:else}<a {href}>the admin UI</a>{/if}
{/snippet}

{#snippet cell(item: Item, open: () => void = () => openItem(item.id, false, item))}
  <button class="cell" data-nav-id={item.id} onclick={open} onfocus={() => { if (tv.on) tv.focused = item }}>
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
          tabindex={tv.on ? -1 : 0}
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
      <!-- A series that gained an episode lately says which, so a returning
           show reads as new rather than as the year it began. -->
      {#if item.kind === 'album'}{item.artist}{#if item.year} · {item.year}{/if}{:else if item.latestEpisode}{epLabel(item.latestEpisode.seasonNum, item.latestEpisode.episodeNum)}{:else if item.year}{item.year}{/if}
    </span>
  </button>
{/snippet}

{#if gate !== 'in'}
  <div class="gate theme-backdrop">
    {#if gate === 'checking'}
      <span class="muted">Connecting…</span>
    {:else if gate === 'login' && loginMode === 'picker' && profiles.length > 0 && !usePassword && !picked}
      <div class="picker">
        <h1>Who's watching?</h1>
        <div class="tiles">
          {#each profiles as p (p.id)}
            <button class="tile" onclick={() => pickProfile(p)}>
              {@render face(p, 'tile-avatar')}
              <span>{p.displayName}</span>
            </button>
          {/each}
        </div>
        <button class="link" onclick={() => (usePassword = true)}>Sign in with a username and password</button>
        {#if autoUser}<button class="link" onclick={backToAutoUser}>Back to {autoUser.displayName}</button>{/if}
      </div>
    {:else if gate === 'login' && picked}
      <form class="pinpad" onsubmit={picked.hasPin ? submitPIN : submitPickedPassword}>
        {@render face(picked, 'tile-avatar big')}
        <h1>{picked.displayName}</h1>
        {#if picked.hasPin}
          <p>Enter your PIN</p>
          <input type="password" inputmode="numeric" pattern="[0-9]*" maxlength="6" autocomplete="one-time-code" bind:value={pin} required aria-label="PIN"
            readonly={tv.on} tabindex={tv.on ? -1 : 0} />
          {#if tv.on}
            <div class="digits" role="group" aria-label="PIN digits">
              {#each ['1', '2', '3', '4', '5', '6', '7', '8', '9', 'del', '0'] as d (d)}
                <button type="button" class:del={d === 'del'} onclick={() => padDigit(d)} aria-label={d === 'del' ? 'Delete a digit' : d}>
                  {#if d === 'del'}<Delete size={20} />{:else}{d}{/if}
                </button>
              {/each}
            </div>
          {/if}
        {:else}
          <p>Enter your password</p>
          <input type="password" class="pw" autocomplete="current-password" bind:value={authPass} required aria-label="Password" />
        {/if}
        {#if authErr}<span class="err">{authErr}</span>{/if}
        <button class="go" type="submit" disabled={authBusy || (picked.hasPin ? pin.length < 4 : !authPass)}>{authBusy ? '…' : 'Sign in'}</button>
        <button class="link" type="button" onclick={() => { picked = null; authErr = '' }}>Not you? Choose another profile</button>
        {#if autoUser}<button class="link" type="button" onclick={backToAutoUser}>Back to {autoUser.displayName}</button>{/if}
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
        {#if gate === 'login' && autoUser}<button class="link" type="button" onclick={backToAutoUser}>Back to {autoUser.displayName}</button>{/if}
      </form>
    {/if}
  </div>
{/if}

<div class="backdrop theme-backdrop" aria-hidden="true"></div>
{#if shownEffect && gate === 'in'}<ThemeEffect kind={shownEffect} />{/if}

<div class="shell" style:--poster-scale={posterScales[posterZoom] ?? 1}>
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
      {#if searchResults.length > 0 || discoverResults.length > 0}
        <div class="search-results">
          {#each searchResults as r (r.id)}
            {@const open = () => { if (r.kind === 'track') playTrackResult(r); else showItem(r); clearSearch() }}
            <div class="sr" role="button" tabindex="0" onclick={open} onkeydown={(e) => e.key === 'Enter' && open()}>
              {@render thumb(r)}
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
          {#if discoverResults.length > 0}
            <!-- After the library's own results, never before: what can be
                 played comes first. -->
            <div class="sr-head">Not in your library</div>
            <div class="sr-found">
              {#each discoverResults as f (f.mediaType + f.tmdbId)}
                <button class="found" onclick={() => openFound(f)} title="{f.title}{f.year ? ` (${f.year})` : ''}">
                  {#if f.poster}
                    <img src={f.poster} alt="" loading="lazy" />
                  {:else}
                    <span class="found-ph">{#if f.mediaType === 'tv'}<Tv size={18} />{:else}<Clapperboard size={18} />{/if}</span>
                  {/if}
                  <span class="found-title">{f.title}</span>
                  <span class="found-sub">{f.mediaType === 'tv' ? 'Series' : 'Film'}{f.year ? ` · ${f.year}` : ''}</span>
                  {#if f.request}<span class="reqstate tiny" data-state={f.request}>{requestLabel[f.request]}</span>{/if}
                </button>
              {/each}
            </div>
          {/if}
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
      <button
        class="avatar"
        title={me ? `${me.displayName} (${me.username})` : 'Account'}
        aria-haspopup="menu"
        aria-expanded={showUserMenu}
        onclick={() => (showUserMenu = !showUserMenu)}
      >
        {#if me}{@render face(me, 'initial')}{:else}<User size={17} />{/if}
      </button>
      {#if showUserMenu && me}
        <div class="usermenu" role="menu">
          <div class="um-name">{me.displayName}</div>
          <div class="um-sub">{me.username} · {me.role}</div>
          {#if me.role === 'admin' && !tv.on}<a class="um-item" href="/admin/">Admin</a>{/if}
          <button class="um-item" onclick={() => { showUserMenu = false; setTab('settings') }}><KeyRound size={14} /> Account settings</button>
          <!-- On a device that signs in by itself, signing out just opens the
               profile picker, so say what it is for. -->
          {#if autoUser}
            <button class="um-item" onclick={signOut}><Users size={14} /> Switch user</button>
          {:else}
            <button class="um-item" onclick={signOut}><LogOut size={14} /> Sign out</button>
          {/if}
        </div>
      {/if}
    </div>
  </div>

  <div class="body" class:sidepinned={sidePinned} style:--music-bar={mq.active && musicBarAt === 'top' ? 'var(--music-bar-h)' : '0rem'} style:--side-scale={sideScale}>
  <aside
    data-nav-group
    class="side"
    class:pinned={sidePinned}
    class:open={sideOpen}
    class:expanded={sideExpanded}
    class:raised={(playing && playerCompact) || (mq.active && musicBarAt === 'bottom')}
    aria-label="Navigation"
    onpointerenter={(e) => sideHoverTo(true, e)}
    onpointerleave={(e) => sideHoverTo(false, e)}
    onfocusin={(e) => (sideFocus = (e.target as HTMLElement).matches(':focus-visible'))}
    onfocusout={(e) => { if (!(e.currentTarget as HTMLElement).contains(e.relatedTarget as Node)) sideFocus = false }}
  >
    <div class="side-head">
      {#if !sidePinned}
        <button class="side-btn side-toggle" title="Open the sidebar" aria-label="Open the sidebar" aria-expanded={sideOpen} onclick={() => (sideOpen = !sideOpen)}><PanelLeftOpen size={sideIcon} /></button>
      {/if}
      <button
        class="side-btn side-pin"
        title={sidePinned ? 'Unpin the sidebar' : 'Pin the sidebar open'}
        aria-label={sidePinned ? 'Unpin the sidebar' : 'Pin the sidebar open'}
        aria-pressed={sidePinned}
        onclick={() => setSidePinned(!sidePinned)}
      >{#if sidePinned}<PinOff size={sideIconSmall} />{:else}<Pin size={sideIconSmall} />{/if}</button>
    </div>
    <button class="side-item" class:active={tab === 'home'} title="Home" onclick={() => setTab('home')}><House size={sideIcon} /> <span class="side-label">Home</span></button>
    <button class="side-item" class:active={tab === 'queue'} title="Queue" onclick={() => setTab('queue')}>
      <ListVideo size={sideIcon} /> <span class="side-label">Queue</span>
      <!-- The count is the feedback for "Add all to queue": without it the
           button appeared to do nothing at all. -->
      {#if queue.length > 0}<span class="side-count">{queue.length}</span>{/if}
    </button>
    {#if reqStatus && reqStatus.level !== 'none'}
      <button class="side-item" class:active={tab === 'requests'} title="Requests" onclick={() => { setTab('requests'); loadMyRequests() }}>
        <Inbox size={sideIcon} /> <span class="side-label">Requests</span>
      </button>
    {/if}
    {#if haVisible()}
      <button class="side-item" class:active={tab === 'rooms'} title="Rooms" onclick={() => setTab('rooms')}>
        <Lamp size={sideIcon} /> <span class="side-label">Rooms</span>
      </button>
    {/if}
    <div class="tree">
      {#each libraryTree as group (group.type)}
        {@const TypeIcon = group.icon}
        <!-- Collapsed to a rule in the rail, so rows keep their places and
             nothing jumps as the panel opens. -->
        <div class="type-label"><span class="side-label">{group.label}</span></div>
        {#each group.libs as lib (lib.id)}
          <button
            class="side-item"
            class:active={tab === 'libraries' && currentLib?.id === lib.id}
            title={lib.name}
            onclick={() => { tab = 'libraries'; openLibrary(lib) }}
          ><TypeIcon size={sideIcon} /> <span class="side-label">{lib.name}</span>{#if lib.storage && !lib.storage.available}<span class="side-warn" title="Storage not reachable: {lib.storage.path}: {lib.storage.error}"><TriangleAlert size={sideIconSmall} /></span>{/if}</button>
        {/each}
        {#if group.type === 'music'}
          <button class="side-item" class:active={tab === 'playlists'} title="Playlists" onclick={() => openPlaylist(null)}>
            <ListMusic size={sideIcon} /> <span class="side-label">Playlists</span>
          </button>
        {/if}
      {/each}
      {#if libraries.length === 0}
        <div class="type-label"><span class="side-label">No libraries yet</span></div>
      {/if}
    </div>
    {#if offline}
      <!-- Only shown when something is wrong: a permanent "connected" dot
           says nothing a working page doesn't already. The build version is
           on the settings page and at /api/v1/health. -->
      <div class="side-status side-bottom">
        <span class="status offline" title="Server unreachable"></span>
        <span class="side-label">offline</span>
      </div>
    {/if}
  </aside>

  <div class="content">
  {#if notice}
    <div class="notice" class:error={notice.kind === 'error'} role={notice.kind === 'error' ? 'alert' : 'status'}>
      {#if notice.kind === 'error'}<TriangleAlert size={16} />{/if}
      <span>{notice.text}</span>
      {#if notice.action}
        {@const act = notice.action}
        <button class="notice-act" onclick={() => { notice = null; act.run() }}>{act.label}</button>
      {/if}
      <button class="notice-x" aria-label="Dismiss" onclick={() => (notice = null)}><X size={14} /></button>
    </div>
  {/if}
  <main class:withbar={(playing && playerCompact) || (!!elsewhere && !playing) || (mq.active && musicBarAt === 'bottom')} class:withmusic={mq.active && musicBarAt === 'top'}>
    {#if tab === 'libraries' && currentLib}
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
    {#if mq.active}
      <MusicPlayer onstopped={onMusicStopped} />
      <MiniBar onlocate={() => { const t = mq.items[mq.index]; if (t) openAlbumOf(t) }}
        onexpand={() => (nowPlayingOpen ? closeNowPlaying() : openNowPlaying())} expanded={nowPlayingOpen} at={musicBarAt} />
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
          onburnsubtitle={burnSubtitle}
          onexpand={() => playing && showItem(playing.item)}
          ontime={(t) => (playingPos = t)}
          devices={otherDevices.filter((d) => d.online)}
          ondevices={loadDevices}
          onplayon={sendTo}
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
                {@render thumb(q)}
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
    {#if tab === 'playlists'}
      <PlaylistsView {playlistId} onopen={openPlaylist} />
    {:else if tab === 'rooms'}
      <RoomsPage />
    {:else if tab === 'requests'}
      <h1>Requests</h1>
      {#if me?.role === 'admin'}
        <p class="muted small reqnote">Everyone's requests. Approve or decline those waiting on the Requests page of {@render adminLink('/admin/requests')}.</p>
      {/if}
      {#if requestsErr}<div class="empty">{requestsErr}</div>{/if}
      {#if myRequests.length === 0 && !requestsErr}
        <div class="empty">
          Nothing requested yet. Search for a film or series: anything the
          library does not have shows under <strong>Not in your library</strong>,
          ready to request.
        </div>
      {:else}
        <ul class="reqlist">
          {#each myRequests as r (r.id)}
            <li class="reqrow">
              <button class="reqart" onclick={() => openDiscover(r.mediaType, r.tmdbId)} aria-label="Open {r.title}">
                {#if r.poster}<img src={r.poster} alt="" loading="lazy" />{:else}<span class="ph">{#if r.mediaType === 'tv'}<Tv size={18} />{:else}<Clapperboard size={18} />{/if}</span>{/if}
              </button>
              <div class="reqmain">
                <button class="reqtitle" onclick={() => openDiscover(r.mediaType, r.tmdbId)}>
                  {r.title}{#if r.year}{' '}<span class="muted">({r.year})</span>{/if}
                </button>
                <span class="muted small">
                  {requestScope(r)} · {utcDate(r.createdAt)}{#if me?.role === 'admin' && r.userId !== me.id}{' · '}{r.userName}{/if}
                </span>
                {#if r.note}<span class="small">“{r.note}”</span>{/if}
                {#if r.error && r.status === 'requested'}<span class="small muted">Not sent yet: {r.error}</span>{/if}
              </div>
              <div class="reqside">
                <span class="reqstate" data-state={r.status}>{requestLabel[r.status]}</span>
                {#if r.status === 'requested' && (r.userId === me?.id || (me?.role === 'admin' && !tv.on))}
                  <button class="linkish small" onclick={() => withdraw(r)}>Withdraw</button>
                {/if}
              </div>
            </li>
          {/each}
        </ul>
      {/if}
    {:else if tab === 'discover'}
      {#if discoverErr}
        <div class="empty">{discoverErr}</div>
      {:else if !discover}
        <p class="muted">Loading…</p>
      {:else}
        {@const d = discover}
        {@const open = d.requests.find((r) => r.status !== 'declined' && r.status !== 'available')}
        {@const whole = !!open && open.seasons.length === 0 && open.episodes.length === 0}
        {@const declined = !open ? d.requests.find((r) => r.status === 'declined') : undefined}
        {#if d.backdrop || d.poster}
          <div class="pagebg" class:from-poster={!d.backdrop} style="background-image: url({d.backdrop || d.poster})" aria-hidden="true"></div>
        {/if}
        <div class="detail">
          {#if d.poster}
            <img class="poster" src={d.poster} alt={d.title} />
          {:else}
            <div class="poster placeholder"><span>{#if d.mediaType === 'tv'}<Tv size={26} />{:else}<Clapperboard size={26} />{/if}</span></div>
          {/if}
          <div class="detail-body">
            <h1>
              {d.title}
              {#if d.year}<span class="muted">({d.year})</span>{/if}
            </h1>
            <p class="meta">
              {d.mediaType === 'tv' ? 'Series' : 'Film'}{#if d.runtimeMins}{' · '}{d.runtimeMins} min{d.mediaType === 'tv' ? ' an episode' : ''}{/if}{#if !d.itemId}{' · '}<span class="notheld">Not in your library</span>{/if}
            </p>
            {#if d.overview}<p class="overview">{d.overview}</p>{/if}
            <div class="actions wrap">
              {#if d.itemId}
                <button class="play" onclick={() => d.itemId && openLibraryItem(d.itemId)}><Play size={15} /> Open in library</button>
              {/if}
              {#if open}
                <span class="reqstate big" data-state={open.status}>
                  {#if open.status === 'requested'}<Clock size={14} /> Requested · waiting for approval
                  {:else if open.status === 'approved'}<Check size={14} /> Approved · on its way
                  {:else if open.status === 'downloading'}<Download size={14} /> Downloading
                  {:else}{requestLabel[open.status]}{/if}
                </span>
              {/if}
              {#if d.canRequest && d.mediaType === 'movie' && !open}
                <button class="play" disabled={requestBusy} onclick={() => requestTitle()}>
                  <Plus size={15} /> {requestBusy ? 'Requesting…' : 'Request'}
                </button>
              {:else if d.canRequest && d.mediaType === 'tv' && !whole}
                <button class="play" disabled={requestBusy} onclick={() => requestTitle()}>
                  <Plus size={15} /> {requestBusy ? 'Requesting…' : d.itemId ? 'Request everything missing' : 'Request the series'}
                </button>
                {#if (d.seasons?.length ?? 0) > 0}
                  <button class="dl" aria-expanded={choosingSeasons} onclick={() => (choosingSeasons = !choosingSeasons)}>Choose seasons</button>
                {/if}
              {/if}
            </div>
            {#if requestMsg}<p class="reqmsg muted">{requestMsg}</p>{/if}
            {#if open?.note}<p class="reqmsg muted">“{open.note}”</p>{/if}
            {#if declined}<p class="reqmsg">Declined{#if declined.note}: “{declined.note}”{/if}</p>{/if}
            {#if d.originalTitle}<p class="origtitle muted">{d.originalTitle}</p>{/if}
            {#if d.tagline}<p class="tagline">{d.tagline}</p>{/if}
            {#if d.rating || d.certification || d.genres.length}
              <p class="facts">
                {#if d.rating}<span class="score" title="{d.voteCount} votes on TMDB">★ {d.rating.toFixed(1)}</span>{/if}
                {#if d.certification}<span class="cert" title="Content rating">{d.certification}</span>{/if}
                {#if d.genres.length}<span class="muted">{d.genres.join(' · ')}</span>{/if}
                {#if d.network}<span class="muted">· {d.network}</span>{/if}
                {#if d.status && d.mediaType === 'tv'}
                  <span class="status" class:ongoing={d.status === 'Returning Series'}>{d.status === 'Returning Series' ? 'Returning' : d.status}</span>
                {/if}
              </p>
            {/if}
            {#if d.crew.some((c) => c.role && CREW_ROLES.includes(c.role))}
              <dl class="crew">
                <!-- The same roles, in the same order, as a library title's page. -->
                {#each CREW_ROLES.filter((role) => d.crew.some((c) => c.role === role)) as role (role)}
                  <div>
                    <dt>{role}</dt>
                    <dd>{[...new Set(d.crew.filter((c) => c.role === role).map((c) => c.name))].join(', ')}</dd>
                  </div>
                {/each}
              </dl>
            {/if}
          </div>
        </div>
        {#if d.mediaType === 'tv' && d.seasons?.length}
          <section class="dseasons" aria-label="Seasons">
            <h2>Seasons</h2>
            <ul>
              {#each d.seasons as ss (ss.seasonNumber)}
                {@const full = ss.episodeCount > 0 && ss.held >= ss.episodeCount}
                {@const taken = full || !!ss.request}
                <li class:held={ss.held > 0}>
                  {#if choosingSeasons}
                    <input type="checkbox" checked={pickedSeasons.includes(ss.seasonNumber)} disabled={taken}
                      onchange={() => toggleSeasonPick(ss.seasonNumber)} aria-label="Request {ss.name}" />
                  {/if}
                  <span class="dsname">{ss.name}</span>
                  <span class="muted small">{ss.episodeCount} episodes{#if ss.airDate}{' · '}{ss.airDate.slice(0, 4)}{/if}</span>
                  <span class="dsstate">
                    {#if ss.request}<span class="reqstate tiny" data-state={ss.request}>{requestLabel[ss.request]}</span>
                    {:else if full}<span class="muted small">In library</span>
                    {:else if ss.held > 0}<span class="muted small">{ss.held} of {ss.episodeCount} in library</span>{/if}
                  </span>
                </li>
              {/each}
            </ul>
            {#if choosingSeasons}
              <div class="actions">
                <button class="play" disabled={requestBusy || pickedSeasons.length === 0} onclick={() => requestTitle(pickedSeasons)}>
                  <Plus size={15} /> {requestBusy ? 'Requesting…' : pickedSeasons.length === 0 ? 'Tick the seasons you want' : pickedSeasons.length === 1 ? 'Request 1 season' : `Request ${pickedSeasons.length} seasons`}
                </button>
                <button class="dl" onclick={() => { choosingSeasons = false; pickedSeasons = [] }}>Cancel</button>
              </div>
            {/if}
          </section>
        {/if}
        {#if d.cast.length}
          <section class="castsec">
            <h2>Cast</h2>
            <div class="castrow">
              {#each d.cast as c (c.tmdbId)}
                <div class="castcard">
                  {#if c.profile}
                    <img class="headshot" src={c.profile} alt="" loading="lazy" />
                  {:else}
                    <span class="headshot ph"><User size={20} /></span>
                  {/if}
                  <span class="castname">{c.name}</span>
                  {#if c.role}<span class="castrole muted">{c.role}</span>{/if}
                </div>
              {/each}
            </div>
          </section>
        {/if}
        <p class="muted small attrib">This product uses the TMDB API but is not endorsed or certified by TMDB.</p>
      {/if}
    {:else if tab === 'queue'}
      <h1>Queue</h1>
      <section>
        {#if queue.length === 0}
          <div class="empty">
            Nothing queued. Open a collection or a film and choose
            <strong>Add to queue</strong> to line things up — the queue plays
            straight through, and you can come back here to reorder it.
          </div>
          {#if takeable.length > 0}
            <h2 class="qhead">Continue from another device</h2>
            <div class="takelist">
              {#each takeable as d (d.id)}
                {@render takeRow(d)}
              {/each}
            </div>
          {/if}
        {:else}
          <div class="qbar">
            <span class="muted small">
              {queue.length} {queue.length === 1 ? 'title' : 'titles'}
              {#if playing}· now playing {queueIndex + 1} of {queue.length}{/if}
            </span>
            <span class="qspacer"></span>
            <button class="ghost" onclick={() => playList(queue, queueIndex)}>
              <Play size={15} /> {playing ? 'Restart' : 'Play'}
            </button>
            <button class="ghost" onclick={emptyQueue}>Clear</button>
            <div class="devwrap">
              <button class="ghost" aria-haspopup="menu" aria-expanded={devMenu === 'send'} onclick={() => (devMenu = devMenu === 'send' ? null : 'send')}>
                <Cast size={15} /> Play on…
              </button>
              {#if devMenu === 'send'}
                <div class="devmenu" role="menu">
                  {#each otherDevices.filter((d) => d.online) as d (d.id)}
                    <button class="um-item" role="menuitem" onclick={() => sendQueueTo(d)}>
                      <MonitorSmartphone size={15} /> <span class="devmenu-name">{deviceTitle(d)}</span>
                    </button>
                  {:else}
                    <p class="devmenu-note muted small">No other device has Cineva open. Open it on the TV or phone you want to watch on, signed in as you.</p>
                  {/each}
                </div>
              {/if}
            </div>
            {#if takeable.length > 0}
              <div class="devwrap">
                <button class="ghost" aria-haspopup="menu" aria-expanded={devMenu === 'take'} onclick={() => (devMenu = devMenu === 'take' ? null : 'take')}>
                  <MonitorSmartphone size={15} /> Continue from…
                </button>
                {#if devMenu === 'take'}
                  <div class="devmenu wide" role="menu">
                    <p class="devmenu-note muted small">Replaces this queue with the other device's, and plays from where it got to.</p>
                    {#each takeable as d (d.id)}
                      {@render takeRow(d)}
                    {/each}
                  </div>
                {/if}
              </div>
            {/if}
          </div>
          <ol class="qlist">
            {#each queue as q, i (q.id + ':' + i)}
              {@const isNow = !!playing && i === queueIndex}
              <li class:qnow={isNow}>
                <span class="qpos muted">{i + 1}</span>
                {@render thumb(q)}
                <!-- The row that is playing expands the player rather than
                     starting it again: clicking "what I am watching" to be
                     sent back to the beginning of it is a trap. -->
                <button
                  class="qtitle"
                  onclick={() => (isNow ? showItem(q) : playList(queue, i))}
                  title={isNow ? 'Back to the player' : 'Play from here'}
                >
                  {q.title}{#if q.year}<span class="qyear muted">{q.year}</span>{/if}
                  {#if isNow}<span class="qnow-tag">Playing</span>
                  {:else if i < queueIndex}<span class="qdone-tag muted">Played</span>{/if}
                </button>
                <span class="qmove">
                  <button onclick={() => moveInQueue(i, -1)} disabled={i === 0} aria-label="Move {q.title} up"><ChevronUp size={15} /></button>
                  <button onclick={() => moveInQueue(i, 1)} disabled={i === queue.length - 1} aria-label="Move {q.title} down"><ChevronDown size={15} /></button>
                </span>
                <button class="qdrop" onclick={() => removeFromQueue(i)} aria-label="Remove {q.title} from the queue">
                  <X size={14} />
                </button>
                {#if isNow && (q.runtimeSecs ?? 0) > 0}
                  <!-- Live position, reported by the player as it plays. -->
                  <span class="qprogress">
                    <span class="progress"><span class="progress-fill" style="width: {Math.min(100, (playingPos / (q.runtimeSecs || 1)) * 100)}%"></span></span>
                    <span class="muted small">{fmtClock(playingPos)} / {fmtClock(q.runtimeSecs ?? 0)}</span>
                  </span>
                {/if}
              </li>
            {/each}
          </ol>
        {/if}
      </section>
    {:else if tab === 'home'}
      <h1 class="visually-hidden">Home</h1>
      <!-- On a TV the banner shows whatever title the focus is on, as a
           preview; its buttons belong to the featured title only, since OK
           on a poster opens that title anyway. -->
      {@const shown = (tv.on && tv.focused ? tv.focused : hero) as typeof hero}
      {#if shown}
        {@const previewing = shown !== hero}
        {@const bg = shown.seriesBackdrop || shown.backdrop || shown.seriesPoster || shown.poster}
        {@const left = !previewing && shown.positionSecs > 0 && shown.runtimeSecs ? Math.max(1, Math.round((shown.runtimeSecs - shown.positionSecs) / 60)) : 0}
        <section class="hero" class:previewing>
          {#if bg}<img class="hero-bg" src={bg} alt="" />{/if}
          <div class="hero-body">
            <div class="hero-kicker">{previewing ? kindLabel(shown.kind) : left ? 'Continue watching' : 'Next up'}</div>
            <h2 class="hero-title">{shown.seriesTitle || shown.title}</h2>
            {#if shown.kind === 'episode'}
              <div class="hero-sub">S{String(shown.seasonNum).padStart(2, '0')}E{String(shown.episodeNum).padStart(2, '0')} · {shown.title}</div>
            {:else if shown.year}
              <div class="hero-sub">{shown.year}</div>
            {/if}
            {#if shown.overview}<p class="hero-overview">{shown.overview}</p>{/if}
            {#if hero && !previewing}
            <div class="actions">
              <button class="play" disabled={starting === hero.id} onclick={() => hero && startPlay(hero)}>
                <Play size={15} /> {starting === hero.id ? 'Starting…' : left ? 'Resume' : 'Play'}
              </button>
              {#if left}<span class="hero-left">{left} min left</span>{/if}
              <button class="dl" onclick={() => hero && showItem(hero)}>More info</button>
            </div>
            {/if}
          </div>
        </section>
      {/if}

      {#if home && home.continueWatching.length > 0}
        {@render shelfHead('Continue Watching', '')}
        <div class="shelf-row" data-nav-group>
          {#each home.continueWatching as r (r.id)}
            <button class="cell" onclick={() => startPlay(r)}>{@render resumeBody(r)}</button>
          {/each}
        </div>
      {/if}
      {#if home && home.nextUp.length > 0}
        {@render shelfHead('Next Up', '')}
        <div class="shelf-row" data-nav-group>
          {#each home.nextUp as r (r.id)}
            <button class="cell" onclick={() => startPlay(r)}>
              {@render poster(r.seriesPoster ? { ...r, poster: r.seriesPoster } : r)}
              <span class="cell-title">{r.seriesTitle || r.title}</span>
              <span class="cell-sub">S{String(r.seasonNum).padStart(2, '0')}E{String(r.episodeNum).padStart(2, '0')} · {r.title}</span>
            </button>
          {/each}
        </div>
      {/if}
      {#if home && home.newEpisodes.length > 0}
        {@render shelfHead('New episodes', '')}
        <div class="shelf-row" data-nav-group>
          {#each home.newEpisodes as r (r.id)}
            {@render newEpisodeCell(r, () => showItem(r))}
          {/each}
        </div>
      {/if}
      {#if home}
        {#each home.shelves as sh (sh.libraryId + sh.kind + sh.title)}
          {@render shelfHead(sh.title, sh.libraryName)}
          <div class="shelf-row" data-nav-group>
            {#each sh.items as item (item.id)}
              {@render cell(item, () => showItem(item))}
            {/each}
          </div>
        {/each}
      {/if}
      {#if home && !hero && home.shelves.length === 0}
        <div class="empty">Nothing here yet — start something from your libraries.</div>
      {/if}

      <section>
        <h2>Libraries</h2>
        {#if libraries.length === 0}
          <div class="empty">No libraries yet — create one in {@render adminLink()}.</div>
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
      {#if currentLib && currentLib.mediaType === 'music' && (!detail || musicDetail)}
        <!-- The artist rail stays beside whatever is open: the tabs, an
             artist's page or an album's, so moving between artists is one
             click rather than a trip back (server/docs/music.md, part 1). -->
        <MusicLibrary lib={currentLib} tab={musicTab} genre={musicGenre} compilations={musicComp} railCurrent={railCurrent}
          ontab={setMusicTab} onartist={(a) => openArtist(a)} onplaylists={() => openPlaylist(null)} page={!!detail}>
          {#snippet heading(lib: Library)}
            <h1>{lib.name}</h1>
            {@render storageWarning(lib.id)}
          {/snippet}
          {@render musicPage()}
        </MusicLibrary>
      {:else if detail && detail.item.kind === 'artist'}
        <ArtistView artist={detail.item} />
      {:else if detail}
        {@render detailPage(detail)}
      {:else if currentLib}
        <h1>{currentLib.name}</h1>
        {@render storageWarning(currentLib.id)}
        {#if gridItems.length === 0}
          {@render emptyLibrary()}
        {:else}
          <div class="libhead" class:hidden={headHidden}>
          <div class="subtabrow">
          <nav class="subtabs" aria-label="{currentLib.name} views">
            {#each [['recommended', 'Recommended'], ['library', 'Library'], ['genres', 'Genres'], ['collections', 'Collections']] as [id, label] (id)}
              <button
                class="subtab"
                class:on={libTab === id}
                aria-current={libTab === id ? 'page' : undefined}
                onclick={() => { libTab = id as LibTab; genre = null; collection = null; syncURL() }}>{label}</button>
            {/each}
          </nav>
          {#if currentLib.mediaType !== 'music'}
            <label class="pzoom" title="Poster size">
              <ZoomOut size={14} />
              <input type="range" min="0" max={posterScales.length - 1} step="1" bind:value={posterZoom} aria-label="Poster size" />
              <ZoomIn size={16} />
            </label>
          {/if}
          </div>
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
                      {@render resumeBody(r)}
                    </button>
                  {/each}
                </div>
              </section>
            {/if}
            {#if newEps.length > 0}
              <section>
                <h2>New episodes</h2>
                <div class="grid">
                  {#each newEps as r (r.id)}
                    {@render newEpisodeCell(r, () => openItem(r.id, false, r))}
                  {/each}
                </div>
              </section>
            {/if}
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
                  <button class="dl" onclick={() => enqueueAll(collItems)}>Add all to queue</button>
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
            <p>Libraries are created in {@render adminLink()} — one media type each.</p>
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
      {#if me}
        <Settings
          {me}
          section={settingsSection}
          onsection={openSettings}
          onme={(u) => (me = u)}
          themes={looks.list}
          theme={looks.chosen}
          occasion={looks.occasion}
          onthemeschanged={() => refreshLooks().catch(() => {})}
          onsettheme={setTheme}
          {devices}
          onreloaddevices={loadDevices}
          onpickavatar={(f) => { avatarMsg = ''; cropFile = f }}
          {avatarBusy}
          {avatarMsg}
        />
      {/if}
    {/if}
  </main>
  <AddToPlaylist />
  {#if nowPlayingOpen && mq.active}
    <NowPlaying onclose={closeNowPlaying} onlocate={openAlbumOf}
      devices={otherDevices.filter((d) => d.online)} onplayon={sendQueueTo} />
  {/if}

  {@render personPanel()}

  {#if elsewhere?.playing && !playing && !mq.active}
    {@const np = elsewhere.playing}
    {@const at = elsewhereAt(elsewhere)}
    <div class="elsewhere" role="region" aria-label="Playing on {deviceTitle(elsewhere)}">
      {#if np.durationSecs}<div class="ew-progress"><div style="width: {Math.min(100, (at / np.durationSecs) * 100)}%"></div></div>{/if}
      {@render thumb(np.item)}
      <div class="ew-body">
        <span class="ew-title">{[
          np.seriesTitle,
          np.item.kind === 'episode' ? `S${String(np.item.seasonNum).padStart(2, '0')}E${String(np.item.episodeNum).padStart(2, '0')}` : '',
          np.item.title,
        ].filter(Boolean).join(' · ')}</span>
        <span class="ew-where"><MonitorSmartphone size={13} /> Playing on {deviceTitle(elsewhere)} · {clock(at)}</span>
      </div>
      <button class="ew-go" disabled={continuing} aria-label="Continue here" onclick={() => elsewhere && continueHere(elsewhere)}>
        <Play size={15} /> <span class="ew-label">{continuing ? 'Pausing it…' : 'Continue here'}</span>
      </button>
    </div>
  {/if}

  <nav class="tabbar">
    {#each navTabs as t (t.id)}
      {@const TabIcon = t.icon}
      <button class:active={tab === t.id} onclick={() => { setTab(t.id); if (t.id === 'requests') loadMyRequests() }}>
        <TabIcon size={19} />
        <span>{t.label}</span>
      </button>
    {/each}
  </nav>
  </div>
  </div>
</div>

<!-- Outside the layout, so no stacking context of the page's can sit over it. -->
{#if cropFile}
  <AvatarCrop file={cropFile} busy={avatarBusy} oncrop={sendAvatar} oncancel={() => (cropFile = null)} />
{/if}
{#if segmentsFor}
  <SegmentEditor item={segmentsFor} onclose={() => (segmentsFor = null)} />
{/if}

{#snippet musicPage()}
  {#if detail && detail.item.kind === 'artist'}
    <ArtistView artist={detail.item} />
  {:else if detail && detail.item.kind === 'album'}
    <AlbumPage album={detail.item} tracks={detail.children.filter((c) => c.kind === 'track')} highlightId={hlTrackId ?? undefined} />
  {:else if detail}
    {@render detailPage(detail)}
  {/if}
{/snippet}

{#snippet detailPage(d: ItemDetail)}
  {#if d.item.backdrop || d.item.poster}
    <!-- The film's own artwork behind the page. The backdrop is the
         wide still meant for this; the poster is a fallback and is
         blurred harder, since cropping a 2:3 image to a wide band
         magnifies it. -->
    <div
      class="pagebg"
      class:from-poster={!d.item.backdrop}
      style="background-image: url({d.item.backdrop || d.item.poster})"
      aria-hidden="true"
    ></div>
  {/if}
  <div class="detail">
    {@render poster(d.item)}
    <div class="detail-body">
      {@render storageWarning(d.item.libraryId)}
      <h1>
        {d.item.title}
        {#if d.item.year}<span class="muted">({d.item.year})</span>{/if}
      </h1>
      {#if d.item.kind === 'season' || d.item.kind === 'series'}
        <div class="actions wrap">
          <!-- A series had no way to start watching at all: only a
               download button, while a film gets Play and an album
               gets Listen. The server decides which episode (see
               CLAUDE.md) so the apps cannot disagree about it. -->
          {#if upNextHere}
            {@const up = upNextHere}
            <button class="play" onclick={() => startPlay(up.item, readQuality(), up.reason === 'restart')}>
              <Play size={15} />
              {#if up.reason === 'resume'}Resume
              {:else if up.reason === 'restart'}Watch again
              {:else}Play{/if}
              <span class="epcode">S{String(up.item.seasonNum).padStart(2, '0')}E{String(up.item.episodeNum).padStart(2, '0')}</span>
            </button>
            <button class="dl" onclick={() => playSeriesFrom(up.item)} title="Play this and everything after it">
              Play all
            </button>
            <PlayInMode itemId={up.item.id} />
          {/if}
          <button class="dl" onclick={() => d && downloadAll(d.item)}>
            <Download size={14} /> Download {d.item.kind === 'season' ? 'season' : 'show'}
          </button>
          {#if canEditSegments(me)}
            <DetectMenu item={d.item} />
          {/if}
        </div>
      {/if}
      {#if d.item.kind === 'album'}
        <p class="meta">
          {#if d.item.parentId}<button class="artistlink" onclick={() => d && goToArtistOf(d.item)}>{d.item.artist}</button>{:else}{d.item.artist}{/if}
          · {d.children.length} tracks
        </p>
        <div class="actions wrap">
          <button class="play" onclick={() => d && startMusic(d.children, 0)}><Play size={15} /> Play</button>
          <button class="dl" onclick={() => d && startMusic(d.children, 0, true)}><Shuffle size={14} /> Shuffle</button>
          <button class="dl" onclick={() => d && addToPlaylist([d.item.id], d.item.title)} title="Add to playlist"><ListMusic size={14} /> Playlist</button>
          <select class="dlsel" title="Download album (ZIP)" onchange={(e) => { d && dlNavigate(d.item.id, (e.target as HTMLSelectElement).value); (e.target as HTMLSelectElement).value = '' }}>
            <option value="">Download…</option>
            <option value="original">Original (ZIP)</option>
            <option value="mp3-320">MP3 320 (ZIP)</option>
            <option value="mp3-192">MP3 192 (ZIP)</option>
            <option value="ogg-192">OGG 192 (ZIP)</option>
          </select>
        </div>
      {/if}
      <p class="meta">
        {#if d.item.kind === 'episode'}S{String(d.item.seasonNum).padStart(2, '0')}E{String(d.item.episodeNum).padStart(2, '0')} · {/if}
        {runtime(d.item.runtimeSecs)}
        {#if d.item.width}· {d.item.width}x{d.item.height} {d.item.videoCodec}{/if}
      </p>
      {#if d.item.overview}<p class="overview">{d.item.overview}</p>{/if}
      {#if d.item.kind === 'movie' || d.item.kind === 'episode'}
        <div class="actions wrap">
          <button class="play" onclick={() => d && startPlay(d.item)}>
            <Play size={15} /> {resumable ? 'Resume' : 'Play'}
          </button>
          {#if resumable}
            <span class="muted">{minutesLeft} min left</span>
            <button class="dl" onclick={() => d && startPlay(d.item, undefined, true)}>
              <RotateCcw size={14} /> Start over
            </button>
          {/if}
          <button class="dl" onclick={toggleWatched} title={d.playState?.watched ? 'Mark as unwatched' : 'Mark as watched'}>
            {#if d.playState?.watched}
              <EyeOff size={14} /> Mark unwatched
            {:else}
              <Eye size={14} /> Mark watched
            {/if}
          </button>
          {#if d.item.kind === 'movie'}
            <button class="dl" onclick={() => d && enqueue(d.item)} title="Play after what is queued">
              Add to queue
            </button>
          {/if}
          <PlayInMode itemId={d.item.id} />
          <a class="dl" href={downloadURL(d.item.id)} download><Download size={14} /> Download</a>
          {#if d.item.kind === 'movie' && canEditSegments(me)}
            {@const film = d.item}
            <DetectMenu item={film} onedit={() => (segmentsFor = film)} />
          {/if}
        </div>
        {#if d.versions && d.versions.length > 1}
          <div class="versions">
            <span class="muted small">This film is held {d.versions.length} times:</span>
            <div class="vchips">
              {#each d.versions as v (v.id)}
                <button
                  class="chip" class:sel={v.id === d.item.id}
                  onclick={() => openItem(v.id, false, v)}
                  title={v.path}>{versionLabel(v)}</button>
              {/each}
            </div>
          </div>
        {/if}
        <!-- Shown only when it differs from the title, which the
             server already decides — an English-language series would
             otherwise carry its own name twice. -->
        {#if d.item.originalTitle}
          <p class="origtitle muted">{d.item.originalTitle}</p>
        {/if}
        {#if d.item.tagline}
          <p class="tagline">{d.item.tagline}</p>
        {/if}
        {#if d.item.rating || d.item.certification || d.item.genres?.length}
          <p class="facts">
            {#if d.item.rating}
              <span class="score" title="{d.item.voteCount} votes on TMDB">★ {d.item.rating.toFixed(1)}</span>
            {/if}
            {#if d.item.certification}
              <span class="cert" title="Content rating">{d.item.certification}</span>
            {/if}
            {#if d.item.genres?.length}
              <span class="muted">{d.item.genres.join(' · ')}</span>
            {/if}
            {#if d.item.collectionName}
              <span class="muted">· {d.item.collectionName}</span>
            {/if}
            <!-- Television only. "Ended" and "Canceled" are different
                 news to someone deciding whether to start a series, so
                 TMDB's own wording is shown rather than a tidied-up
                 "finished". -->
            {#if d.item.network}
              <span class="muted">· {d.item.network}</span>
            {/if}
            {#if d.item.status}
              <span class="status" class:ongoing={d.item.status === 'Returning Series'}>
                {d.item.status === 'Returning Series' ? 'Returning' : d.item.status}
              </span>
            {/if}
            <!-- Only a running series has one, and it stops being
                 true the moment that episode airs, so it is never
                 shown for anything finished. -->
            {#if d.item.nextAirDate}
              <span class="muted" title="Next episode">· next {airLabel(d.item.nextAirDate)}</span>
            {/if}
            {#if d.item.kind === 'series' && d.item.seasonCount}
              <span class="muted" title="Seasons held of the {d.item.seasonCount} that exist">
                · {seasonsHeld} of {d.item.seasonCount} seasons
              </span>
            {/if}
          </p>
        {/if}
        {#if d.crew?.length}
          <dl class="crew">
            {#each crewByRole(d.crew) as g (g.role)}
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
          <div><dt>Video</dt><dd>{d.item.height ? `${d.item.height}p ` : ''}({d.item.videoCodec})</dd></div>
          <div><dt>Audio</dt><dd>{(d.item.audioCodec ?? '').toUpperCase()}</dd></div>
          <div>
            <dt>Subtitles</dt>
            <dd>{detailSubs.length ? detailSubs.map((s) => s.title || s.language || s.codec).join(' · ') : 'None'}</dd>
          </div>
        </dl>
      {/if}
    </div>
  </div>
  {#if d.cast?.length}
    <section class="castsec">
      <h2>Cast</h2>
      <div class="castrow">
        {#each d.cast as c (c.id)}
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
  {#if d.children.length > 0}
    {#if d.item.kind === 'series'}
      <!-- Seasons as a poster grid -->
      <div class="grid seasons">
        {#each d.children as child (child.id)}
          {@const gaps = missing?.seasons.find((m) => m.seasonNumber === child.seasonNum)?.episodes.filter((e) => e.status === 'missing').length ?? 0}
          <button class="cell" onclick={() => openItem(child.id, true, child)}>
            {@render poster(child)}
            <span class="cell-title">{child.title}</span>
            {#if gaps > 0}<span class="cell-sub">{gaps} not in library</span>{/if}
          </button>
        {/each}
      </div>
    {:else if d.item.kind === 'season'}
      <!-- Episodes: terminal level. Each row carries everything —
           the thumbnail plays, the overview expands in place, and
           the rarer actions sit in the ⋯ menu. -->
      <ul class="eplist">
        {#each d.children as child (child.id)}
          {@const live = playing?.item.id === child.id}
          {@const resumeAt = epResumable(child)}
          <li id="ep-{child.id}" class="eprow" class:sel={selEpId === child.id} class:nowplaying={live}>
            <button class="epthumb" onclick={() => startPlay(child)} title={resumeAt ? 'Resume' : 'Play'}>
              {#if child.poster}
                <img src={child.poster} alt="" loading="lazy" />
              {:else}
                <span class="ph"><Tv size={20} /></span>
              {/if}
              <span class="epplay"><Play size={18} /></span>
              {#if child.watched && !live}<span class="epseen" title="Watched"><Check size={12} /></span>{/if}
              {#if live || resumeAt}
                <span class="ep-progress"><span style="width: {epProgress(child)}%"></span></span>
              {/if}
            </button>
            <div class="epmain">
              <div class="eptop">
                <span class="eptitle"><span class="epnum">{child.episodeNum}.</span> {child.title}</span>
                {#if live}<span class="ep-live" title="Now playing"><AudioLines size={15} /></span>{/if}
                <span class="epmeta muted">
                  {runtime(child.runtimeSecs)}{#if child.airDate}<span class="epair" title="First broadcast">{' · '}{airLabel(child.airDate)}</span>{/if}{#if resumeAt && !live && child.runtimeSecs}{' · '}{Math.max(1, Math.round((child.runtimeSecs - (child.positionSecs ?? 0)) / 60))} min left{/if}
                </span>
              </div>
              {#if child.overview}
                <button class="epover muted" class:open={epOpen === child.id} onclick={() => (epOpen = epOpen === child.id ? null : child.id)}>
                  {child.overview}
                </button>
              {/if}
              {#if epDetect?.id === child.id}
                <p class="epdetect muted" role="status">{epDetect.text}</p>
              {/if}
            </div>
            <div class="epmenuwrap">
              <button class="epmore" title="More" aria-haspopup="menu" onclick={() => (epMenu = epMenu === child.id ? null : child.id)}>
                <Ellipsis size={18} />
              </button>
              {#if epMenu === child.id}
                <div class="usermenu" role="menu">
                  {#if resumeAt}
                    <button class="um-item" role="menuitem" onclick={() => { epMenu = null; startPlay(child, undefined, true) }}>
                      <RotateCcw size={14} /> Start over
                    </button>
                  {/if}
                  <button class="um-item" role="menuitem" onclick={() => toggleEpWatched(child)}>
                    {#if child.watched}<EyeOff size={14} /> Mark unwatched{:else}<Eye size={14} /> Mark watched{/if}
                  </button>
                  <a class="um-item" role="menuitem" href={downloadURL(child.id)} download onclick={() => (epMenu = null)}>
                    <Download size={14} /> Download
                  </a>
                  {#if canEditSegments(me)}
                    <button class="um-item" role="menuitem" onclick={() => { epMenu = null; segmentsFor = child }}>
                      <Clock size={14} /> Intro &amp; credits…
                    </button>
                    <button class="um-item" role="menuitem" onclick={() => detectEpisode(child)}>
                      <ScanSearch size={14} /> Detect intro &amp; credits
                    </button>
                  {/if}
                </div>
              {/if}
            </div>
          </li>
        {/each}
      </ul>
    {:else}
      <ul class="children">
        {#each d.children as child, ci (child.id)}
          <li class:hl={child.id === hlTrackId} class:trackrow={child.kind === 'track'} class:trackstars={child.kind === 'track'}>
            {#if child.kind === 'track'}
              <button onclick={() => d && startMusic(d.children, ci)}>
                <span>
                  <span class="epnum">{child.trackNum || ci + 1}.</span>
                  {child.title}
                  {#if child.artist && child.artist !== d.item.artist}<span class="muted"> — {child.artist}</span>{/if}
                </span>
                <span class="trackend">
                  {#if child.playCount}<span class="muted trackplays">{playsLabel(child.playCount)}</span>{/if}
                  <span class="muted">{child.runtimeSecs ? `${Math.floor(child.runtimeSecs / 60)}:${String(child.runtimeSecs % 60).padStart(2, '0')}` : ''}</span>
                </span>
              </button>
              <Stars track={child} />
              <TrackMenu track={child} album={false} />
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
  {#if d.item.kind === 'series' && absentSeasons.length > 0}
    <!-- Seasons Sonarr knows of with nothing on disk: greyed, after
         what can be played. -->
    <section class="missingsec" aria-label="Seasons not in your library">
      <header class="missinghead"><h2>Not in your library</h2></header>
      <div class="grid seasons">
        {#each absentSeasons as ms (ms.seasonNumber)}
          {@const unaired = ms.episodes.length > 0 && ms.episodes.every((e) => e.status === 'upcoming')}
          {@const firstAir = ms.episodes.find((e) => e.airDate)?.airDate}
          <div class="cell ghostcell">
            <div class="poster placeholder"><span>{#if unaired}<CalendarClock size={26} />{:else}<Tv size={26} />{/if}</span></div>
            <span class="cell-title">Season {ms.seasonNumber}</span>
            <span class="cell-sub">{unaired ? (firstAir ? `Airs ${airLabel(firstAir)}` : 'Not aired yet') : `${ms.total} episodes`}</span>
            {#if !unaired}{@render askButton(ms.seasonNumber, undefined, 'season')}{/if}
          </div>
        {/each}
      </div>
    </section>
  {:else if d.item.kind === 'season' && missingSeason && missingSeason.episodes.length > 0}
    {@const ms = missingSeason}
    {@const aired = ms.episodes.filter((e) => e.status === 'missing').length}
    {@const coming = ms.episodes.length - aired}
    <section class="missingsec" aria-label="Episodes not in your library">
      <header class="missinghead">
        <h2>Not in your library</h2>
        <span class="muted small">
          {[aired ? `${aired} missing` : '', coming ? `${coming} still to air` : ''].filter(Boolean).join(' · ')}
        </span>
        {#if aired > 1}{@render askButton(ms.seasonNumber, undefined, 'season')}{/if}
      </header>
      <ul class="eplist">
        {#each ms.episodes as ep (ep.episode)}
          <li class="eprow ghost">
            <span class="epthumb"><span class="ph">{#if ep.status === 'upcoming'}<CalendarClock size={18} />{:else}<Tv size={18} />{/if}</span></span>
            <div class="epmain">
              <div class="eptop">
                <span class="eptitle"><span class="epnum">{ep.episode}.</span> {ep.title || `Episode ${ep.episode}`}</span>
                <span class="epmeta muted">
                  {#if ep.status === 'upcoming'}{ep.airDate ? `Airs ${airLabel(ep.airDate)}` : 'Not aired yet'}{:else}{ep.airDate ? `Aired ${airLabel(ep.airDate)}` : 'Aired'}{/if}
                </span>
              </div>
            </div>
            {#if ep.status === 'missing'}{@render askButton(ep.season, ep.episode)}{/if}
          </li>
        {/each}
      </ul>
    </section>
  {/if}
{/snippet}

<style>
  /* The theme's picture and its effect sit behind the app: the shell is
     its own layer above them. */
  .backdrop { position: fixed; inset: 0; z-index: 0; pointer-events: none; }
  .shell { position: relative; z-index: 1; min-height: 100vh; display: flex; flex-direction: column; }
  .body { flex: 1; display: flex; }
  .content { flex: 1; min-width: 0; display: flex; flex-direction: column; }

  /* ---- Sign-in / first-run gate ---- */
  .gate { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; padding: 1rem; }
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
  /* A chosen colour replaces the default gradient; the initial takes the
     page background so it reads on any of the palette's colours. */
  .tile-avatar.tinted { background: var(--face); color: var(--bg); }
  .face-img { object-fit: cover; padding: 0; }
  .avatar { overflow: hidden; padding: 0; }
  .avatar .initial.tinted { background: var(--face); color: var(--bg); width: 100%; height: 100%; display: grid; place-items: center; }
  .avatar img.initial { width: 100%; height: 100%; }
  .tag { font-size: 0.7rem; padding: 0.05rem 0.45rem; border-radius: 999px; border: 1px solid var(--border); color: var(--muted); font-weight: 400; }
  .tag.on { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 50%, transparent); }
  .chip.danger { color: var(--err); border-color: var(--err); }
  .devwrap { position: relative; }
  .devmenu {
    position: absolute; right: 0; top: calc(100% + 0.4rem); z-index: 60; width: min(20rem, calc(100vw - 2rem));
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.4rem;
    display: flex; flex-direction: column; box-shadow: 0 8px 24px color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .devmenu.wide { width: min(26rem, calc(100vw - 2rem)); }
  .devmenu-note { padding: 0.4rem 0.6rem; margin: 0; }
  .devmenu-name { display: inline-flex; align-items: center; gap: 0.4rem; font-weight: 500; }
  .takerow { align-items: center; }
  .takebody { display: flex; flex-direction: column; min-width: 0; }
  .takebody .small { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .takelist { display: flex; flex-direction: column; max-width: 32rem; background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.4rem; }
  .qhead { font-size: 1rem; margin: 1.2rem 0 0.5rem; }
  .pinpad { align-items: center; text-align: center; }
  .pinpad input { text-align: center; font-size: 1.6rem; letter-spacing: 0.6rem; width: 12rem; }
  .pinpad input.pw { font-size: 1rem; letter-spacing: normal; width: 16rem; }
  /* The remote's number pad, on a TV. */
  .digits { display: grid; grid-template-columns: repeat(3, 3.6rem); gap: 0.6rem; justify-content: center; }
  .digits button {
    height: 3.6rem; border-radius: 12px; font-size: 1.4rem; font-weight: 600;
    background: var(--panel); border: 1px solid var(--border); color: var(--text);
    display: grid; place-items: center;
  }
  .digits button:focus { background: var(--accent); color: var(--on-accent); }
  .digits .del { color: var(--muted); }
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

  /* ---- Persistent top bar: logo, universal search, profile ---- */
  /* --surface-radius rounds the big floating surfaces alike: the sidebar
     and the player. */
  :root { --topbar-h: 3.4rem; --surface-radius: 16px; --music-bar-h: 4.6rem; }
  @media (max-width: 767px) { :root { --music-bar-h: 3.6rem; } }
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
  /* List artwork (the thumb snippet). Each list sets one --thumb-h so its
     rows line up whatever the shape: a poster is 2:3, an episode's still
     16:9 like the season's episode list, music square. Every list it sits
     in is on --panel, so the empty box is --bg. */
  .thumb {
    height: var(--thumb-h, 3rem);
    width: calc(var(--thumb-h, 3rem) * 2 / 3);
    flex: none;
    display: grid;
    place-items: center;
    overflow: hidden;
    border-radius: 4px;
    background: var(--bg);
    color: var(--muted);
  }
  .thumb.wide { width: calc(var(--thumb-h, 3rem) * 16 / 9); }
  .thumb.square { width: var(--thumb-h, 3rem); }
  .thumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .sr-text { display: flex; flex-direction: column; min-width: 0; }
  /* "Not in your library": a row of posters under the library's results. */
  .sr-head {
    padding: 0.6rem 0.8rem 0.3rem; border-top: 1px solid var(--border);
    font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted);
  }
  .sr-found { display: flex; gap: 0.6rem; overflow-x: auto; padding: 0.2rem 0.8rem 0.8rem; scrollbar-width: thin; }
  .found { flex: none; width: 5.5rem; display: flex; flex-direction: column; gap: 0.2rem; text-align: left; }
  .found img, .found-ph {
    width: 100%; aspect-ratio: 2 / 3; object-fit: cover; border-radius: 6px;
    border: 1px solid var(--border); background: var(--bg);
  }
  .found-ph { display: grid; place-items: center; color: var(--muted); }
  .found:hover img, .found:focus-visible img { border-color: var(--accent); }
  .found-title { font-size: 0.78rem; line-height: 1.2; overflow: hidden; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; line-clamp: 2; }
  .found-sub { font-size: 0.7rem; color: var(--muted); }

  /* A request's state, the same pill everywhere it appears. */
  .reqstate {
    display: inline-flex; align-items: center; gap: 0.35rem; white-space: nowrap;
    border: 1px solid var(--border); border-radius: 999px; padding: 0.2rem 0.65rem;
    font-size: 0.8rem; color: var(--muted);
  }
  .reqstate.big { padding: 0.5rem 0.9rem; font-size: 0.88rem; border-radius: 8px; }
  .reqstate.tiny { padding: 0.05rem 0.45rem; font-size: 0.68rem; align-self: flex-start; }
  .reqstate[data-state='approved'] { color: var(--accent-2); border-color: color-mix(in srgb, var(--accent-2) 45%, transparent); }
  .reqstate[data-state='downloading'] { color: var(--accent); border-color: color-mix(in srgb, var(--accent) 45%, transparent); }
  .reqstate[data-state='available'] { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 45%, transparent); }
  .reqstate[data-state='declined'] { color: var(--err); border-color: color-mix(in srgb, var(--err) 45%, transparent); }
  .notheld { color: var(--accent-2); }
  .reqmsg { font-size: 0.88rem; margin-top: 0.6rem; }
  .actions.wrap { flex-wrap: wrap; }
  .play:disabled { opacity: 0.55; cursor: progress; }

  /* Seasons of a title outside the library. */
  .dseasons { margin-top: 1.5rem; }
  .dseasons h2, .missinghead h2 { font-size: 1rem; margin: 0; }
  .dseasons ul { list-style: none; margin: 0.6rem 0 0.9rem; }
  .dseasons li {
    display: flex; align-items: center; flex-wrap: wrap; gap: 0.3rem 0.75rem;
    padding: 0.55rem 0.3rem; border-bottom: 1px solid var(--border);
  }
  .dseasons li:last-child { border-bottom: none; }
  .dsname { font-weight: 500; }
  .dsstate { margin-left: auto; }

  /* What a series held is missing: quieter than what can be played. */
  .missingsec { margin-top: 1.75rem; }
  .missinghead { display: flex; align-items: center; flex-wrap: wrap; gap: 0.4rem 0.8rem; }
  .missinghead .askbtn { margin-left: auto; }
  .eprow.ghost { opacity: 0.6; align-items: center; }
  .eprow.ghost:hover, .eprow.ghost:focus-within { opacity: 1; }
  .ghostcell { opacity: 0.6; }
  .ghostcell:hover, .ghostcell:focus-within { opacity: 1; }
  .ghostcell .poster { filter: grayscale(1); }
  .askbtn {
    display: inline-flex; align-items: center; gap: 0.3rem; flex: none; white-space: nowrap; align-self: flex-start;
    border: 1px solid var(--border); border-radius: 999px; padding: 0.2rem 0.65rem;
    font-size: 0.78rem; color: var(--accent-2);
  }
  .eprow.ghost .askbtn { align-self: center; }
  .askbtn:hover { border-color: var(--accent); }
  .askbtn:disabled { opacity: 0.6; cursor: progress; }

  /* The Requests page. */
  .reqnote { margin-bottom: 1rem; }
  .reqnote a { color: var(--accent-2); }
  .reqlist { list-style: none; }
  .reqrow {
    display: flex; align-items: center; gap: 0.9rem; flex-wrap: wrap;
    padding: 0.7rem 0.3rem; border-bottom: 1px solid var(--border);
  }
  .reqrow:last-child { border-bottom: none; }
  .reqart {
    width: 3.2rem; aspect-ratio: 2 / 3; flex: none; border-radius: 6px; overflow: hidden;
    border: 1px solid var(--border); background: var(--panel);
  }
  .reqart img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .reqart .ph { display: grid; place-items: center; height: 100%; color: var(--muted); }
  .reqmain { flex: 1 1 12rem; min-width: 0; display: flex; flex-direction: column; gap: 0.15rem; }
  .reqtitle { text-align: left; font-weight: 500; overflow-wrap: anywhere; }
  .reqtitle:hover { color: var(--accent-2); }
  .reqside { display: flex; flex-direction: column; align-items: flex-end; gap: 0.3rem; margin-left: auto; }
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

  /* ---- Sidebar: desktop only; mobile keeps the bottom tab bar ----
     A rounded glass panel floating clear of the edges. Unpinned it is a
     rail of icons that opens over the content on hover (or keyboard focus,
     or its button on a touch screen); pinned it stays open and the content
     moves over for it. Rows keep the same icon column in both states, so
     opening only reveals labels — nothing shifts under the pointer. */
  .side { display: none; }
  @media (min-width: 768px) {
    .body {
      /* --side-scale is the sidebar-size preference (1 compact, 1.15
         regular, 1.35 large), set on .body from the script; the rail,
         the open panel, the rows and their type all follow it. */
      --rail-w: calc(3.5rem * var(--side-scale, 1.15));
      --panel-w: calc(15rem * var(--side-scale, 1.15));
      /* One gutter for the whole frame: window to sidebar, sidebar to
         content, top bar to both, and the page's right edge — so the
         sidebar and the page line up as one layout. */
      --side-gap: 1rem;
    }
    .side {
      display: flex;
      flex-direction: column;
      gap: 0.15rem;
      position: fixed;
      z-index: 30;
      top: calc(var(--topbar-h) + var(--music-bar, 0rem) + var(--side-gap));
      bottom: var(--side-gap);
      left: var(--side-gap);
      width: var(--rail-w);
      padding: calc(0.5rem * var(--side-scale, 1.15)) calc(0.4rem * var(--side-scale, 1.15));
      overflow: hidden auto;
      scrollbar-width: none;
      background: color-mix(in srgb, var(--panel) 68%, transparent);
      -webkit-backdrop-filter: blur(18px) saturate(1.4);
      backdrop-filter: blur(18px) saturate(1.4);
      border: 1px solid color-mix(in srgb, var(--border) 75%, transparent);
      border-radius: var(--surface-radius);
      box-shadow: 0 8px 28px color-mix(in srgb, var(--bg) 55%, transparent);
      transition: width 0.18s ease, box-shadow 0.18s ease;
    }
    /* Clear of the mini player and music bar when either is up. */
    .side.raised { bottom: calc(6.5rem + var(--side-gap)); }
    .side.expanded { width: var(--panel-w); scrollbar-width: thin; }
    /* Open over the content rather than beside it: lift it further. */
    .side.expanded:not(.pinned) {
      box-shadow: 0 18px 48px color-mix(in srgb, var(--bg) 75%, transparent);
    }
    /* Where the page starts: past the rail, or past the pinned panel. */
    .body { --content-left: calc(var(--rail-w) + var(--side-gap)); }
    .body.sidepinned { --content-left: calc(var(--panel-w) + var(--side-gap)); }
    .content {
      margin-left: var(--content-left);
      transition: margin-left 0.18s ease;
    }
    .tabbar { display: none !important; }
  }
  @media (prefers-reduced-motion: reduce) {
    .side, .content, .side-label { transition: none !important; }
  }
  .side-head {
    display: flex;
    align-items: center;
    min-height: calc(2rem * var(--side-scale, 1.15));
    margin-bottom: 0.2rem;
  }
  .side-btn {
    display: grid;
    place-items: center;
    width: calc(2.1rem * var(--side-scale, 1.15));
    height: calc(2rem * var(--side-scale, 1.15));
    flex: none;
    border-radius: 8px;
    color: var(--muted);
  }
  .side-btn:hover { background: color-mix(in srgb, var(--bg) 70%, transparent); color: var(--text); }
  .side-pin { margin-left: auto; }
  .side-pin[aria-pressed='true'] { color: var(--accent-2); }
  main.withbar { padding-bottom: 6.5rem; }
  /* The music bar sits under the top bar, so the page starts under it. */
  main.withmusic { padding-top: calc(1.25rem + var(--music-bar, 0rem)); }
  /* "Playing on Living-room TV · Continue here": where the music bar sits,
     which it never shares the screen with. */
  .elsewhere {
    position: fixed;
    left: 0; right: 0; bottom: 0;
    z-index: 45;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    min-height: 4.2rem;
    padding: 0.55rem 0.9rem;
    background: var(--panel);
    border-top: 1px solid var(--border);
    --thumb-h: 2.9rem;
  }
  .ew-progress { position: absolute; top: 0; left: 0; right: 0; height: 3px; background: var(--border); }
  .ew-progress div { height: 100%; background: var(--accent); transition: width 1s linear; }
  .ew-body { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.15rem; }
  .ew-title { font-weight: 600; font-size: 0.9rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .ew-where { display: flex; align-items: center; gap: 0.35rem; font-size: 0.78rem; color: var(--accent-2); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; font-variant-numeric: tabular-nums; }
  .ew-where :global(svg) { flex: none; }
  .ew-go {
    flex: none;
    display: inline-flex; align-items: center; gap: 0.4rem;
    padding: 0.55rem 1rem;
    border-radius: 999px;
    background: var(--accent);
    color: var(--on-accent);
    font-weight: 600;
    font-size: 0.88rem;
  }
  .ew-go:disabled { opacity: 0.7; }
  @media (max-width: 767px) {
    .elsewhere { bottom: 3.35rem; gap: 0.6rem; --thumb-h: 2.5rem; }
    /* The words go: a phone needs the room for what is playing where. */
    .ew-label { display: none; }
    .ew-go { width: 2.75rem; height: 2.75rem; padding: 0; justify-content: center; }
  }
  .side-item {
    position: relative;
    display: flex;
    align-items: center;
    gap: calc(0.6rem * var(--side-scale, 1.15));
    text-align: left;
    padding: calc(0.5rem * var(--side-scale, 1.15)) calc(0.65rem * var(--side-scale, 1.15));
    border-radius: 10px;
    color: var(--muted);
    font-size: calc(0.92rem * var(--side-scale, 1.15));
    white-space: nowrap;
  }
  .side-item :global(svg) { flex: none; }
  .side-item:hover { background: color-mix(in srgb, var(--bg) 70%, transparent); color: var(--text); }
  .side-item.active { background: color-mix(in srgb, var(--accent) 16%, transparent); color: var(--accent-2); }
  .side-label {
    min-width: 0;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
    transition: opacity 0.12s ease;
  }
  .tree { display: flex; flex-direction: column; gap: 0.1rem; margin-top: 0.6rem; flex: 1; }
  .type-label {
    position: relative;
    display: flex;
    align-items: center;
    height: calc(1.9rem * var(--side-scale, 1.15));
    color: var(--muted);
    font-size: calc(0.72rem * var(--side-scale, 1.15));
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: calc(0.55rem * var(--side-scale, 1.15)) calc(0.65rem * var(--side-scale, 1.15)) 0;
    white-space: nowrap;
  }
  /* The rail: labels hidden, headings become a short rule, and the badges
     sit on the icon rather than at the row's end. */
  @media (min-width: 768px) {
    .side:not(.expanded) .side-label { opacity: 0; }
    .side:not(.expanded) .type-label::after {
      content: '';
      position: absolute;
      left: 0.75rem; right: 0.75rem; bottom: 0.35rem;
      border-top: 1px solid var(--border);
    }
    .side:not(.expanded) :is(.side-count, .side-warn) {
      position: absolute;
      top: 0.1rem;
      left: calc(1.45rem * var(--side-scale, 1.15));
      margin: 0;
    }
    .side:not(.expanded) .side-pin { display: none; }
    .side.expanded .side-toggle { display: none; }
  }
  .type-icon { margin-right: 0.35rem; font-size: 1.15rem; vertical-align: -0.2rem; }
  .side-warn { margin-left: auto; color: var(--err); display: inline-grid; }

  .enrich-note { font-size: 0.85rem; margin: -0.3rem 0 0.9rem; }
  .storage-warn {
    display: flex; align-items: flex-start; gap: 0.5rem;
    margin: 0.25rem 0 1rem; padding: 0.65rem 0.9rem;
    border: 1px solid color-mix(in srgb, var(--err) 55%, transparent);
    background: color-mix(in srgb, var(--err) 12%, var(--panel));
    border-radius: 10px; font-size: 0.9rem;
  }
  .storage-warn :global(svg) { color: var(--err); flex: none; margin-top: 0.1rem; }

  /* Above the bottom bars (mini player, music, phone nav), centred. */
  .notice {
    position: fixed; z-index: 90;
    left: 50%; bottom: 5.5rem; transform: translateX(-50%);
    width: max-content; max-width: min(36rem, calc(100vw - 2rem));
    display: flex; align-items: flex-start; gap: 0.6rem;
    padding: 0.7rem 0.9rem;
    background: var(--panel); color: var(--text);
    border: 1px solid var(--border); border-radius: 12px;
    box-shadow: 0 8px 28px color-mix(in srgb, var(--bg) 70%, transparent);
    font-size: 0.9rem;
  }
  .notice.error { border-color: color-mix(in srgb, var(--err) 60%, transparent); }
  .notice.error :global(svg) { color: var(--err); flex: none; margin-top: 0.1rem; }
  .notice-x { color: var(--muted); flex: none; display: grid; place-items: center; }
  .notice-act { color: var(--accent-2); font-weight: 600; padding: 0.15rem 0.5rem; border-radius: 6px; }
  .notice-act:hover { background: var(--bg); }
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
  .origtitle { margin: -0.3rem 0 0.4rem; font-size: 0.95rem; }
  .guests { margin: 0.2rem 0 0.5rem; }
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
  /* Desktop: the gutter is main's own padding, so the page's top and left
     edges meet the sidebar's — the same distance below the top bar, one
     gutter apart. After the base rules above, which it overrides. */
  @media (min-width: 768px) {
    main { padding: var(--side-gap) var(--side-gap) 5rem; }
    main.withmusic { padding-top: calc(var(--side-gap) + var(--music-bar, 0rem)); }
    /* The backdrop runs on under the sidebar to the window's edge, so the
       glass panel sits over the same picture as the page rather than ending
       it in a hard seam. The clip that keeps it from widening the page moves
       out to .body, which spans the window, so main no longer cuts it off. */
    main { overflow-x: visible; }
    .body { overflow-x: clip; }
    .pagebg { left: calc(-1 * var(--content-left)); right: 0; margin: 0; }
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

  /* ---- Home ---- */
  .visually-hidden {
    position: absolute; width: 1px; height: 1px; overflow: hidden;
    clip-path: inset(50%); white-space: nowrap;
  }
  .hero {
    position: relative;
    isolation: isolate;
    min-height: 19rem;
    margin-bottom: 1.5rem;
    border-radius: 16px;
    overflow: hidden;
    border: 1px solid var(--border);
    background: var(--panel);
    display: flex;
    align-items: flex-end;
  }
  .hero-bg {
    /* Under the fade below: both sit behind the text, and at equal depth
       the image, coming later in the page, would paint over the fade. */
    position: absolute; inset: 0; z-index: -2;
    width: 100%; height: 100%; object-fit: cover; object-position: center 25%;
  }
  /* Fades the artwork into the panel from the left, where the text sits,
     so the text reads on a bright image too, and in a light theme. */
  .hero::before {
    content: ''; position: absolute; inset: 0; z-index: -1;
    background:
      linear-gradient(to right, var(--panel) 0%, color-mix(in srgb, var(--panel) 92%, transparent) 30%,
        color-mix(in srgb, var(--panel) 45%, transparent) 55%, transparent 75%),
      linear-gradient(to top, color-mix(in srgb, var(--panel) 70%, transparent), transparent 45%);
  }
  @media (max-width: 40rem) {
    /* No room for the image beside the text: veil it all instead. */
    .hero::before { background: color-mix(in srgb, var(--panel) 82%, transparent); }
  }
  .hero-body { padding: 1.5rem 1.75rem; max-width: 40rem; display: flex; flex-direction: column; gap: 0.35rem; }
  .hero-kicker { font-size: 0.75rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--accent-2); font-weight: 600; }
  .hero-title { font-size: 2rem; line-height: 1.1; margin: 0; }
  .hero-sub { color: var(--muted); }
  .hero-overview {
    display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 3; line-clamp: 3;
    overflow: hidden; margin: 0.25rem 0 0.5rem; line-height: 1.45;
  }
  .hero-left { color: var(--muted); font-size: 0.88rem; }
  @media (max-width: 30rem) {
    .hero { min-height: 15rem; }
    .hero-body { padding: 1rem 1.1rem; }
    .hero-title { font-size: 1.45rem; }
    .hero-overview { -webkit-line-clamp: 2; line-clamp: 2; }
  }

  .shelf-head { display: flex; align-items: center; justify-content: space-between; margin-top: 1.25rem; }
  .shelf-head h2 { margin: 0; }
  .shelf-lib { color: var(--muted); font-weight: 400; }
  .shelf-nav { display: flex; gap: 0.25rem; }
  .shelf-nav button {
    display: grid; place-items: center; width: 1.9rem; height: 1.9rem; border-radius: 50%;
    border: 1px solid var(--border); color: var(--muted);
  }
  .shelf-nav button:hover { color: var(--text); border-color: var(--accent); }
  @media (hover: none) { .shelf-nav { display: none; } }
  /* One row that scrolls sideways inside itself; the page never does. */
  .shelf-row {
    display: grid;
    grid-auto-flow: column;
    grid-auto-columns: 9rem;
    gap: 0.9rem;
    overflow-x: auto;
    overscroll-behavior-x: contain;
    scroll-snap-type: x mandatory;
    padding: 0.6rem 0 0.4rem;
    /* Hidden: arrows scroll it with a pointer, a swipe or a trackpad
       without, and a bar under every row is clutter. */
    scrollbar-width: none;
  }
  .shelf-row::-webkit-scrollbar { display: none; }
  .shelf-row > * { scroll-snap-align: start; }
  @media (max-width: 30rem) { .shelf-row { grid-auto-columns: 7.25rem; gap: 0.7rem; } }

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
  .subtabrow { display: flex; align-items: center; gap: 1rem; border-bottom: 1px solid var(--border); }
  .subtabrow .subtabs { flex: 1; min-width: 0; border-bottom: 0; }
  /* Desktop only: on a phone the column count is already decided by the
     screen, and the row has no room to spare. */
  .pzoom { display: none; }
  @media (min-width: 768px) {
    .pzoom { display: flex; align-items: center; gap: 0.45rem; flex: none; color: var(--muted); padding-right: 0.25rem; }
    .pzoom input { width: 6.5rem; accent-color: var(--accent); cursor: pointer; }
  }
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
    top: calc(var(--topbar-h) + var(--music-bar, 0rem));
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
    top: calc(var(--topbar-h) + var(--music-bar, 0rem));
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

  .small { font-size: 0.8rem; }
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
  /* At phone width the role sits above the names rather than beside them,
     where a 10rem label left the names a sliver of the screen. */
  @media (max-width: 30rem) {
    .crew div { flex-direction: column; gap: 0; margin-bottom: 0.35rem; }
    .crew dt { min-width: 0; }
  }

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
    /* Smaller artwork than the queue page: this is a glance under the
       player, not the place to browse. */
    --thumb-h: 2rem;
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

  /* ---- Queue page ----
     The in-player panel above is a glance at what is next; this is the
     full queue, including what has already played, where it can be
     reordered. Shares the .q* parts so the two read as the same thing. */
  /* The episode a series button will start, so "Resume" says what it will
     resume rather than leaving it to be discovered by pressing it. */
  .origtitle { margin: -0.3rem 0 0.4rem; font-size: 0.95rem; }
  .guests { margin: 0.2rem 0 0.5rem; }
  .status {
    font-size: 0.74rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.05rem 0.45rem;
    color: var(--muted);
  }
  .status.ongoing { color: var(--ok); border-color: color-mix(in srgb, var(--ok) 45%, transparent); }
  /* Hidden on a phone: the title and runtime matter more than the date when
     the row is only 390px wide. */
  @media (max-width: 30rem) { .epair { display: none; } }
  .epcode {
    margin-left: 0.15rem;
    font-variant-numeric: tabular-nums;
    opacity: 0.85;
  }
  .qbar { display: flex; align-items: center; gap: 0.6rem; margin-bottom: 0.6rem; flex-wrap: wrap; }
  .qlist {
    list-style: none;
    margin: 0;
    padding: 0;
    display: flex;
    flex-direction: column;
    gap: 0.15rem;
  }
  .qlist li {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.5rem 0.6rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    background: var(--panel);
    /* Wraps at every width, not just on a phone: the progress bar below
       takes a full-width basis, and without wrapping it shares the one line
       and squeezes the title down to nothing. */
    flex-wrap: wrap;
  }
  .qlist li.qnow { border-color: var(--accent); }
  .qnow-tag {
    margin-left: 0.5rem;
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.04em;
    color: var(--accent);
  }
  .qdone-tag { margin-left: 0.5rem; font-size: 0.72rem; }
  /* Takes the whole row under the title so the bar is wide enough to read
     at a glance rather than squeezed between the controls. */
  .qprogress {
    flex: 1 1 100%;
    display: flex;
    align-items: center;
    gap: 0.6rem;
    margin-top: 0.1rem;
  }
  .qprogress .progress { flex: 1; }
  .qmove { display: flex; flex: none; gap: 0.1rem; }
  .qmove button {
    color: var(--muted);
    display: flex;
    padding: 0.2rem;
    border-radius: 5px;
  }
  .qmove button:hover:not(:disabled) { color: var(--text); background: var(--bg); }
  .qmove button:disabled { opacity: 0.3; cursor: default; }
  /* At phone width the reorder controls would crowd the title off the row,
     so they sit under it instead of beside it, with the artwork spanning
     both lines on the left. */
  @media (max-width: 30rem) {
    .qlist li {
      display: grid;
      grid-template-columns: auto auto 1fr auto auto;
      grid-template-areas:
        'pos thumb title title title'
        'pos thumb . move drop'
        'prog prog prog prog prog';
      row-gap: 0.15rem;
    }
    .qlist .qpos { grid-area: pos; }
    .qlist .thumb { grid-area: thumb; }
    .qlist .qtitle { grid-area: title; }
    .qlist .qmove { grid-area: move; }
    .qlist .qdrop { grid-area: drop; }
    .qlist .qprogress { grid-area: prog; }
  }

  /* Count beside the sidebar's Queue entry: the feedback that "Add all to
     queue" actually did something. */
  .side-count {
    margin-left: auto;
    font-size: calc(0.75rem * var(--side-scale, 1.15));
    font-variant-numeric: tabular-nums;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.05rem 0.4rem;
    color: var(--muted);
  }

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
  /* The poster-size slider (.pzoom) scales the film and series grids on
     desktop. After the base rules, which it overrides. */
  @media (min-width: 768px) {
    .grid { grid-template-columns: repeat(auto-fill, minmax(calc(7.5rem * var(--poster-scale, 1)), 1fr)); }
    .grid.seasons { grid-template-columns: repeat(auto-fill, minmax(calc(9rem * var(--poster-scale, 1)), 1fr)); }
    .shelf-row { grid-auto-columns: calc(9rem * var(--poster-scale, 1)); }
  }

  /* ---- Episode list (season page, terminal level) ---- */
  .eplist { list-style: none; margin-top: 1.25rem; }
  .eprow {
    display: flex;
    align-items: flex-start;
    gap: 0.9rem;
    padding: 0.6rem 0.4rem;
    border-bottom: 1px solid var(--border);
  }
  .eprow:last-child { border-bottom: none; }
  .eprow.sel { background: var(--panel); border-radius: 10px; }
  .epthumb {
    position: relative;
    width: 9.5rem;
    aspect-ratio: 16 / 9;
    border-radius: 8px;
    border: 1px solid var(--border);
    overflow: hidden;
    flex: none;
    background: var(--panel);
  }
  .epthumb img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .epthumb .ph { display: grid; place-items: center; height: 100%; color: var(--muted); }
  .epplay {
    position: absolute; inset: 0;
    display: grid; place-items: center;
    color: var(--text);
    background: color-mix(in srgb, var(--bg) 45%, transparent);
    opacity: 0;
    transition: opacity 0.15s;
  }
  .epthumb:hover .epplay, .epthumb:focus-visible .epplay { opacity: 1; }
  .epseen {
    position: absolute; top: 0.3rem; right: 0.3rem;
    display: grid; place-items: center;
    width: 1.2rem; height: 1.2rem; border-radius: 50%;
    background: var(--accent); color: var(--text);
  }
  .ep-progress {
    position: absolute; left: 0; right: 0; bottom: 0;
    height: 3px;
    background: color-mix(in srgb, var(--bg) 60%, transparent);
  }
  .ep-progress span { display: block; height: 100%; background: var(--accent); }
  .epmain { flex: 1; min-width: 0; }
  .eptop { display: flex; align-items: baseline; flex-wrap: wrap; gap: 0.2rem 0.5rem; }
  .eptitle { font-weight: 500; }
  .eprow.nowplaying .eptitle { color: var(--accent-2); }
  .ep-live { color: var(--accent-2); align-self: center; display: grid; }
  .epmeta { margin-left: auto; font-size: 0.82rem; white-space: nowrap; }
  .epover {
    display: -webkit-box;
    -webkit-box-orient: vertical;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    overflow: hidden;
    margin-top: 0.25rem;
    font-size: 0.88rem;
    line-height: 1.4;
    text-align: left;
    overflow-wrap: anywhere;
  }
  .epover.open { display: block; }
  .epdetect { margin: 0.3rem 0 0; font-size: 0.82rem; }
  .epmenuwrap { position: relative; flex: none; }
  .epmore { display: grid; place-items: center; padding: 0.3rem; border-radius: 6px; color: var(--muted); }
  .epmore:hover { color: var(--text); background: var(--panel); }
  /* At phone width the thumbnail shrinks and the date goes, so the title
     and overview keep a usable width. */
  @media (max-width: 30rem) {
    .epthumb { width: 6.5rem; }
    .epmeta { margin-left: 0; }
  }

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
  /* An album's track: the row plays it, the menu beside it does the rest. */
  .children li.trackrow { display: flex; align-items: center; gap: 0.25rem; }
  .children li.trackrow > button { flex: 1; min-width: 0; }
  .trackend { display: inline-flex; gap: 0.9rem; flex: none; }
  .trackplays { font-size: 0.82rem; }
  @media (max-width: 30rem) { .trackplays { display: none; } }
  .artistlink { padding: 0; color: var(--text); text-decoration: underline; text-decoration-color: var(--border); text-underline-offset: 3px; }
  .artistlink:hover { color: var(--accent-2); text-decoration-color: currentColor; }

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

  /* ---- TV mode (docs/tv.md) ----
     The same screens, sized for across the room and worked with a remote.
     Everything is in rem, so the root size scales the whole interface:
     16px on a 1280-wide screen, 24px at 1080p. */
  :global(html.tv) { font-size: clamp(16px, 1.25vw, 40px); }
  /* Icons are drawn at fixed pixel sizes; they grow with the text. */
  :global(html.tv svg.lucide-icon) { zoom: var(--tv-scale, 1); }
  /* Focus is the only cursor there is: it has to be unmissable, and
     whatever has it is kept clear of the screen's edges and the banner. */
  :global(html.tv :focus) { outline: 0.18rem solid var(--accent-2); outline-offset: 0.18rem; scroll-margin: 12vh 3rem; }
  /* Tabs and menus light up instead of wearing a ring, as a television's
     own menus do: the focused one fills with the accent, the current one
     keeps a quieter tint. (A ring would also lose its top and bottom to a
     tab bar that scrolls sideways.) */
  :global(html.tv) .subtabs { gap: 0.5rem; padding: 0.55rem 0; }
  :global(html.tv) .subtab {
    padding: 0.45rem 1.15rem;
    border-bottom: 0;
    border-radius: 999px;
    transition: background-color 0.15s ease, color 0.15s ease;
  }
  :global(html.tv) .subtab.on { color: var(--text); background: color-mix(in srgb, var(--accent) 18%, transparent); }
  /* TV mode sets the whole root size from the screen; the sidebar
     preference does not stack on top of that. */
  :global(html.tv) .body { --side-scale: 1; }
  :global(html.tv) .side-item { transition: background-color 0.15s ease, color 0.15s ease; }
  :global(html.tv) .subtab:focus,
  :global(html.tv) .side-item:focus,
  :global(html.tv) .azl:focus {
    outline: none;
    background: var(--accent);
    color: var(--on-accent);
  }
  :global(html.tv) .cell:focus { outline: none; }
  :global(html.tv) .poster { transition: transform 0.15s ease, box-shadow 0.15s ease; }
  :global(html.tv) .cell:focus .poster {
    transform: scale(1.07);
    border-color: var(--accent-2);
    box-shadow: 0 0 0 0.18rem var(--accent-2), 0 1rem 2rem rgb(0 0 0 / 0.45);
  }
  :global(html.tv) .cell:focus .cell-title { color: var(--accent-2); }
  /* Room for a focused poster to grow without being clipped. */
  :global(html.tv) .shelf-row { padding: 0.8rem 0.6rem; scroll-snap-type: none; }
  /* What a remote cannot use: scroll arrows (focus scrolls the rows), the
     poster-size slider, and the hover-only watched tick. */
  :global(html.tv) .shelf-nav,
  :global(html.tv) .pzoom,
  :global(html.tv) .seentoggle { display: none; }
  /* Home: the banner stays while the rows move under it, showing the
     focused title. */
  :global(html.tv) .hero {
    position: sticky;
    top: var(--topbar-h);
    z-index: 3;
    min-height: 42vh;
    margin-bottom: 0.5rem;
    background: var(--bg);
  }
  :global(html.tv) .hero.previewing .hero-overview { -webkit-line-clamp: 3; line-clamp: 3; }
  :global(html.tv) .shelf-row .cell { scroll-margin-top: calc(var(--topbar-h) + 46vh); }
</style>
