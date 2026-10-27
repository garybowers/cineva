<script lang="ts">
  // Albums: a grid of covers with title, artist and year; a letter index on
  // the name sort; sorts; chips for a genre, a decade and compilations; a
  // search box. A page at a time.
  import { libraryAlbums, itemTracks, type Item } from '../../lib/api'
  import { music } from '../actions.svelte'
  import { play } from '../player/queue.svelte'
  import { createPager, nearEnd } from './paged.svelte'
  import AlbumGrid from './AlbumGrid.svelte'
  import LetterIndex from './LetterIndex.svelte'

  let {
    libraryId,
    genre = '',
    compilations = false,
    ongenre,
  }: {
    libraryId: string
    /** Narrowed to one genre (from the Genres tab). */
    genre?: string
    /** Only albums by various artists (the rail's Compilations). */
    compilations?: boolean
    /** The genre chip was cleared. */
    ongenre?: (genre: string) => void
  } = $props()

  const pager = createPager()
  let sort = $state('name')
  let letter = $state('')
  let decade = $state(0)
  let q = $state('')
  let typed = $state('')
  let timer: ReturnType<typeof setTimeout> | undefined
  function onInput(e: Event) {
    typed = (e.target as HTMLInputElement).value
    clearTimeout(timer)
    timer = setTimeout(() => (q = typed.trim()), 250)
  }
  $effect(() => {
    const id = libraryId, s = sort, l = letter, query = q, g = genre, d = decade, c = compilations
    pager.start((page) => libraryAlbums(id, { sort: s, letter: l, q: query, genre: g, decade: d || undefined, compilations: c, page }))
  })
  const decades = (() => {
    const out: number[] = []
    for (let y = Math.floor(new Date().getFullYear() / 10) * 10; y >= 1950; y -= 10) out.push(y)
    return out
  })()

  async function playAlbum(al: Item) {
    try {
      play(await itemTracks(al.id), 0, { source: { kind: 'album', id: al.id, title: al.title } })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<div class="tab">
  <div class="toolbar">
    <input class="box" type="search" placeholder="Find an album…" value={typed} oninput={onInput} aria-label="Find an album" />
    <label class="sortl">Sort
      <select class="sort" bind:value={sort} aria-label="Sort albums">
        <option value="name">Name</option>
        <option value="artist">Artist</option>
        <option value="year">Year</option>
        <option value="added">Recently added</option>
        <option value="played">Recently played</option>
        <option value="plays">Most played</option>
        <option value="rating">Highest rated</option>
        <option value="random">Random</option>
      </select>
    </label>
    <label class="sortl">Decade
      <select class="sort" bind:value={decade} aria-label="Decade">
        <option value={0}>Any</option>
        {#each decades as d (d)}<option value={d}>{d}s</option>{/each}
      </select>
    </label>
    {#if genre}
      <button class="chip" onclick={() => ongenre?.('')} title="Show every genre">{genre} <span aria-hidden="true">×</span></button>
    {/if}
    {#if compilations}<span class="chip static">Compilations</span>{/if}
    <span class="count muted">{pager.total === 1 ? '1 album' : `${pager.total} albums`}</span>
  </div>
  <div class="body">
    <div class="grid-wrap">
      {#if pager.error}<p class="err">{pager.error}</p>{/if}
      <AlbumGrid items={pager.items} onopen={(al) => music().openAlbum(al)} onplay={playAlbum} />
      {#if !pager.done}<div class="more" use:nearEnd={() => pager.more()}></div>{/if}
      {#if pager.done && pager.items.length === 0 && !pager.loading}<p class="muted empty">No albums here.</p>{/if}
    </div>
    {#if sort === 'name'}
      <LetterIndex index={pager.index} current={letter} onpick={(l) => (letter = l)} />
    {/if}
  </div>
</div>

<style>
  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 0.6rem; margin-bottom: 1rem; }
  .box { background: var(--panel); border: 1px solid var(--border); border-radius: 999px; color: var(--text); font: inherit; font-size: 0.86rem; padding: 0.4rem 0.9rem; width: 14rem; max-width: 100%; }
  .box:focus { outline: none; border-color: var(--accent); }
  .sortl { display: inline-flex; align-items: center; gap: 0.4rem; font-size: 0.82rem; color: var(--muted); }
  .sort { background: var(--panel); color: var(--text); border: 1px solid var(--border); border-radius: 8px; font: inherit; font-size: 0.84rem; padding: 0.3rem 0.5rem; }
  .chip { display: inline-flex; align-items: center; gap: 0.35rem; border-radius: 999px; padding: 0.3rem 0.8rem; font-size: 0.82rem; background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent-2); border: 1px solid transparent; }
  .chip:hover { border-color: var(--accent); }
  .chip.static { cursor: default; }
  .count { font-size: 0.82rem; margin-left: auto; }
  .muted { color: var(--muted); }
  .err { color: var(--err); }
  .empty { padding: 2rem 0; text-align: center; }
  .body { display: flex; gap: 0.8rem; align-items: flex-start; }
  .grid-wrap { flex: 1; min-width: 0; }
  .more { height: 1px; }
  @media (max-width: 767px) { .grid-wrap { padding-right: 1.6rem; } }
</style>
