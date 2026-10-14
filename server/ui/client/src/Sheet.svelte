<!--
  A panel over everything else: centred on a wide screen, a sheet from the
  bottom edge on a phone. It closes on its X, a click on the dimmed page
  behind it, or Escape, and keeps keys to itself while open, so the
  player's shortcuts (space, arrows, f) do not fire behind it.

  Fixed to the viewport rather than placed inside whatever opened it: with
  the page fullscreen the viewport is the screen, so it shows over the
  player there too.
-->
<script lang="ts">
  import { onMount, type Snippet } from 'svelte'
  import { X } from '@lucide/svelte'

  let {
    title,
    onclose,
    wide = false,
    flush = false,
    actions,
    children,
  }: {
    title: string
    onclose: () => void
    /** Room for a picture or a grid, rather than a form. */
    wide?: boolean
    /** No padding round the content, for a picture edge to edge. */
    flush?: boolean
    /** Buttons beside the close button. */
    actions?: Snippet
    children: Snippet
  } = $props()

  let panel: HTMLDivElement

  onMount(() => panel?.focus())

  function onKeydown(e: KeyboardEvent) {
    e.stopPropagation()
    if (e.key === 'Escape') {
      e.preventDefault()
      onclose()
    }
  }
</script>

<div class="sheet-back" role="presentation" onclick={onclose}></div>
<div class="sheet" class:wide role="dialog" aria-modal="true" aria-label={title} tabindex="-1"
  bind:this={panel} onkeydown={onKeydown}>
  <div class="sheet-head">
    <h2>{title}</h2>
    {@render actions?.()}
    <button class="sheet-x" onclick={onclose} aria-label="Close" title="Close"><X size={18} /></button>
  </div>
  <div class="sheet-body" class:flush>
    {@render children()}
  </div>
</div>

<style>
  .sheet-back {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--bg) 60%, transparent);
    z-index: 500;
  }
  .sheet {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(28rem, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    display: flex;
    flex-direction: column;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    z-index: 501;
    outline: none;
    text-align: left;
    overflow: hidden;
  }
  .sheet.wide { width: min(56rem, calc(100vw - 2rem)); }
  .sheet-head {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.75rem 0.75rem 0.5rem 1rem;
    flex: none;
  }
  h2 {
    flex: 1;
    min-width: 0;
    margin: 0;
    font-size: 1.05rem;
    color: var(--text);
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .sheet-x {
    display: grid;
    place-items: center;
    width: 2.25rem;
    height: 2.25rem;
    border-radius: 8px;
    color: var(--muted);
    flex: none;
  }
  .sheet-x:hover { color: var(--text); background: color-mix(in srgb, var(--text) 8%, transparent); }
  .sheet-body {
    padding: 0 1rem 1rem;
    overflow-y: auto;
    overscroll-behavior: contain;
    min-height: 0;
  }
  .sheet-body.flush { padding: 0; }
  /* A phone: a sheet up from the bottom edge, full width, clear of the
     home indicator. */
  @media (max-width: 40rem) {
    .sheet,
    .sheet.wide {
      top: auto;
      bottom: 0;
      left: 0;
      right: 0;
      transform: none;
      width: auto;
      max-height: 88dvh;
      border-radius: 16px 16px 0 0;
      border-bottom: none;
      padding-bottom: env(safe-area-inset-bottom);
    }
  }
</style>
