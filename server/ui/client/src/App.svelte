<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, libraryItems, itemDetail,
    type Health, type Library, type Item, type ItemDetail,
  } from './lib/api'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'

  type Tab = 'home' | 'libraries' | 'settings'
  const tabs: { id: Tab; label: string; icon: string }[] = [
    { id: 'home', label: 'Home', icon: '⌂' },
    { id: 'libraries', label: 'Libraries', icon: '▤' },
    { id: 'settings', label: 'Settings', icon: '⚙' },
  ]

  let tab: Tab = $state('home')
  let health: Health | null = $state(null)
  let offline = $state(false)
  let themes: ThemeInfo[] = $state([])
  let theme = $state(currentTheme())

  // Libraries browse state: a simple drill-down stack.
  let libraries: Library[] = $state([])
  let currentLib: Library | null = $state(null)
  let gridItems: Item[] = $state([])
  let detail: ItemDetail | null = $state(null)
  let stack: string[] = $state([]) // item-id trail for Back

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  async function poll() {
    try {
      health = await getHealth()
      offline = false
      libraries = await listLibraries()
    } catch {
      health = null
      offline = true
    }
  }

  async function openLibrary(lib: Library) {
    currentLib = lib
    detail = null
    stack = []
    const items = await libraryItems(lib.id)
    gridItems = items.filter((i) => i.kind === 'movie' || i.kind === 'series')
  }

  async function openItem(id: string) {
    if (detail) stack = [...stack, detail.item.id]
    detail = await itemDetail(id)
  }

  async function back() {
    if (stack.length > 0) {
      const prev = stack[stack.length - 1]
      stack = stack.slice(0, -1)
      detail = await itemDetail(prev)
    } else if (detail) {
      detail = null
    } else {
      currentLib = null
      gridItems = []
    }
  }

  function runtime(secs?: number): string {
    if (!secs) return ''
    const m = Math.round(secs / 60)
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`
  }

  onMount(() => {
    poll()
    listThemes().then((t) => (themes = t)).catch(() => {})
    const id = setInterval(poll, 5000)
    return () => clearInterval(id)
  })
</script>

{#snippet poster(item: Item)}
  {#if item.poster}
    <img class="poster" src={item.poster} alt={item.title} loading="lazy" />
  {:else}
    <div class="poster placeholder"><span>{item.title.slice(0, 1)}</span></div>
  {/if}
{/snippet}

<div class="shell">
  <header>
    <span class="logo">Cineva</span>
    <span class="status" class:offline title={offline ? 'Server unreachable' : `Connected · ${health?.version ?? ''}`}></span>
  </header>

  <main>
    {#if tab === 'home'}
      <h1>Home</h1>
      <section>
        <h2>Continue Watching</h2>
        <div class="empty">Nothing in progress yet — playback arrives in a later milestone.</div>
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
        <button class="backbtn" onclick={back}>← Back</button>
        <div class="detail">
          {@render poster(detail.item)}
          <div class="detail-body">
            <h1>
              {detail.item.title}
              {#if detail.item.year}<span class="muted">({detail.item.year})</span>{/if}
            </h1>
            <p class="meta">
              {#if detail.item.kind === 'episode'}S{String(detail.item.seasonNum).padStart(2, '0')}E{String(detail.item.episodeNum).padStart(2, '0')} · {/if}
              {runtime(detail.item.runtimeSecs)}
              {#if detail.item.width}· {detail.item.width}x{detail.item.height} {detail.item.videoCodec}{/if}
            </p>
            {#if detail.item.overview}<p class="overview">{detail.item.overview}</p>{/if}
            {#if detail.item.kind === 'movie' || detail.item.kind === 'episode'}
              <button class="play" disabled title="Playback arrives in milestone 3">▶ Play</button>
            {/if}
          </div>
        </div>
        {#if detail.children.length > 0}
          <ul class="children">
            {#each detail.children as child (child.id)}
              <li>
                <button onclick={() => openItem(child.id)}>
                  <span>
                    {#if child.kind === 'episode'}<span class="epnum">{child.episodeNum}.</span> {/if}
                    {child.title}
                  </span>
                  <span class="muted">{runtime(child.runtimeSecs)}</span>
                </button>
              </li>
            {/each}
          </ul>
        {/if}
      {:else if currentLib}
        <button class="backbtn" onclick={back}>← Libraries</button>
        <h1>{currentLib.name}</h1>
        {#if gridItems.length === 0}
          <div class="empty">Nothing here yet — run a scan from the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="grid">
            {#each gridItems as item (item.id)}
              <button class="cell" onclick={() => openItem(item.id)}>
                {@render poster(item)}
                <span class="cell-title">{item.title}</span>
                {#if item.year}<span class="cell-sub">{item.year}</span>{/if}
              </button>
            {/each}
          </div>
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
        <div class="row">
          <span>Next-episode autoplay</span>
          <span class="muted">arrives with playback</span>
        </div>
      </section>
    {/if}
  </main>

  <nav>
    {#each tabs as t (t.id)}
      <button class:active={tab === t.id} onclick={() => (tab = t.id)}>
        <span class="icon">{t.icon}</span>
        <span>{t.label}</span>
      </button>
    {/each}
  </nav>
</div>

<style>
  .shell { min-height: 100vh; display: flex; flex-direction: column; }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    padding: 0.9rem 1.25rem;
    border-bottom: 1px solid var(--border);
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
    flex: 1;
    padding: 1.25rem;
    padding-bottom: 5rem;
    max-width: 60rem;
    width: 100%;
    margin: 0 auto;
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

  .backbtn { color: var(--accent-2); margin-bottom: 0.75rem; padding: 0.25rem 0; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr)); gap: 0.9rem; }
  .cell { display: flex; flex-direction: column; gap: 0.35rem; text-align: left; }
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
    font-size: 2.2rem;
    font-weight: 700;
    color: var(--muted);
  }
  .cell:hover .poster { border-color: var(--accent); }
  .cell-title { font-size: 0.86rem; line-height: 1.25; }
  .cell-sub { font-size: 0.75rem; color: var(--muted); }

  .detail { display: flex; gap: 1.25rem; align-items: flex-start; }
  .detail .poster { width: 9rem; flex: none; }
  .detail-body { flex: 1; min-width: 0; }
  .meta { color: var(--muted); font-size: 0.88rem; margin: 0.4rem 0 0.8rem; }
  .overview { font-size: 0.95rem; line-height: 1.5; margin-bottom: 1rem; }
  .play {
    background: var(--accent);
    color: white;
    padding: 0.55rem 1.4rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .play:disabled { opacity: 0.45; cursor: not-allowed; }

  .children { list-style: none; margin-top: 1.25rem; }
  .children li { border-bottom: 1px solid var(--border); }
  .children li:last-child { border-bottom: none; }
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

  nav {
    position: fixed;
    bottom: 0; left: 0; right: 0;
    display: flex;
    background: var(--panel);
    border-top: 1px solid var(--border);
  }
  nav button {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.15rem;
    padding: 0.6rem 0 0.7rem;
    color: var(--muted);
    font-size: 0.75rem;
  }
  nav button .icon { font-size: 1.15rem; }
  nav button.active { color: var(--accent-2); }

  @media (min-width: 768px) {
    nav {
      position: sticky;
      top: 0;
      bottom: auto;
      order: -1;
      justify-content: center;
      gap: 1rem;
      border-top: none;
      border-bottom: 1px solid var(--border);
      background: var(--bg);
    }
    nav button {
      flex: none;
      flex-direction: row;
      gap: 0.4rem;
      padding: 0.75rem 1rem;
      font-size: 0.9rem;
    }
    main { padding-bottom: 1.25rem; }
  }
</style>
