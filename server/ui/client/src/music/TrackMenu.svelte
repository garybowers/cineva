<script lang="ts">
  // The ⋯ menu on a track row: queue it, put it on a playlist, or go to its
  // album or artist. The same menu wherever a track is listed.
  import { Ellipsis, ListPlus, ListMusic, Disc3, MicVocal, Trash2, ArrowUp, ArrowDown } from '@lucide/svelte'
  import { onDismiss } from '../lib/dismiss'
  import { itemDetail, type Item } from '../lib/api'
  import { music, addToPlaylist } from './actions.svelte'

  let {
    track,
    album = true,
    artist = true,
    onremove,
    onmove,
  }: {
    track: Item
    /** Offer "Go to album" (not on the album's own page). */
    album?: boolean
    /** Offer "Go to artist". */
    artist?: boolean
    /** On a playlist: take this entry out. */
    onremove?: () => void
    /** On a playlist: move this entry up or down one place. */
    onmove?: (delta: -1 | 1) => void
  } = $props()

  let open = $state(false)
  const uid = `tm-${Math.random().toString(36).slice(2, 9)}`

  $effect(() => {
    if (!open) return
    return onDismiss(`[data-menu="${uid}"]`, () => (open = false))
  })

  function act(f: () => void) {
    open = false
    f()
  }

  // A track carries only its album's id; the album's artist is its parent,
  // which the app resolves.
  async function goToArtist() {
    if (!track.parentId) return
    try {
      const album = (await itemDetail(track.parentId)).item
      if (album.parentId) music().openArtist({ id: album.parentId, libraryId: track.libraryId })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<span class="tmenu" data-menu={uid}>
  <button class="tm-btn" title="More" aria-label="More for {track.title}" aria-haspopup="menu" aria-expanded={open}
    onclick={(e) => { e.stopPropagation(); open = !open }}>
    <Ellipsis size={18} />
  </button>
  {#if open}
    <div class="tm-pop" role="menu">
      <button class="tm-item" role="menuitem" onclick={() => act(() => music().queue([track]))}><ListPlus size={15} /> Add to queue</button>
      <button class="tm-item" role="menuitem" onclick={() => act(() => addToPlaylist([track.id], track.title))}><ListMusic size={15} /> Add to playlist…</button>
      {#if onmove}
        <button class="tm-item" role="menuitem" onclick={() => act(() => onmove?.(-1))}><ArrowUp size={15} /> Move up</button>
        <button class="tm-item" role="menuitem" onclick={() => act(() => onmove?.(1))}><ArrowDown size={15} /> Move down</button>
      {/if}
      {#if album && track.parentId}
        <button class="tm-item" role="menuitem"
          onclick={() => act(() => music().openAlbum({ id: track.parentId!, libraryId: track.libraryId }))}>
          <Disc3 size={15} /> Go to album
        </button>
      {/if}
      {#if artist}
        <button class="tm-item" role="menuitem" onclick={() => act(goToArtist)}><MicVocal size={15} /> Go to artist</button>
      {/if}
      {#if onremove}
        <button class="tm-item danger" role="menuitem" onclick={() => act(() => onremove?.())}><Trash2 size={15} /> Remove from playlist</button>
      {/if}
    </div>
  {/if}
</span>

<style>
  .tmenu { position: relative; flex: none; display: inline-flex; }
  .tm-btn {
    display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 50%;
    color: var(--muted);
  }
  .tm-btn:hover, .tm-btn[aria-expanded='true'] { color: var(--text); background: var(--bg); }
  .tm-pop {
    position: absolute; right: 0; top: calc(100% + 0.25rem); z-index: 70; min-width: 12.5rem;
    background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.35rem;
    display: flex; flex-direction: column;
    box-shadow: 0 8px 24px color-mix(in srgb, var(--bg) 70%, transparent);
  }
  .tm-item {
    display: flex; align-items: center; gap: 0.55rem; padding: 0.5rem 0.6rem; border-radius: 6px;
    text-align: left; font-size: 0.88rem; color: var(--text); white-space: nowrap;
  }
  .tm-item:hover { background: var(--bg); }
  .tm-item.danger { color: var(--err); }
</style>
