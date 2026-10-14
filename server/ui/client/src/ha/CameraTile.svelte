<!--
  A camera's snapshot, fetched again every ten seconds while it is on
  screen and the tab is in view. The next picture is loaded off screen and
  swapped in once it has arrived, so the tile never blanks between them.
-->
<script lang="ts">
  import { Cctv, Play } from '@lucide/svelte'
  import { haSnapshotURL, type HAEntity } from '../lib/api'

  let { entity, disabled = false, every = 10_000, onopen }: {
    entity: HAEntity
    /** Home Assistant is unreachable: the last picture stays, nothing refreshes. */
    disabled?: boolean
    every?: number
    onopen: (e: HAEntity) => void
  } = $props()

  let tile: HTMLButtonElement
  let src = $state('')
  let failed = $state(false)
  let onScreen = $state(false)
  let tabShown = $state(typeof document === 'undefined' || !document.hidden)

  const live = $derived(onScreen && tabShown && !disabled && entity.available)

  function refresh() {
    const next = `${haSnapshotURL(entity.entityId)}?t=${Date.now()}`
    const img = new Image()
    img.onload = () => {
      src = next
      failed = false
    }
    img.onerror = () => (failed = true)
    img.src = next
  }

  $effect(() => {
    const io = new IntersectionObserver((entries) => {
      onScreen = entries.some((e) => e.isIntersecting)
    })
    io.observe(tile)
    const onVis = () => (tabShown = !document.hidden)
    document.addEventListener('visibilitychange', onVis)
    return () => {
      io.disconnect()
      document.removeEventListener('visibilitychange', onVis)
    }
  })

  $effect(() => {
    if (!live) return
    refresh()
    const t = setInterval(refresh, every)
    return () => clearInterval(t)
  })
</script>

<button class="cam" class:unavailable={!entity.available} bind:this={tile}
  disabled={!entity.available} onclick={() => onopen(entity)}
  aria-label="{entity.name}: {entity.stream ? 'watch live' : 'open'}">
  {#if src}
    <img {src} alt="" />
  {:else}
    <span class="ph"><Cctv size={26} /></span>
  {/if}
  <span class="cam-foot">
    <span class="cam-name">{entity.name}</span>
    {#if !entity.available}<span class="cam-state">Unavailable</span>
    {:else if failed && !src}<span class="cam-state">No picture</span>
    {:else if entity.stream}<span class="cam-live"><Play size={12} /> Live</span>{/if}
  </span>
</button>

<style>
  .cam {
    position: relative;
    display: block;
    width: 100%;
    aspect-ratio: 16 / 9;
    border-radius: 12px;
    overflow: hidden;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    text-align: left;
  }
  .cam:hover:not(:disabled) { border-color: var(--accent); }
  .cam.unavailable { opacity: 0.5; cursor: default; }
  img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .ph { position: absolute; inset: 0; display: grid; place-items: center; color: var(--muted); }
  .cam-foot {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    display: flex;
    align-items: center;
    gap: 0.5rem;
    padding: 0.35rem 0.6rem;
    background: color-mix(in srgb, var(--panel) 82%, transparent);
    font-size: 0.82rem;
  }
  .cam-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .cam-state { color: var(--muted); flex: none; }
  .cam-live { display: inline-flex; align-items: center; gap: 0.2rem; color: var(--accent-2); flex: none; }
</style>
