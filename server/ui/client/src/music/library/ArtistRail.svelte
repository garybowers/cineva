<script lang="ts">
  // The artist rail: every artist's name down the side of the music
  // library, beside every tab and beside an open artist's or album's page,
  // so moving between artists is one click. Fed a page at a time by the
  // artists listing, with its own filter box.
  import { libraryArtists, type Item } from '../../lib/api'
  import { createPager, nearEnd } from './paged.svelte'

  let {
    libraryId,
    current = '',
    onall,
    oncompilations,
    onpick,
    compilations = true,
  }: {
    libraryId: string
    /** The highlighted entry: an artist's id, 'ALL' or 'COMP'. */
    current?: string
    onall: () => void
    oncompilations: () => void
    onpick: (artist: Item) => void
    compilations?: boolean
  } = $props()

  const pager = createPager()
  let filter = $state('')
  let timer: ReturnType<typeof setTimeout> | undefined
  let q = $state('')
  function onInput(e: Event) {
    const v = (e.target as HTMLInputElement).value
    filter = v
    clearTimeout(timer)
    timer = setTimeout(() => (q = v.trim()), 200)
  }
  $effect(() => {
    const id = libraryId
    const query = q
    pager.start((page) => libraryArtists(id, { q: query, sort: 'name', page, size: 120 }))
  })
</script>

<aside class="mrail" data-nav-group="rail">
  <div class="rail-top">
    <input
      class="rail-filter"
      type="search"
      placeholder="Filter artists…"
      value={filter}
      oninput={onInput}
      onkeydown={(e) => { if (e.key === 'Escape') { filter = ''; q = ''; (e.target as HTMLInputElement).value = '' } }}
      aria-label="Filter artists"
    />
    <button class="rail-item" class:sel={current === 'ALL'} onclick={onall} aria-current={current === 'ALL' ? 'page' : undefined}>
      All artists <span class="rail-n">{pager.total || ''}</span>
    </button>
    {#if compilations}
      <button class="rail-item" class:sel={current === 'COMP'} onclick={oncompilations} aria-current={current === 'COMP' ? 'page' : undefined}>Compilations</button>
    {/if}
    <div class="rail-div"></div>
  </div>
  {#each pager.items as a (a.id)}
    <button class="rail-item" class:sel={current === a.id} onclick={() => onpick(a)} data-nav-id={a.id} aria-current={current === a.id ? 'page' : undefined}>{a.title}</button>
  {/each}
  {#if !pager.done}
    <div class="rail-more" use:nearEnd={() => pager.more()}></div>
  {:else if pager.items.length === 0 && !pager.loading}
    <div class="rail-empty muted">No artists match</div>
  {/if}
</aside>

<style>
  .mrail {
    flex: none;
    width: 12.5rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.5rem;
    position: sticky;
    top: calc(var(--topbar-h, 3.4rem) + var(--music-bar, 0rem) + 1rem);
    max-height: calc(100vh - var(--topbar-h, 3.4rem) - var(--music-bar, 0rem) - 2rem);
    overflow-y: auto;
  }
  .rail-top { position: sticky; top: 0; z-index: 1; background: var(--panel); padding-bottom: 0.15rem; margin-bottom: 0.15rem; }
  .rail-filter {
    width: 100%; background: var(--bg); border: 1px solid var(--border); border-radius: 999px;
    color: var(--text); font: inherit; font-size: 0.82rem; padding: 0.35rem 0.8rem; margin-bottom: 0.4rem;
  }
  .rail-filter:focus { outline: none; border-color: var(--accent); }
  .rail-empty { padding: 0.5rem 0.7rem; font-size: 0.85rem; }
  .rail-more { height: 1px; }
  .rail-item {
    display: flex; justify-content: space-between; align-items: center; gap: 0.5rem;
    width: 100%; text-align: left; padding: 0.45rem 0.7rem; border-radius: 8px; font-size: 0.9rem; color: var(--text);
  }
  .rail-item:hover { background: var(--bg); }
  .rail-item.sel { background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent-2); font-weight: 600; }
  .rail-n { color: var(--muted); font-size: 0.75rem; font-weight: 400; }
  .rail-div { height: 1px; background: var(--border); margin: 0.4rem 0.3rem; }
  .muted { color: var(--muted); }
  @media (max-width: 767px) {
    .mrail { position: static; width: 100%; max-height: none; display: flex; overflow-x: auto; gap: 0.25rem; padding: 0.4rem; }
    .rail-top { position: static; display: flex; gap: 0.25rem; align-items: center; }
    .rail-filter { width: 9rem; margin: 0; }
    .rail-item { width: auto; white-space: nowrap; }
    .rail-div, .rail-empty { display: none; }
  }
  :global(html.tv) .rail-item:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
