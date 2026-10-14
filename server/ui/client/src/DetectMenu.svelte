<!--
  The intro and credits actions for a series, season or film page, for an
  administrator or trusted editor: a ⋯ menu with "Detect intro & credits"
  (a film: "Detect credits" and the credits editor), and a line saying how
  detection asked for here is getting on.
-->
<script lang="ts">
  import { Ellipsis, ScanSearch, Clock } from '@lucide/svelte'
  import type { Item } from './lib/api'
  import { onDismiss } from './lib/dismiss'
  import { detect, detectionLine } from './lib/detection.svelte'

  let { item, onedit }: { item: Item; onedit?: () => void } = $props()

  let open = $state(false)
  let busy = $state(false)
  let msg = $state('')

  $effect(() => {
    if (!open) return
    return onDismiss('.detectwrap', () => (open = false))
  })

  const film = $derived(item.kind === 'movie')
  const line = $derived(detectionLine(item))

  // A message belongs to the item it was about.
  let seenWork = false
  $effect(() => {
    void item.id
    msg = ''
    seenWork = false
  })
  // While detection works here its progress line says enough; once the
  // work leaves the queue, say it has finished.
  $effect(() => {
    if (line) {
      seenWork = true
      msg = ''
    } else if (seenWork) {
      seenWork = false
      msg = 'Detection finished.'
    }
  })

  async function run() {
    open = false
    busy = true
    msg = ''
    try {
      msg = await detect(item, film ? ['credits'] : undefined)
    } catch (err) {
      msg = err instanceof Error ? err.message : String(err)
    } finally {
      busy = false
    }
  }
</script>

<div class="detectwrap">
  <button class="more" title="Intro and credits" aria-label="Intro and credits" aria-haspopup="menu" aria-expanded={open}
    onclick={() => (open = !open)} disabled={busy}>
    <Ellipsis size={18} />
  </button>
  {#if open}
    <div class="menu" role="menu">
      <button class="item" role="menuitem" onclick={run}>
        <ScanSearch size={14} /> {film ? 'Detect credits' : 'Detect intro & credits'}
      </button>
      {#if film && onedit}
        <button class="item" role="menuitem" onclick={() => { open = false; onedit?.() }}>
          <Clock size={14} /> Credits…
        </button>
      {/if}
    </div>
  {/if}
</div>
{#if line || msg}
  <p class="detectnote" role="status">{line || msg}</p>
{/if}

<style>
  .detectwrap { position: relative; flex: none; }
  .more {
    display: grid;
    place-items: center;
    padding: 0.5rem;
    border: 1px solid var(--border);
    border-radius: 8px;
    color: var(--muted);
  }
  .more:hover:not(:disabled) { color: var(--text); }
  .menu {
    position: absolute;
    right: 0;
    top: calc(100% + 0.4rem);
    min-width: 12rem;
    z-index: 60;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 10px;
    padding: 0.4rem;
    display: flex;
    flex-direction: column;
    box-shadow: 0 8px 24px color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.45rem 0.6rem;
    border-radius: 6px;
    color: var(--text);
    text-align: left;
    font-size: 0.9rem;
    white-space: nowrap;
  }
  .item:hover { background: var(--bg); }
  /* On its own line under the buttons, so a long message never pushes
     them off a phone's screen. */
  .detectnote {
    flex-basis: 100%;
    margin: 0;
    color: var(--muted);
    font-size: 0.82rem;
  }
</style>
