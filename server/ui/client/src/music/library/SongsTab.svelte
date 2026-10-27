<script lang="ts">
  // Songs: every track, a page at a time, with sorts, a letter index on
  // the title sort and a search box. Playing one queues what is loaded
  // from it on.
  import { Shuffle } from '@lucide/svelte'
  import { libraryTracks } from '../../lib/api'
  import { music } from '../actions.svelte'
  import { mq, play } from '../player/queue.svelte'
  import { createPager, nearEnd } from './paged.svelte'
  import TrackList from '../TrackList.svelte'
  import LetterIndex from './LetterIndex.svelte'

  let { libraryId, libraryName = '', genre = '' }: { libraryId: string; libraryName?: string; genre?: string } = $props()

  const pager = createPager()
  let sort = $state('title')
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
    pager.start((page) => libraryTracks(id, { sort: s, letter: l, q: query, genre: g, page, size: 100 }))
  })
  const source = $derived(genre
    ? { kind: 'genre', id: genre, title: genre }
    : { kind: 'library', id: libraryId, title: libraryName })
  const playing = $derived(mq.active ? mq.items[mq.index]?.id : undefined)

  async function shuffleAll() {
    try {
      const p = await libraryTracks(libraryId, { sort: 'random', size: 1000, genre: genre || undefined, q: q || undefined })
      play(p.items, 0, { source, shuffle: true })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<div class="tab">
  <div class="toolbar">
    <input class="box" type="search" placeholder="Find a song…" value={typed} oninput={onInput} aria-label="Find a song" />
    <label class="sortl">Sort
      <select class="sort" bind:value={sort} aria-label="Sort songs">
        <option value="title">Title</option>
        <option value="artist">Artist</option>
        <option value="album">Album</option>
        <option value="added">Recently added</option>
        <option value="played">Recently played</option>
        <option value="plays">Most played</option>
        <option value="rating">Highest rated</option>
      </select>
    </label>
    <button class="btn" onclick={shuffleAll} title="Shuffle a thousand of these"><Shuffle size={15} /> Shuffle</button>
    <span class="count muted">{pager.total === 1 ? '1 song' : `${pager.total} songs`}{genre ? ` in ${genre}` : ''}</span>
  </div>
  <div class="body">
    <div class="list-wrap">
      {#if pager.error}<p class="err">{pager.error}</p>{/if}
      <TrackList tracks={pager.items} mode="list" currentId={playing}
        onplay={(i) => play(pager.items, i, { source, shuffle: false })} />
      {#if !pager.done}<div class="more" use:nearEnd={() => pager.more()}></div>{/if}
      {#if pager.done && pager.items.length === 0 && !pager.loading}<p class="muted empty">No songs here.</p>{/if}
    </div>
    {#if sort === 'title'}
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
  .btn { display: inline-flex; align-items: center; gap: 0.4rem; border-radius: 999px; font-size: 0.86rem; padding: 0.4rem 0.9rem; border: 1px solid var(--border); background: var(--panel); color: var(--text); }
  .btn:hover { border-color: var(--accent); }
  .count { font-size: 0.82rem; margin-left: auto; }
  .muted { color: var(--muted); }
  .err { color: var(--err); }
  .empty { padding: 2rem 0; text-align: center; }
  .body { display: flex; gap: 0.8rem; align-items: flex-start; }
  .list-wrap { flex: 1; min-width: 0; }
  .more { height: 1px; }
  @media (max-width: 767px) { .list-wrap { padding-right: 1.6rem; } }
</style>
