<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, createLibrary, deleteLibrary, scanLibrary, listScans,
    cancelScan, listSessions, stopSession,
    type Health, type Library, type Scan, type PlaySession,
  } from './lib/api'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'

  type Page = 'dashboard' | 'libraries' | 'users' | 'activity' | 'settings'
  const pages: { id: Page; label: string }[] = [
    { id: 'dashboard', label: 'Dashboard' },
    { id: 'libraries', label: 'Libraries' },
    { id: 'users', label: 'Users' },
    { id: 'activity', label: 'Activity' },
    { id: 'settings', label: 'Settings' },
  ]
  const mediaTypes = ['movies', 'tv', 'music', 'audiobooks', 'podcasts', 'books']

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
    try {
      health = await getHealth()
      offline = false
      libraries = await listLibraries()
      scans = await listScans()
      sessions = await listSessions()
    } catch {
      offline = true
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

  function libName(id: string): string {
    return libraries.find((l) => l.id === id)?.name ?? id.slice(0, 8)
  }

  onMount(() => {
    refresh()
    listThemes().then((t) => (themes = t)).catch(() => {})
    const id = setInterval(refresh, 5000)
    return () => clearInterval(id)
  })
</script>

<div class="shell">
  <aside>
    <div class="logo">Cineva <span>admin</span></div>
    <nav>
      {#each pages as p (p.id)}
        <button class:active={page === p.id} onclick={() => (page = p.id)}>{p.label}</button>
      {/each}
    </nav>
    <a class="back" href="/">← Back to Cineva</a>
  </aside>

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
          <label>Path <input bind:value={newPath} placeholder="/media/movies" required /></label>
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
      <h1>Users</h1>
      <div class="empty">User management arrives in milestone 4. Until then, use the <code>cineva user add</code> CLI.</div>
    {:else if page === 'activity'}
      <h1>Activity</h1>
      <h2>Streams</h2>
      {#if sessions.length === 0}
        <div class="empty slim">No active playback sessions.</div>
      {:else}
        <div class="tablewrap">
          <table>
            <thead><tr><th>Title</th><th>Session</th><th></th></tr></thead>
            <tbody>
              {#each sessions as s (s.id)}
                <tr>
                  <td>{s.title}</td>
                  <td class="muted">{s.id.slice(0, 8)} · {s.numSegments} segments</td>
                  <td class="actions"><button class="danger" onclick={() => stopSession(s.id).then(refresh)}>Stop</button></td>
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
                  </td>
                  <td>{s.filesSeen}</td>
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

  .bar { margin-bottom: 1rem; }
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

  .tablewrap { overflow-x: auto; background: var(--panel); border: 1px solid var(--border); border-radius: 12px; }
  table { width: 100%; border-collapse: collapse; font-size: 0.9rem; }
  th, td { text-align: left; padding: 0.7rem 1rem; border-bottom: 1px solid var(--border); }
  th { color: var(--muted); font-weight: 500; font-size: 0.8rem; }
  tr:last-child td { border-bottom: none; }
  .actions { display: flex; gap: 0.5rem; }
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
