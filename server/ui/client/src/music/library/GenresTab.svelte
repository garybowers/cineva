<script lang="ts">
  // Genres: a tile per genre with its album and track counts; a tile opens
  // the albums in it.
  import { Tag } from '@lucide/svelte'
  import { libraryGenres, type GenreCount } from '../../lib/api'

  let { libraryId, onpick }: { libraryId: string; onpick: (genre: string) => void } = $props()

  let genres = $state<GenreCount[]>([])
  let error = $state('')
  $effect(() => {
    const id = libraryId
    libraryGenres(id)
      .then((g) => { if (id === libraryId) genres = g })
      .catch((err) => (error = err instanceof Error ? err.message : String(err)))
  })
</script>

{#if error}<p class="err">{error}</p>{/if}
<div class="tiles" data-nav-group="genres">
  {#each genres as g (g.genre)}
    <button class="tile" onclick={() => onpick(g.genre)} data-nav-id={g.genre}>
      <span class="icon"><Tag size={18} /></span>
      <span class="name">{g.genre}</span>
      <span class="sub">{g.count === 1 ? '1 album' : `${g.count} albums`}{g.tracks ? ` · ${g.tracks} tracks` : ''}</span>
    </button>
  {:else}
    <p class="muted empty">No genres yet: they come from the files' tags on the next scan.</p>
  {/each}
</div>

<style>
  .tiles { display: grid; grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr)); gap: 0.8rem; }
  .tile {
    display: flex; flex-direction: column; align-items: flex-start; gap: 0.2rem; text-align: left;
    padding: 1rem; border-radius: 12px; border: 1px solid var(--border);
    background: linear-gradient(135deg, color-mix(in srgb, var(--accent) 18%, var(--panel)), var(--panel));
    min-height: 6rem;
  }
  .tile:hover { border-color: var(--accent); }
  .icon { color: var(--accent-2); }
  .name { font-weight: 600; font-size: 1rem; margin-top: 0.4rem; }
  .sub { color: var(--muted); font-size: 0.78rem; }
  .err { color: var(--err); }
  .muted { color: var(--muted); }
  .empty { grid-column: 1 / -1; padding: 2rem 0; text-align: center; }
  :global(html.tv) .tile:focus { outline: none; border-color: var(--accent-2); box-shadow: 0 0 0 0.18rem var(--accent-2); }
</style>
