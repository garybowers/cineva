<script lang="ts">
  // A grid of album covers, or of artists' pictures: the card opens it and
  // a play button on the cover plays it, visible on hover and on focus
  // (a D-pad has no hover).
  import { Play, Music as MusicIcon, MicVocal } from '@lucide/svelte'
  import type { Item } from '../../lib/api'

  let {
    items,
    onopen,
    onplay,
    showArtist = true,
    small = false,
  }: {
    items: Item[]
    onopen: (item: Item) => void
    onplay?: (item: Item) => void
    /** Under an album: its artist; always its year. */
    showArtist?: boolean
    small?: boolean
  } = $props()

  function sub(it: Item): string {
    if (it.kind === 'artist') return it.childCount ? (it.childCount === 1 ? '1 album' : `${it.childCount} albums`) : ''
    return [showArtist ? it.artist : '', it.year ? String(it.year) : ''].filter(Boolean).join(' · ')
  }
</script>

<div class="grid" class:small data-nav-group="albums">
  {#each items as it (it.id)}
    <div class="card" class:round={it.kind === 'artist'} data-nav-id={it.id}>
      <div class="cwrap">
        <button class="cover" onclick={() => onopen(it)} aria-label={it.title}>
          {#if it.poster}
            <img src={it.poster} alt="" loading="lazy" />
          {:else}
            <span class="ph">{#if it.kind === 'artist'}<MicVocal size={28} />{:else}<MusicIcon size={28} />{/if}</span>
          {/if}
        </button>
        {#if onplay}
          <button class="cardplay" title="Play {it.title}" aria-label="Play {it.title}" onclick={() => onplay(it)}><Play size={16} /></button>
        {/if}
      </div>
      <button class="cname" onclick={() => onopen(it)}>{it.title}</button>
      {#if sub(it)}<span class="csub">{sub(it)}</span>{/if}
    </div>
  {/each}
</div>

<style>
  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr)); gap: 1rem 0.9rem; }
  .grid.small { grid-template-columns: repeat(auto-fill, minmax(7rem, 1fr)); gap: 0.8rem 0.7rem; }
  .card { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
  .cwrap { position: relative; }
  .cover { display: block; width: 100%; padding: 0; border-radius: 10px; overflow: hidden; aspect-ratio: 1; border: 1px solid var(--border); background: var(--panel); }
  .round .cover { border-radius: 50%; }
  .round .cname { text-align: center; }
  .round .csub { text-align: center; }
  .cover img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.15s ease; }
  .cover .ph { width: 100%; height: 100%; display: grid; place-items: center; color: var(--muted); }
  .card:hover .cover, .cover:focus-visible { border-color: var(--accent); }
  .card:hover .cover img { transform: scale(1.03); }
  .cardplay {
    position: absolute; right: 0.45rem; bottom: 0.45rem; width: 2.4rem; height: 2.4rem;
    display: grid; place-items: center; border-radius: 50%;
    background: color-mix(in srgb, var(--accent) 85%, var(--panel)); color: var(--on-accent, var(--text));
    opacity: 0; transition: opacity 0.15s ease;
  }
  .round .cardplay { right: 0.2rem; bottom: 0.2rem; }
  .card:hover .cardplay, .cardplay:focus-visible, .card:focus-within .cardplay { opacity: 1; }
  @media (hover: none) { .cardplay { opacity: 1; width: 2rem; height: 2rem; } }
  .cname { text-align: left; padding: 0; font-size: 0.86rem; line-height: 1.25; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .cname:hover { color: var(--accent-2); }
  .csub { font-size: 0.75rem; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  @media (min-width: 48rem) { .grid { grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr)); } }
  :global(html.tv) .cover:focus { outline: none; transform: scale(1.06); border-color: var(--accent-2); box-shadow: 0 0 0 0.18rem var(--accent-2); }
</style>
