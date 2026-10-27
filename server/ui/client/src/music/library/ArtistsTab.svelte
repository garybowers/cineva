<script lang="ts">
  // Artists: a grid of pictures with name and album count, a letter index,
  // a sort and a search box, a page at a time.
  import { libraryArtists, itemTracks, type Item } from '../../lib/api'
  import { music } from '../actions.svelte'
  import { play } from '../player/queue.svelte'
  import { createPager, nearEnd } from './paged.svelte'
  import AlbumGrid from './AlbumGrid.svelte'
  import LetterIndex from './LetterIndex.svelte'

  let { libraryId, genre = '' }: { libraryId: string; genre?: string } = $props()

  const pager = createPager()
  let sort = $state('name')
  let letter = $state('')
  let q = $state('')
  let typed = $state('')
  let timer: ReturnType<typeof setTimeout> | undefined
  function onInput(e: Event) {
    typed = (e.target as HTMLInputElement).value
    clearTimeout(timer)
    timer = setTimeout(() => (q = typed.trim()), 250)
  }
  $effect(() => {
    const id = libraryId, s = sort, l = letter, query = q, g = genre
    pager.start((page) => libraryArtists(id, { sort: s, letter: l, q: query, genre: g, page }))
  })

  async function playArtist(a: Item) {
    try {
      play(await itemTracks(a.id), 0, { source: { kind: 'artist', id: a.id, title: a.title } })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<div class="tab">
  <div class="toolbar">
    <input class="box" type="search" placeholder="Find an artist…" value={typed} oninput={onInput} aria-label="Find an artist" />
    <label class="sortl">Sort
      <select class="sort" bind:value={sort} aria-label="Sort artists">
        <option value="name">Name</option>
        <option value="added">Recently added</option>
        <option value="plays">Most played</option>
        <option value="played">Recently played</option>
      </select>
    </label>
    <span class="count muted">{pager.total === 1 ? '1 artist' : `${pager.total} artists`}{genre ? ` in ${genre}` : ''}</span>
  </div>
  <div class="body">
    <div class="grid-wrap">
      {#if pager.error}<p class="err">{pager.error}</p>{/if}
      <AlbumGrid items={pager.items} onopen={(a) => music().openArtist(a)} onplay={playArtist} />
      {#if !pager.done}<div class="more" use:nearEnd={() => pager.more()}></div>{/if}
      {#if pager.done && pager.items.length === 0 && !pager.loading}<p class="muted empty">No artists here.</p>{/if}
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
  .count { font-size: 0.82rem; margin-left: auto; }
  .muted { color: var(--muted); }
  .err { color: var(--err); }
  .empty { padding: 2rem 0; text-align: center; }
  .body { display: flex; gap: 0.8rem; align-items: flex-start; }
  .grid-wrap { flex: 1; min-width: 0; }
  .more { height: 1px; }
  @media (max-width: 767px) { .grid-wrap { padding-right: 1.6rem; } }
</style>
