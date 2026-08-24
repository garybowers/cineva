<script lang="ts">
  import { onMount } from 'svelte'
  import { getHealth, type Health } from './lib/api'
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

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  async function poll() {
    try {
      health = await getHealth()
      offline = false
    } catch {
      health = null
      offline = true
    }
  }

  onMount(() => {
    poll()
    listThemes().then((t) => (themes = t)).catch(() => {})
    const id = setInterval(poll, 5000)
    return () => clearInterval(id)
  })
</script>

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
        <h2>Recently Added</h2>
        <div class="empty">Your libraries will fill this row once scanning lands.</div>
      </section>
    {:else if tab === 'libraries'}
      <h1>Libraries</h1>
      <div class="empty tall">
        <p><strong>No libraries yet.</strong></p>
        <p>Libraries are created in the <a href="/admin/">admin UI</a> — one media type each: Movies, TV, Music, Audiobooks, Podcasts, or Books.</p>
      </div>
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
  .shell {
    min-height: 100vh;
    display: flex;
    flex-direction: column;
  }
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
    width: 10px;
    height: 10px;
    border-radius: 50%;
    background: var(--ok);
    box-shadow: 0 0 8px var(--ok);
  }
  .status.offline {
    background: var(--err);
    box-shadow: 0 0 8px var(--err);
  }
  main {
    flex: 1;
    padding: 1.25rem;
    padding-bottom: 5rem; /* room for the bottom tab bar */
    max-width: 60rem;
    width: 100%;
    margin: 0 auto;
  }
  h1 { font-size: 1.4rem; margin-bottom: 1rem; }
  h2 { font-size: 1rem; color: var(--muted); margin: 1.25rem 0 0.5rem; }
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
  .empty a { color: var(--accent-2); }
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
  .muted { color: var(--muted); }
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
    bottom: 0;
    left: 0;
    right: 0;
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

  /* Desktop: tab bar moves into the header row area as a top nav */
  @media (min-width: 768px) {
    .shell { flex-direction: column; }
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
