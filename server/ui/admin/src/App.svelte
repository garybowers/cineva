<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, createLibrary, deleteLibrary, scanLibrary, listScans,
    queueFullScan, cancelQueuedFullScan,
    listGroups, createGroup, updateGroup, deleteGroup, getLibraryAccess, setLibraryAccess,
    type UserGroup, type LibraryAccess,
    listIntegrations, createIntegration, updateIntegration, deleteIntegration,
    type Integration, type IntegrationKind, type IntegrationRequest,
    integrationStatus, setIntegrationOptions, type IntegrationStatus, type PathMap,
    getHomeAssistant, putHomeAssistant, deleteHomeAssistant, type HAConnection,
    listRequests, approveRequest, declineRequest, deleteRequest, type MediaRequest, type RequestLevel, type RequestState,
    pauseSegments, resumeSegments, runSegments, previewStatus, type PreviewStatus,
    cancelScan, segmentStatus, type IntroStatus, type IntroResult,
    redetectLibrary, redetectSelection, segmentReports, resolveSegmentReport, type SegmentReport, type RedetectResult, listSessions, stopSession, stopPlayback, subscribeEvents, browseDirs, systemStats, playStats, playTotals, diskStats, auditLog,
    libraryManage, type ManageFilter, itemMatches, applyMatch, refreshItem, type Item, type MatchCandidate,
    updateLibrary,
    refreshLibrary, cancelRefresh, refreshStatus, type RefreshProgress,
    libraryCollections, collectionDetail, createCollection, renameCollection,
    setCollectionItems, deleteCollection, updateItem, clearItemLocks,
    type Collection, type ItemEdit,
    authStatus, login, logout, listUsers, createUser, updateUser, deleteUser, UNAUTHORISED, getSettings, putSettings,
    uploadUserAvatar, deleteUserAvatar, userDevices, revokeUserDevice, AVATAR_COLOURS, type AvatarColour, type Device,
    type ServerSettings,
    type Health, type Library, type Scan, type PlaySession, type DirListing, type SystemSample, type PlayDay, type PlayTotals, type DiskVolume, type AuditEntry, type User, type UserRequest,
  } from './lib/api'
  import LineChart from './LineChart.svelte'
  import AvatarCrop from './AvatarCrop.svelte'
  import BarChart from './BarChart.svelte'
  import Modal from './Modal.svelte'
  import AccessPicker from './AccessPicker.svelte'
  import SmartHome from './SmartHome.svelte'
  import Devices from './Devices.svelte'
  import Notifications from './Notifications.svelte'
  import ImportWizard from './ImportWizard.svelte'
  import type { ImportRun } from './lib/api'
  import { Import as ImportIcon } from '@lucide/svelte'
  import {
    Plus, Tv, Film, Music, House, ChevronRight, RefreshCw, SlidersHorizontal, Pencil, Unplug, TriangleAlert, X,
  } from '@lucide/svelte'
  import Defaults from './Defaults.svelte'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'

  /** Write a browsed path back into whichever row opened the browser. */
  function setBrowsePath(p: string) {
    setBrowsePaths([p])
  }

  /**
   * Write one or more browsed paths back, starting at the row that opened
   * the browser. Extra selections become rows after it, in the order they
   * were ticked, so a library of several folders is one trip through the
   * picker rather than one trip each.
   *
   * Paths the library already holds are dropped rather than duplicated, and
   * blank rows are cleared out once there is something real to show.
   */
  function setBrowsePaths(paths: string[]) {
    const list = editLib ? editPaths : newPaths
    const next = [...list]
    const existing = new Set(next.map((p) => p.trim()).filter(Boolean))
    // The row that opened the browser is being replaced, so whatever it held
    // must not block a selection of the same path.
    existing.delete((next[browseTarget] ?? '').trim())
    const fresh = paths.filter((p) => !existing.has(p.trim()))
    if (fresh.length === 0) {
      return
    }
    next[browseTarget] = fresh[0]
    if (fresh.length > 1) next.splice(browseTarget + 1, 0, ...fresh.slice(1))
    const trimmed = next.filter((p) => p.trim() !== '')
    const final = trimmed.length > 0 ? trimmed : ['']
    if (editLib) editPaths = final
    else newPaths = final
  }

  type Page = 'dashboard' | 'libraries' | 'users' | 'devices' | 'defaults' | 'requests' | 'activity' | 'logs' | 'integrations' | 'notifications' | 'smarthome' | 'settings'
  const pages: { id: Page; label: string }[] = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'libraries', label: 'Libraries' },
    { id: 'users', label: 'Users' },
    { id: 'devices', label: 'Devices' },
    { id: 'defaults', label: 'Defaults' },
    { id: 'requests', label: 'Requests' },
    { id: 'activity', label: 'Activity' },
    { id: 'logs', label: 'Logs' },
    { id: 'integrations', label: 'Integrations' },
    { id: 'notifications', label: 'Notifications' },
    { id: 'smarthome', label: 'Smart home' },
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

  async function setRequestDefault(level: RequestLevel) {
    const before = settings
    if (settings) settings = { ...settings, requestDefaultLevel: level } // optimistic
    try {
      settings = await putSettings({ requestDefaultLevel: level })
    } catch (err) {
      settings = before
      error = err instanceof Error ? err.message : String(err)
    }
  }

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

  async function setDetectionRate(mb: number) {
    const before = settings
    if (settings) settings = { ...settings, detectionRateMB: mb } // optimistic
    try {
      settings = await putSettings({ detectionRateMB: mb })
    } catch (err) {
      settings = before
      error = err instanceof Error ? err.message : String(err)
    }
  }

  // When detection may run. The window's times are saved when a field is
  // left, not on every keystroke.
  async function setDetectionSchedule(patch: Partial<ServerSettings>) {
    const before = settings
    if (settings) settings = { ...settings, ...patch } // optimistic
    try {
      settings = await putSettings(patch)
    } catch (err) {
      settings = before
      error = err instanceof Error ? err.message : String(err)
    }
  }
  async function analyseNow() {
    try {
      intro = await runSegments()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  async function toggleDetectionPause() {
    try {
      intro = bgPaused ? await resumeSegments() : await pauseSegments()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }
  const pauseText: Record<string, string> = {
    manual: 'Paused — resume when you are ready.',
    playing: 'Paused while something is playing; carries on a minute after it stops.',
    scanning: 'Paused while a library scans; carries on a minute after it finishes.',
    detecting: 'Waiting for intro detection to finish; one reads at a time.',
    schedule: 'Waiting for its window — it only reads between the times set in Settings. "Analyse now" starts it anyway.',
    'on-demand': 'Set to run only when started — press "Analyse now" to begin.',
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
  // Import from Plex or Jellyfin: the dialogue, and the latest run from the event stream.
  let importOpen = $state(false)
  let importRun: ImportRun | null = $state(null)
  const loadUsers = () => listUsers().then((u) => (users = u)).catch(() => {})
  let editing: (UserRequest & { id?: string; clearPin?: boolean; hasPin?: boolean }) | null = $state(null) // null = closed; no id = new
  let userErr = $state('')
  let userSaving = $state(false)
  function newUser() {
    editing = { username: '', displayName: '', password: '', pin: '', role: 'user', disabled: false, requestLevel: settings?.requestDefaultLevel ?? 'approval', segmentEditor: false }
    userErr = ''
  }
  function editUser(u: User) {
    editing = { id: u.id, displayName: u.displayName, password: '', pin: '', hasPin: u.hasPin, clearPin: false, role: u.role, disabled: u.disabled, requestLevel: u.requestLevel, segmentEditor: !!u.segmentEditor }
    userErr = ''
    editDevices = []
    userDevices(u.id).then((d) => (editDevices = d)).catch(() => {})
  }
  // The user being edited as it stands, for the picture, which changes at
  // once rather than waiting for Save.
  const editingUser = $derived(editing?.id ? users.find((u) => u.id === editing?.id) : undefined)
  let editDevices: Device[] = $state([])
  let avatarBusy = $state(false)
  function replaceUser(u: User) {
    users = users.map((x) => (x.id === u.id ? u : x))
  }
  // The photo being framed before it goes up (AvatarCrop), and whose.
  let crop: { file: File; user: User } | null = $state(null)
  function pickUserAvatar(e: Event, u: User) {
    const input = e.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = ''
    if (!file) return
    userErr = ''
    crop = { file, user: u }
  }
  async function sendUserAvatar(picture: Blob) {
    if (!crop) return
    avatarBusy = true
    userErr = ''
    try {
      replaceUser(await uploadUserAvatar(crop.user.id, picture))
    } catch (err) {
      userErr = String(err instanceof Error ? err.message : err)
    } finally {
      avatarBusy = false
      crop = null
    }
  }
  async function dropUserAvatar(u: User) {
    try {
      replaceUser(await deleteUserAvatar(u.id))
    } catch (err) {
      userErr = String(err instanceof Error ? err.message : err)
    }
  }
  async function chooseUserColour(u: User, colour: AvatarColour | '') {
    try {
      replaceUser(await updateUser(u.id, { avatarColour: colour }))
    } catch (err) {
      userErr = String(err instanceof Error ? err.message : err)
    }
  }
  async function signOutUserDevice(u: User, d: Device) {
    const before = editDevices
    editDevices = editDevices.filter((x) => x.id !== d.id)
    try {
      await revokeUserDevice(u.id, d.id)
    } catch (err) {
      editDevices = before
      userErr = String(err instanceof Error ? err.message : err)
    }
  }
  // ---- Groups ----
  // Named sets of users, for restricting a library without listing every
  // account. Kept with the users, since that is who they group.
  let groups: UserGroup[] = $state([])
  const loadGroups = () => listGroups().then((g) => (groups = g)).catch(() => {})
  let editingGroup: { id?: string; name: string; memberIds: string[] } | null = $state(null)
  let groupErr = $state('')
  let groupSaving = $state(false)
  function newGroup() {
    editingGroup = { name: '', memberIds: [] }
    groupErr = ''
  }
  function editGroup(g: UserGroup) {
    editingGroup = { id: g.id, name: g.name, memberIds: [...g.memberIds] }
    groupErr = ''
  }
  function toggleMember(id: string) {
    if (!editingGroup) return
    const set = new Set(editingGroup.memberIds)
    if (set.has(id)) set.delete(id); else set.add(id)
    editingGroup.memberIds = [...set]
  }
  async function saveGroup(e: Event) {
    e.preventDefault()
    if (!editingGroup) return
    groupSaving = true
    groupErr = ''
    try {
      const g = editingGroup
      if (g.id) await updateGroup(g.id, g.name, g.memberIds)
      else await createGroup(g.name, g.memberIds)
      editingGroup = null
      loadGroups()
    } catch (err) {
      groupErr = String(err instanceof Error ? err.message : err)
    } finally {
      groupSaving = false
    }
  }
  async function removeGroup(g: UserGroup) {
    if (!confirm(`Delete the group "${g.name}"? Libraries restricted to it will no longer be visible to its members.`)) return
    try {
      await deleteGroup(g.id)
      loadGroups()
    } catch (err) {
      error = String(err instanceof Error ? err.message : err)
    }
  }
  // ---- Integrations ----
  // Each kind Cineva can connect, in the order the Add dialogue offers them;
  // a new kind is an entry here alongside its server side. A connection is
  // known by its name, unique across them all, so two of one kind (Sonarr
  // and Sonarr 4K) are told apart here and when approving a request.
  //
  // Saving registers Cineva's webhook in the instance, which tests it by
  // calling back before it answers — hence "Connecting…" rather than a
  // plain save, and the instance's own error when it cannot reach us.
  const integrationKinds: Record<IntegrationKind, { label: string; blurb: string; url: string; icon: typeof Tv }> = {
    sonarr: {
      label: 'Sonarr', icon: Tv, url: 'http://sonarr.lan:8989',
      blurb: 'Series. Episodes it imports are scanned in within seconds, and requests for series are sent to it.',
    },
    radarr: {
      label: 'Radarr', icon: Film, url: 'http://radarr.lan:7878',
      blurb: 'Films. Films it imports are scanned in within seconds, and requests for films are sent to it.',
    },
    lidarr: {
      label: 'Lidarr', icon: Music, url: 'http://lidarr.lan:8686',
      blurb: 'Music. Albums it imports are scanned into the music libraries within seconds.',
    },
  }
  // Requests go to Sonarr and Radarr; Lidarr only brings imports in, so it
  // has paths to map but no profile or folder to choose.
  const takesRequests = (k: IntegrationKind) => k !== 'lidarr'
  const kindLabel = (k: IntegrationKind) => integrationKinds[k].label
  let integrations: Integration[] = $state([])
  // The Add dialogue's first step, choosing the kind; editingInt is the
  // second, and editing an existing connection.
  let pickingKind = $state(false)
  let editingInt: (IntegrationRequest & { id?: string; kind: IntegrationKind; title: string }) | null = $state(null)
  let disconnecting: Integration | null = $state(null)
  let intErr = $state('')
  let intNote = $state('')
  let intSaving = $state(false)
  async function loadIntegrations() {
    try {
      integrations = await listIntegrations()
    } catch {
      /* the page shows what it last had */
    }
  }
  // The kind's own name if no connection has it yet, else "Sonarr 2" and on.
  function freeName(label: string): string {
    const taken = new Set(integrations.map((i) => i.name.toLowerCase()))
    for (let n = 1; ; n++) {
      const name = n === 1 ? label : `${label} ${n}`
      if (!taken.has(name.toLowerCase())) return name
    }
  }
  function newIntegration(kind: IntegrationKind) {
    pickingKind = false
    editingInt = { kind, title: `Connect ${kindLabel(kind)}`, name: freeName(kindLabel(kind)), url: '', apiKey: '', callbackUrl: location.origin }
    intErr = ''
    intNote = ''
  }
  function editIntegration(i: Integration) {
    editingInt = { id: i.id, kind: i.kind, title: `Edit ${i.name}`, name: i.name, url: i.url, apiKey: '', callbackUrl: i.callbackUrl }
    intErr = ''
    intNote = ''
  }
  async function saveIntegration(e: Event) {
    e.preventDefault()
    if (!editingInt) return
    intSaving = true
    intErr = ''
    try {
      const { id, kind, name, url, apiKey, callbackUrl } = editingInt
      const body = { kind, name, url, apiKey, callbackUrl }
      if (id) await updateIntegration(id, body)
      else await createIntegration(body)
      editingInt = null
      loadIntegrations()
    } catch (err) {
      intErr = String(err instanceof Error ? err.message : err)
    } finally {
      intSaving = false
    }
  }
  async function removeIntegration(i: Integration) {
    intNote = ''
    intSaving = true
    try {
      const res = await deleteIntegration(i.id)
      if (res?.warning) intNote = res.warning
      loadIntegrations()
    } catch (err) {
      intNote = String(err instanceof Error ? err.message : err)
    } finally {
      intSaving = false
      disconnecting = null
    }
  }

  // ---- Home Assistant ----
  // A single connection, unlike Sonarr and Radarr: GET answers configured
  // false when there is none, and PUT both connects and changes it. Saving
  // opens Home Assistant's websocket and signs in before anything is kept,
  // so a wrong address or token comes back with Home Assistant's reason.
  // What it exposes, the rooms and the modes live on the Smart home page.
  const haKind = {
    label: 'Home Assistant', icon: House, url: 'http://homeassistant.local:8123',
    blurb: 'Rooms, controls, cameras and modes. People work the lights and amplifier of the room they watch in, look at the cameras, and start a mode that sets the room up and plays.',
  }
  let ha: HAConnection | null = $state(null)
  let editingHA: { name: string; url: string; token: string; playbackEvents: boolean; existing: boolean } | null = $state(null)
  let disconnectingHA = $state(false)
  let haErr = $state('')
  let haSaving = $state(false)
  let smartHome: ReturnType<typeof SmartHome> | undefined = $state()
  let devicesPage: ReturnType<typeof Devices> | undefined = $state()
  const loadHA = () => getHomeAssistant().then((c) => (ha = c)).catch(() => {})
  function connectHA() {
    pickingKind = false
    editingHA = { name: 'Home Assistant', url: '', token: '', playbackEvents: false, existing: false }
    haErr = ''
  }
  function editHA() {
    if (!ha?.configured) return
    editingHA = { name: ha.name ?? 'Home Assistant', url: ha.url ?? '', token: '', playbackEvents: !!ha.playbackEvents, existing: true }
    haErr = ''
  }
  async function saveHA(e: Event) {
    e.preventDefault()
    if (!editingHA) return
    haSaving = true
    haErr = ''
    try {
      const { name, url, token, playbackEvents } = editingHA
      ha = await putHomeAssistant({ name: name.trim() || 'Home Assistant', url: url.trim(), token: token.trim() || undefined, playbackEvents })
      editingHA = null
    } catch (err) {
      haErr = String(err instanceof Error ? err.message : err)
    } finally {
      haSaving = false
    }
  }
  async function removeHA() {
    intNote = ''
    haSaving = true
    try {
      await deleteHomeAssistant()
      ha = { configured: false }
    } catch (err) {
      intNote = String(err instanceof Error ? err.message : err)
    } finally {
      haSaving = false
      disconnectingHA = false
    }
  }

  // A connection checked live: version, health, and what requests can be
  // made with. Run for every card when the page opens, and by Test.
  let intStatus: Record<string, IntegrationStatus | 'checking'> = $state({})
  const unreachable = (error: string): IntegrationStatus => ({
    reachable: false, error, health: [], qualityProfiles: [], rootFolders: [], suggestedPathMaps: [],
  })
  async function testIntegration(i: Integration) {
    intStatus = { ...intStatus, [i.id]: 'checking' }
    let st: IntegrationStatus
    try {
      st = await integrationStatus(i.id)
    } catch (err) {
      st = unreachable(String(err instanceof Error ? err.message : err))
    }
    intStatus = { ...intStatus, [i.id]: st }
  }
  const checked = (id: string): IntegrationStatus | null => {
    const st = intStatus[id]
    return st && st !== 'checking' ? st : null
  }
  let intTested = false
  $effect(() => {
    if (page !== 'integrations' || intTested || integrations.length === 0) return
    intTested = true
    for (const i of integrations) void testIntegration(i)
  })

  // What requests are made with, and how the instance's paths map onto
  // Cineva's. Saved without touching the webhook.
  let optsFor: { id: string; qualityProfileId: number; rootFolder: string; pathMaps: PathMap[] } | null = $state(null)
  let optsErr = $state('')
  let optsSaving = $state(false)
  const optsConn = $derived(optsFor ? integrations.find((i) => i.id === optsFor?.id) ?? null : null)
  function editOptions(i: Integration) {
    optsFor = { id: i.id, qualityProfileId: i.qualityProfileId, rootFolder: i.rootFolder, pathMaps: i.pathMaps.map((m) => ({ ...m })) }
    optsErr = ''
    if (!checked(i.id)) void testIntegration(i)
  }
  function addSuggested(maps: PathMap[]) {
    if (!optsFor) return
    const have = new Set(optsFor.pathMaps.map((m) => m.from))
    optsFor.pathMaps = [...optsFor.pathMaps, ...maps.filter((m) => !have.has(m.from)).map((m) => ({ ...m }))]
  }
  async function saveOptions(e: Event) {
    e.preventDefault()
    if (!optsFor) return
    optsSaving = true
    optsErr = ''
    try {
      const { id, ...body } = optsFor
      await setIntegrationOptions(id, body)
      optsFor = null
      loadIntegrations()
    } catch (err) {
      optsErr = String(err instanceof Error ? err.message : err)
    } finally {
      optsSaving = false
    }
  }
  // SQLite's datetime('now') is UTC without a zone; say so before parsing.
  const utc = (s: string) => (s.includes('T') ? s : s.replace(' ', 'T') + 'Z')
  const freeSpace = (bytes: number) =>
    bytes >= 1e12 ? `${(bytes / 1e12).toFixed(1)} TB free` : `${Math.round(bytes / 1e9)} GB free`

  // ---- Requests: titles users asked for ----
  // Waiting ones lead, with Approve and Decline; the rest are history.
  let requestsList: MediaRequest[] = $state([])
  const pendingRequests = $derived(requestsList.filter((r) => r.status === 'requested'))
  const pastRequests = $derived(requestsList.filter((r) => r.status !== 'requested').slice(0, 60))
  let deciding: {
    id: string; mode: 'approve' | 'decline'; note: string
    connectionId: string; qualityProfileId: number; rootFolder: string
  } | null = $state(null)
  let reqErr = $state('')
  let reqBusy = $state(false)
  const requestStatusLabel: Record<RequestState, string> = {
    requested: 'Waiting', approved: 'Approved', downloading: 'Downloading', available: 'Available', declined: 'Declined',
  }
  const levelLabel: Record<RequestLevel, string> = {
    none: 'No requests', approval: 'Needs approval', auto: 'Sent straight away',
  }
  const connsFor = (r: MediaRequest) => integrations.filter((i) => i.kind === (r.mediaType === 'movie' ? 'radarr' : 'sonarr'))
  function scopeLabel(r: MediaRequest): string {
    if (r.mediaType === 'movie') return 'Film'
    const parts: string[] = []
    if (r.seasons.length) parts.push(`${r.seasons.length === 1 ? 'Season' : 'Seasons'} ${r.seasons.join(', ')}`)
    if (r.episodes.length) {
      parts.push(r.episodes.map((e) => `S${String(e.season).padStart(2, '0')}E${String(e.episode).padStart(2, '0')}`).join(', '))
    }
    return parts.length ? parts.join(' · ') : 'Whole series'
  }
  async function loadRequests() {
    try {
      requestsList = await listRequests()
    } catch {
      /* the page shows what it last had */
    }
  }
  function startDecision(r: MediaRequest, mode: 'approve' | 'decline') {
    const conn = connsFor(r).find((c) => c.id === r.connectionId) ?? connsFor(r)[0]
    deciding = {
      id: r.id, mode, note: '',
      connectionId: conn?.id ?? '', qualityProfileId: conn?.qualityProfileId ?? 0, rootFolder: conn?.rootFolder ?? '',
    }
    reqErr = ''
    if (mode === 'approve' && conn && !checked(conn.id)) void testIntegration(conn)
  }
  function pickDecisionConn(id: string) {
    if (!deciding) return
    const conn = integrations.find((c) => c.id === id)
    deciding = { ...deciding, connectionId: id, qualityProfileId: conn?.qualityProfileId ?? 0, rootFolder: conn?.rootFolder ?? '' }
    if (conn && !checked(conn.id)) void testIntegration(conn)
  }
  async function submitDecision(e: Event) {
    e.preventDefault()
    if (!deciding) return
    reqBusy = true
    reqErr = ''
    try {
      if (deciding.mode === 'approve') {
        await approveRequest(deciding.id, {
          connectionId: deciding.connectionId || undefined,
          qualityProfileId: deciding.qualityProfileId || undefined,
          rootFolder: deciding.rootFolder || undefined,
          note: deciding.note.trim() || undefined,
        })
      } else {
        await declineRequest(deciding.id, deciding.note.trim())
      }
      deciding = null
    } catch (err) {
      reqErr = String(err instanceof Error ? err.message : err)
    } finally {
      reqBusy = false
      loadRequests()
    }
  }
  async function removeRequest(r: MediaRequest) {
    if (!confirm(`Remove the request for ${r.title}? Nothing is changed in ${r.mediaType === 'movie' ? 'Radarr' : 'Sonarr'}.`)) return
    try {
      await deleteRequest(r.id)
      loadRequests()
    } catch (err) {
      error = String(err instanceof Error ? err.message : err)
    }
  }

  const userName = (id: string) => {
    const u = users.find((x) => x.id === id)
    return u ? u.displayName || u.username : id.slice(0, 8)
  }

  // ---- Library access ----
  // Open to everyone until restricted to chosen users and groups.
  let accessLib: Library | null = $state(null)
  let access: LibraryAccess = $state({ restricted: false, userIds: [], groupIds: [] })
  let accessErr = $state('')
  let accessBusy = $state(false)
  async function openAccess(lib: Library) {
    accessErr = ''
    accessLib = lib
    loadUsers()
    loadGroups()
    try {
      access = await getLibraryAccess(lib.id)
    } catch (err) {
      accessErr = String(err instanceof Error ? err.message : err)
    }
  }
  async function saveAccess() {
    if (!accessLib) return
    accessBusy = true
    accessErr = ''
    try {
      await setLibraryAccess(accessLib.id, access)
      accessLib = null
      listLibraries().then((l) => (libraries = l)).catch(() => {})
    } catch (err) {
      accessErr = String(err instanceof Error ? err.message : err)
    } finally {
      accessBusy = false
    }
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

  // Intro detection runs on its own after TV scans and at startup. Its
  // status arrives over the event stream; the clock only ticks so elapsed
  // and remaining time move between events.
  let intro = $state<IntroStatus | null>(null)
  // Seek-bar previews are built the same way, under the same schedule and
  // pause; one line says how they are getting on.
  let previews = $state<PreviewStatus | null>(null)
  const bgPaused = $derived(
    (intro?.paused && intro.pauseReason === 'manual') || (previews?.paused && previews.pauseReason === 'manual'),
  )
  const scheduleHeld = (r?: string) => r === 'schedule' || r === 'on-demand'
  const previewWait: Record<string, string> = {
    manual: 'Paused',
    playing: 'Waiting while something plays',
    scanning: 'Waiting while a library scans',
    detecting: 'Waiting for intro detection',
    schedule: 'Waiting for the window',
    'on-demand': 'Waiting to be started',
  }
  const previewLine = $derived.by(() => {
    const p = previews
    if (!p) return ''
    const failed = p.failed ? ` · ${p.failed} unreadable` : ''
    if (p.running && p.paused && p.pauseReason) return `${previewWait[p.pauseReason] ?? 'Paused'} · ${p.waiting} waiting`
    if (p.running && p.current) return `Building ${p.current} · ${p.done} done, ${p.waiting} waiting${failed}`
    if (p.running) return `${p.done} done, ${p.waiting} waiting${failed}`
    if (p.paused) return 'Paused'
    if (p.last) return `Finished ${since(p.last.finishedAt)} · ${p.last.done} made${p.last.failed ? ` · ${p.last.failed} unreadable` : ''}`
    return 'Up to date — built after each scan, newest titles first.'
  })
  let now = $state(Date.now())
  $effect(() => {
    if (!intro?.running) return
    const id = setInterval(() => (now = Date.now()), 5000)
    return () => clearInterval(id)
  })
  // "1 h 40 min", "12 min", "40 s".
  const span = (secs: number) => {
    secs = Math.max(0, Math.round(secs))
    if (secs < 60) return `${secs} s`
    const m = Math.round(secs / 60)
    return m < 60 ? `${m} min` : `${Math.floor(m / 60)} h ${m % 60} min`
  }
  const introElapsed = $derived(intro?.startedAt ? (now - Date.parse(intro.startedAt)) / 1000 : 0)
  // Extrapolated from the episodes done so far; withheld until there are
  // enough of them for the rate to mean something.
  const introLeft = $derived(
    intro && intro.episodesDone >= 5 && intro.episodesTotal > intro.episodesDone
      ? (introElapsed / intro.episodesDone) * (intro.episodesTotal - intro.episodesDone)
      : null,
  )
  const phaseLabel = { reading: 'reading audio', comparing: 'comparing', saving: 'saving' }
  const epCode = (r: IntroResult) => `S${String(r.season).padStart(2, '0')}E${String(r.episode).padStart(2, '0')}`
  // Films count alongside episodes: one check each, for their credits.
  const introDone = $derived(intro ? intro.episodesDone + (intro.filmsDone ?? 0) : 0)
  const introTotal = $derived(intro ? intro.episodesTotal + (intro.filmsTotal ?? 0) : 0)

  // ---- Detecting again: a library, or a selection in its Manage list ----

  /**
   * Run a re-detection, asking first whether times set by hand should be
   * replaced too, and say what came of it. send is called again with
   * includeLocked when the answer is yes.
   */
  async function redetectAsking(send: (includeLocked: boolean) => Promise<RedetectResult>): Promise<string> {
    let res = await send(false)
    let replaced = false
    if (res.locked > 0 && confirm(`Also replace ${res.locked} ${res.locked === 1 ? 'title' : 'titles'} whose times were set by hand?`)) {
      res = await send(true)
      replaced = true
    }
    const kept = res.locked > 0 && !replaced ? ` · ${res.locked} set by hand kept` : ''
    return `${res.items} ${res.items === 1 ? 'title' : 'titles'} sent to detection${kept}.`
  }

  let detectNote: { libraryId: string; text: string } | null = $state(null)
  async function detectLibrary(lib: Library) {
    error = ''
    try {
      const text = await redetectAsking((includeLocked) => redetectLibrary(lib.id, { includeLocked }))
      detectNote = { libraryId: lib.id, text }
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // ---- Reports of wrong intro and credits times ----
  let reports: SegmentReport[] = $state([])
  const loadReports = () => segmentReports().then((r) => (reports = r)).catch(() => {})
  let resolving: string[] = $state([])
  async function resolveReports(ids: string[]) {
    resolving = [...resolving, ...ids]
    try {
      for (const id of ids) await resolveSegmentReport(id)
      reports = reports.filter((r) => !ids.includes(r.id))
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      resolving = resolving.filter((id) => !ids.includes(id))
    }
  }
  // Seasons with three or more open reports: better detected again than
  // corrected episode by episode.
  const crowdedSeasons = $derived.by(() => {
    const out: { seasonId: string; label: string; ids: string[] }[] = []
    for (const r of reports) {
      if (!r.seasonId || r.seasonOpen < 3 || out.some((x) => x.seasonId === r.seasonId)) continue
      out.push({
        seasonId: r.seasonId,
        label: `${r.seriesTitle || 'Unknown series'} · Season ${r.seasonNum ?? 0}`,
        ids: reports.filter((x) => x.seasonId === r.seasonId).map((x) => x.id),
      })
    }
    return out
  })
  async function redetectReported(season: { seasonId: string; ids: string[] }) {
    error = ''
    try {
      await redetectAsking((includeLocked) => redetectSelection([season.seasonId], { includeLocked }))
      await resolveReports(season.ids)
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }
  const reportLabel = (r: SegmentReport) =>
    r.itemKind === 'episode'
      ? `${r.seriesTitle || 'Unknown series'} S${String(r.seasonNum ?? 0).padStart(2, '0')}E${String(r.episodeNum ?? 0).padStart(2, '0')}`
      : r.title

  async function startScan(lib: Library, full = false) {
    error = ''
    go('activity') // move there now; progress arrives over the stream
    try {
      const s = await scanLibrary(lib.id, full)
      if (!scans.some((x) => x.id === s.id)) scans = [s, ...scans]
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // A full rescan reads every file, so it can wait for the background work
  // window instead of starting now; the "libraries" event refreshes the flag.
  async function toggleQueuedScan(lib: Library) {
    error = ''
    const queued = !lib.fullScanQueued
    try {
      await (queued ? queueFullScan(lib.id) : cancelQueuedFullScan(lib.id))
      libraries = libraries.map((l) => (l.id === lib.id ? { ...l, fullScanQueued: queued } : l))
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // ---- Library contents: browse what was scanned, fix what mismatched ----
  let manage: Library | null = $state(null)   // library being managed, null = list
  // The Manage list is paged and filtered on the server: a library can hold
  // tens of thousands of items, far too many to send or render at once.
  const ITEM_PAGE_SIZE = 100
  let items: Item[] = $state([])              // the page on screen
  let itemsLoading = $state(false)
  let itemFilter = $state('')
  let onlyUnmatched = $state(false)
  let itemPage = $state(1)
  let itemTotal = $state(0)                   // items matching the filter, every page
  let matchedCount = $state(0)                // the whole library's, for "X of N matched"
  let allCount = $state(0)
  // The filter the rows on screen were loaded with. The box may hold text
  // still waiting out its debounce, and refresh must act on what the count
  // beside it describes.
  let shownFilter: ManageFilter = $state({})
  let itemsTop: HTMLElement | undefined = $state()
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

  // Rows ticked for a bulk action, on the page on screen; a new page or
  // filter starts with none.
  let selected: string[] = $state([])
  let bulkNote = $state('')
  const canDetect = $derived(!!manage && (manage.mediaType === 'tv' || manage.mediaType === 'movies') && intro?.enabled !== false)
  function toggleSelected(id: string) {
    selected = selected.includes(id) ? selected.filter((x) => x !== id) : [...selected, id]
  }
  async function detectSelected() {
    error = ''
    bulkNote = ''
    const ids = selected
    try {
      bulkNote = await redetectAsking((includeLocked) => redetectSelection(ids, { includeLocked }))
      selected = []
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  // Manual metadata edit.
  let editItem: Item | null = $state(null)
  let editForm: ItemEdit = $state({})
  let editBusy = $state(false)

  async function openManage(lib: Library) {
    manage = lib
    manageTab = 'films'
    itemFilter = ''
    onlyUnmatched = false
    clearTimeout(filterTimer)
    itemPage = 1
    items = []
    itemTotal = matchedCount = allCount = 0
    shownFilter = {}
    itemsLoading = true
    metaRefresh = null
    editingColl = null
    collections = []
    selected = []
    bulkNote = ''
    try {
      await loadItems()
      const st = await refreshStatus(lib.id)
      if (st.running) metaRefresh = st
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      itemsLoading = false
    }
    loadCollections()
  }

  // Each load is numbered so a slow response for an old filter or page
  // cannot land on top of a newer one.
  let itemsSeq = 0

  /** Load the current page for the current filter. quiet keeps a background
   *  reload (an event) from putting up an error over the admin's work. */
  async function loadItems(quiet = false) {
    if (!manage) return
    const lib = manage
    const seq = ++itemsSeq
    const f: ManageFilter = { q: itemFilter.trim(), unmatched: onlyUnmatched }
    try {
      let r = await libraryManage(lib.id, f, itemPage, ITEM_PAGE_SIZE)
      // The page emptied under us — items pruned by a scan, or the last
      // unmatched ones on it fixed — so step back to the last page there is.
      if (r.items.length === 0 && r.total > 0 && itemPage > 1) {
        r = await libraryManage(lib.id, f, Math.ceil(r.total / r.size), ITEM_PAGE_SIZE)
      }
      if (seq !== itemsSeq || manage?.id !== lib.id) return
      // Keep the ticks only on rows still on screen.
      selected = selected.filter((id) => r.items.some((it) => it.id === id))
      items = r.items
      itemPage = r.page
      itemTotal = r.total
      matchedCount = r.matched
      allCount = r.all
      shownFilter = f
    } catch (e) {
      if (!quiet && seq === itemsSeq) error = String(e instanceof Error ? e.message : e)
    }
  }

  // Typing narrows the list once the typist pauses, rather than a request
  // per keystroke; any change of filter starts again from the first page.
  let filterTimer: ReturnType<typeof setTimeout> | undefined
  function onItemFilterInput() {
    clearTimeout(filterTimer)
    filterTimer = setTimeout(() => {
      itemPage = 1
      loadItems()
    }, 250)
  }

  function onUnmatchedChange() {
    clearTimeout(filterTimer)
    itemPage = 1
    loadItems()
  }

  const pageCount = $derived(Math.max(1, Math.ceil(itemTotal / ITEM_PAGE_SIZE)))

  async function goItemPage(n: number) {
    itemPage = Math.min(Math.max(n, 1), pageCount)
    await loadItems()
    // From the pager under the table, bring the new page's first row into view.
    itemsTop?.scrollIntoView({ block: 'nearest' })
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

  // Search results for the add-a-film box. The Manage list holds only one
  // page, so the search goes to the server rather than through `items`.
  let addResults: Item[] = $state([])
  let addTimer: ReturnType<typeof setTimeout> | undefined
  function onAddFilmInput() {
    clearTimeout(addTimer)
    const q = addFilmQuery.trim()
    if (!q || !manage) {
      addResults = []
      return
    }
    const lib = manage
    addTimer = setTimeout(async () => {
      try {
        const r = await libraryManage(lib.id, { q }, 1, 50)
        if (addFilmQuery.trim() === q) addResults = r.items
      } catch (e) {
        error = String(e instanceof Error ? e.message : e)
      }
    }, 250)
  }

  /** Films not already in the collection, narrowed by the search box. */
  const addCandidates = $derived.by(() => {
    if (!addFilmQuery.trim()) return []
    const have = new Set(collFilms.map((i) => i.id))
    return addResults.filter((i) => !have.has(i.id) && !i.versionOf).slice(0, 8)
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
      // A new title can move the row to another page, or out of the filter.
      loadItems()
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

  // True when the list is narrowed, in which case refresh acts on everything
  // the filter matches — every page of it — rather than the whole library.
  const filtered = $derived(!!shownFilter.unmatched || !!shownFilter.q)

  const refreshLabel = $derived(
    !filtered
      ? 'Refresh all metadata'
      : `Refresh ${itemTotal} ${shownFilter.unmatched && !shownFilter.q ? 'unmatched' : 'matching'}`,
  )

  async function startRefreshAll() {
    if (!manage) return
    error = ''
    const lib = manage
    // Send the filter rather than ids, so the server resolves every page of
    // matches and the button does exactly what it says; no filter lets the
    // server walk the whole library.
    try {
      metaRefresh = await refreshLibrary(lib.id, filtered ? shownFilter : {})
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
      // The new title may sort elsewhere, and a fixed row leaves "only unmatched".
      loadItems()
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

  // Ends any playback, transcoding or not. The row goes immediately; a
  // direct-play client stops on its next heartbeat, so the stream itself
  // can persist for a few seconds after the row disappears.
  async function endPlayback(s: PlaySession) {
    const before = sessions
    sessions = sessions.filter((x) => x.id !== s.id)
    try {
      await stopPlayback(s.userId, s.itemId)
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
  let totals: PlayTotals | null = $state(null)

  async function loadStats() {
    try {
      samples = await systemStats(sysRange)
      plays = await playStats(playDays)
      totals = await playTotals(playDays)
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
    playTotals(d).then((x) => (totals = x)).catch(() => {})
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
    { id: 'segments.', label: 'Intro & credits' },
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
      'library.delete': 'Deleted library', 'transcode.stop': 'Stopped transcode',
      'segments.set': 'Set intro/credits', 'segments.clear': 'Handed back to detection',
      'segments.redetect': 'Detect again', 'segments.report.resolve': 'Resolved report' })[a] ?? a
  const when = (iso: string) => {
    const d = new Date(iso.includes('T') ? iso : iso.replace(' ', 'T') + 'Z')
    return d.toLocaleString([], { day: 'numeric', month: 'short', hour: '2-digit', minute: '2-digit', second: '2-digit' })
  }

  const playsToday = $derived(plays.length ? plays[plays.length - 1].movies + plays[plays.length - 1].episodes + plays[plays.length - 1].tracks : 0)
  const playsTotal = $derived(plays.reduce((n, d) => n + d.movies + d.episodes + d.tracks, 0))
  // Watch time reads better as hours once there are more than a couple.
  const watchTime = $derived.by(() => {
    const s = totals?.secs ?? 0
    if (s < 60) return null
    const h = Math.floor(s / 3600)
    const m = Math.round((s % 3600) / 60)
    return h > 0 ? `${h}h ${m}m` : `${m}m`
  })
  // Share of plays that reached the end. Only meaningful once something has
  // been measured — history recorded before durations existed counts as
  // neither watched nor completed, so a library with only old rows shows
  // nothing rather than a misleading 0%.
  const completionPct = $derived(
    totals && totals.plays > 0 ? Math.round((totals.completed / totals.plays) * 100) : null,
  )

  // Folder picker for the path field: browses the server's filesystem one
  // level at a time (directories only).
  let browsing = $state(false)
  let listing: DirListing | null = $state(null)
  let browseErr = $state('')
  let showHidden = $state(false)
  // Folders ticked in the picker. An array rather than a Set so the order
  // they were chosen in is the order they are added.
  let picked: string[] = $state([])

  function togglePick(path: string) {
    picked = picked.includes(path) ? picked.filter((p) => p !== path) : [...picked, path]
  }

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
    picked = []
    const cur = (editLib ? editPaths : newPaths)[browseTarget] ?? ''
    const start = cur.trim() && cur.startsWith('/') ? cur.trim() : '/'
    browse(start)
  }

  function chooseFolder() {
    if (listing) setBrowsePath(listing.path)
    browsing = false
  }

  function choosePicked() {
    if (picked.length > 0) setBrowsePaths(picked)
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
    loadGroups()
    loadIntegrations()
    loadHA()
    loadRequests()
    loadSettings()
    segmentStatus().then((s) => (intro = s)).catch(() => {})
    loadReports()
    previewStatus().then((p) => (previews = p)).catch(() => {})
    // Live updates over SSE; the slow poll is only a fallback for missed events.
    const closeEvents = subscribeEvents(
      {
        scan: onScanEvent,
        segments: (data) => (intro = data as IntroStatus | null),
        segmentReports: loadReports,
        previews: (data) => (previews = data as PreviewStatus | null),
        refresh: (data) => {
          const p = data as RefreshProgress | null
          if (!p || !manage || p.libraryId !== manage.id) return
          metaRefresh = p.running ? p : null
          // The run is over: pick up every title it rewrote.
          if (!p.running) loadItems(true)
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
          loadItems(true)
          if (manageTab === 'collections' && !editingColl) loadCollections()
        },
        stats: onSample,
        logs: () => { if (!logsLoading) loadLogs() },
        users: () => { loadUsers(); loadGroups() },
        import: (data) => (importRun = data as ImportRun | null),
        requests: loadRequests,
        integrations: loadIntegrations,
        // States that changed carry entities and only matter to the Smart
        // home page; no data means the connection, exposure, rooms or
        // modes changed.
        homeassistant: (data) => {
          if (Array.isArray((data as { entities?: unknown } | null)?.entities)) {
            smartHome?.onEvent(data)
            return
          }
          loadHA()
          smartHome?.onEvent(data)
          devicesPage?.refresh()
        },
        devices: () => devicesPage?.refresh(),
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
      const pages: Page[] = ['dashboard', 'libraries', 'users', 'devices', 'defaults', 'requests', 'activity', 'logs', 'integrations', 'notifications', 'smarthome', 'settings']
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

<!-- A user's face: their picture, or their initial on the colour they chose
     (a theme token, so each theme draws it in its own palette). -->
{#snippet face(u: User, cls: string)}
  {#if u.avatar}
    <img class={cls} src={u.avatar} alt="" />
  {:else}
    <span class={cls} class:tinted={!!u.avatarColour} style:--face={u.avatarColour ? `var(--${u.avatarColour})` : undefined}>{u.displayName.slice(0, 1).toUpperCase()}</span>
  {/if}
{/snippet}

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
      {#each pages.filter((p) => p.id !== 'smarthome' || ha?.configured || page === 'smarthome') as p (p.id)}
        <button class:active={page === p.id} onclick={() => go(p.id)}>
          {p.label}{#if p.id === 'requests' && pendingRequests.length > 0}<span class="navcount" title="{pendingRequests.length} waiting for a decision">{pendingRequests.length}</span>{/if}
        </button>
      {/each}
    </nav>
    {#if me}<div class="who">{me.displayName} <button onclick={signOut}>Sign out</button></div>{/if}
    <a class="back" href="/api/docs/" target="_blank" rel="noopener">API reference</a>
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
            <li class="dirrow">
              <label class="pick" title="Add {d.name} to this library">
                <input type="checkbox" checked={picked.includes(d.path)} onchange={() => togglePick(d.path)} />
              </label>
              <button type="button" class="dirname" onclick={() => browse(d.path)}>{d.name}</button>
            </li>
          {/each}
          {#if listing && listing.dirs.length === 0}
            <li class="muted small">No subfolders</li>
          {/if}
        </ul>
        <div class="modal-foot">
          <span class="muted small">{listing?.path ?? ''}</span>
          <span>
            <button type="button" onclick={() => (browsing = false)}>Cancel</button>
            {#if picked.length > 0}
              <button type="button" class="primary" onclick={choosePicked}>
                Add {picked.length} folder{picked.length === 1 ? '' : 's'}
              </button>
            {:else}
              <button type="button" class="primary" disabled={!listing} onclick={chooseFolder}>Select this folder</button>
            {/if}
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
          <div class="sub">
            today · {playsTotal} in the last {playDays} days{#if watchTime}
              · {watchTime} watched{/if}{#if completionPct !== null}
              · {completionPct}% finished{/if}
          </div>
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
              <input placeholder="Add a film — search by title…" bind:value={addFilmQuery} oninput={onAddFilmInput} />
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
      <div class="lib-toolbar" bind:this={itemsTop}>
        <label class="lib-srch">
          <input placeholder="Filter by title or path…" bind:value={itemFilter} oninput={onItemFilterInput} />
        </label>
        <label class="lib-chk">
          <input type="checkbox" bind:checked={onlyUnmatched} onchange={onUnmatchedChange} />
          Only unmatched
        </label>
        <span class="muted small">
          {matchedCount} of {allCount} matched to TMDB
          {#if allCount - matchedCount > 0}· {allCount - matchedCount} without metadata{/if}
        </span>
        <span class="lib-spacer"></span>
        {#if metaRefresh}
          <button class="lib-btn danger" onclick={stopRefreshAll}>Stop</button>
        {:else}
          <button class="primary" disabled={itemTotal === 0} onclick={startRefreshAll}>{refreshLabel}</button>
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
      {:else if items.length === 0}
        <div class="empty">{allCount === 0 ? 'Nothing scanned yet.' : 'Nothing matches that filter.'}</div>
      {:else}
        {#snippet pager()}
          {#if pageCount > 1}
            <nav class="lib-pager" aria-label="Pages">
              <button class="lib-btn" disabled={itemPage <= 1} onclick={() => goItemPage(itemPage - 1)}>‹ Prev</button>
              <span class="muted small">Page {itemPage} of {pageCount}</span>
              <button class="lib-btn" disabled={itemPage >= pageCount} onclick={() => goItemPage(itemPage + 1)}>Next ›</button>
            </nav>
          {/if}
        {/snippet}
        {@render pager()}
        {#if canDetect && (selected.length > 0 || bulkNote)}
          <div class="lib-bulk" role="status">
            {#if selected.length > 0}
              <span>{selected.length} selected</span>
              <button class="primary" onclick={detectSelected}>{manage?.mediaType === 'movies' ? 'Detect credits' : 'Detect intro & credits'}</button>
              <button class="lib-btn" onclick={() => (selected = [])}>Clear</button>
            {:else}
              <span class="muted small">{bulkNote} Progress is on the Activity page.</span>
            {/if}
          </div>
        {/if}
        <div class="tablewrap">
          <table class="lib-table">
            <thead><tr>
              {#if canDetect}
                <th class="lib-check">
                  <input type="checkbox" aria-label="Select every row on this page"
                    checked={items.length > 0 && items.every((it) => selected.includes(it.id))}
                    onchange={(e) => (selected = (e.currentTarget as HTMLInputElement).checked ? items.map((it) => it.id) : [])} />
                </th>
              {/if}
              <th></th><th>Title</th><th>Year</th><th>Metadata</th><th>File</th><th></th>
            </tr></thead>
            <tbody>
              {#each items as it (it.id)}
                <tr class:lib-sel={selected.includes(it.id)}>
                  {#if canDetect}
                    <td class="lib-check">
                      <input type="checkbox" aria-label="Select {it.title}" checked={selected.includes(it.id)} onchange={() => toggleSelected(it.id)} />
                    </td>
                  {/if}
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
        {@render pager()}
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
      {#if accessLib}
        <div class="lib-modal-back" role="presentation" onclick={() => (accessLib = null)}></div>
        <div class="lib-modal" role="dialog" aria-modal="true" aria-label="Library access">
          <h2>Who can see “{accessLib.name}”</h2>
          <form class="form wide" onsubmit={(e) => { e.preventDefault(); saveAccess() }}>
            <AccessPicker bind:access {users} {groups} noun="library" />
            <p class="hint">Admins always see every library.</p>
            {#if accessErr}<div class="error slim">{accessErr}</div>{/if}
            <div class="formrow">
              <button class="primary" type="submit" disabled={accessBusy}>{accessBusy ? 'Saving…' : 'Save'}</button>
              <button type="button" onclick={() => (accessLib = null)}>Cancel</button>
            </div>
          </form>
        </div>
      {/if}
      {#if libraries.length === 0}
        <div class="empty">No libraries yet.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Name</th><th>Type</th><th>Paths</th><th>Items</th><th>Access</th><th></th></tr></thead>
            <tbody>
              {#each libraries as lib (lib.id)}
                <tr>
                  <td>
                    {lib.name}
                    {#if lib.storage && !lib.storage.available}
                      <div class="storage-down">Storage unreachable{#if lib.storage.since} (went down {since(lib.storage.since)}){/if}: {lib.storage.path} — {lib.storage.error}</div>
                    {/if}
                    {#if lib.fullScanQueued}
                      <div class="muted small">Full rescan queued for the background work window</div>
                    {/if}
                    {#if detectNote?.libraryId === lib.id}
                      <div class="muted small detectnote">{detectNote.text} Progress is on the Activity page.</div>
                    {/if}
                  </td>
                  <td><span class="tag">{lib.mediaType}</span></td>
                  <td class="muted">{lib.paths.join(', ')}</td>
                  <td>{lib.itemCount}</td>
                  <td>{#if lib.restricted}<span class="tag">restricted</span>{:else}<span class="muted">everyone</span>{/if}</td>
                  <td class="actions">
                    <button onclick={() => { openManage(lib); syncURL() }}>Manage</button>
                    <button onclick={() => startEditLibrary(lib)}>Edit…</button>
                    <button onclick={() => openAccess(lib)}>Access…</button>
                    <button onclick={() => startScan(lib)} title="Read new and changed files">Scan</button>
                    <button onclick={() => startScan(lib, true)} title="Reread and rematch every file — slow, but picks up new metadata">Full rescan</button>
                    <button
                      onclick={() => toggleQueuedScan(lib)}
                      title={lib.fullScanQueued
                        ? 'Take the queued full rescan off the queue'
                        : 'Start a full rescan in the background work window, once nothing is playing'}
                    >{lib.fullScanQueued ? 'Cancel tonight’s rescan' : 'Full rescan tonight'}</button>
                    {#if (lib.mediaType === 'tv' || lib.mediaType === 'movies') && intro?.enabled !== false}
                      <button
                        onclick={() => detectLibrary(lib)}
                        title={lib.mediaType === 'tv'
                          ? 'Detect every episode’s intro and credits again, starting now whatever the schedule says'
                          : 'Read every film’s chapters for its credits again, starting now whatever the schedule says'}
                      >{lib.mediaType === 'tv' ? 'Detect intro & credits' : 'Detect credits'}</button>
                    {/if}
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
        <div class="titleacts">
          <button class="lib-btn ibtn" onclick={() => (importOpen = true)}><ImportIcon size={15} /> Import from Plex or Jellyfin</button>
          <button class="primary" onclick={newUser}>+ Add user</button>
        </div>
      </div>
      {#if importOpen}
        <ImportWizard {users} live={importRun} onclose={() => (importOpen = false)} />
      {/if}
      {#if editing}
        <form class="panel form wide" onsubmit={saveUser}>
          {#if !editing.id}
            <label>Username <input bind:value={editing.username} required autocomplete="off" /></label>
          {/if}
          <label>Display name <input bind:value={editing.displayName} placeholder={editing.username || ''} /></label>
          {#if editingUser}
            {@const u = editingUser}
            <div class="facefield">
              <span class="facelabel">Picture <span class="muted small">— changes straight away</span></span>
              <div class="facerow">
                {@render face(u, 'face big')}
                <div class="faceside">
                  <div class="formrow wrap">
                    <label class="lib-btn upload" class:busy={avatarBusy}>
                      {avatarBusy ? 'Uploading…' : u.avatar ? 'Change photo' : 'Upload a photo'}
                      <input type="file" accept="image/jpeg,image/png,image/gif,image/webp" onchange={(e) => pickUserAvatar(e, u)} disabled={avatarBusy} />
                    </label>
                    {#if u.avatar}<button type="button" class="lib-btn" onclick={() => dropUserAvatar(u)}>Remove photo</button>{/if}
                  </div>
                  {#if !u.avatar}
                    <div class="swatches" role="radiogroup" aria-label="Colour behind the initial">
                      <button type="button" class="swatch" class:sel={!u.avatarColour} role="radio" aria-checked={!u.avatarColour} aria-label="Default" title="Default" onclick={() => chooseUserColour(u, '')}></button>
                      {#each AVATAR_COLOURS as c (c)}
                        <button type="button" class="swatch" class:sel={u.avatarColour === c} role="radio" aria-checked={u.avatarColour === c} aria-label={c} title={c} style:--face="var(--{c})" onclick={() => chooseUserColour(u, c)}></button>
                      {/each}
                    </div>
                  {/if}
                </div>
              </div>
            </div>
          {/if}
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
            <p class="hint">Sees every library unless a library is restricted — set that from a library's Access… button on the Libraries page, or add them to a group.</p>
          {:else}
            <p class="hint">Admins see every library and can manage the server.</p>
          {/if}
          {#if editing.role !== 'admin'}
            <label>Requests
              <select bind:value={editing.requestLevel}>
                <option value="none">{levelLabel.none}</option>
                <option value="approval">{levelLabel.approval}</option>
                <option value="auto">{levelLabel.auto}</option>
              </select>
            </label>
            <p class="hint">What happens when they ask for a film or series that is not in the library: nothing (no request screens at all), a request waiting on the Requests page for you to approve, or sent straight to Sonarr or Radarr.</p>
          {:else}
            <p class="hint">Admins' requests always go straight to Sonarr or Radarr.</p>
          {/if}
          {#if editing.role !== 'admin'}
            <label class="check"><input type="checkbox" bind:checked={editing.segmentEditor} /> Intro &amp; credits editor</label>
            <p class="hint">A trusted editor can correct intro and credits times and start their detection on the titles they can see, without being an admin. Detecting a whole library stays with admins, and the log records who changed what.</p>
          {/if}
          {#if editing.id && editing.id !== me?.id}
            <label class="check"><input type="checkbox" bind:checked={editing.disabled} /> Disabled (cannot sign in)</label>
          {/if}
          {#if editingUser}
            {@const u = editingUser}
            <div class="facefield">
              <span class="facelabel">Signed-in devices <span class="muted small">— sign one out if it is lost or not theirs</span></span>
              {#each editDevices as d (d.id)}
                <div class="devrow">
                  <div class="devtext">
                    <span>{d.name || d.client || 'Unknown browser'}{#if d.current} <span class="tag">you, here</span>{:else if d.online} <span class="tag ok">open now</span>{/if}</span>
                    <span class="muted small">{[d.name ? d.client : '', d.clientIp, `last active ${d.lastSeenAt.slice(0, 16)}`].filter(Boolean).join(' · ')}</span>
                  </div>
                  <button type="button" class="lib-btn" onclick={() => signOutUserDevice(u, d)}>Sign out</button>
                </div>
              {:else}
                <span class="muted small">Not signed in anywhere.</span>
              {/each}
            </div>
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
          <thead><tr><th>User</th><th>Role</th><th>Libraries</th><th>Requests</th><th>Created</th><th></th></tr></thead>
          <tbody>
            {#each users as u (u.id)}
              <tr>
                <td>
                  <div class="userCell">
                    {@render face(u, 'face')}
                    <div>
                      <div>{u.displayName}{u.disabled ? ' · disabled' : ''}</div>
                      <div class="muted small">{u.username}{u.id === me?.id ? ' (you)' : ''}</div>
                    </div>
                  </div>
                </td>
                <td><span class="tag" class:ok={u.role === 'admin'}>{u.role}</span>{#if u.role !== 'admin' && u.segmentEditor}<span class="tag pin" title="Corrects intro and credits times">editor</span>{/if}{#if u.hasPin}<span class="tag pin">PIN</span>{/if}{#if !u.hasPassword}<span class="muted small"> no password</span>{/if}</td>
                <td class="muted">{u.role === 'admin' ? 'all' : u.libraryIds.map(libName).join(', ') || 'none'}</td>
                <td class="muted nowrap">{u.role === 'admin' ? levelLabel.auto : levelLabel[u.requestLevel] ?? u.requestLevel}</td>
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

      <div class="titlerow grouphead">
        <h2>Groups</h2>
        <button onclick={newGroup}>+ Add group</button>
      </div>
      <p class="hint">Groups let you restrict a library to, say, the grown-ups without listing every account.</p>
      {#if editingGroup}
        <form class="panel form wide" onsubmit={saveGroup}>
          <label>Name <input bind:value={editingGroup.name} required placeholder="Grown-ups" /></label>
          <fieldset class="grants">
            <legend>Members</legend>
            {#each users as u (u.id)}
              <label class="check"><input type="checkbox" checked={editingGroup.memberIds.includes(u.id)} onchange={() => toggleMember(u.id)} /> {u.displayName || u.username} <span class="muted small">{u.username}</span></label>
            {/each}
          </fieldset>
          {#if groupErr}<div class="error slim">{groupErr}</div>{/if}
          <div class="formrow">
            <button class="primary" type="submit" disabled={groupSaving}>{groupSaving ? 'Saving…' : editingGroup.id ? 'Save' : 'Create'}</button>
            <button type="button" onclick={() => (editingGroup = null)}>Cancel</button>
          </div>
        </form>
      {/if}
      {#if groups.length > 0}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Group</th><th>Members</th><th></th></tr></thead>
            <tbody>
              {#each groups as g (g.id)}
                <tr>
                  <td>{g.name}</td>
                  <td class="muted">{g.memberIds.map(userName).join(', ') || 'nobody yet'}</td>
                  <td class="actions">
                    <button onclick={() => editGroup(g)}>Edit</button>
                    <button class="danger" onclick={() => removeGroup(g)}>Delete</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {:else if page === 'defaults'}
      <Defaults {themes} />
    {:else if page === 'requests'}
      <div class="titlerow">
        <h1>Requests</h1>
      </div>
      <p class="hint intro">Films and series people asked for. Approving sends a request to Radarr or Sonarr with the connection's quality profile and folder, unless you choose others here; it then shows as downloading when they grab it, and available once a scan finds it.</p>
      {#if settings && !settings.tmdbConfigured}
        <div class="error slim">No TMDB key is configured, so nobody can look up titles outside the library. Set metadata.tmdb.api_key (CINEVA_METADATA_TMDB_API_KEY) and restart.</div>
      {:else if !integrations.some((i) => i.kind === 'radarr') && !integrations.some((i) => i.kind === 'sonarr')}
        <div class="panel muted">Connect Radarr or Sonarr on the <button class="linkbtn" onclick={() => go('integrations')}>Integrations</button> page to let people request what is not in the library.</div>
      {/if}
      {#if reqErr}<div class="error slim">{reqErr}</div>{/if}
      <h2>Waiting for you ({pendingRequests.length})</h2>
      {#if pendingRequests.length === 0}
        <div class="panel muted">Nothing waiting.</div>
      {/if}
      <div class="reqlist">
        {#each pendingRequests as r (r.id)}
          <div class="panel reqcard">
            {#if r.poster}<img class="reqposter" src={r.poster} alt="" loading="lazy" />{:else}<span class="reqposter ph"></span>{/if}
            <div class="reqbody">
              <div class="reqtitle"><strong>{r.title}</strong>{#if r.year}{' '}<span class="muted">({r.year})</span>{/if}</div>
              <div class="muted small">{scopeLabel(r)} · asked by {r.userName || userName(r.userId)} {since(utc(r.createdAt))}</div>
              {#if r.error}<div class="reqerr small">Could not be sent: {r.error}</div>{/if}
              {#if deciding?.id === r.id}
                <form class="form reqform" onsubmit={submitDecision}>
                  {#if deciding.mode === 'approve'}
                    {@const conns = connsFor(r)}
                    {@const st = checked(deciding.connectionId)}
                    {#if conns.length > 1}
                      <label>Send to
                        <select value={deciding.connectionId} onchange={(e) => pickDecisionConn((e.currentTarget as HTMLSelectElement).value)}>
                          {#each conns as c (c.id)}<option value={c.id}>{c.name}</option>{/each}
                        </select>
                      </label>
                    {/if}
                    <label>Quality profile
                      <select bind:value={deciding.qualityProfileId}>
                        <option value={0}>The connection's default</option>
                        {#each st?.qualityProfiles ?? [] as q (q.id)}<option value={q.id}>{q.name}</option>{/each}
                      </select>
                    </label>
                    <label>Folder
                      <select bind:value={deciding.rootFolder}>
                        <option value="">The connection's default</option>
                        {#each st?.rootFolders ?? [] as f (f.id)}<option value={f.path}>{f.path}</option>{/each}
                      </select>
                    </label>
                    {#if intStatus[deciding.connectionId] === 'checking'}<span class="hint">Asking {conns.find((c) => c.id === deciding?.connectionId)?.name ?? 'the instance'} for its profiles…</span>{/if}
                  {/if}
                  <label>Note <span class="muted small">— optional, shown to {r.userName || 'them'}</span>
                    <input bind:value={deciding.note} placeholder={deciding.mode === 'approve' ? 'Enjoy' : 'Already on the shelf'} />
                  </label>
                  <div class="formrow">
                    <button class="primary" type="submit" disabled={reqBusy}>
                      {reqBusy ? (deciding.mode === 'approve' ? 'Sending…' : 'Declining…') : deciding.mode === 'approve' ? 'Approve and send' : 'Decline'}
                    </button>
                    <button type="button" onclick={() => (deciding = null)}>Cancel</button>
                  </div>
                </form>
              {:else}
                <div class="formrow wrap">
                  <button class="primary" onclick={() => startDecision(r, 'approve')}>{r.error ? 'Try again' : 'Approve'}</button>
                  <button onclick={() => startDecision(r, 'decline')}>Decline</button>
                </div>
              {/if}
            </div>
          </div>
        {/each}
      </div>
      {#if pastRequests.length > 0}
        <h2>Recent</h2>
        <div class="tablewrap">
          <table>
            <thead><tr><th>Title</th><th>Asked by</th><th>Status</th><th>When</th><th></th></tr></thead>
            <tbody>
              {#each pastRequests as r (r.id)}
                <tr>
                  <td>
                    <div>{r.title}{#if r.year}{' '}<span class="muted">({r.year})</span>{/if}</div>
                    <div class="muted small">{scopeLabel(r)}{#if r.note}{' · '}“{r.note}”{/if}</div>
                  </td>
                  <td class="muted">{r.userName || userName(r.userId)}</td>
                  <td><span class="tag" class:ok={r.status === 'available'} class:bad={r.status === 'declined'}>{requestStatusLabel[r.status]}</span></td>
                  <td class="muted nowrap">{since(utc(r.updatedAt))}</td>
                  <td class="actions"><button class="danger" onclick={() => removeRequest(r)}>Remove</button></td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
    {:else if page === 'integrations'}
      <div class="titlerow">
        <h1>Integrations</h1>
        <button class="primary ibtn addint" onclick={() => (pickingKind = true)}><Plus size={16} /> Add integration</button>
      </div>
      {#if settings && !settings.tmdbConfigured}
        <div class="error slim">No TMDB key is configured, so requests are off: titles outside the library cannot be looked up. Set metadata.tmdb.api_key (CINEVA_METADATA_TMDB_API_KEY) and restart.</div>
      {/if}
      {#if intNote}<div class="error slim">{intNote}</div>{/if}
      {#if integrations.length === 0 && !ha?.configured}
        <div class="empty slim">Nothing connected yet. Add Sonarr for series, Radarr for films, Lidarr for music, and Home Assistant for the rooms you watch in.</div>
      {/if}
      <div class="intgrid">
        {#each integrations as i (i.id)}
          {@const st = checked(i.id)}
          {@const warnings = st?.health.filter((h) => h.type !== 'ok') ?? []}
          {@const KindIcon = integrationKinds[i.kind].icon}
          <div class="panel intcard">
            <div class="inthead">
              <h3 class="intname">{i.name}</h3>
              <span class="tag kindtag"><KindIcon size={13} /> {kindLabel(i.kind)}</span>
            </div>
            <div class="muted small inturl">{i.url}{#if i.version}{' · '}v{i.version}{/if}</div>
            <div class="intchips">
              {#if intStatus[i.id] === 'checking'}
                <span class="tag">Checking…</span>
              {:else if st}
                {#if st.reachable}<span class="tag ok">Reachable</span>{:else}<span class="tag bad">Unreachable</span>{/if}
              {/if}
              {#if i.notificationId > 0}<span class="tag ok">Webhook registered</span>{:else}<span class="tag bad">No webhook</span>{/if}
            </div>
            {#if st?.error}<div class="small reqerr inturl">{st.error}</div>{/if}
            {#if warnings.length > 0}
              <ul class="intwarns">
                {#each warnings as h (h.source + h.message)}
                  <li class="small" class:bad={h.type === 'error'}><TriangleAlert size={14} /><span>{h.message}</span></li>
                {/each}
              </ul>
            {/if}
            <dl class="intfacts small">
              <dt>Last event</dt>
              <dd>
                {#if i.lastEventAt}
                  {i.lastEvent}{#if i.lastEventTitle}{' · '}{i.lastEventTitle}{/if}{' · '}{since(utc(i.lastEventAt))}
                {:else}
                  None received yet
                {/if}
              </dd>
              {#if takesRequests(i.kind)}
                <dt>Requests</dt>
                <dd>
                  {st?.qualityProfiles.find((q) => q.id === i.qualityProfileId)?.name ?? (i.qualityProfileId ? `Profile ${i.qualityProfileId}` : 'First quality profile')}
                  · {i.rootFolder || 'first root folder'}
                </dd>
              {/if}
              {#if i.pathMaps.length > 0}
                <dt>Paths</dt>
                <dd>{i.pathMaps.map((m) => `${m.from} → ${m.to}`).join(', ')}</dd>
              {/if}
              <dt>Calls back to</dt>
              <dd>{i.callbackUrl}</dd>
            </dl>
            <div class="intactions">
              <button class="lib-btn ibtn" onclick={() => testIntegration(i)} disabled={intStatus[i.id] === 'checking'}>
                <RefreshCw size={15} class={intStatus[i.id] === 'checking' ? 'spin' : ''} /> Test
              </button>
              <button class="lib-btn ibtn" onclick={() => editOptions(i)}><SlidersHorizontal size={15} /> {takesRequests(i.kind) ? 'Requests & paths' : 'Paths'}</button>
              <button class="lib-btn ibtn" onclick={() => editIntegration(i)}><Pencil size={15} /> Edit</button>
              <button class="lib-btn ibtn danger push" onclick={() => (disconnecting = i)}><Unplug size={15} /> Disconnect</button>
            </div>
          </div>
        {/each}
        {#if ha?.configured}
          <div class="panel intcard">
            <div class="inthead">
              <h3 class="intname">{ha.name || haKind.label}</h3>
              <span class="tag kindtag"><House size={13} /> {haKind.label}</span>
            </div>
            <div class="muted small inturl">{ha.url}{#if ha.version}{' · '}v{ha.version}{/if}</div>
            <div class="intchips">
              {#if ha.connected}<span class="tag ok">Connected</span>{:else}<span class="tag bad">Unreachable</span>{/if}
              {#if ha.isAdmin}<span class="tag ok">Admin user</span>{:else}<span class="tag">Not an admin user</span>{/if}
            </div>
            {#if ha.error}<div class="small reqerr inturl">{ha.error}</div>{/if}
            <dl class="intfacts small">
              <dt>Entities</dt>
              <dd>{ha.exposed ?? 0} exposed of {ha.entities ?? 0}</dd>
              <dt>Playback events</dt>
              <dd>
                {#if ha.playbackEvents}On: plays, pauses and stops are sent to Home Assistant{:else if ha.isAdmin}Off{:else}Off: they need Cineva's Home Assistant user to be an administrator there{/if}
              </dd>
            </dl>
            <div class="intactions">
              <button class="lib-btn ibtn" onclick={() => go('smarthome')}><SlidersHorizontal size={15} /> Manage</button>
              <button class="lib-btn ibtn" onclick={editHA}><Pencil size={15} /> Edit</button>
              <button class="lib-btn ibtn danger push" onclick={() => (disconnectingHA = true)}><Unplug size={15} /> Disconnect</button>
            </div>
          </div>
        {/if}
      </div>

      {#if pickingKind}
        <Modal title="Add an integration" onclose={() => (pickingKind = false)}>
          <div class="kindlist">
            {#each Object.entries(integrationKinds) as [kind, k] (kind)}
              {@const Icon = k.icon}
              <button class="kindopt" onclick={() => newIntegration(kind as IntegrationKind)}>
                <span class="kindicon"><Icon size={20} /></span>
                <span class="kindtext">
                  <strong>{k.label}</strong>
                  <span class="muted small">{k.blurb}</span>
                </span>
                <ChevronRight size={18} class="kindgo" />
              </button>
            {/each}
            {#if ha && !ha.configured}
              <button class="kindopt" onclick={connectHA}>
                <span class="kindicon"><House size={20} /></span>
                <span class="kindtext">
                  <strong>{haKind.label}</strong>
                  <span class="muted small">{haKind.blurb}</span>
                </span>
                <ChevronRight size={18} class="kindgo" />
              </button>
            {/if}
          </div>
        </Modal>
      {/if}

      {#if editingInt}
        {@const label = kindLabel(editingInt.kind)}
        <Modal title={editingInt.title} onclose={() => (editingInt = null)}>
          <form id="int-form" class="form modalform" onsubmit={saveIntegration}>
            <label>Name
              <input bind:value={editingInt.name} required placeholder="{label} 4K" />
            </label>
            <p class="hint">How this connection is shown here and when approving requests. No two can share a name.</p>
            <label>{label} URL
              <input type="url" bind:value={editingInt.url} required placeholder={integrationKinds[editingInt.kind].url} />
            </label>
            <label>{editingInt.id ? 'API key (leave blank to keep)' : 'API key'}
              <input type="password" bind:value={editingInt.apiKey} required={!editingInt.id} autocomplete="off" />
            </label>
            <p class="hint">In {label}: Settings → General → Security → API Key.</p>
            <label>Cineva address, as {label} reaches it
              <input type="url" bind:value={editingInt.callbackUrl} required />
            </label>
            <p class="hint">Change this when {label} runs in a container or on another machine and reaches Cineva at a different address, such as http://192.168.1.10:4747.</p>
            {#if intErr}<div class="error slim">{intErr}</div>{/if}
          </form>
          {#snippet foot()}
            {#if editingInt && !editingInt.id}
              <button type="button" class="lib-btn push-end" onclick={() => { editingInt = null; pickingKind = true }}>Back</button>
            {/if}
            <button type="button" class="lib-btn" onclick={() => (editingInt = null)}>Cancel</button>
            <button class="primary" type="submit" form="int-form" disabled={intSaving}>{intSaving ? 'Connecting…' : editingInt?.id ? 'Save' : 'Connect'}</button>
          {/snippet}
        </Modal>
      {/if}

      {#if optsFor && optsConn}
        {@const st = checked(optsConn.id)}
        {@const label = kindLabel(optsConn.kind)}
        <Modal title="{takesRequests(optsConn.kind) ? 'Requests and paths' : 'Paths'} · {optsConn.name}" onclose={() => (optsFor = null)}>
          <form id="opts-form" class="form modalform" onsubmit={saveOptions}>
            {#if takesRequests(optsConn.kind)}
            {#if intStatus[optsConn.id] === 'checking'}<p class="hint">Asking {optsConn.name} for its profiles and folders…</p>{/if}
            <label>Quality profile for requests
              <select bind:value={optsFor.qualityProfileId}>
                <option value={0}>The first one ({st?.qualityProfiles[0]?.name ?? `as ${label} lists them`})</option>
                {#each st?.qualityProfiles ?? [] as q (q.id)}<option value={q.id}>{q.name}</option>{/each}
              </select>
            </label>
            <label>Folder for requests
              <select bind:value={optsFor.rootFolder}>
                <option value="">The first one ({st?.rootFolders[0]?.path ?? 'as listed'})</option>
                {#each st?.rootFolders ?? [] as f (f.id)}<option value={f.path}>{f.path} · {freeSpace(f.freeSpace)}</option>{/each}
              </select>
            </label>
            {/if}
            <div class="facefield">
              <span>Path mapping</span>
              <p class="hint">For when {label} sees the media at other paths than Cineva does, as it does in a container.</p>
              {#each optsFor.pathMaps as m, mi (mi)}
                <div class="maprow">
                  <input bind:value={m.from} placeholder="{label}'s path, e.g. /tv" aria-label="{label}'s path" />
                  <span class="muted">→</span>
                  <input bind:value={m.to} placeholder="Cineva's path, e.g. /mnt/media/TV" aria-label="Cineva's path" />
                  <button type="button" class="lib-btn ibtn danger iconly" aria-label="Remove this mapping" onclick={() => optsFor && (optsFor.pathMaps = optsFor.pathMaps.filter((_, x) => x !== mi))}><X size={15} /></button>
                </div>
              {/each}
              <div class="formrow wrap">
                <button type="button" class="lib-btn ibtn" onclick={() => optsFor && (optsFor.pathMaps = [...optsFor.pathMaps, { from: '', to: '' }])}><Plus size={15} /> Add mapping</button>
                {#if st && st.suggestedPathMaps.some((sm) => !optsFor?.pathMaps.some((m) => m.from === sm.from))}
                  <button type="button" class="lib-btn" onclick={() => st && addSuggested(st.suggestedPathMaps)}>
                    Use suggested: {st.suggestedPathMaps.map((m) => `${m.from} → ${m.to}`).join(', ')}
                  </button>
                {/if}
              </div>
            </div>
            {#if optsErr}<div class="error slim">{optsErr}</div>{/if}
          </form>
          {#snippet foot()}
            <button type="button" class="lib-btn" onclick={() => (optsFor = null)}>Cancel</button>
            <button class="primary" type="submit" form="opts-form" disabled={optsSaving}>{optsSaving ? 'Saving…' : 'Save'}</button>
          {/snippet}
        </Modal>
      {/if}

      {#if editingHA}
        <Modal title={editingHA.existing ? `Edit ${editingHA.name || haKind.label}` : `Connect ${haKind.label}`} onclose={() => (editingHA = null)}>
          <form id="ha-form" class="form modalform" onsubmit={saveHA}>
            <label>Name
              <input bind:value={editingHA.name} required placeholder={haKind.label} />
            </label>
            <label>Home Assistant URL
              <input type="url" bind:value={editingHA.url} required placeholder={haKind.url} />
            </label>
            <label>{editingHA.existing ? 'Long-lived access token (leave blank to keep)' : 'Long-lived access token'}
              <input type="password" bind:value={editingHA.token} required={!editingHA.existing} autocomplete="off" />
            </label>
            <p class="hint">Create one on your user profile page in Home Assistant, on the Security tab. A Home Assistant user of its own for Cineva is best, so its access can be seen and taken away on its own.</p>
            <label class="check"><input type="checkbox" bind:checked={editingHA.playbackEvents} /> Send playback events to Home Assistant</label>
            <p class="hint">Plays, pauses, resumes and stops become events on Home Assistant's event bus, for automations such as raising the lights on pause. Firing events needs Cineva's Home Assistant user to be an administrator there{#if editingHA.existing && ha && !ha.isAdmin}, which the current one is not{/if}.</p>
            {#if haErr}<div class="error slim">{haErr}</div>{/if}
          </form>
          {#snippet foot()}
            {#if editingHA && !editingHA.existing}
              <button type="button" class="lib-btn push-end" onclick={() => { editingHA = null; pickingKind = true }}>Back</button>
            {/if}
            <button type="button" class="lib-btn" onclick={() => (editingHA = null)}>Cancel</button>
            <button class="primary" type="submit" form="ha-form" disabled={haSaving}>{haSaving ? 'Connecting…' : editingHA?.existing ? 'Save' : 'Connect'}</button>
          {/snippet}
        </Modal>
      {/if}

      {#if disconnectingHA && ha?.configured}
        {@const name = ha.name || haKind.label}
        <Modal title="Disconnect {name}?" onclose={() => (disconnectingHA = false)}>
          <p>Cineva closes its connection to {name}, and the rooms, controls, cameras and modes leave the player until it is connected again. Which entities are exposed and to whom, the devices' rooms and the modes are all kept, so connecting the same instance again restores them.</p>
          {#snippet foot()}
            <button type="button" class="lib-btn" onclick={() => (disconnectingHA = false)}>Cancel</button>
            <button type="button" class="lib-btn ibtn danger solid" disabled={haSaving} onclick={removeHA}>
              <Unplug size={15} /> {haSaving ? 'Disconnecting…' : 'Disconnect'}
            </button>
          {/snippet}
        </Modal>
      {/if}

      {#if disconnecting}
        {@const conn = disconnecting}
        <Modal title="Disconnect {conn.name}?" onclose={() => (disconnecting = null)}>
          <p>Cineva removes its webhook from {conn.name}, and what it imports waits for the next scheduled rescan. Requests can no longer be sent to it.</p>
          {#snippet foot()}
            <button type="button" class="lib-btn" onclick={() => (disconnecting = null)}>Cancel</button>
            <button type="button" class="lib-btn ibtn danger solid" disabled={intSaving} onclick={() => removeIntegration(conn)}>
              <Unplug size={15} /> {intSaving ? 'Disconnecting…' : 'Disconnect'}
            </button>
          {/snippet}
        </Modal>
      {/if}
    {:else if page === 'notifications'}
      <Notifications {libraries} />
    {:else if page === 'devices'}
      <Devices bind:this={devicesPage} {users} homeAssistant={!!ha?.configured} />
    {:else if page === 'smarthome'}
      {#if ha?.configured}
        <SmartHome bind:this={smartHome} connection={ha} {users} {groups} />
      {:else}
        <h1>Smart home</h1>
        <div class="empty">{ha ? 'Home Assistant is not connected.' : 'Loading…'} Connect it on the <button class="linkbtn" onclick={() => go('integrations')}>Integrations</button> page to choose what Cineva can see of the house, put devices in rooms and build modes.</div>
      {/if}
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
                    <button
                      class="danger"
                      onclick={() => endPlayback(s)}
                      title={s.sessionId
                        ? 'Stop the stream and tell the client to stop playing'
                        : 'Tell the client to stop playing (direct play has no stream to cut)'}
                    >Stop playback</button>
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        </div>
      {/if}
      <div class="titlerow grouphead">
        <h2>Intro &amp; credits detection</h2>
        <!-- Both buttons act on detection and seek-bar previews together:
             they share one schedule and one pause. -->
        {#if intro || previews}
          <div class="formrow">
            {#if (intro?.paused && scheduleHeld(intro.pauseReason)) || (previews?.paused && scheduleHeld(previews.pauseReason))}
              <button onclick={analyseNow}>Analyse now</button>
            {/if}
            <button onclick={toggleDetectionPause}>{bgPaused ? 'Resume' : 'Pause'}</button>
          </div>
        {/if}
      </div>
      <div class="introjob">
        {#if intro?.paused && intro.pauseReason}
          <div class="pausenote">{pauseText[intro.pauseReason] ?? 'Paused.'}</div>
        {/if}
        {#if intro && intro.enabled === false}
          <div class="muted">Turned off — set <code>libraries.intro_detection</code> to enable it.</div>
        {:else}
          {#if intro?.targets?.length}
            <!-- Asked for by name ("Detect intro & credits"), ahead of any
                 library and whatever the schedule says. -->
            <div class="targets">
              <div class="small">Asked for</div>
              {#each intro.targets as t (t.itemId)}
                <div class="small">
                  {t.title || 'Unknown'}{#if t.kind === 'season'} · Season {t.season ?? 0}{:else} <span class="muted">(film)</span>{/if}
                  <span class="muted">— {t.working ? (intro.current ? `${intro.current.kind === 'credits' ? 'credits' : 'intros'}: ${phaseLabel[intro.current.phase]} ${intro.current.done} of ${intro.current.total}` : 'working') : 'waiting'}</span>
                </div>
              {/each}
            </div>
          {/if}
          {#if intro?.running}
            <div>
              Analysing {libName(intro.libraryId ?? '')}
              <span class="muted">· {introDone} of {introTotal} checks · {intro.introsFound} intros, {intro.creditsFound} credits found{#if intro.unreadable} · {intro.unreadable} unreadable{/if}</span>
            </div>
            <div class="scanbar"><div class="scanbar-fill" style="width: {introTotal ? (introDone / introTotal) * 100 : 0}%"></div></div>
            {#if intro.current && !intro.targets?.some((t) => t.working)}
              <div class="small">
                {#if intro.current.film}{intro.current.series || 'Unknown film'}{:else}{intro.current.series || 'Unknown series'} · Season {intro.current.season}{/if}
                <span class="muted">— {intro.current.kind === 'credits' ? 'credits' : 'intros'}: {phaseLabel[intro.current.phase]} {intro.current.done} of {intro.current.total}</span>
              </div>
            {/if}
            <div class="muted small">
              {#if intro.seasonsTotal > 0}Season {Math.min(intro.seasonsDone + 1, intro.seasonsTotal)} of {intro.seasonsTotal} · {/if}{#if intro.filmsTotal}{intro.filmsDone ?? 0} of {intro.filmsTotal} films · {/if}running {span(introElapsed)}{#if introLeft !== null} · about {span(introLeft)} left{/if}
            </div>
          {:else if intro?.last}
            <div>
              {intro.last.cancelled ? 'Stopped' : 'Finished'} {since(intro.last.finishedAt)}
              <span class="muted">· {libName(intro.last.libraryId)} · {intro.last.episodes} checks in {intro.last.seasons} seasons{#if intro.last.films} · {intro.last.films} films{/if} · {intro.last.introsFound} intros, {intro.last.creditsFound} credits found{#if intro.last.unreadable} · {intro.last.unreadable} unreadable{/if} · took {span(intro.last.seconds)}</span>
            </div>
          {:else if !intro?.targets?.length}
            <div class="muted">Idle. Runs after each film or TV library scan, on episodes not yet analysed for intros and credits, and films not yet looked at for credits.</div>
          {/if}
        {/if}
        {#if previewLine}
          <div class="previewline small">Seek-bar previews <span class="muted">· {previewLine}</span></div>
        {/if}
        {#if intro?.recent?.length}
          <table class="introrecent">
            <thead><tr><th>Latest checked</th><th>Looked for</th><th>Found</th></tr></thead>
            <tbody>
              {#each intro.recent as r (r.itemId + r.kind)}
                <tr>
                  <td>{r.series}{#if !r.film} <span class="muted">{epCode(r)}</span>{/if}</td>
                  <td class="muted">{r.kind}</td>
                  <td>
                    {#if r.found && r.startSecs !== undefined && r.endSecs !== undefined}
                      {clock(r.startSecs)} – {clock(r.endSecs)} <span class="muted">({Math.round(r.endSecs - r.startSecs)} s)</span>
                    {:else}
                      <span class="muted">none found</span>
                    {/if}
                  </td>
                </tr>
              {/each}
            </tbody>
          </table>
        {/if}
      </div>
      <div class="titlerow grouphead">
        <h2>Wrong times reported</h2>
      </div>
      {#if reports.length === 0}
        <div class="empty slim">No open reports. Viewers report a misplaced intro or credits skip from the player's menu.</div>
      {:else}
        {#each crowdedSeasons as cs (cs.seasonId)}
          <div class="reportprompt">
            <span>{cs.ids.length} open reports on {cs.label}: detecting the season again may fix them together.</span>
            <button class="primary" disabled={cs.ids.some((id) => resolving.includes(id))} onclick={() => redetectReported(cs)}>Detect season again</button>
          </div>
        {/each}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Title</th><th>Wrong</th><th>At</th><th>Reported by</th><th></th></tr></thead>
            <tbody>
              {#each reports as r (r.id)}
                <tr>
                  <td>
                    <div>{reportLabel(r)}</div>
                    {#if r.itemKind === 'episode' && r.title}<div class="muted small">{r.title}</div>{/if}
                    {#if r.note}<div class="small reportnote">“{r.note}”</div>{/if}
                  </td>
                  <td class="muted">{r.kind}</td>
                  <td class="muted nowrap">{clock(r.positionSecs)}</td>
                  <td class="muted">{r.userName}<div class="small">{r.createdAt.slice(0, 16)} UTC</div></td>
                  <td class="actions">
                    <a class="lib-btn" href="/items/{r.itemId}/segments" target="_blank" rel="noopener" title="Open the intro and credits editor in the web client">Edit times…</a>
                    <button disabled={resolving.includes(r.id)} onclick={() => resolveReports([r.id])}>Resolve</button>
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
                    <!-- Before the first progress report the scanner is still
                         walking the folders, which on a large share takes a
                         while and otherwise looks like a stalled "0 files". -->
                    {#if s.status === 'running' && !scanTotals.get(s.id)}<div class="muted small">Checking files…</div>{/if}
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
          <span>
            Requests for new accounts
            <span class="muted small">
              — what happens when someone asks for a film or series that is
              not in the library. Set each person's own on the Users page;
              admins' requests always go straight through.
              {#if settings && !settings.tmdbConfigured}Requests are off until a TMDB key is configured.{/if}
            </span>
          </span>
          <select
            class="setting-select"
            value={settings?.requestDefaultLevel ?? 'approval'}
            onchange={(e) => setRequestDefault((e.currentTarget as HTMLSelectElement).value as RequestLevel)}
            aria-label="Request level for new accounts"
          >
            {#each settings?.requestLevels ?? ['none', 'approval', 'auto'] as l (l)}
              <option value={l}>{levelLabel[l as RequestLevel]}</option>
            {/each}
          </select>
        </div>
        <div class="setting">
          <span>
            Background work schedule
            <span class="muted small">
              — when intro &amp; credits detection and seek-bar previews may
              read your media. Playback always comes first: both give way to
              anything playing or scanning, and stop as soon as a share stops
              answering.
            </span>
          </span>
          <div class="schedule">
            <select
              class="setting-select"
              value={settings?.detectionMode ?? 'window'}
              onchange={(e) => setDetectionSchedule({ detectionMode: (e.currentTarget as HTMLSelectElement).value as ServerSettings['detectionMode'] })}
              aria-label="When background work runs"
            >
              <option value="window">Only between set times</option>
              <option value="idle">Whenever nothing is playing</option>
              <option value="manual">Only when I start it</option>
            </select>
            {#if (settings?.detectionMode ?? 'window') === 'window'}
              <label class="clock">From
                <input type="time" value={settings?.detectionWindowStart ?? '02:00'}
                  onchange={(e) => setDetectionSchedule({ detectionWindowStart: (e.currentTarget as HTMLInputElement).value })} />
              </label>
              <label class="clock">to
                <input type="time" value={settings?.detectionWindowEnd ?? '06:00'}
                  onchange={(e) => setDetectionSchedule({ detectionWindowEnd: (e.currentTarget as HTMLInputElement).value })} />
              </label>
            {/if}
          </div>
        </div>
        <div class="setting">
          <span>
            Background work speed
            <span class="muted small">
              — how fast intro &amp; credits detection and seek-bar previews
              may read from your media storage. Between them they read much
              of every file, which at full speed can fill the link to a NAS.
              They always pause while anything plays or scans; a cap also
              keeps them gentle the rest of the time.
            </span>
          </span>
          <select
            class="setting-select"
            value={String(settings?.detectionRateMB ?? 10)}
            onchange={(e) => setDetectionRate(Number((e.currentTarget as HTMLSelectElement).value))}
            aria-label="Background read speed"
          >
            {#each settings?.detectionRateOptions ?? [0] as mb (mb)}
              <option value={String(mb)}>{mb === 0 ? 'Unlimited' : `Up to ${mb} MB/s`}</option>
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

<!-- Outside the layout, so no transformed dialogue it is opened from can
     trap it. -->
{#if crop}
  <AvatarCrop file={crop.file} busy={avatarBusy} oncrop={sendUserAvatar} oncancel={() => (crop = null)} />
{/if}

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
  .titleacts { display: flex; flex-wrap: wrap; justify-content: flex-end; gap: 0.5rem; margin-bottom: 1rem; }
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
  .schedule { display: flex; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
  .clock { display: flex; align-items: center; gap: 0.4rem; color: var(--muted); font-size: 0.9rem; }
  .clock input { background: var(--bg); border: 1px solid var(--border); border-radius: 8px; color: var(--text); padding: 0.35rem 0.5rem; font: inherit; color-scheme: dark light; }
  .formrow.wrap { flex-wrap: wrap; }
  .face {
    width: 2rem; height: 2rem; flex: none; border-radius: 50%; display: grid; place-items: center;
    font-weight: 600; font-size: 0.85rem; object-fit: cover;
    background: linear-gradient(135deg, var(--accent), var(--accent-2)); color: var(--bg);
  }
  .face.tinted { background: var(--face); }
  .face.big { width: 4rem; height: 4rem; border-radius: 14px; font-size: 1.6rem; }
  .userCell { display: flex; align-items: center; gap: 0.6rem; }
  .facefield { display: flex; flex-direction: column; gap: 0.45rem; font-size: 0.9rem; color: var(--muted); }
  .facerow { display: flex; align-items: center; gap: 0.9rem; flex-wrap: wrap; }
  .faceside { display: flex; flex-direction: column; gap: 0.5rem; min-width: 0; }
  .upload { position: relative; cursor: pointer; }
  .upload input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
  .upload.busy { opacity: 0.6; cursor: progress; }
  .swatches { display: flex; gap: 0.45rem; flex-wrap: wrap; }
  .swatch {
    width: 1.7rem; height: 1.7rem; border-radius: 50%; cursor: pointer;
    background: var(--face, linear-gradient(135deg, var(--accent), var(--accent-2)));
    border: 2px solid transparent; outline: 1px solid var(--border);
  }
  .swatch.sel { border-color: var(--bg); outline: 2px solid var(--text); }
  .devrow { display: flex; align-items: center; gap: 0.6rem; justify-content: space-between; }
  .devtext { display: flex; flex-direction: column; min-width: 0; color: var(--text); }
  .devtext .small { overflow-wrap: anywhere; }
  .addrow { flex-wrap: wrap; justify-content: flex-end; }
  .intro { margin-bottom: 1rem; }
  .navcount {
    margin-left: 0.4rem; padding: 0 0.4rem; border-radius: 999px; font-size: 0.72rem; font-weight: 600;
    background: var(--accent); color: var(--bg);
  }
  .linkbtn { color: var(--accent-2); text-decoration: underline; }
  .reqlist { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 27rem), 1fr)); gap: 1rem; }
  .reqcard { display: flex; gap: 0.9rem; margin-bottom: 0; align-items: flex-start; }
  .reqposter { width: 4.5rem; aspect-ratio: 2 / 3; flex: none; border-radius: 8px; object-fit: cover; background: var(--bg); }
  .reqbody { display: flex; flex-direction: column; gap: 0.45rem; min-width: 0; flex: 1; }
  .reqtitle { overflow-wrap: anywhere; }
  .reqerr { color: var(--err); }
  .reqform { max-width: none; margin-top: 0.25rem; }
  .maprow { display: flex; gap: 0.4rem; align-items: center; flex-wrap: wrap; }
  .maprow input {
    flex: 1 1 9rem; min-width: 0; background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.6rem; font: inherit;
  }
  /* Integrations: a card per connection, headed by its name, with the
     actions as a bar of bordered buttons along the bottom. */
  .intgrid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(100%, 27rem), 1fr)); gap: 1rem; }
  .intcard { display: flex; flex-direction: column; gap: 0.6rem; margin-bottom: 0; }
  .inthead { display: flex; align-items: flex-start; justify-content: space-between; gap: 0.75rem; }
  .intname { font-size: 1.05rem; font-weight: 600; overflow-wrap: anywhere; }
  .kindtag { display: inline-flex; align-items: center; gap: 0.3rem; color: var(--muted); flex: none; }
  .inturl { overflow-wrap: anywhere; }
  .intchips { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .intwarns { list-style: none; display: flex; flex-direction: column; gap: 0.3rem; }
  .intwarns li { display: flex; gap: 0.4rem; align-items: flex-start; color: var(--muted); overflow-wrap: anywhere; }
  .intwarns li :global(svg) { flex: none; margin-top: 0.1rem; color: var(--accent-2); }
  .intwarns li.bad, .intwarns li.bad :global(svg) { color: var(--err); }
  .intfacts { display: grid; grid-template-columns: auto 1fr; gap: 0.3rem 0.9rem; }
  .intfacts dt { color: var(--muted); white-space: nowrap; }
  .intfacts dd { overflow-wrap: anywhere; }
  .intactions {
    display: flex; flex-wrap: wrap; gap: 0.5rem;
    margin-top: auto; padding-top: 0.85rem; border-top: 1px solid var(--border);
  }
  .intactions .push { margin-left: auto; }
  /* A button with an icon before its label. */
  .ibtn { display: inline-flex; align-items: center; gap: 0.4rem; }
  .lib-btn.ibtn { padding: 0.4rem 0.75rem; font-size: 0.875rem; }
  .lib-btn.iconly { padding: 0.4rem; }
  .lib-btn.danger:hover { border-color: var(--err); }
  .lib-btn.danger.solid { background: var(--err); border-color: var(--err); color: var(--bg); }
  .lib-btn :global(.spin) { animation: spin 0.9s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  .addint { margin-bottom: 1rem; flex: none; }
  /* The Add dialogue's first step: one row per kind. */
  .kindlist { display: flex; flex-direction: column; gap: 0.5rem; }
  .kindopt {
    display: flex; align-items: center; gap: 0.85rem; text-align: left;
    padding: 0.8rem 0.9rem; border: 1px solid var(--border); border-radius: 10px; background: var(--bg);
  }
  .kindopt:hover, .kindopt:focus-visible { border-color: var(--accent); }
  .kindicon {
    display: grid; place-items: center; width: 2.4rem; height: 2.4rem; flex: none;
    border-radius: 8px; background: var(--panel); color: var(--accent-2);
  }
  .kindtext { display: flex; flex-direction: column; gap: 0.15rem; flex: 1; min-width: 0; }
  .kindopt :global(.kindgo) { flex: none; color: var(--muted); }
  .modalform { max-width: none; }
  .modalform .error.slim { margin: 0; }
  .push-end { margin-right: auto; }
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
  /* A row does two things: the checkbox adds the folder, the name navigates
     into it. The checkbox gets its own hit area so a mis-tap on a phone
     navigates rather than silently ticking something. */
  .dirrow { display: flex; align-items: center; gap: 0.1rem; }
  .pick {
    display: grid; place-items: center;
    padding: 0.45rem 0.5rem; border-radius: 6px; cursor: pointer;
  }
  .pick:hover { background: var(--bg); }
  .pick input { cursor: pointer; }
  .dirrow .dirname { flex: 1; }
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
  .lib-btn:disabled { opacity: 0.5; }
  .lib-modal-foot { display: flex; justify-content: flex-end; margin-top: 0.8rem; }
  .lib-pager { display: flex; align-items: center; justify-content: center; gap: 0.8rem; margin: 0.6rem 0; }
  .lib-spacer { flex: 1; }
  .lib-bulk {
    display: flex; flex-wrap: wrap; align-items: center; gap: 0.5rem 0.8rem;
    margin: 0.6rem 0; padding: 0.5rem 0.8rem;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px;
  }
  .lib-check { width: 2rem; }
  .lib-check input { width: 1.1rem; height: 1.1rem; }
  tr.lib-sel td { background: color-mix(in srgb, var(--accent) 8%, transparent); }
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
  .pausenote { color: var(--accent-2); font-size: 0.88rem; }
  .storage-down { color: var(--err); font-size: 0.8rem; margin-top: 0.2rem; }
  .grouphead { margin-top: 1.5rem; }
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
    .back + .back { margin-top: 0; padding-top: 0.5rem; }
  }
  .introjob {
    display: flex; flex-direction: column; gap: 0.4rem;
    margin-bottom: 1rem; padding: 0.75rem 1rem;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px;
  }
  .previewline { border-top: 1px solid var(--border); padding-top: 0.4rem; }
  .introrecent { margin-top: 0.5rem; }
  .introrecent td, .introrecent th { padding-left: 0; }
  .targets { display: flex; flex-direction: column; gap: 0.2rem; padding-bottom: 0.4rem; border-bottom: 1px solid var(--border); }
  /* Several reports on one season: a nudge to detect it again, above the
     list. Wraps under the text at phone width. */
  .reportprompt {
    display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 0.5rem 1rem;
    margin-bottom: 0.75rem; padding: 0.6rem 0.9rem;
    border: 1px solid var(--accent-2); border-radius: 10px; font-size: 0.9rem;
  }
  .reportnote { margin-top: 0.2rem; overflow-wrap: anywhere; }
  a.lib-btn { text-decoration: none; display: inline-block; }
  .detectnote { margin-top: 0.2rem; }
</style>
