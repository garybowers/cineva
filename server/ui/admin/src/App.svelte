<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, createLibrary, deleteLibrary, scanLibrary, listScans,
    cancelScan, listSessions, stopSession, subscribeEvents, browseDirs, systemStats, playStats, diskStats, auditLog,
    libraryItems, itemMatches, applyMatch, refreshItem, type Item, type MatchCandidate,
    updateLibrary,
    refreshLibrary, cancelRefresh, refreshStatus, type RefreshProgress,
    libraryCollections, collectionDetail, createCollection, renameCollection,
    setCollectionItems, deleteCollection, updateItem, clearItemLocks,
    type Collection, type ItemEdit,
    authStatus, login, logout, listUsers, createUser, updateUser, deleteUser, UNAUTHORISED, getSettings, putSettings,
    type ServerSettings,
    type Health, type Library, type Scan, type PlaySession, type DirListing, type SystemSample, type PlayDay, type DiskVolume, type AuditEntry, type User, type UserRequest,
  } from './lib/api'
  import LineChart from './LineChart.svelte'
  import BarChart from './BarChart.svelte'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'

  /** Write a browsed path back into whichever row opened the browser. */
  function setBrowsePath(p: string) {
    const list = editLib ? editPaths : newPaths
    const next = [...list]
    next[browseTarget] = p
    if (editLib) editPaths = next
    else newPaths = next
  }

  type Page = 'dashboard' | 'libraries' | 'users' | 'activity' | 'logs' | 'settings'
  const pages: { id: Page; label: string }[] = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'libraries', label: 'Libraries' },
    { id: 'users', label: 'Users' },
    { id: 'activity', label: 'Activity' },
    { id: 'logs', label: 'Logs' },
    { id: 'settings', label: 'Settings' },
  ]
  const mediaTypes = ['movies', 'tv', 'music', 'audiobooks', 'podcasts', 'books']

  // ---- Sign-in gate: the admin UI is for admins only ----
  let me: User | null = $state(null)
  let gate: 'checking' | 'login' | 'forbidden' | 'in' = $state('checking')
  let authUser = $state(''), authPass = $state(''), authErr = $state(''), authBusy = $state(false)
  async function checkAuth() {
    try {
      const st = await authStatus()
      if (st.setupRequired) { location.href = '/'; return } // first-run screen lives in the client
      if (!st.user) gate = 'login'
      else if (st.user.role !== 'admin') { me = st.user; gate = 'forbidden' }
      else { me = st.user; gate = 'in' }
    } catch {
      gate = 'login'
    }
  }
  async function submitAuth(e: Event) {
    e.preventDefault()
    authBusy = true
    authErr = ''
    try {
      const u = await login(authUser, authPass)
      authPass = ''
      if (u.role !== 'admin') { me = u; gate = 'forbidden'; return }
      me = u
      gate = 'in'
      boot()
    } catch (err) {
      authErr = err instanceof Error ? err.message : String(err)
    } finally {
      authBusy = false
    }
  }
  async function signOut() {
    try { await logout() } catch { /* gone */ }
    me = null
    gate = 'login'
  }

  // ---- Server settings (runtime, stored in the database) ----
  let settings: ServerSettings | null = $state(null)
  const loadSettings = () => getSettings().then((s) => (settings = s)).catch(() => {})
  async function setHWAccel(on: boolean) {
    const before = settings
    if (settings) settings = { ...settings, hwAccel: on } // optimistic
    try {
      settings = await putSettings({ hwAccel: on })
    } catch (err) {
      settings = before
      error = err instanceof Error ? err.message : String(err)
    }
  }

  // Spelled out rather than shown as bare codes: "GB" and "IE" are not
  // obviously BBFC and IFCO to someone picking from a list.
  const regionNames: Record<string, string> = {
    GB: 'United Kingdom (BBFC)', US: 'United States (MPA)', IE: 'Ireland (IFCO)',
    AU: 'Australia (ACB)', NZ: 'New Zealand (OFLC)', CA: 'Canada',
    DE: 'Germany (FSK)', FR: 'France (CNC)', NL: 'Netherlands (Kijkwijzer)',
    ES: 'Spain (ICAA)', IT: 'Italy', SE: 'Sweden', NO: 'Norway',
    DK: 'Denmark', FI: 'Finland',
  }
  const regionName = (code: string) => regionNames[code] ?? code

  async function setCertRegion(region: string) {
    const before = settings
    if (settings) settings = { ...settings, certRegion: region } // optimistic
    try {
      settings = await putSettings({ certRegion: region })
    } catch (err) {
      settings = before
      error = err instanceof Error ? err.message : String(err)
    }
  }

  async function setLoginMode(mode: 'picker' | 'form') {
    try {
      settings = await putSettings({ loginMode: mode })
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  // ---- Users page ----
  let users: User[] = $state([])
  const loadUsers = () => listUsers().then((u) => (users = u)).catch(() => {})
  let editing: (UserRequest & { id?: string; clearPin?: boolean; hasPin?: boolean }) | null = $state(null) // null = closed; no id = new
  let userErr = $state('')
  let userSaving = $state(false)
  function newUser() {
    editing = { username: '', displayName: '', password: '', pin: '', role: 'user', disabled: false, libraryIds: [] }
    userErr = ''
  }
  function editUser(u: User) {
    editing = { id: u.id, displayName: u.displayName, password: '', pin: '', hasPin: u.hasPin, clearPin: false, role: u.role, disabled: u.disabled, libraryIds: [...u.libraryIds] }
    userErr = ''
  }
  function toggleGrant(id: string) {
    if (!editing) return
    const set = new Set(editing.libraryIds ?? [])
    if (set.has(id)) set.delete(id); else set.add(id)
    editing.libraryIds = [...set]
  }
  async function saveUser(e: Event) {
    e.preventDefault()
    if (!editing) return
    userSaving = true
    userErr = ''
    try {
      const { id, clearPin, hasPin, ...body } = editing
      if (!body.password) delete body.password
      if (clearPin) body.pin = ''
      else if (!body.pin) delete body.pin
      const saved = id ? await updateUser(id, body) : await createUser(body)
      users = id ? users.map((u) => (u.id === saved.id ? saved : u)) : [...users, saved]
      editing = null
    } catch (err) {
      userErr = err instanceof Error ? err.message : String(err)
    } finally {
      userSaving = false
    }
  }
  async function removeUser(u: User) {
    if (!confirm(`Delete user "${u.username}"? Their watch history, sessions and API keys go with them.`)) return
    const before = users
    users = users.filter((x) => x.id !== u.id)
    try {
      await deleteUser(u.id)
    } catch (err) {
      users = before
      error = err instanceof Error ? err.message : String(err)
    }
  }

  let page: Page = $state('dashboard')
  let health: Health | null = $state(null)
  let offline = $state(false)
  let themes: ThemeInfo[] = $state([])
  let theme = $state(currentTheme())
  let libraries: Library[] = $state([])
  let scans: Scan[] = $state([])
  let sessions: PlaySession[] = $state([])
  let error = $state('')

  // Add-library form
  let showAdd = $state(false)
  let newName = $state('')
  let newType = $state('movies')
  // A library may cover several paths; the form holds a list with at least
  // one row rather than a single field.
  let newPaths: string[] = $state([''])
  // Which path row the folder browser is filling, so Browse… on the third
  // row does not overwrite the first.
  let browseTarget = $state(0)

  // Editing an existing library: same shape, so one set of controls serves
  // both. Null when nothing is being edited.
  let editLib: Library | null = $state(null)
  let editName = $state('')
  let editPaths: string[] = $state([])
  let libEditBusy = $state(false)

  function startEditLibrary(lib: Library) {
    editLib = lib
    editName = lib.name
    editPaths = lib.paths.length > 0 ? [...lib.paths] : ['']
  }

  async function saveLibraryEdit() {
    if (!editLib) return
    libEditBusy = true
    try {
      const paths = editPaths.map((p) => p.trim()).filter(Boolean)
      if (paths.length === 0) throw new Error('a library needs at least one path')
      const updated = await updateLibrary(editLib.id, { name: editName.trim(), paths })
      libraries = libraries.map((l) => (l.id === updated.id ? { ...l, ...updated } : l))
      editLib = null
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      libEditBusy = false
    }
  }
  let saving = $state(false)

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  // The four lists are independent, so they go out together: a refresh
  // costs one round-trip, not four stacked end to end.
  async function refresh() {
    refreshing = true
    try {
      const [h, libs, sc, sess] = await Promise.all([getHealth(), listLibraries(), listScans(), listSessions()])
      health = h
      libraries = libs
      scans = sc
      sessions = sess
      offline = false
    } catch {
      offline = true
    } finally {
      refreshing = false
    }
  }

  async function submitLibrary() {
    saving = true
    error = ''
    try {
      const paths = newPaths.map((p) => p.trim()).filter(Boolean)
      const lib = await createLibrary(newName, newType, paths)
      libraries = [...libraries, lib] // the "libraries" event settles the order
      showAdd = false
      newName = ''
      newPaths = ['']
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      saving = false
    }
  }

  // Deletes drop the row before the request goes out, so the UI never sits
  // waiting on a round-trip. The server's "libraries" event reconciles; a
  // failure restores the row and reports why.
  async function removeLibrary(lib: Library) {
    if (!confirm(`Delete library "${lib.name}"? Scanned items are removed; media files on disk are untouched.`)) return
    const before = libraries
    libraries = libraries.filter((l) => l.id !== lib.id)
    try {
      await deleteLibrary(lib.id)
    } catch (e) {
      libraries = before
      error = String(e instanceof Error ? e.message : e)
    }
  }

  async function startScan(lib: Library) {
    error = ''
    go('activity') // move there now; progress arrives over the stream
    try {
      const s = await scanLibrary(lib.id)
      if (!scans.some((x) => x.id === s.id)) scans = [s, ...scans]
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // ---- Library contents: browse what was scanned, fix what mismatched ----
  let manage: Library | null = $state(null)   // library being managed, null = list
  let items: Item[] = $state([])
  let itemsLoading = $state(false)
  let itemFilter = $state('')
  let onlyUnmatched = $state(false)
  let busyItem: string | null = $state(null)  // id of the item being refreshed
  let matchFor: Item | null = $state(null)    // item whose match dialogue is open
  let matchQuery = $state('')
  let matchResults: MatchCandidate[] = $state([])
  let matchBusy = $state(false)
  let matchErr = $state('')

  // Progress of a library-wide refresh. Driven by the event stream, but
  // also fetched on open so a page loaded mid-run shows the bar straight
  // away rather than waiting for the next event.
  let metaRefresh: RefreshProgress | null = $state(null)

  // ---- Manage screen: Films | Collections ----
  type ManageTab = 'films' | 'collections'
  let manageTab: ManageTab = $state('films')

  let collections: Collection[] = $state([])
  let editingColl: Collection | null = $state(null)
  let collFilms: Item[] = $state([]) // the collection's films, in its order
  let collBusy = $state(false)
  let addFilmQuery = $state('')

  // Manual metadata edit.
  let editItem: Item | null = $state(null)
  let editForm: ItemEdit = $state({})
  let editBusy = $state(false)

  async function openManage(lib: Library) {
    manage = lib
    manageTab = 'films'
    itemFilter = ''
    onlyUnmatched = false
    itemsLoading = true
    metaRefresh = null
    editingColl = null
    collections = []
    try {
      items = await libraryItems(lib.id)
      const st = await refreshStatus(lib.id)
      if (st.running) metaRefresh = st
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      itemsLoading = false
    }
    loadCollections()
  }

  async function loadCollections() {
    if (!manage) return
    try {
      collections = await libraryCollections(manage.id)
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  async function openCollection(c: Collection) {
    if (!manage) return
    collBusy = true
    try {
      const d = await collectionDetail(manage.id, c.key)
      editingColl = d.collection
      collFilms = d.items
      addFilmQuery = ''
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      collBusy = false
    }
  }

  /** Persist the current film list; it is both membership and order. */
  async function saveCollFilms(next: Item[]) {
    if (!manage || !editingColl) return
    const before = collFilms
    collFilms = next // optimistic: reordering should feel immediate
    collBusy = true
    try {
      const d = await setCollectionItems(manage.id, editingColl.key, next.map((i) => i.id))
      // Adopting a provider collection changes its key, so take the
      // server's version rather than keeping the one we were opened with.
      editingColl = d.collection
      collFilms = d.items
      await loadCollections()
    } catch (e) {
      collFilms = before
      error = String(e instanceof Error ? e.message : e)
    } finally {
      collBusy = false
    }
  }

  function moveFilm(index: number, delta: number) {
    const next = [...collFilms]
    const to = index + delta
    if (to < 0 || to >= next.length) return
    ;[next[index], next[to]] = [next[to], next[index]]
    saveCollFilms(next)
  }

  const removeFilm = (id: string) => saveCollFilms(collFilms.filter((i) => i.id !== id))
  const addFilm = (it: Item) => saveCollFilms([...collFilms, it])

  /** Films not already in the collection, narrowed by the search box. */
  const addCandidates = $derived.by(() => {
    const q = addFilmQuery.trim().toLowerCase()
    if (!q) return []
    const have = new Set(collFilms.map((i) => i.id))
    return items
      .filter((i) => !have.has(i.id) && !i.versionOf && i.title.toLowerCase().includes(q))
      .slice(0, 8)
  })

  async function renameColl(name: string) {
    if (!manage || !editingColl || !name.trim()) return
    collBusy = true
    try {
      const d = await renameCollection(manage.id, editingColl.key, name.trim())
      editingColl = d.collection
      collFilms = d.items
      await loadCollections()
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      collBusy = false
    }
  }

  async function newCollection() {
    if (!manage) return
    const name = prompt('Name for the new collection')
    if (!name?.trim()) return
    try {
      const c = await createCollection(manage.id, name.trim())
      await loadCollections()
      await openCollection(c)
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  async function removeCollection(c: Collection) {
    const msg = c.tmdbId
      ? `Discard your changes to “${c.name}” and go back to the provider's own grouping?`
      : `Delete the collection “${c.name}”? The films themselves are not touched.`
    if (!manage || !confirm(msg)) return
    try {
      await deleteCollection(manage.id, c.key)
      if (editingColl?.key === c.key) editingColl = null
      await loadCollections()
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // ---- Manual metadata edit ----
  function openEdit(it: Item) {
    editItem = it
    // Pre-filled with what is there now, so the form shows the current
    // metadata rather than making the operator retype it.
    editForm = {
      title: it.title,
      year: it.year,
      overview: it.overview ?? '',
      tagline: it.tagline ?? '',
      certification: it.certification ?? '',
      rating: it.rating,
      poster: it.poster ?? '',
      backdrop: it.backdrop ?? '',
      genres: it.genres ?? [],
    }
  }

  async function saveEdit() {
    if (!editItem) return
    editBusy = true
    try {
      const updated = await updateItem(editItem.id, editForm)
      items = items.map((i) => (i.id === updated.id ? { ...i, ...updated } : i))
      editItem = null
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      editBusy = false
    }
  }

  async function unlockItem(it: Item) {
    if (!confirm(`Let the metadata provider manage “${it.title}” again? Your edits stay until the next refresh.`)) return
    try {
      await clearItemLocks(it.id)
      items = items.map((i) => (i.id === it.id ? { ...i, lockedFields: [] } : i))
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // True when the list is narrowed, in which case refresh acts on what is
  // on screen rather than the whole library.
  const filtered = $derived(onlyUnmatched || itemFilter.trim() !== '')

  const refreshLabel = $derived(
    !filtered
      ? 'Refresh all metadata'
      : `Refresh ${shownItems.length} ${onlyUnmatched && !itemFilter.trim() ? 'unmatched' : 'shown'}`,
  )

  async function startRefreshAll() {
    if (!manage) return
    error = ''
    const lib = manage
    // Send the visible ids when filtered, so the button does exactly what it
    // says; otherwise let the server walk the whole library.
    const only = filtered ? shownItems.map((i) => i.id) : undefined
    try {
      metaRefresh = await refreshLibrary(lib.id, only)
    } catch (e) {
      metaRefresh = null
      error = String(e instanceof Error ? e.message : e)
    }
  }

  async function stopRefreshAll() {
    if (!manage) return
    try {
      await cancelRefresh(manage.id)
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  const refreshPct = $derived(
    metaRefresh && metaRefresh.total > 0 ? Math.round((metaRefresh.done / metaRefresh.total) * 100) : 0,
  )

  const matchedCount = $derived(items.filter((i) => i.tmdbId).length)

  const shownItems = $derived.by(() => {
    const q = itemFilter.trim().toLowerCase()
    return items.filter((i) => {
      if (onlyUnmatched && i.tmdbId) return false
      if (!q) return true
      return i.title.toLowerCase().includes(q) || i.path.toLowerCase().includes(q)
    })
  })

  // Replaces the row in place so the table does not jump while working
  // through a list of corrections.
  function replaceItem(updated: Item) {
    items = items.map((i) => (i.id === updated.id ? updated : i))
  }

  async function refreshOne(item: Item) {
    busyItem = item.id
    error = ''
    try {
      replaceItem(await refreshItem(item.id))
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      busyItem = null
    }
  }

  async function openMatch(item: Item) {
    matchFor = item
    matchQuery = item.title
    matchResults = []
    matchErr = ''
    await runMatchSearch()
  }

  async function runMatchSearch() {
    if (!matchFor) return
    matchBusy = true
    matchErr = ''
    try {
      // Year is dropped on a manual search: the operator is correcting a
      // wrong match, so the year that produced it is as likely to be wrong.
      const r = await itemMatches(matchFor.id, matchQuery, 0)
      matchResults = r.results
      if (r.results.length === 0) matchErr = 'No matches for that title.'
    } catch (e) {
      matchResults = []
      matchErr = String(e instanceof Error ? e.message : e)
    } finally {
      matchBusy = false
    }
  }

  async function chooseMatch(c: MatchCandidate) {
    if (!matchFor) return
    matchBusy = true
    try {
      replaceItem(await applyMatch(matchFor.id, c.tmdbId))
      matchFor = null
    } catch (e) {
      matchErr = String(e instanceof Error ? e.message : e)
    } finally {
      matchBusy = false
    }
  }

  // A stopped transcode leaves the list at once; the "sessions" event confirms.
  async function stopTranscode(sessionId: string) {
    const before = sessions
    sessions = sessions.filter((x) => x.sessionId !== sessionId)
    try {
      await stopSession(sessionId)
    } catch (e) {
      sessions = before
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // Cancelling takes until the scanner finishes the file it is on, so the
  // button reports "Cancelling…" straight away rather than claiming the scan
  // has already stopped. The scan event replaces it with the real status.
  let cancelling: string[] = $state([])
  async function requestCancel(id: string) {
    cancelling = [...cancelling, id]
    try {
      await cancelScan(id)
    } catch (e) {
      cancelling = cancelling.filter((x) => x !== id)
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // ---- Dashboard: host CPU/RAM samples + play history ----
  let sysRange: '1h' | '24h' = $state('1h')
  let playDays = $state(30)
  let samples: SystemSample[] = $state([])
  let plays: PlayDay[] = $state([])

  async function loadStats() {
    try {
      samples = await systemStats(sysRange)
      plays = await playStats(playDays)
    } catch {
      /* dashboard is best-effort; the poll will retry */
    }
  }
  function setSysRange(r: '1h' | '24h') {
    sysRange = r
    systemStats(r).then((x) => (samples = x)).catch(() => {})
  }
  function setPlayDays(d: number) {
    playDays = d
    playStats(d).then((x) => (plays = x)).catch(() => {})
  }
  // Live sample from the event stream: the 1h view takes every sample; the
  // 24h view keeps one a minute to match the server's coarse ring.
  function onSample(data: unknown) {
    const smp = data as SystemSample | null
    if (!smp?.at) return
    const last = samples[samples.length - 1]
    if (sysRange === '24h' && last && Date.parse(smp.at) - Date.parse(last.at) < 60_000) return
    const keep = sysRange === '1h' ? 3_600_000 : 86_400_000
    const cut = Date.parse(smp.at) - keep
    samples = [...samples.filter((x) => Date.parse(x.at) >= cut), smp]
  }
  const latest = $derived(samples[samples.length - 1] ?? null)
  const gb = (b: number) => (b / 1024 ** 3).toFixed(1)
  const cpuSeries = $derived([{ name: 'CPU', token: 1 as const, points: samples.map((x) => ({ t: Date.parse(x.at), v: x.cpuPct })) }])
  const memSeries = $derived([
    { name: 'System used', token: 1 as const, points: samples.map((x) => ({ t: Date.parse(x.at), v: x.memUsed / 1024 ** 3 })) },
    { name: 'Cineva', token: 2 as const, points: samples.map((x) => ({ t: Date.parse(x.at), v: x.procRss / 1024 ** 3 })) },
  ])
  const playBars = $derived(plays.map((d) => ({ label: d.date, values: [d.movies, d.episodes, d.tracks] })))
  // Storage: one row per filesystem backing a library.
  let disks: DiskVolume[] = $state([])
  const loadDisks = () => diskStats().then((d) => (disks = d)).catch(() => {})
  const gbShort = (b: number) => (b >= 1024 ** 4 ? `${(b / 1024 ** 4).toFixed(2)} TB` : `${(b / 1024 ** 3).toFixed(0)} GB`)
  const usedPct = (d: DiskVolume) => (d.totalBytes ? Math.round((d.usedBytes / d.totalBytes) * 100) : 0)

  // Audit trail (Logs page): newest first, filter by action prefix.
  let logs: AuditEntry[] = $state([])
  let logFilter = $state('')
  let logsLoading = $state(false)
  const logFilters = [
    { id: '', label: 'All' }, { id: 'play.', label: 'Playback' }, { id: 'download', label: 'Downloads' },
    { id: 'scan.', label: 'Scans' }, { id: 'library.', label: 'Libraries' }, { id: 'transcode.', label: 'Transcodes' },
  ]
  async function loadLogs(more = false) {
    logsLoading = true
    try {
      const page = await auditLog(100, more ? logs.length : 0, logFilter)
      logs = more ? [...logs, ...page] : page
    } catch {
      /* fallback poll retries */
    } finally {
      logsLoading = false
    }
  }
  function setLogFilter(f: string) {
    logFilter = f
    loadLogs()
  }
  const actionLabel = (a: string) =>
    ({ 'play.start': 'Played', download: 'Downloaded', 'scan.start': 'Started scan', 'scan.cancel': 'Cancelled scan',
      'scan.completed': 'Scan completed', 'scan.failed': 'Scan failed', 'library.create': 'Created library',
      'library.delete': 'Deleted library', 'transcode.stop': 'Stopped transcode' })[a] ?? a
  const when = (iso: string) => {
    const d = new Date(iso.includes('T') ? iso : iso.replace(' ', 'T') + 'Z')
    return d.toLocaleString([], { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit' })
  }

  const playsToday = $derived(plays.length ? plays[plays.length - 1].movies + plays[plays.length - 1].episodes + plays[plays.length - 1].tracks : 0)
  const playsTotal = $derived(plays.reduce((n, d) => n + d.movies + d.episodes + d.tracks, 0))

  // Folder picker for the path field: browses the server's filesystem one
  // level at a time (directories only).
  let browsing = $state(false)
  let listing: DirListing | null = $state(null)
  let browseErr = $state('')
  let showHidden = $state(false)

  async function browse(path: string) {
    browseErr = ''
    try {
      listing = await browseDirs(path, showHidden)
    } catch (e) {
      browseErr = String(e instanceof Error ? e.message : e)
    }
  }

  function openBrowser(row = 0) {
    browseTarget = row
    browsing = true
    const cur = (editLib ? editPaths : newPaths)[browseTarget] ?? ''
    const start = cur.trim() && cur.startsWith('/') ? cur.trim() : '/'
    browse(start)
  }

  function chooseFolder() {
    if (listing) setBrowsePath(listing.path)
    browsing = false
  }

  const crumbs = (p: string) => {
    const parts = p.split('/').filter(Boolean)
    return [{ name: '/', path: '/' }, ...parts.map((n, i) => ({ name: n, path: '/' + parts.slice(0, i + 1).join('/') }))]
  }

  let refreshing = $state(false)
  const clock = (secs: number) => {
    const h = Math.floor(secs / 3600), m = Math.floor((secs % 3600) / 60), s = Math.floor(secs % 60)
    return h ? `${h}:${String(m).padStart(2, '0')}:${String(s).padStart(2, '0')}` : `${m}:${String(s).padStart(2, '0')}`
  }
  const since = (iso: string) => {
    const mins = Math.round((Date.now() - Date.parse(iso)) / 60000)
    return mins < 1 ? 'just now' : mins < 60 ? `${mins} min ago` : `${Math.floor(mins / 60)} h ${mins % 60} min ago`
  }

  function libName(id: string): string {
    return libraries.find((l) => l.id === id)?.name ?? id.slice(0, 8)
  }

  // Live scan totals arrive on the event stream only (the scanner counts its
  // file listing up front); keyed by scan id for the progress bars below.
  let scanTotals: Map<string, number> = $state(new Map())

  function onScanEvent(data: unknown) {
    const s = data as (Scan & { filesTotal?: number }) | null
    if (!s?.id) return
    if (s.filesTotal) scanTotals = new Map(scanTotals).set(s.id, s.filesTotal)
    const i = scans.findIndex((x) => x.id === s.id)
    if (i >= 0) scans[i] = s
    else scans = [s, ...scans]
    if (s.status !== 'running') {
      // Finished scans change item counts.
      listLibraries().then((l) => (libraries = l)).catch(() => {})
      cancelling = cancelling.filter((x) => x !== s.id)
    }
  }

  const pct = (s: Scan): number | null => {
    const total = scanTotals.get(s.id)
    return s.status === 'running' && total ? Math.min(100, Math.round((s.filesSeen / total) * 100)) : null
  }

  let booted = false
  let teardown: (() => void) | null = null
  function boot() {
    if (booted) return
    booted = true
    // Libraries must be loaded before the route is applied: a deep link to
    // /admin/libraries/<name> resolves that name against the list.
    refresh().then(() => applyRoute(location.pathname))
    loadStats()
    loadDisks()
    loadLogs()
    loadUsers()
    loadSettings()
    // Live updates over SSE; the slow poll is only a fallback for missed events.
    const closeEvents = subscribeEvents(
      {
        scan: onScanEvent,
        refresh: (data) => {
          const p = data as RefreshProgress | null
          if (!p || !manage || p.libraryId !== manage.id) return
          metaRefresh = p.running ? p : null
          // The run is over: pick up every title it rewrote.
          if (!p.running) libraryItems(manage.id).then((i) => (items = i)).catch(() => {})
        },
        libraries: () => {
          listLibraries().then((l) => (libraries = l)).catch(() => {})
          listScans().then((s) => (scans = s)).catch(() => {}) // a deleted library takes its scans with it
          loadDisks()
        },
        // Items changed — a scan finding files, a match applied, a manual
        // edit, a collection edited. Only the manage screen shows them, so
        // only refetch while it is open; matching the client here means an
        // admin watching a scan sees titles arrive rather than a stale list.
        items: () => {
          if (!manage || itemsLoading) return
          libraryItems(manage.id).then((i) => (items = i)).catch(() => {})
          if (manageTab === 'collections' && !editingColl) loadCollections()
        },
        stats: onSample,
        logs: () => { if (!logsLoading) loadLogs() },
        users: loadUsers,
        settings: loadSettings,
        sessions: () => listSessions().then((x) => (sessions = x)).catch(() => {}),
        plays: () => playStats(playDays).then((x) => (plays = x)).catch(() => {}),
      },
      (connected) => {
        if (connected && offline) refresh()
      },
    )
    const id = setInterval(refresh, 30000)
    teardown = () => {
      clearInterval(id)
      closeEvents()
    }
  }

  // ---- URL routing ----
  // The admin is served under /admin/, and every screen has an address:
  // /admin/libraries, /admin/libraries/<name>, /admin/users and so on. The
  // server returns the admin's index.html for any such path, so a deep link
  // or a reload lands on the right screen rather than the dashboard.
  const BASE = '/admin'
  let routing = false // true while applyRoute drives state, to suppress pushState

  function buildPath(): string {
    if (page === 'libraries' && manage) return `${BASE}/libraries/${encodeURIComponent(manage.name)}`
    if (page === 'dashboard') return `${BASE}/`
    return `${BASE}/${page}`
  }

  function syncURL() {
    if (routing) return
    const p = buildPath()
    if (p !== location.pathname) history.pushState({}, '', p)
  }

  // Libraries are addressed by name to match the client's scheme. A name
  // that no longer resolves falls back to the list rather than a blank page.
  async function applyRoute(pathname: string) {
    routing = true
    try {
      const rest = pathname.startsWith(BASE) ? pathname.slice(BASE.length) : pathname
      const parts = rest.split('/').filter(Boolean).map(decodeURIComponent)
      const [first, second] = parts
      const pages: Page[] = ['dashboard', 'libraries', 'users', 'activity', 'logs', 'settings']
      const target = pages.includes(first as Page) ? (first as Page) : 'dashboard'
      page = target
      if (target === 'libraries' && second) {
        const lib = libraries.find((l) => l.name === second)
        if (lib) await openManage(lib)
        else manage = null
      } else if (target === 'libraries') {
        manage = null
      }
    } finally {
      routing = false
    }
  }

  // Every state change that has an address goes through these, so the URL
  // and the screen cannot drift apart.
  function go(p: Page) {
    page = p
    if (p !== 'libraries') manage = null
    syncURL()
  }

  onMount(() => {
    listThemes().then((t) => (themes = t)).catch(() => {})
    checkAuth().then(() => { if (gate === 'in') boot() })
    const onPop = () => applyRoute(location.pathname)
    window.addEventListener('popstate', onPop)
    const onUnauthorised = () => { if (gate === 'in') { me = null; gate = 'login' } }
    window.addEventListener(UNAUTHORISED, onUnauthorised)
    return () => {
      teardown?.()
      window.removeEventListener('popstate', onPop)
      window.removeEventListener(UNAUTHORISED, onUnauthorised)
    }
  })
</script>

{#if gate !== 'in'}
  <div class="gate">
    {#if gate === 'checking'}
      <span class="muted">Connecting…</span>
    {:else if gate === 'forbidden'}
      <form onsubmit={(e) => { e.preventDefault(); signOut() }}>
        <h1>Admin only</h1>
        <p>{me?.displayName} is signed in but is not an administrator.</p>
        <a class="primary" href="/">Back to Cineva</a>
        <button type="submit">Sign out</button>
      </form>
    {:else}
      <form onsubmit={submitAuth}>
        <h1>Cineva admin</h1>
        <p>Sign in with an administrator account.</p>
        <input placeholder="Username" bind:value={authUser} autocomplete="username" required />
        <input type="password" placeholder="Password" bind:value={authPass} autocomplete="current-password" required />
        {#if authErr}<span class="err">{authErr}</span>{/if}
        <button class="primary" type="submit" disabled={authBusy}>{authBusy ? '…' : 'Sign in'}</button>
      </form>
    {/if}
  </div>
{/if}

<div class="shell">
  <aside>
    <div class="logo">Cineva <span>admin</span></div>
    <nav>
      {#each pages as p (p.id)}
        <button class:active={page === p.id} onclick={() => go(p.id)}>{p.label}</button>
      {/each}
    </nav>
    {#if me}<div class="who">{me.displayName} <button onclick={signOut}>Sign out</button></div>{/if}
    <a class="back" href="/">← Back to Cineva</a>
  </aside>

  {#if browsing}
    <div class="modal-bg" role="presentation" onclick={() => (browsing = false)}>
      <div class="modal" role="dialog" aria-label="Choose a folder" onclick={(e) => e.stopPropagation()}>
        <div class="modal-head">
          <strong>Choose a folder</strong>
          <label class="hidden-toggle"><input type="checkbox" bind:checked={showHidden} onchange={() => listing && browse(listing.path)} /> Show hidden</label>
        </div>
        {#if listing}
          <div class="crumbs">
            {#each crumbs(listing.path) as c, i (c.path)}
              {#if i > 0}<span class="sep">/</span>{/if}
              <button type="button" class="crumb" onclick={() => browse(c.path)}>{c.name}</button>
            {/each}
          </div>
        {/if}
        {#if browseErr}<div class="error slim">{browseErr}</div>{/if}
        <ul class="dirs">
          {#if listing?.parent}
            <li><button type="button" onclick={() => browse(listing!.parent!)}>‥ up</button></li>
          {/if}
          {#each listing?.dirs ?? [] as d (d.path)}
            <li><button type="button" onclick={() => browse(d.path)}>{d.name}</button></li>
          {/each}
          {#if listing && listing.dirs.length === 0}
            <li class="muted small">No subfolders</li>
          {/if}
        </ul>
        <div class="modal-foot">
          <span class="muted small">{listing?.path ?? ''}</span>
          <span>
            <button type="button" onclick={() => (browsing = false)}>Cancel</button>
            <button type="button" class="primary" disabled={!listing} onclick={chooseFolder}>Select this folder</button>
          </span>
        </div>
      </div>
    </div>
  {/if}

  <main>
    {#if error}
      <div class="error">{error} <button onclick={() => (error = '')}>✕</button></div>
    {/if}

    {#if page === 'dashboard'}
      <h1>Dashboard</h1>
      <div class="cards">
        <div class="card">
          <div class="k">Server</div>
          <div class="v" class:err={offline}>{offline ? 'Unreachable' : 'Online'}</div>
          <div class="sub">{health ? `${health.service} ${health.version}` : 'no response from /api/v1/health'}</div>
        </div>
        <div class="card">
          <div class="k">Libraries</div>
          <div class="v">{libraries.length}</div>
          <div class="sub">{libraries.reduce((n, l) => n + l.itemCount, 0)} playable items</div>
        </div>
        <div class="card">
          <div class="k">Active streams</div>
          <div class="v">{sessions.length}</div>
          <div class="sub">{scans.filter((s) => s.status === 'running').length} scans running</div>
        </div>
      </div>

      <div class="filters">
        <span class="muted small">System</span>
        <button class="chip" class:sel={sysRange === '1h'} onclick={() => setSysRange('1h')}>Last hour</button>
        <button class="chip" class:sel={sysRange === '24h'} onclick={() => setSysRange('24h')}>Last 24 hours</button>
        <span class="muted small gap">Plays</span>
        <button class="chip" class:sel={playDays === 7} onclick={() => setPlayDays(7)}>7 days</button>
        <button class="chip" class:sel={playDays === 30} onclick={() => setPlayDays(30)}>30 days</button>
        <button class="chip" class:sel={playDays === 90} onclick={() => setPlayDays(90)}>90 days</button>
      </div>
      <div class="graphs">
        <div class="card">
          <div class="k">CPU</div>
          <div class="v">{latest ? `${Math.round(latest.cpuPct)}%` : '—'}</div>
          <div class="sub">{latest ? `${latest.numCpu} cores · whole host` : 'collecting…'}</div>
          <LineChart series={cpuSeries} yMax={100} format={(v) => `${Math.round(v)}%`} />
        </div>
        <div class="card">
          <div class="k">Memory</div>
          <div class="v">{latest ? `${gb(latest.memUsed)} GB` : '—'}</div>
          <div class="sub">{latest ? `of ${gb(latest.memTotal)} GB · Cineva ${(latest.procRss / 1024 ** 2).toFixed(0)} MB` : 'collecting…'}</div>
          <LineChart series={memSeries} yMax={latest ? latest.memTotal / 1024 ** 3 : undefined} format={(v) => `${v.toFixed(v < 10 ? 1 : 0)} GB`} />
        </div>
        <div class="card wide">
          <div class="k">Plays</div>
          <div class="v">{playsToday}</div>
          <div class="sub">today · {playsTotal} in the last {playDays} days</div>
          <BarChart bars={playBars} series={['Movies', 'Episodes', 'Tracks']} />
        </div>
        <div class="card wide">
          <div class="k">Storage</div>
          {#if disks.length === 0}
            <div class="sub">No library paths yet.</div>
          {:else}
            <ul class="disks">
              {#each disks as d (d.paths.join('|'))}
                <li>
                  <div class="disk-head">
                    <span class="disk-libs">{d.libraries.join(', ')}</span>
                    <span class="muted small">{d.paths.join(', ')}</span>
                  </div>
                  {#if d.error}
                    <div class="muted small">{d.error}</div>
                  {:else}
                    <div class="meter" role="meter" aria-valuenow={usedPct(d)} aria-valuemin="0" aria-valuemax="100" aria-label="{d.libraries.join(', ')} disk usage">
                      <div class="meter-fill" class:warn={usedPct(d) >= 85} class:crit={usedPct(d) >= 95} style="width: {usedPct(d)}%"></div>
                    </div>
                    <div class="disk-nums"><strong>{gbShort(d.freeBytes)} free</strong><span class="muted">{gbShort(d.usedBytes)} used of {gbShort(d.totalBytes)} · {usedPct(d)}%</span></div>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        </div>
      </div>
    {:else if page === 'libraries' && manage}
      <nav class="lib-crumbs">
        <button class="lib-link" onclick={() => go('libraries')}>Libraries</button>
        <span class="muted">›</span>
        <span>{manage.name}</span>
      </nav>
      <h1>{manage.name}</h1>
      <nav class="lib-tabs" aria-label="{manage.name} management">
        {#each [['films', 'Films'], ['collections', 'Collections']] as [id, label] (id)}
          <button
            class="lib-tab"
            class:on={manageTab === id}
            aria-current={manageTab === id ? 'page' : undefined}
            onclick={() => { manageTab = id as ManageTab; editingColl = null }}>{label}</button>
        {/each}
      </nav>
      {#if manageTab === 'collections'}
        {#if editingColl}
          <div class="lib-toolbar">
            <button onclick={() => (editingColl = null)}>← All collections</button>
            <strong>{editingColl.name}</strong>
            {#if editingColl.managed}<span class="tag ok">edited</span>{:else}<span class="tag">from provider</span>{/if}
            <span class="lib-spacer"></span>
            <button onclick={() => { const n = prompt('Collection name', editingColl?.name); if (n) renameColl(n) }}>Rename…</button>
            {#if editingColl.managed}
              <button class="lib-btn danger" onclick={() => editingColl && removeCollection(editingColl)}>
                {editingColl.tmdbId ? 'Revert to provider' : 'Delete'}
              </button>
            {/if}
          </div>
          <p class="hint">
            The order here is the order the collection plays in. Editing a
            provider collection takes it over, so a later scan cannot undo
            your changes.
          </p>
          <div class="tablewrap">
            <table class="lib-table">
              <thead><tr><th></th><th>#</th><th>Title</th><th>Year</th><th>Order</th><th></th></tr></thead>
              <tbody>
                {#each collFilms as it, i (it.id)}
                  <tr>
                    <td class="lib-pcell">
                      {#if it.poster}<img class="lib-poster" src={it.poster} alt="" loading="lazy" />{:else}<span class="lib-poster ph"></span>{/if}
                    </td>
                    <td class="muted">{i + 1}</td>
                    <td class="lib-title">{it.title}</td>
                    <td class="muted">{it.year ?? '—'}</td>
                    <td class="actions">
                      <button disabled={i === 0 || collBusy} onclick={() => moveFilm(i, -1)} aria-label="Move up">↑</button>
                      <button disabled={i === collFilms.length - 1 || collBusy} onclick={() => moveFilm(i, 1)} aria-label="Move down">↓</button>
                    </td>
                    <td class="actions">
                      <button class="lib-btn danger" disabled={collBusy} onclick={() => removeFilm(it.id)}>Remove</button>
                    </td>
                  </tr>
                {/each}
                {#if collFilms.length === 0}
                  <tr><td colspan="6" class="muted">No films in this collection yet.</td></tr>
                {/if}
              </tbody>
            </table>
          </div>
          <div class="lib-toolbar">
            <label class="lib-srch">
              <input placeholder="Add a film — search by title…" bind:value={addFilmQuery} />
            </label>
          </div>
          {#if addCandidates.length > 0}
            <ul class="coll-add">
              {#each addCandidates as it (it.id)}
                <li>
                  <span>{it.title} <span class="muted">{it.year ?? ''}</span></span>
                  <button disabled={collBusy} onclick={() => { addFilm(it); addFilmQuery = '' }}>Add</button>
                </li>
              {/each}
            </ul>
          {:else if addFilmQuery.trim()}
            <p class="hint">Nothing else matches “{addFilmQuery}”.</p>
          {/if}
        {:else}
          <div class="lib-toolbar">
            <span class="muted small">
              {collections.length} {collections.length === 1 ? 'collection' : 'collections'} — grouped by the metadata
              provider until you edit one.
            </span>
            <span class="lib-spacer"></span>
            <button class="primary" onclick={newCollection}>New collection</button>
          </div>
          {#if collections.length === 0}
            <div class="empty">No collections yet — they appear once matched films belong to one.</div>
          {:else}
            <div class="tablewrap">
              <table class="lib-table">
                <thead><tr><th></th><th>Name</th><th>Films</th><th>Source</th><th></th></tr></thead>
                <tbody>
                  {#each collections as c (c.key)}
                    <tr>
                      <td class="lib-pcell">
                        {#if c.poster}<img class="lib-poster" src={c.poster} alt="" loading="lazy" />{:else}<span class="lib-poster ph"></span>{/if}
                      </td>
                      <td class="lib-title">{c.name}</td>
                      <td class="muted">{c.count}</td>
                      <td>
                        {#if c.managed}<span class="tag ok">edited</span>{:else}<span class="tag">provider</span>{/if}
                      </td>
                      <td class="actions">
                        <button onclick={() => openCollection(c)}>Manage…</button>
                        {#if c.managed}
                          <button class="lib-btn danger" onclick={() => removeCollection(c)}>
                            {c.tmdbId ? 'Revert' : 'Delete'}
                          </button>
                        {/if}
                      </td>
                    </tr>
                  {/each}
                </tbody>
              </table>
            </div>
          {/if}
        {/if}
      {:else}
      <div class="lib-toolbar">
        <label class="lib-srch">
          <input placeholder="Filter by title or path…" bind:value={itemFilter} />
        </label>
        <label class="lib-chk">
          <input type="checkbox" bind:checked={onlyUnmatched} />
          Only unmatched
        </label>
        <span class="muted small">
          {matchedCount} of {items.length} matched to TMDB
          {#if items.length - matchedCount > 0}· {items.length - matchedCount} without metadata{/if}
        </span>
        <span class="lib-spacer"></span>
        {#if metaRefresh}
          <button class="lib-btn danger" onclick={stopRefreshAll}>Stop</button>
        {:else}
          <button class="primary" disabled={shownItems.length === 0} onclick={startRefreshAll}>{refreshLabel}</button>
        {/if}
      </div>
      {#if metaRefresh}
        <div class="lib-progress">
          <div class="lib-bar" role="progressbar" aria-valuenow={refreshPct} aria-valuemin="0" aria-valuemax="100">
            <div class="lib-bar-fill" style="width: {refreshPct}%"></div>
          </div>
          <span class="muted small">
            {metaRefresh.done} of {metaRefresh.total} · {metaRefresh.matched} matched{#if metaRefresh.failed > 0} · {metaRefresh.failed} without a match{/if}
          </span>
        </div>
      {/if}
      {#if itemsLoading}
        <div class="empty">Loading…</div>
      {:else if shownItems.length === 0}
        <div class="empty">{items.length === 0 ? 'Nothing scanned yet.' : 'Nothing matches that filter.'}</div>
      {:else}
        <div class="tablewrap">
          <table class="lib-table">
            <thead><tr><th></th><th>Title</th><th>Year</th><th>Metadata</th><th>File</th><th></th></tr></thead>
            <tbody>
              {#each shownItems as it (it.id)}
                <tr>
                  <td class="lib-pcell">
                    {#if it.poster}<img class="lib-poster" src={it.poster} alt="" loading="lazy" />{:else}<span class="lib-poster ph"></span>{/if}
                  </td>
                  <td class="lib-title">{it.title}</td>
                  <td class="muted">{it.year ?? '—'}</td>
                  <td>
                    {#if it.tmdbId}
                      <span class="tag ok">TMDB {it.tmdbId}</span>
                    {:else}
                      <span class="tag warn">unmatched</span>
                    {/if}
                    {#if it.lockedFields?.length}
                      <button
                        class="tag lock"
                        title="Edited by hand: {it.lockedFields.join(', ')} — click to hand back to the provider"
                        onclick={() => unlockItem(it)}>edited</button>
                    {/if}
                  </td>
                  <td class="muted small lib-path">{it.path}</td>
                  <td class="actions">
                    <button onclick={() => openEdit(it)}>Edit…</button>
                    <button onclick={() => openMatch(it)}>Fix match…</button>
                    <button disabled={busyItem === it.id} onclick={() => refreshOne(it)}>
                      {busyItem === it.id ? 'Refreshing…' : 'Refresh'}
                    </button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}

      {/if}

      {#if editItem}
        <div class="lib-modal-back" role="presentation" onclick={() => (editItem = null)}></div>
        <div class="lib-modal" role="dialog" aria-modal="true" aria-label="Edit metadata">
          <h2>Edit “{editItem.title}”</h2>
          <p class="hint">
            Every field you change is locked, so scans and metadata refreshes
            leave it alone. Fields you do not touch stay the provider's to
            update.
          </p>
          <form class="form wide" onsubmit={(e) => { e.preventDefault(); saveEdit() }}>
            <label>Title
              <input bind:value={editForm.title} required />
            </label>
            <div class="formrow">
              <label>Year
                <input type="number" inputmode="numeric" bind:value={editForm.year} />
              </label>
              <label>Certification
                <input bind:value={editForm.certification} placeholder="15, PG-13…" />
              </label>
              <label>Rating
                <input type="number" step="0.1" min="0" max="10" bind:value={editForm.rating} />
              </label>
            </div>
            <label>Tagline
              <input bind:value={editForm.tagline} />
            </label>
            <label>Synopsis
              <textarea rows="4" bind:value={editForm.overview}></textarea>
            </label>
            <label>Genres <span class="hint">comma separated</span>
              <input
                value={(editForm.genres ?? []).join(', ')}
                oninput={(e) => (editForm.genres = (e.currentTarget as HTMLInputElement).value.split(',').map((g) => g.trim()).filter(Boolean))}
              />
            </label>
            <div class="formrow">
              <label>Poster URL
                <input bind:value={editForm.poster} placeholder="/artwork/… or https://…" />
              </label>
              <label>Backdrop URL
                <input bind:value={editForm.backdrop} placeholder="/artwork/… or https://…" />
              </label>
            </div>
            <div class="formrow">
              <button class="primary" type="submit" disabled={editBusy}>{editBusy ? 'Saving…' : 'Save'}</button>
              <button type="button" onclick={() => (editItem = null)}>Cancel</button>
            </div>
          </form>
        </div>
      {/if}

      {#if matchFor}
        <div class="lib-modal-back" role="presentation" onclick={() => (matchFor = null)}></div>
        <div class="lib-modal" role="dialog" aria-modal="true" aria-label="Fix match">
          <h2>Fix match</h2>
          <p class="muted small">{matchFor.path}</p>
          <form class="lib-matchbar" onsubmit={(e) => { e.preventDefault(); runMatchSearch() }}>
            <input bind:value={matchQuery} placeholder="Search TMDB by title…" />
            <button class="primary" type="submit" disabled={matchBusy}>{matchBusy ? 'Searching…' : 'Search'}</button>
          </form>
          {#if matchErr}<div class="err">{matchErr}</div>{/if}
          <ul class="lib-cands">
            {#each matchResults as c (c.tmdbId)}
              <li>
                <button class="lib-cand" disabled={matchBusy} onclick={() => chooseMatch(c)}>
                  {#if c.poster}<img class="lib-poster" src={c.poster} alt="" loading="lazy" />{:else}<span class="lib-poster ph"></span>{/if}
                  <span class="lib-cand-body">
                    <strong>{c.title}</strong> <span class="muted">{c.year ?? ''}</span>
                    {#if c.overview}<span class="muted small lib-cand-ov">{c.overview}</span>{/if}
                  </span>
                </button>
              </li>
            {/each}
          </ul>
          <div class="lib-modal-foot"><button class="lib-btn" onclick={() => (matchFor = null)}>Close</button></div>
        </div>
      {/if}

    {:else if page === 'libraries'}
      <h1>Libraries</h1>
      <div class="bar">
        <button class="primary" onclick={() => (showAdd = !showAdd)}>{showAdd ? 'Cancel' : '+ Add library'}</button>
      </div>
      {#if showAdd}
        <form class="panel form" onsubmit={(e) => { e.preventDefault(); submitLibrary() }}>
          <label>Name <input bind:value={newName} placeholder="Movies" required /></label>
          <label>Media type
            <select bind:value={newType}>
              {#each mediaTypes as t (t)}<option value={t}>{t}</option>{/each}
            </select>
          </label>
          <label>
            Paths
            {#each newPaths as _, i (i)}
              <span class="pathrow">
                <input bind:value={newPaths[i]} placeholder="/media/movies" required={i === 0} />
                <button type="button" onclick={() => openBrowser(i)}>Browse…</button>
                {#if newPaths.length > 1}
                  <button type="button" class="danger" onclick={() => (newPaths = newPaths.filter((_, j) => j !== i))} aria-label="Remove this path">✕</button>
                {/if}
              </span>
            {/each}
            <button type="button" class="addpath" onclick={() => (newPaths = [...newPaths, ''])}>+ Add another path</button>
          </label>
          <button class="primary" disabled={saving} type="submit">{saving ? 'Creating…' : 'Create'}</button>
          <p class="hint">One media type per library, chosen now and immutable. Paths can be changed later.</p>
        </form>
      {/if}
      {#if editLib}
        <div class="lib-modal-back" role="presentation" onclick={() => (editLib = null)}></div>
        <div class="lib-modal" role="dialog" aria-modal="true" aria-label="Edit library">
          <h2>Edit “{editLib.name}”</h2>
          <form class="form wide" onsubmit={(e) => { e.preventDefault(); saveLibraryEdit() }}>
            <label>Name <input bind:value={editName} required /></label>
            <label>
              Paths
              {#each editPaths as _, i (i)}
                <span class="pathrow">
                  <input bind:value={editPaths[i]} placeholder="/media/movies" />
                  <button type="button" onclick={() => openBrowser(i)}>Browse…</button>
                  {#if editPaths.length > 1}
                    <button type="button" class="danger" onclick={() => (editPaths = editPaths.filter((_, j) => j !== i))} aria-label="Remove this path">✕</button>
                  {/if}
                </span>
              {/each}
              <button type="button" class="addpath" onclick={() => (editPaths = [...editPaths, ''])}>+ Add another path</button>
            </label>
            <p class="hint">
              The media type cannot be changed — everything already scanned is keyed to it.
              Removing a path does not delete its items here; the next scan prunes whatever it
              no longer finds.
            </p>
            <div class="formrow">
              <button class="primary" type="submit" disabled={libEditBusy}>{libEditBusy ? 'Saving…' : 'Save'}</button>
              <button type="button" onclick={() => (editLib = null)}>Cancel</button>
            </div>
          </form>
        </div>
      {/if}
      {#if libraries.length === 0}
        <div class="empty">No libraries yet.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Name</th><th>Type</th><th>Paths</th><th>Items</th><th></th></tr></thead>
            <tbody>
              {#each libraries as lib (lib.id)}
                <tr>
                  <td>{lib.name}</td>
                  <td><span class="tag">{lib.mediaType}</span></td>
                  <td class="muted">{lib.paths.join(', ')}</td>
                  <td>{lib.itemCount}</td>
                  <td class="actions">
                    <button onclick={() => { openManage(lib); syncURL() }}>Manage</button>
                    <button onclick={() => startEditLibrary(lib)}>Edit…</button>
                    <button onclick={() => startScan(lib)}>Scan</button>
                    <button class="danger" onclick={() => removeLibrary(lib)}>Delete</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {:else if page === 'users'}
      <div class="titlerow">
        <h1>Users</h1>
        <button class="primary" onclick={newUser}>+ Add user</button>
      </div>
      {#if editing}
        <form class="panel form wide" onsubmit={saveUser}>
          {#if !editing.id}
            <label>Username <input bind:value={editing.username} required autocomplete="off" /></label>
          {/if}
          <label>Display name <input bind:value={editing.displayName} placeholder={editing.username || ''} /></label>
          <label>{editing.id ? 'New password (leave blank to keep)' : editing.role === 'admin' ? 'Password' : 'Password (optional when a PIN is set)'}
            <input type="password" bind:value={editing.password} minlength="8" required={!editing.id && (editing.role === 'admin' || !editing.pin)} autocomplete="new-password" />
          </label>
          <label>{editing.id ? `Quick sign-in PIN (${editing.hasPin ? 'set — enter a new one to change' : 'not set'})` : 'Quick sign-in PIN (optional, 4–6 digits)'}
            <input type="password" inputmode="numeric" pattern="[0-9]{4,6}" maxlength="6" bind:value={editing.pin} autocomplete="off" placeholder="Household sign-in: tap name, enter PIN" />
          </label>
          {#if editing.id && editing.hasPin}
            <label class="check"><input type="checkbox" bind:checked={editing.clearPin} /> Remove PIN</label>
          {/if}
          <label>Role
            <select bind:value={editing.role}><option value="user">user</option><option value="admin">admin</option></select>
          </label>
          {#if editing.role !== 'admin'}
            <fieldset class="grants">
              <legend>Libraries this user can see</legend>
              {#each libraries as lib (lib.id)}
                <label class="check"><input type="checkbox" checked={editing.libraryIds?.includes(lib.id)} onchange={() => toggleGrant(lib.id)} /> {lib.name} <span class="muted small">({lib.mediaType})</span></label>
              {/each}
              {#if libraries.length === 0}<span class="muted small">No libraries yet.</span>{/if}
            </fieldset>
          {:else}
            <p class="hint">Admins see every library and can manage the server.</p>
          {/if}
          {#if editing.id && editing.id !== me?.id}
            <label class="check"><input type="checkbox" bind:checked={editing.disabled} /> Disabled (cannot sign in)</label>
          {/if}
          {#if userErr}<div class="error slim">{userErr}</div>{/if}
          <div class="formrow">
            <button class="primary" type="submit" disabled={userSaving}>{userSaving ? 'Saving…' : editing.id ? 'Save' : 'Create'}</button>
            <button type="button" onclick={() => (editing = null)}>Cancel</button>
          </div>
        </form>
      {/if}
      <div class="tablewrap">
        <table>
          <thead><tr><th>User</th><th>Role</th><th>Libraries</th><th>Created</th><th></th></tr></thead>
          <tbody>
            {#each users as u (u.id)}
              <tr>
                <td>
                  <div>{u.displayName}{u.disabled ? ' · disabled' : ''}</div>
                  <div class="muted small">{u.username}{u.id === me?.id ? ' (you)' : ''}</div>
                </td>
                <td><span class="tag" class:ok={u.role === 'admin'}>{u.role}</span>{#if u.hasPin}<span class="tag pin">PIN</span>{/if}{#if !u.hasPassword}<span class="muted small"> no password</span>{/if}</td>
                <td class="muted">{u.role === 'admin' ? 'all' : u.libraryIds.map(libName).join(', ') || 'none'}</td>
                <td class="muted">{u.createdAt.slice(0, 10)}</td>
                <td class="actions">
                  <button onclick={() => editUser(u)}>Edit</button>
                  {#if u.id !== me?.id}<button class="danger" onclick={() => removeUser(u)}>Delete</button>{/if}
                </td>
              </tr>
            {/each}
          </tbody>
        </table>
      </div>
    {:else if page === 'activity'}
      <div class="titlerow">
        <h1>Activity</h1>
        <button class="refresh" onclick={refresh} disabled={refreshing}>{refreshing ? 'Refreshing…' : 'Refresh'}</button>
      </div>
      <h2>Now playing</h2>
      {#if sessions.length === 0}
        <div class="empty slim">Nothing is playing right now.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Title</th><th>Client</th><th>Playback</th><th>Progress</th><th>Started</th><th></th></tr></thead>
            <tbody>
              {#each sessions as s (s.id)}
                <tr>
                  <td>
                    <div>{s.title}</div>
                    {#if s.subtitle}<div class="muted small">{s.subtitle}</div>{/if}
                  </td>
                  <td>
                    <div>{s.client.browser}{s.client.os ? ` · ${s.client.os}` : ''}</div>
                    <div class="muted small">{s.client.ip} · {s.userId}</div>
                  </td>
                  <td>
                    <span class="tag">{s.mode === 'hls' ? `transcode · ${s.transcode?.quality?.label ?? s.quality ?? ''}` : 'direct play'}</span>
                    {#if s.transcode}<div class="muted small">{s.transcode.numSegments} segments ready</div>{/if}
                  </td>
                  <td>
                    <div>{clock(s.positionSecs)}{s.durationSecs ? ` / ${clock(s.durationSecs)}` : ''}{s.paused ? ' · paused' : ''}</div>
                    {#if s.durationSecs}
                      <div class="scanbar"><div class="scanbar-fill" style="width: {Math.min(100, (s.positionSecs / s.durationSecs) * 100)}%"></div></div>
                    {/if}
                  </td>
                  <td class="muted">{since(s.startedAt)}</td>
                  <td class="actions">
                    {#if s.sessionId}
                      <button class="danger" onclick={() => stopTranscode(s.sessionId!)}>Stop transcode</button>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      <h2>Scans</h2>
      {#if scans.length === 0}
        <div class="empty slim">No scans yet — trigger one from the Libraries page.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Library</th><th>Status</th><th>Files</th><th>Items</th><th>Started</th><th></th></tr></thead>
            <tbody>
              {#each scans as s (s.id)}
                <tr>
                  <td>{libName(s.libraryId)}</td>
                  <td>
                    <span class="tag" class:ok={s.status === 'completed'} class:bad={s.status === 'failed'}>{s.status}</span>
                    {#if s.error}<div class="muted small">{s.error}</div>{/if}
                    {#if pct(s) !== null}
                      <div class="scanbar"><div class="scanbar-fill" style="width: {pct(s)}%"></div></div>
                    {/if}
                  </td>
                  <td>{s.filesSeen}{#if s.status === 'running' && scanTotals.get(s.id)}<span class="muted"> / {scanTotals.get(s.id)}</span>{/if}</td>
                  <td>{s.itemsCreated}</td>
                  <td class="muted">{s.startedAt}</td>
                  <td class="actions">
                    {#if s.status === 'running'}
                      <button class="danger" disabled={cancelling.includes(s.id)} onclick={() => requestCancel(s.id)}>{cancelling.includes(s.id) ? 'Cancelling…' : 'Cancel'}</button>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {:else if page === 'logs'}
      <div class="titlerow">
        <h1>Logs</h1>
        <button class="refresh" onclick={() => loadLogs()} disabled={logsLoading}>{logsLoading ? 'Refreshing…' : 'Refresh'}</button>
      </div>
      <div class="filters">
        {#each logFilters as f (f.id)}
          <button class="chip" class:sel={logFilter === f.id} onclick={() => setLogFilter(f.id)}>{f.label}</button>
        {/each}
      </div>
      {#if logs.length === 0}
        <div class="empty slim">Nothing logged yet.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>When</th><th>Who</th><th>What</th><th>Target</th><th>Detail</th><th>Client</th></tr></thead>
            <tbody>
              {#each logs as e (e.id)}
                <tr>
                  <td class="muted nowrap">{when(e.at)}</td>
                  <td>{e.userId}</td>
                  <td><span class="tag">{actionLabel(e.action)}</span></td>
                  <td>
                    {#if e.targetTitle}<div>{e.targetTitle}</div>{/if}
                    {#if e.targetKind}<div class="muted small">{e.targetKind}</div>{/if}
                  </td>
                  <td class="muted">{e.detail ?? ''}</td>
                  <td class="muted small">{e.client ?? ''}{e.clientIp ? ` · ${e.clientIp}` : ''}</td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
        {#if logs.length % 100 === 0}
          <div class="bar top"><button class="refresh" onclick={() => loadLogs(true)} disabled={logsLoading}>Load more</button></div>
        {/if}
      {/if}
    {:else}
      <h1>Settings</h1>
      <div class="panel">
        <div class="setting">
          <span>Sign-in screen <span class="muted small">— what everyone sees before signing in</span></span>
          <div class="theme-picker">
            <button class="chip" class:sel={settings?.loginMode === 'picker'} onclick={() => setLoginMode('picker')} title="Every user shown as a tile; pick one and enter its PIN or password">Choose a user</button>
            <button class="chip" class:sel={settings?.loginMode === 'form'} onclick={() => setLoginMode('form')} title="A username box and a password-or-PIN box; no names are revealed">Username + password / PIN</button>
          </div>
        </div>
        <div class="setting">
          <span>
            Hardware acceleration
            <span class="muted small">
              — use the GPU for transcoding when one is available.
              {#if settings}
                Detected: <strong>{settings.hwAccelStatus}</strong>{#if settings.hwAccelStatus === 'none'} (transcodes run on the CPU){/if}
              {/if}
            </span>
          </span>
          <div class="theme-picker">
            <button class="chip" class:sel={settings?.hwAccel === true} onclick={() => setHWAccel(true)} title="Use the GPU when one is usable, falling back to the CPU">Enabled</button>
            <button class="chip" class:sel={settings?.hwAccel === false} onclick={() => setHWAccel(false)} title="Always transcode on the CPU">Disabled</button>
          </div>
        </div>
        <div class="setting">
          <span>
            Certification region
            <span class="muted small">
              — whose content rating to show. Classifications are issued per
              country by different boards, so a BBFC “15” and an FSK “16” are
              different judgements rather than two spellings. Falls back to the
              US rating where a title is unclassified in your region. Applies to
              titles scanned or refreshed from now on.
            </span>
          </span>
          <select
            class="setting-select"
            value={settings?.certRegion ?? 'GB'}
            onchange={(e) => setCertRegion((e.currentTarget as HTMLSelectElement).value)}
            aria-label="Certification region"
          >
            {#each settings?.certRegions ?? [] as r (r)}
              <option value={r}>{regionName(r)}</option>
            {/each}
          </select>
        </div>
        <div class="setting">
          <span>Theme</span>
          <div class="theme-picker">
            {#each themes as t (t.id)}
              <button class="chip" class:sel={theme === t.id} onclick={() => setTheme(t.id)}>{t.name}</button>
            {/each}
          </div>
        </div>
      </div>
      <div class="empty">
        Everything else (ports, paths, metadata keys) lives in <code>cineva.yaml</code> or
        <code>CINEVA_*</code> variables and needs a restart.
      </div>
    {/if}
  </main>
</div>

<style>
  .shell { display: flex; min-height: 100vh; flex-direction: column; }

  aside {
    background: var(--panel);
    border-bottom: 1px solid var(--border);
    padding: 0.9rem 1rem;
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.75rem;
  }
  .logo {
    font-weight: 700;
    font-size: 1.15rem;
    letter-spacing: -0.02em;
    background: linear-gradient(120deg, var(--accent), var(--accent-2));
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }
  .logo span { font-weight: 400; }
  /* Scoped to the sidebar. A bare `nav` here reaches the breadcrumbs and
     the library tab bar too, silently giving them this one's flex layout,
     gap and button padding — which is how the media-query version of this
     rule broke the breadcrumbs once already. */
  aside nav { display: flex; gap: 0.25rem; overflow-x: auto; }
  aside nav button {
    padding: 0.45rem 0.8rem;
    border-radius: 8px;
    color: var(--muted);
    white-space: nowrap;
  }
  aside nav button.active { background: var(--bg); color: var(--text); }
  .back { margin-left: auto; color: var(--muted); font-size: 0.85rem; text-decoration: none; }
  .back:hover { color: var(--text); }

  /* One width for every admin screen. The library tables are dense — long
     paths, several columns — so the cap is generous; narrow content inside
     (forms, charts) sets its own width rather than the page changing shape
     from screen to screen, which is what made the admin feel inconsistent. */
  main { flex: 1; padding: 1.25rem; width: 100%; max-width: min(110rem, 100%); margin: 0 auto; }
  h1 { font-size: 1.35rem; margin-bottom: 1rem; }
  h2 { font-size: 0.95rem; color: var(--muted); margin: 1.25rem 0 0.6rem; }
  .empty.slim { padding: 1rem 1.25rem; }

  .error {
    background: color-mix(in srgb, var(--err) 15%, transparent);
    border: 1px solid var(--err);
    border-radius: 8px;
    padding: 0.6rem 1rem;
    margin-bottom: 1rem;
    display: flex;
    justify-content: space-between;
    gap: 1rem;
  }

  .cards { display: grid; grid-template-columns: repeat(auto-fit, minmax(14rem, 1fr)); gap: 1rem; }
  .card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.1rem 1.25rem;
  }
  .k { color: var(--muted); font-size: 0.85rem; }
  .v { font-size: 1.5rem; font-weight: 600; margin-top: 0.25rem; color: var(--ok); }
  .v.err { color: var(--err); }
  .card:not(:first-child) .v { color: var(--text); }
  .sub { color: var(--muted); font-size: 0.8rem; margin-top: 0.25rem; }
  .disks { list-style: none; display: flex; flex-direction: column; gap: 0.9rem; margin-top: 0.75rem; }
  .disk-head { display: flex; justify-content: space-between; gap: 1rem; flex-wrap: wrap; margin-bottom: 0.35rem; }
  .disk-libs { font-weight: 500; }
  .meter { height: 8px; border-radius: 999px; background: var(--bg); overflow: hidden; }
  .meter-fill { height: 100%; border-radius: 999px; background: var(--chart-1); transition: width 0.6s ease; }
  .meter-fill.warn { background: var(--chart-3); }
  .meter-fill.crit { background: var(--err); }
  .disk-nums { display: flex; justify-content: space-between; gap: 1rem; font-size: 0.85rem; margin-top: 0.35rem; flex-wrap: wrap; }
  .nowrap { white-space: nowrap; }
  .bar.top { margin-top: 1rem; }
  .filters { display: flex; flex-wrap: wrap; gap: 0.4rem; align-items: center; margin: 1.25rem 0 0.75rem; }
  .filters .gap { margin-left: 0.75rem; }
  .graphs { display: grid; grid-template-columns: 1fr; gap: 1rem; }
  .graphs .card .sub { margin-bottom: 0.75rem; }
  .graphs .card .v { color: var(--text); }
  @media (min-width: 900px) {
    .graphs { grid-template-columns: 1fr 1fr; }
    .graphs .wide { grid-column: 1 / -1; }
  }

  .bar { margin-bottom: 1rem; }
  .titlerow { display: flex; align-items: center; justify-content: space-between; gap: 1rem; }
  .titlerow h1 { margin-bottom: 1rem; }
  .refresh { border: 1px solid var(--border); border-radius: 8px; padding: 0.4rem 0.8rem; color: var(--text); margin-bottom: 1rem; }
  .refresh:hover { border-color: var(--accent); }
  .refresh:disabled { opacity: 0.5; }
  .primary {
    background: var(--accent);
    color: white;
    padding: 0.5rem 1rem;
    border-radius: 8px;
  }
  .primary:disabled { opacity: 0.45; cursor: not-allowed; }

  .panel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem 1.25rem;
    margin-bottom: 1rem;
  }
  .form { display: flex; flex-direction: column; gap: 0.75rem; max-width: 28rem; }
  .form label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: var(--muted); }
  .form input, .form select, .form textarea {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.5rem 0.7rem;
    font: inherit;
  }
  .form textarea { resize: vertical; min-height: 4.5rem; font-family: inherit; }
  .hint { color: var(--muted); font-size: 0.8rem; }
  .form.wide { max-width: 34rem; }
  .grants { border: 1px solid var(--border); border-radius: 8px; padding: 0.6rem 0.8rem; display: flex; flex-direction: column; gap: 0.35rem; }
  .grants legend { color: var(--muted); font-size: 0.8rem; padding: 0 0.3rem; }
  .check { flex-direction: row !important; align-items: center; gap: 0.5rem !important; color: var(--text) !important; }
  .formrow { display: flex; gap: 0.6rem; }
  .formrow button:not(.primary) { color: var(--muted); }
  .who { color: var(--muted); font-size: 0.85rem; display: flex; gap: 0.6rem; align-items: center; }
  .who button { color: var(--accent-2); }
  .gate { position: fixed; inset: 0; z-index: 100; display: grid; place-items: center; background: var(--bg); padding: 1rem; }
  .gate form { width: min(22rem, 100%); background: var(--panel); border: 1px solid var(--border); border-radius: 14px; padding: 1.6rem; display: flex; flex-direction: column; gap: 0.7rem; }
  .gate h1 { margin-bottom: 0.2rem; }
  .gate p { color: var(--muted); font-size: 0.85rem; }
  .gate input { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 0.6rem 0.75rem; font: inherit; }
  .gate .err { color: var(--err); font-size: 0.85rem; }
  .gate .primary { padding: 0.65rem; }
  .pathrow { display: flex; gap: 0.5rem; }
  .pathrow input { flex: 1; min-width: 0; }
  .pathrow button {
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0 0.8rem;
    color: var(--text);
    white-space: nowrap;
  }
  .pathrow button:hover { border-color: var(--accent); }

  /* Folder picker */
  .modal-bg {
    position: fixed; inset: 0; z-index: 50;
    background: color-mix(in srgb, var(--bg) 70%, transparent);
    display: grid; place-items: center; padding: 1rem;
  }
  .modal {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    width: min(34rem, 100%);
    max-height: 80vh;
    display: flex; flex-direction: column;
  }
  .modal-head, .modal-foot {
    display: flex; justify-content: space-between; align-items: center; gap: 1rem;
    padding: 0.8rem 1rem;
  }
  .modal-head { border-bottom: 1px solid var(--border); }
  .modal-foot { border-top: 1px solid var(--border); }
  .modal-foot button { padding: 0.45rem 0.9rem; border-radius: 8px; }
  .modal-foot button:not(.primary) { color: var(--muted); }
  .hidden-toggle { font-size: 0.8rem; color: var(--muted); display: flex; gap: 0.4rem; align-items: center; }
  .crumbs { display: flex; flex-wrap: wrap; align-items: center; gap: 0.1rem; padding: 0.5rem 1rem; font-size: 0.85rem; }
  .crumb { color: var(--accent-2); padding: 0.1rem 0.25rem; border-radius: 4px; }
  .crumb:hover { background: var(--bg); }
  .sep { color: var(--muted); }
  .dirs { list-style: none; overflow-y: auto; flex: 1; min-height: 12rem; padding: 0.25rem 0.5rem; }
  .dirs li button {
    width: 100%; text-align: left; padding: 0.45rem 0.6rem; border-radius: 6px;
  }
  .dirs li button:hover { background: var(--bg); }
  .dirs li.muted { padding: 0.6rem; }
  .error.slim { margin: 0.5rem 1rem; padding: 0.4rem 0.7rem; }

  /* ---- Library contents management ---- */
  /* The admin styles controls per context — there is no global input or
     button rule — so this view supplies its own rather than inheriting
     browser defaults. */
  .lib-toolbar {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.6rem;
    margin-bottom: 0.9rem;
  }
  .lib-toolbar input:not([type='checkbox']),
  .lib-matchbar input {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.45rem 0.7rem;
    font: inherit;
  }
  .lib-btn {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.45rem 0.9rem;
    font: inherit;
  }
  .lib-btn:hover { border-color: var(--accent); }
  .lib-btn.danger { color: var(--err); border-color: color-mix(in srgb, var(--err) 45%, var(--border)); }
  .lib-modal-foot { display: flex; justify-content: flex-end; margin-top: 0.8rem; }
  .lib-spacer { flex: 1; }
  .lib-progress { display: flex; align-items: center; gap: 0.7rem; margin-bottom: 0.7rem; }
  .lib-bar { flex: 1; height: 6px; border-radius: 3px; background: var(--bg); overflow: hidden; }
  .lib-bar-fill { height: 100%; background: var(--accent); transition: width 0.2s linear; }

  .lib-crumbs {
    display: flex;
    flex-direction: row;
    align-items: center;
    gap: 0.4rem;
    margin-bottom: 0.5rem;
    font-size: 0.85rem;
  }
  .lib-link { border: 0; background: none; color: var(--accent); font: inherit; cursor: pointer; padding: 0; }
  .lib-srch input { min-width: 16rem; }
  .lib-srch { display: flex; }
  .lib-chk { display: flex; align-items: center; gap: 0.35rem; font-size: 0.85rem; white-space: nowrap; }
  /* Paths are long; let the column take the slack and ellipsise. */
  /* Everything but the path hugs its content; the path absorbs the slack
     and ellipsises, so widening the page widens the useful column. */
  .lib-table { table-layout: auto; width: 100%; }
  .lib-table th, .lib-table td { white-space: nowrap; }
  .lib-table .lib-path { width: 100%; max-width: 0; overflow: hidden; text-overflow: ellipsis; }
  .lib-table .lib-title { white-space: normal; min-width: 12rem; }
  .lib-poster {
    width: 2rem;
    height: 3rem;
    object-fit: cover;
    border-radius: 3px;
    display: block;
    background: var(--bg);
    border: 1px solid var(--border);
  }
  .lib-poster.ph { opacity: 0.4; }
  .lib-pcell { width: 2rem; }
  .tag.ok { background: color-mix(in srgb, var(--ok) 18%, transparent); color: var(--ok); }
  .tag.warn { background: color-mix(in srgb, var(--err) 16%, transparent); color: var(--err); }

  .lib-modal-back {
    position: fixed;
    inset: 0;
    background: rgb(0 0 0 / 0.55);
    z-index: 20;
  }
  .lib-modal {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(38rem, calc(100vw - 2rem));
    max-height: min(42rem, calc(100vh - 3rem));
    overflow-y: auto;
    padding: 1rem 1.1rem;
    border-radius: 12px;
    border: 1px solid var(--border);
    background: var(--panel);
    z-index: 21;
  }
  .lib-matchbar { display: flex; gap: 0.5rem; margin: 0.6rem 0; }
  .lib-matchbar input { flex: 1; }
  .lib-cands { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.3rem; }
  .lib-cand {
    display: flex;
    gap: 0.6rem;
    width: 100%;
    text-align: left;
    padding: 0.4rem;
    border: 1px solid transparent;
    border-radius: 8px;
    background: none;
    color: inherit;
    font: inherit;
    cursor: pointer;
  }
  .lib-cand:hover { background: var(--bg); border-color: var(--border); }
  .lib-cand-body { display: flex; flex-direction: column; gap: 0.15rem; min-width: 0; }
  /* Two lines of synopsis is enough to tell two films of the same name apart. */
  .lib-cand-ov {
    display: -webkit-box;
    -webkit-line-clamp: 2;
    line-clamp: 2;
    -webkit-box-orient: vertical;
    overflow: hidden;
  }

  .tablewrap { overflow-x: auto; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; }
  table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
  th, td { text-align: left; padding: 0.7rem 1rem; border-bottom: 1px solid var(--border); }
  th { color: var(--muted); font-weight: 500; font-size: 0.8rem; }
  tr:last-child td { border-bottom: none; }
  /* td stays a table cell — flex here shoves the borders outside the table */
  .actions { white-space: nowrap; }
  .actions button + button { margin-left: 0.5rem; }
  .actions button { color: var(--accent-2); }
  .actions .danger { color: var(--err); }
  .tag {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.15rem 0.6rem;
    font-size: 0.78rem;
    white-space: nowrap;
    display: inline-block;
  }
  .tag.ok { color: var(--ok); border-color: var(--ok); }
  .tag.bad { color: var(--err); border-color: var(--err); }
  .tag.pin { margin-left: 0.3rem; color: var(--accent-2); }
  .scanbar {
    margin-top: 0.4rem;
    height: 4px;
    border-radius: 999px;
    background: var(--bg);
    overflow: hidden;
    min-width: 6rem;
  }
  .scanbar-fill {
    height: 100%;
    background: var(--accent);
    border-radius: 999px;
    transition: width 0.6s ease;
  }
  .small { font-size: 0.78rem; }

  .setting + .setting { margin-top: 0.9rem; padding-top: 0.9rem; border-top: 1px solid var(--border); }
  .setting {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
  }
  .theme-picker { display: flex; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end; }
  /* Matches .form select, which is scoped to forms and so does not reach
     the settings rows. */
  /* Manage screen tabs. Mirrors the client's sub-tabs so the two UIs read
     the same way. */
  .lib-tabs {
    display: flex;
    gap: 0.25rem;
    border-bottom: 1px solid var(--border);
    margin: 0.75rem 0 1rem;
    overflow-x: auto;
    /* Explicit, and not optional: when overflow-x is anything but visible,
       a visible overflow-y computes to auto. The tabs' -1px bottom margin
       then overflows the box by exactly one pixel and the browser renders a
       vertical scrollbar for it. */
    overflow-y: hidden;
    scrollbar-width: none;
  }
  .lib-tabs::-webkit-scrollbar { display: none; }
  .lib-tab {
    padding: 0.5rem 0.9rem;
    color: var(--muted);
    border-bottom: 2px solid transparent;
    /* Pulls the active tab's 2px indicator over the container's 1px border
       so they read as one line. This is what overflows vertically, hence
       the overflow-y above. */
    margin-bottom: -1px;
    white-space: nowrap;
  }
  /* Drawn inside, since overflow-y: hidden would clip an outset ring. */
  .lib-tab:focus-visible { outline: 2px solid var(--accent-2); outline-offset: -2px; }
  .lib-tab:hover { color: var(--text); }
  .lib-tab.on { color: var(--text); border-bottom-color: var(--accent); font-weight: 600; }

  .coll-add {
    list-style: none;
    margin: 0 0 1rem;
    padding: 0;
    border: 1px solid var(--border);
    border-radius: 8px;
    max-width: 34rem;
  }
  .coll-add li {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.45rem 0.7rem;
  }
  .coll-add li + li { border-top: 1px solid var(--border); }

  /* A tag that is also a button, for clearing an item's manual locks. */
  .tag.lock { cursor: pointer; border-color: var(--accent); color: var(--accent-2); }

  /* A path row: field, Browse…, and a remove button when there is more
     than one. */
  .pathrow { display: flex; gap: 0.4rem; align-items: center; }
  .pathrow input { flex: 1; min-width: 0; }
  .addpath {
    align-self: flex-start;
    color: var(--accent-2);
    font-size: 0.85rem;
    padding: 0.2rem 0;
  }

  .setting-select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.4rem 0.6rem;
    font: inherit;
    flex: none;
  }
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

  .empty {
    background: var(--panel);
    border: 1px dashed var(--border);
    border-radius: 12px;
    padding: 2rem 1.5rem;
    color: var(--muted);
  }
  code { color: var(--accent-2); }
  .muted { color: var(--muted); }

  @media (min-width: 900px) {
    .shell { flex-direction: row; }
    aside {
      flex-direction: column;
      align-items: stretch;
      width: 14rem;
      border-bottom: none;
      border-right: 1px solid var(--border);
      padding: 1.25rem 1rem;
    }
    aside nav { flex-direction: column; overflow: visible; margin-top: 1rem; }
    aside nav button { text-align: left; }
    .back { margin: auto 0 0; padding-top: 1rem; }
  }
</style>
