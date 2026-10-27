<script lang="ts">
  // The one track list: an album's tracks with their numbers and disc
  // headings, a ranked or searched list with thumbnails and the album
  // under each title, or the queue with what has played dimmed and rows
  // that can be moved and removed. Every row has the same stars and the
  // same menu, so a fix lands everywhere. Long lists grow as they scroll
  // rather than rendering a thousand rows at once.
  import { Music as MusicIcon, GripVertical } from '@lucide/svelte'
  import type { Item } from '../lib/api'
  import { trackTime, plays } from './actions.svelte'
  import Stars from './Stars.svelte'
  import TrackMenu from './TrackMenu.svelte'

  let {
    tracks,
    mode = 'list',
    currentId,
    currentIndex = -1,
    highlightId,
    albumArtist,
    onplay,
    onremove,
    onmove,
    menuAlbum,
    menuArtist = true,
  }: {
    tracks: Item[]
    /** album: numbered, disc headings; list: thumbnails, artist and album; queue: as list, with the played dimmed and rows movable. */
    mode?: 'album' | 'list' | 'queue'
    /** The track playing, marked in the list. */
    currentId?: string
    /** In queue mode, where the queue is: rows before it have played. */
    currentIndex?: number
    /** A row to pick out (an album opened on a song). */
    highlightId?: string
    /** In album mode: a track's own artist shows only when it differs. */
    albumArtist?: string
    onplay?: (index: number) => void
    onremove?: (index: number) => void
    onmove?: (from: number, to: number) => void
    /** Whether the menu offers "Go to album" (not on the album's own page). */
    menuAlbum?: boolean
    menuArtist?: boolean
  } = $props()

  const PAGE = 150
  let shown = $state(PAGE)
  $effect(() => {
    void tracks.length
    shown = PAGE
  })
  const visibleTracks = $derived(tracks.slice(0, shown))
  const discs = $derived(mode === 'album' && new Set(tracks.map((t) => t.discNum ?? 0)).size > 1)

  // More rows as the end comes into view.
  function grow(el: HTMLElement) {
    const io = new IntersectionObserver((entries) => {
      if (entries.some((e) => e.isIntersecting)) shown = Math.min(tracks.length, shown + PAGE)
    }, { rootMargin: '600px' })
    io.observe(el)
    return { destroy: () => io.disconnect() }
  }

  function sub(t: Item): string {
    if (mode === 'album') return t.artist && albumArtist && t.artist !== albumArtist ? t.artist : ''
    return [t.artist, t.album].filter(Boolean).join(' · ')
  }

  // Dragging a row to a new place, where there is a pointer; the menu's
  // Move up and Move down do the same without one.
  let dragFrom = $state<number | null>(null)
  let dragOver = $state<number | null>(null)
  function drop(to: number) {
    if (dragFrom !== null && dragFrom !== to) onmove?.(dragFrom, to)
    dragFrom = null
    dragOver = null
  }
</script>

<ol class="tl" class:album={mode === 'album'} class:queue={mode === 'queue'} data-nav-group="tracks">
  {#each visibleTracks as t, i (`${t.id}:${i}`)}
    {#if discs && (i === 0 || (tracks[i - 1].discNum ?? 0) !== (t.discNum ?? 0))}
      <li class="disc" aria-hidden="true">Disc {t.discNum || 1}</li>
    {/if}
    <li
      class="trackstars"
      class:current={t.id === currentId && (mode !== 'queue' || i === currentIndex)}
      class:hl={t.id === highlightId}
      class:played={mode === 'queue' && i < currentIndex}
      class:over={dragOver === i}
      draggable={!!onmove}
      ondragstart={() => (dragFrom = i)}
      ondragover={(e) => { if (onmove) { e.preventDefault(); dragOver = i } }}
      ondragleave={() => { if (dragOver === i) dragOver = null }}
      ondrop={(e) => { e.preventDefault(); drop(i) }}
      ondragend={() => { dragFrom = null; dragOver = null }}
    >
      {#if onmove}<span class="grip" aria-hidden="true"><GripVertical size={14} /></span>{/if}
      <button class="row" onclick={() => onplay?.(i)} data-nav-id={t.id} aria-current={t.id === currentId ? 'true' : undefined}>
        {#if mode === 'album'}
          <span class="tno">{t.trackNum || i + 1}</span>
        {:else if t.poster}
          <img class="thumb" src={t.poster} alt="" loading="lazy" />
        {:else}
          <span class="thumb ph"><MusicIcon size={14} /></span>
        {/if}
        <span class="tmain">
          <span class="ttitle">{t.title}</span>
          {#if sub(t)}<span class="tsub">{sub(t)}</span>{/if}
        </span>
        {#if t.playCount}<span class="tplays">{plays(t.playCount)}</span>{/if}
        <span class="ttime">{trackTime(t.runtimeSecs)}</span>
      </button>
      <Stars track={t} />
      <TrackMenu track={t} album={menuAlbum ?? mode !== 'album'} artist={menuArtist}
        onremove={onremove ? () => onremove(i) : undefined}
        onmove={onmove ? (d) => onmove(i, i + d) : undefined} />
    </li>
  {/each}
</ol>
{#if shown < tracks.length}
  <div class="more" use:grow aria-hidden="true"></div>
{/if}

<style>
  .tl { list-style: none; margin: 0; padding: 0; display: flex; flex-direction: column; gap: 0.1rem; }
  .tl li { display: flex; align-items: center; min-width: 0; border-radius: 8px; gap: 0.15rem; }
  .tl li:hover { background: var(--panel); }
  .tl li.current { background: color-mix(in srgb, var(--accent) 14%, transparent); }
  .tl li.hl { background: color-mix(in srgb, var(--accent) 14%, transparent); }
  .tl li.played { opacity: 0.55; }
  .tl li.over { outline: 2px solid var(--accent); outline-offset: -2px; }
  .disc {
    font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.06em; color: var(--muted);
    padding: 0.9rem 0.5rem 0.2rem;
  }
  .disc:hover { background: none; }
  .row { flex: 1; min-width: 0; display: flex; align-items: center; gap: 0.7rem; padding: 0.4rem 0.5rem; text-align: left; border-radius: 8px; }
  .row:hover .ttitle { color: var(--accent-2); }
  .current .ttitle { color: var(--accent-2); font-weight: 600; }
  .tno { width: 1.6rem; text-align: right; color: var(--muted); font-size: 0.82rem; font-variant-numeric: tabular-nums; flex: none; }
  .thumb { width: 2.6rem; height: 2.6rem; border-radius: 5px; object-fit: cover; flex: none; }
  .thumb.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); border: 1px solid var(--border); }
  .tmain { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .ttitle, .tsub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttitle { font-size: 0.92rem; }
  .tsub { font-size: 0.78rem; color: var(--muted); }
  .tplays { color: var(--muted); font-size: 0.78rem; white-space: nowrap; flex: none; }
  .ttime { color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; flex: none; width: 2.8rem; text-align: right; }
  .grip { color: var(--muted); cursor: grab; padding: 0 0.1rem 0 0.35rem; display: grid; place-items: center; }
  .more { height: 1px; }
  .album .row { padding: 0.5rem 0.5rem; }
  @media (max-width: 30rem) { .tplays { display: none; } }
  @media (hover: none) { .grip { display: none; } }
  :global(html.tv) .row:focus { outline: none; background: var(--accent); color: var(--on-accent); }
  :global(html.tv) .row:focus .ttitle, :global(html.tv) .row:focus .tsub, :global(html.tv) .row:focus .ttime,
  :global(html.tv) .row:focus .tno, :global(html.tv) .row:focus .tplays { color: var(--on-accent); }
</style>
