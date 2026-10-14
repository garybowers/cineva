<script lang="ts">
  // A dialogue over the page: a title bar with a close button, a body that
  // scrolls when it is tall, and a footer that stays put for the actions.
  // A form in the body names its id so a footer button can submit it
  // (<button form="…">). Escape and the close button call onclose; a click
  // on the backdrop does not, so a stray click cannot lose a filled form.
  import { onMount, type Snippet } from 'svelte'
  import { X } from '@lucide/svelte'

  let {
    title,
    onclose,
    children,
    foot,
  }: { title: string; onclose: () => void; children: Snippet; foot?: Snippet } = $props()

  const titleId = `modal-${Math.random().toString(36).slice(2)}`
  let box: HTMLDivElement

  // Focus moves into the dialogue on open and back to whatever opened it
  // on close, so keyboard users are not dropped at the top of the page.
  onMount(() => {
    const opener = document.activeElement as HTMLElement | null
    box?.querySelector<HTMLElement>('input:not([type=hidden]), select, textarea, .body button')?.focus()
    // Only when focus would otherwise be lost: a dialogue that replaces
    // this one (the Add steps) has already focused its own field.
    return () => {
      const at = document.activeElement
      if (!at || at === document.body || box.contains(at)) opener?.focus()
    }
  })
</script>

<svelte:window onkeydown={(e) => e.key === 'Escape' && onclose()} />

<div class="back" role="presentation"></div>
<div class="box" role="dialog" aria-modal="true" aria-labelledby={titleId} bind:this={box}>
  <header>
    <h2 id={titleId}>{title}</h2>
    <button type="button" class="close" aria-label="Close" onclick={onclose}><X size={18} /></button>
  </header>
  <div class="body">{@render children()}</div>
  {#if foot}<footer>{@render foot()}</footer>{/if}
</div>

<style>
  .back {
    position: fixed;
    inset: 0;
    z-index: 40;
    background: rgb(0 0 0 / 0.55);
  }
  .box {
    position: fixed;
    z-index: 41;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(34rem, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    display: flex;
    flex-direction: column;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: 0 1.5rem 3rem rgb(0 0 0 / 0.35);
  }
  header {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.85rem 1rem 0.85rem 1.25rem;
    border-bottom: 1px solid var(--border);
  }
  h2 { font-size: 1.05rem; font-weight: 600; overflow-wrap: anywhere; }
  .close {
    display: grid;
    place-items: center;
    width: 2rem;
    height: 2rem;
    flex: none;
    border-radius: 8px;
    color: var(--muted);
  }
  .close:hover { color: var(--text); background: var(--bg); }
  .body { padding: 1rem 1.25rem; overflow-y: auto; }
  footer {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    justify-content: flex-end;
    gap: 0.5rem;
    padding: 0.8rem 1.25rem;
    border-top: 1px solid var(--border);
  }
</style>
