<script lang="ts">
  // The A to Z down the side of a name-sorted grid: how many file under
  // each letter comes with the page, so the strip is drawn from counts and
  // a letter with nothing under it is dimmed. On a phone a touch strip; on
  // a TV a column the D-pad enters.
  let {
    index,
    current = '',
    onpick,
  }: {
    index: Record<string, number> | undefined
    current?: string
    onpick: (letter: string) => void
  } = $props()

  const LETTERS = ['#', ...'ABCDEFGHIJKLMNOPQRSTUVWXYZ']
  const has = (l: string) => (index?.[l] ?? 0) > 0

  // Sliding a finger down the strip picks as it goes.
  function onPointer(e: PointerEvent) {
    if (e.buttons === 0 && e.type !== 'pointerdown') return
    const el = document.elementFromPoint(e.clientX, e.clientY) as HTMLElement | null
    const l = el?.dataset.letter
    if (l && has(l) && l !== current) onpick(l)
  }
</script>

<nav class="az" aria-label="Letters" data-nav-group="letters" onpointerdown={onPointer} onpointermove={onPointer}>
  <button class="azl" class:on={current === ''} onclick={() => onpick('')} title="All">All</button>
  {#each LETTERS as l (l)}
    <button class="azl" class:on={current.toUpperCase() === l} class:none={!has(l)} data-letter={l}
      disabled={!has(l)} onclick={() => onpick(l)} aria-label="Starting with {l}">{l}</button>
  {/each}
</nav>

<style>
  .az {
    display: flex; flex-direction: column; align-items: center; gap: 0.05rem;
    position: sticky; top: calc(var(--topbar-h, 3.4rem) + var(--music-bar, 0rem) + 1rem);
    max-height: calc(100vh - var(--topbar-h, 3.4rem) - var(--music-bar, 0rem) - 2rem);
    overflow: hidden; flex: none; width: 1.7rem; touch-action: none; user-select: none;
  }
  .azl {
    width: 1.6rem; height: 1.2rem; display: grid; place-items: center; border-radius: 5px;
    font-size: 0.7rem; line-height: 1; color: var(--muted); padding: 0;
  }
  .azl:first-child { font-size: 0.62rem; height: 1.4rem; }
  .azl:hover:not(:disabled) { color: var(--text); background: var(--panel); }
  .azl.on { color: var(--on-accent); background: var(--accent); }
  .azl.none { opacity: 0.3; cursor: default; }
  @media (max-width: 767px) {
    .az { position: fixed; right: 0.15rem; top: calc(var(--topbar-h, 3.4rem) + var(--music-bar, 0rem) + 0.5rem); bottom: 4rem; max-height: none; z-index: 5; justify-content: center; }
    .azl { height: auto; flex: 1; min-height: 0; font-size: 0.6rem; }
  }
  :global(html.tv) .azl:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
