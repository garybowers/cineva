<script lang="ts">
  // A music library: the artist rail down the side and, beside it, the
  // tabs (Listen, Artists, Albums, Songs, Genres, Playlists) or whatever
  // page is open (an artist's, an album's), which App hands in as `page`.
  import { Shuffle } from '@lucide/svelte'
  import type { Snippet } from 'svelte'
  import { libraryTracks, type Item, type Library } from '../../lib/api'
  import { music } from '../actions.svelte'
  import { play } from '../player/queue.svelte'
  import ArtistRail from './ArtistRail.svelte'
  import ListenTab from './ListenTab.svelte'
  import ArtistsTab from './ArtistsTab.svelte'
  import AlbumsTab from './AlbumsTab.svelte'
  import SongsTab from './SongsTab.svelte'
  import GenresTab from './GenresTab.svelte'

  export type MusicTab = 'listen' | 'artists' | 'albums' | 'songs' | 'genres'

  let {
    lib,
    tab = 'listen',
    genre = '',
    compilations = false,
    railCurrent = 'ALL',
    ontab,
    onartist,
    onplaylists,
    page = false,
    heading,
    children,
  }: {
    lib: Library
    tab?: MusicTab
    /** The Albums and Songs tabs narrowed to a genre. */
    genre?: string
    /** The Albums tab narrowed to compilations (the rail's entry). */
    compilations?: boolean
    /** What the rail highlights: an artist's id, 'ALL' or 'COMP'. */
    railCurrent?: string
    ontab: (tab: MusicTab, opts?: { genre?: string; compilations?: boolean }) => void
    onartist: (artist: Item) => void
    onplaylists: () => void
    /** An open page (artist, album) is in children, shown instead of the tabs. */
    page?: boolean
    /** The library's heading, shown above the tabs. It lives in the content
     *  column rather than above the whole block so the rail stays at the
     *  same height when a page replaces the tabs. */
    heading?: Snippet<[Library]>
    children?: Snippet
  } = $props()

  const tabs: { id: MusicTab | 'playlists'; label: string }[] = [
    { id: 'listen', label: 'Listen' }, { id: 'artists', label: 'Artists' }, { id: 'albums', label: 'Albums' },
    { id: 'songs', label: 'Songs' }, { id: 'genres', label: 'Genres' }, { id: 'playlists', label: 'Playlists' },
  ]

  async function shuffleLibrary() {
    try {
      const p = await libraryTracks(lib.id, { sort: 'random', size: 1000 })
      play(p.items, 0, { source: { kind: 'library', id: lib.id, title: lib.name }, shuffle: true })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<div class="music">
  <ArtistRail libraryId={lib.id} current={railCurrent}
    onall={() => ontab('albums', { compilations: false })}
    oncompilations={() => ontab('albums', { compilations: true })}
    onpick={onartist} />
  <section class="mcontent">
    {#if page}
      {@render children?.()}
    {:else}
      {@render heading?.(lib)}
      <header class="mhead">
        <nav class="subtabs" aria-label="{lib.name} views" data-nav-group="musictabs">
          {#each tabs as t (t.id)}
            <button class="subtab" class:on={t.id === tab}
              aria-current={t.id === tab ? 'page' : undefined}
              onclick={() => (t.id === 'playlists' ? onplaylists() : ontab(t.id))}>{t.label}</button>
          {/each}
        </nav>
        <button class="shuffle" onclick={shuffleLibrary} title="Shuffle the library"><Shuffle size={15} /> Shuffle</button>
      </header>
      {#if tab === 'listen'}
        <ListenTab libraryId={lib.id} {onplaylists} />
      {:else if tab === 'artists'}
        <ArtistsTab libraryId={lib.id} {genre} />
      {:else if tab === 'albums'}
        <AlbumsTab libraryId={lib.id} {genre} {compilations} ongenre={(g) => ontab('albums', { genre: g })} />
      {:else if tab === 'songs'}
        <SongsTab libraryId={lib.id} libraryName={lib.name} {genre} />
      {:else if tab === 'genres'}
        <GenresTab libraryId={lib.id} onpick={(g) => ontab('albums', { genre: g })} />
      {/if}
    {/if}
  </section>
</div>

<style>
  .music { display: flex; gap: 1.25rem; align-items: flex-start; }
  .mcontent { flex: 1; min-width: 0; }
  .mhead { display: flex; align-items: center; justify-content: space-between; gap: 1rem; margin-bottom: 0.9rem; flex-wrap: wrap; }
  .subtabs { display: flex; gap: 0.2rem; overflow-x: auto; scrollbar-width: none; max-width: 100%; }
  .subtab {
    padding: 0.45rem 0.9rem; border-radius: 999px; font-size: 0.9rem; color: var(--muted); white-space: nowrap;
    transition: background-color 0.15s ease, color 0.15s ease;
  }
  .subtab:hover { color: var(--text); background: var(--panel); }
  .subtab.on { color: var(--text); background: color-mix(in srgb, var(--accent) 18%, transparent); font-weight: 600; }
  :global(html.tv) .subtab:focus { outline: none; background: var(--accent); color: var(--on-accent); }
  .shuffle { display: inline-flex; align-items: center; gap: 0.4rem; border-radius: 999px; font-size: 0.86rem; padding: 0.4rem 0.9rem; border: 1px solid var(--border); background: var(--panel); color: var(--text); }
  .shuffle:hover { border-color: var(--accent); }
  @media (max-width: 767px) { .music { flex-direction: column; } }
</style>
