<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, createLibrary, deleteLibrary, scanLibrary, listScans,
    cancelScan, listSessions, stopSession, subscribeEvents, browseDirs, systemStats, playStats, diskStats, auditLog,
    authStatus, login, logout, listUsers, createUser, updateUser, deleteUser, UNAUTHORISED,
    type Health, type Library, type Scan, type PlaySession, type DirListing, type SystemSample, type PlayDay, type DiskVolume, type AuditEntry, type User, type UserRequest,
  } from './lib/api'
  import LineChart from './LineChart.svelte'
  import BarChart from './BarChart.svelte'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'

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
      if (id) await updateUser(id, body)
      else await createUser(body)
      editing = null
      loadUsers()
    } catch (err) {
      userErr = err instanceof Error ? err.message : String(err)
    } finally {
      userSaving = false
    }
  }
  async function removeUser(u: User) {
    if (!confirm(`Delete user "${u.username}"? Their watch history, sessions and API keys go with them.`)) return
    try {
      await deleteUser(u.id)
      loadUsers()
    } catch (err) {
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
  let newPath = $state('')
  let saving = $state(false)

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  async function refresh() {
    refreshing = true
    try {
      health = await getHealth()
      offline = false
      libraries = await listLibraries()
      scans = await listScans()
      sessions = await listSessions()
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
      await createLibrary(newName, newType, [newPath])
      showAdd = false
      newName = newPath = ''
      await refresh()
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    } finally {
      saving = false
    }
  }

  async function removeLibrary(lib: Library) {
    if (!confirm(`Delete library "${lib.name}"? Scanned items are removed; media files on disk are untouched.`)) return
    try {
      await deleteLibrary(lib.id)
      await refresh()
    } catch (e) {
      error = String(e instanceof Error ? e.message : e)
    }
  }

  async function startScan(lib: Library) {
    error = ''
    try {
      await scanLibrary(lib.id)
      page = 'activity'
      await refresh()
    } catch (e) {
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

  function openBrowser() {
    browsing = true
    const start = newPath.trim() && newPath.startsWith('/') ? newPath.trim() : '/'
    browse(start)
  }

  function chooseFolder() {
    if (listing) newPath = listing.path
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
    refresh()
    loadStats()
    loadDisks()
    loadLogs()
    loadUsers()
    // Live updates over SSE; the slow poll is only a fallback for missed events.
    const closeEvents = subscribeEvents(
      {
        scan: onScanEvent,
        libraries: () => { listLibraries().then((l) => (libraries = l)).catch(() => {}); loadDisks() },
        stats: onSample,
        logs: () => { if (!logsLoading) loadLogs() },
        users: loadUsers,
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

  onMount(() => {
    listThemes().then((t) => (themes = t)).catch(() => {})
    checkAuth().then(() => { if (gate === 'in') boot() })
    const onUnauthorised = () => { if (gate === 'in') { me = null; gate = 'login' } }
    window.addEventListener(UNAUTHORISED, onUnauthorised)
    return () => {
      teardown?.()
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
        <button class:active={page === p.id} onclick={() => (page = p.id)}>{p.label}</button>
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
          <label>Path
            <span class="pathrow">
              <input bind:value={newPath} placeholder="/media/movies" required />
              <button type="button" onclick={openBrowser}>Browse…</button>
            </span>
          </label>
          <button class="primary" disabled={saving} type="submit">{saving ? 'Creating…' : 'Create'}</button>
          <p class="hint">One media type per library, chosen now and immutable. Add more paths later.</p>
        </form>
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
                      <button class="danger" onclick={() => stopSession(s.sessionId!).then(refresh)}>Stop transcode</button>
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
                      <button class="danger" onclick={() => cancelScan(s.id).then(refresh).catch((e) => (error = String(e)))}>Cancel</button>
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
          <span>Theme</span>
          <div class="theme-picker">
            {#each themes as t (t.id)}
              <button class="chip" class:sel={theme === t.id} onclick={() => setTheme(t.id)}>{t.name}</button>
            {/each}
          </div>
        </div>
      </div>
      <div class="empty">
        Server settings are file/env driven for now — edit <code>cineva.yaml</code> or set
        <code>CINEVA_*</code> variables, then restart. A UI for safe-to-change settings comes later.
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
  nav { display: flex; gap: 0.25rem; overflow-x: auto; }
  nav button {
    padding: 0.45rem 0.8rem;
    border-radius: 8px;
    color: var(--muted);
    white-space: nowrap;
  }
  nav button.active { background: var(--bg); color: var(--text); }
  .back { margin-left: auto; color: var(--muted); font-size: 0.85rem; text-decoration: none; }
  .back:hover { color: var(--text); }

  main { flex: 1; padding: 1.25rem; max-width: 70rem; width: 100%; margin: 0 auto; }
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
  .form input, .form select {
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--text);
    padding: 0.5rem 0.7rem;
    font: inherit;
  }
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

  .setting {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
  }
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
    nav { flex-direction: column; overflow: visible; margin-top: 1rem; }
    nav button { text-align: left; }
    .back { margin: auto 0 0; padding-top: 1rem; }
  }
</style>
