<script lang="ts">
  // "Download…" as a menu of formats rather than a <select>: a D-pad's
  // arrows change a focused select's value, and the change fired a download
  // by accident (docs/tv.md).
  import { Download } from '@lucide/svelte'
  import { onDismiss } from '../lib/dismiss'
  import { music } from './actions.svelte'

  let { id, label = 'Download', zip = true, compact = false }: {
    /** The track, album or artist. */
    id: string
    label?: string
    /** An album or artist comes as a ZIP. */
    zip?: boolean
    /** Icon only. */
    compact?: boolean
  } = $props()

  let open = $state(false)
  const uid = `dl-${Math.random().toString(36).slice(2, 9)}`
  $effect(() => {
    if (!open) return
    return onDismiss(`[data-menu="${uid}"]`, () => (open = false))
  })
  const suffix = $derived(zip ? ' (ZIP)' : '')
  const formats = [
    ['original', 'Original'], ['mp3-320', 'MP3 320'], ['mp3-192', 'MP3 192'], ['ogg-192', 'OGG 192'],
  ]
  function pick(f: string) {
    open = false
    music().download(id, f)
  }
</script>

<span class="dlwrap" data-menu={uid}>
  <button class="dl" class:compact aria-haspopup="menu" aria-expanded={open} title={label} aria-label={label}
    onclick={(e) => { e.stopPropagation(); open = !open }}>
    <Download size={14} />{#if !compact} {label}{/if}
  </button>
  {#if open}
    <div class="pop" role="menu" data-nav-scope>
      {#each formats as [f, name] (f)}
        <button class="item" role="menuitem" onclick={() => pick(f)}>{name}{suffix}</button>
      {/each}
    </div>
  {/if}
</span>

<style>
  .dlwrap { position: relative; display: inline-flex; }
  .dl {
    display: inline-flex; align-items: center; gap: 0.4rem; border-radius: 999px; font-size: 0.9rem;
    padding: 0.5rem 1rem; border: 1px solid var(--border); background: var(--panel); color: var(--text);
  }
  .dl:hover { border-color: var(--accent); }
  .dl.compact { padding: 0.5rem 0.65rem; }
  .pop {
    position: absolute; left: 0; top: calc(100% + 0.25rem); z-index: 70; min-width: 11rem;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.35rem;
    display: flex; flex-direction: column;
    box-shadow: 0 8px 24px color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .item { padding: 0.5rem 0.6rem; border-radius: 6px; text-align: left; font-size: 0.88rem; color: var(--text); white-space: nowrap; }
  .item:hover { background: var(--bg); }
  :global(html.tv) .item:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
