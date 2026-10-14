<!--
  Modes as large buttons: "Movie mode" sets the room up and plays a film.
  A mode that plays the title it is started from uses the one given here
  (the player passes what is playing); without one it asks which, with a
  search. Each button says how its run is getting on.
-->
<script lang="ts">
  import { Clapperboard, LoaderCircle, Sparkles, TriangleAlert, Check } from '@lucide/svelte'
  import type { HAMode, HAModeRun, Item } from '../lib/api'
  import { ha, startMode } from '../lib/homeassistant.svelte'
  import TitlePicker from './TitlePicker.svelte'

  let { modes, itemId = '', disabled = false, onstart }: {
    modes: HAMode[]
    /** The title a mode that needs one plays; '' to ask. */
    itemId?: string
    /** Home Assistant is unreachable. */
    disabled?: boolean
    /** Told of each run started here, so the player can follow it. */
    onstart?: (mode: HAMode, runId: string) => void
  } = $props()

  let err = $state('')
  let starting = $state('')
  let picking: HAMode | null = $state(null)

  async function run(m: HAMode, id?: string) {
    err = ''
    starting = m.id
    try {
      const runId = await startMode(m, id)
      onstart?.(m, runId)
    } catch (e) {
      err = `${m.name}: ${e instanceof Error ? e.message : String(e)}`
    } finally {
      starting = ''
    }
  }

  function press(m: HAMode) {
    if (m.needsTitle && !itemId) {
      picking = m
      return
    }
    void run(m, m.needsTitle ? itemId : undefined)
  }

  function picked(item: Item) {
    const m = picking
    picking = null
    if (m) void run(m, item.id)
  }

  function runState(r: HAModeRun): string {
    if (r.status === 'done') return 'Done'
    if (r.status === 'failed') return r.error || 'Stopped'
    return r.step > 0 && r.steps > 0 ? `Running step ${Math.min(r.step, r.steps)} of ${r.steps}` : 'Starting'
  }

  const busy = (m: HAMode) => starting === m.id || ha.runs[m.id]?.status === 'running'
</script>

<div class="modes">
  {#each modes as m (m.id)}
    {@const r = ha.runs[m.id]}
    <button class="mode" class:running={busy(m)} disabled={disabled || busy(m)} onclick={() => press(m)}>
      <span class="mode-icon">
        {#if busy(m)}<LoaderCircle size={20} class="spin" />
        {:else if r?.status === 'done'}<Check size={20} />
        {:else if r?.status === 'failed'}<TriangleAlert size={20} />
        {:else if m.needsTitle}<Clapperboard size={20} />
        {:else}<Sparkles size={20} />{/if}
      </span>
      <span class="mode-text">
        <span class="mode-name">{m.name}</span>
        <span class="mode-line" class:bad={r?.status === 'failed'}>
          {#if r}{runState(r)}
          {:else if m.needsTitle && !itemId}Choose a title
          {:else}{m.steps.length} {m.steps.length === 1 ? 'step' : 'steps'}{/if}
        </span>
      </span>
    </button>
  {/each}
</div>
{#if err}<p class="mode-err" role="alert">{err}</p>{/if}

{#if picking}
  <TitlePicker title={picking.name} onpick={picked} onclose={() => (picking = null)} />
{/if}

<style>
  .modes {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(min(100%, 13rem), 1fr));
    gap: 0.6rem;
  }
  .mode {
    display: flex;
    align-items: center;
    gap: 0.8rem;
    min-height: 4.25rem;
    padding: 0.8rem 1rem;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    text-align: left;
  }
  .mode:hover:not(:disabled) { border-color: var(--accent); }
  .mode:disabled { cursor: default; }
  .mode:disabled:not(.running) { opacity: 0.5; }
  .mode-icon {
    display: grid;
    place-items: center;
    width: 2.6rem;
    height: 2.6rem;
    border-radius: 50%;
    flex: none;
    color: var(--on-accent);
    background: var(--accent);
  }
  .mode-text { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.1rem; }
  .mode-name { font-weight: 600; font-size: 1rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .mode-line { font-size: 0.8rem; color: var(--muted); }
  .mode-line.bad { color: var(--err); }
  .mode-err { margin: 0.5rem 0 0; color: var(--err); font-size: 0.85rem; }
  .mode-icon :global(.spin) { animation: spin 1s linear infinite; }
  @keyframes spin { to { transform: rotate(360deg); } }
  @media (prefers-reduced-motion: reduce) {
    .mode-icon :global(.spin) { animation: none; }
  }
</style>
