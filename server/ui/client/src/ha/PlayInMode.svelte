<!--
  "Play in Movie mode" on a title's page: runs a mode that plays the title
  it is started from, with this one. One mode is one button; several are a
  small menu. How the run goes is told by the app's notice.
-->
<script lang="ts">
  import { Clapperboard, ChevronDown } from '@lucide/svelte'
  import type { HAMode } from '../lib/api'
  import { onDismiss } from '../lib/dismiss'
  import { ha, startMode, titleModes } from '../lib/homeassistant.svelte'

  let { itemId }: { itemId: string } = $props()

  const modes = $derived(titleModes())
  const offline = $derived(!!ha.rooms && !ha.rooms.connected)
  let open = $state(false)
  let err = $state('')
  let busy = $state(false)

  $effect(() => {
    if (!open) return
    return onDismiss('.playinwrap', () => (open = false))
  })
  // A message belongs to the title it was about.
  $effect(() => {
    void itemId
    err = ''
  })

  async function run(m: HAMode) {
    open = false
    err = ''
    busy = true
    try {
      await startMode(m, itemId)
    } catch (e) {
      err = `${m.name}: ${e instanceof Error ? e.message : String(e)}`
    } finally {
      busy = false
    }
  }
  const running = (m: HAMode) => ha.runs[m.id]?.status === 'running'
</script>

{#if modes.length === 1}
  {@const m = modes[0]}
  <button class="dl" disabled={offline || busy || running(m)} onclick={() => run(m)}
    title={offline ? 'Home Assistant cannot be reached just now' : `Set the room up and play this in ${m.name}`}>
    <Clapperboard size={14} /> Play in {m.name}
  </button>
{:else if modes.length > 1}
  <div class="playinwrap">
    <button class="dl" disabled={offline || busy} aria-haspopup="menu" aria-expanded={open} onclick={() => (open = !open)}
      title={offline ? 'Home Assistant cannot be reached just now' : 'Set the room up and play this'}>
      <Clapperboard size={14} /> Play in… <ChevronDown size={14} />
    </button>
    {#if open}
      <div class="menu" role="menu">
        {#each modes as m (m.id)}
          <button class="item" role="menuitem" disabled={running(m)} onclick={() => run(m)}>{m.name}</button>
        {/each}
      </div>
    {/if}
  </div>
{/if}
{#if err}<p class="playin-err" role="alert">{err}</p>{/if}

<style>
  /* As the page's other secondary actions. */
  .dl {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--accent-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.55rem 1rem;
    font-size: 0.9rem;
  }
  .dl:disabled { opacity: 0.5; cursor: default; }
  .playinwrap { position: relative; flex: none; }
  .menu {
    position: absolute;
    left: 0;
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
    padding: 0.45rem 0.6rem;
    border-radius: 6px;
    color: var(--text);
    text-align: left;
    font-size: 0.9rem;
    white-space: nowrap;
  }
  .item:hover:not(:disabled) { background: var(--bg); }
  .item:disabled { opacity: 0.5; cursor: default; }
  .playin-err {
    flex-basis: 100%;
    margin: 0;
    color: var(--err);
    font-size: 0.85rem;
  }
</style>
