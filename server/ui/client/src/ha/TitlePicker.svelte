<!--
  "Which title?" for a mode that plays the title it is started from, when
  it is started from somewhere with no title of its own (the Rooms page).
  Searches the library as the top bar's search does.
-->
<script lang="ts">
  import { Clapperboard, Tv, Music } from '@lucide/svelte'
  import { searchItems, type Item } from '../lib/api'
  import Sheet from '../Sheet.svelte'

  let { title, onpick, onclose }: {
    /** What is asking: "Movie mode". */
    title: string
    onpick: (item: Item) => void
    onclose: () => void
  } = $props()

  // Titles a play step can start; a series or an artist is a page, not
  // something that plays.
  const PLAYABLE = new Set<Item['kind']>(['movie', 'episode', 'track'])

  let q = $state('')
  let results: Item[] = $state([])
  let searched = $state(false)
  let err = $state('')
  let timer: ReturnType<typeof setTimeout> | undefined
  let input: HTMLInputElement | undefined = $state()

  $effect(() => {
    // After the sheet takes focus for itself.
    const t = setTimeout(() => input?.focus(), 30)
    return () => clearTimeout(t)
  })

  function onInput() {
    clearTimeout(timer)
    const term = q.trim()
    err = ''
    if (term.length < 2) {
      results = []
      searched = false
      return
    }
    timer = setTimeout(async () => {
      try {
        const r = await searchItems(term)
        if (q.trim() !== term) return
        results = r.filter((i) => PLAYABLE.has(i.kind))
        searched = true
      } catch (e) {
        err = e instanceof Error ? e.message : String(e)
      }
    }, 250)
  }

  const code = (i: Item) => `S${String(i.seasonNum).padStart(2, '0')}E${String(i.episodeNum).padStart(2, '0')}`
  function sub(i: Item): string {
    if (i.kind === 'episode') return code(i)
    if (i.kind === 'track') return i.artist ?? 'Track'
    return i.year ? String(i.year) : 'Film'
  }
</script>

<Sheet title="{title}: which title?" {onclose}>
  <input class="q" type="search" placeholder="Search for a film or episode" aria-label="Search for a title"
    bind:this={input} bind:value={q} oninput={onInput} />
  {#if err}<p class="note err" role="alert">{err}</p>{/if}
  {#if results.length > 0}
    <ul class="res">
      {#each results as r (r.id)}
        <li>
          <button class="pick" onclick={() => onpick(r)}>
            <span class="art" class:wide={r.kind === 'episode'} class:square={r.kind === 'track'}>
              {#if r.poster}<img src={r.poster} alt="" loading="lazy" />
              {:else if r.kind === 'track'}<Music size={16} />
              {:else if r.kind === 'episode'}<Tv size={16} />
              {:else}<Clapperboard size={16} />{/if}
            </span>
            <span class="txt">
              <span class="t">{r.title}</span>
              <span class="s">{sub(r)}</span>
            </span>
          </button>
        </li>
      {/each}
    </ul>
  {:else if searched}
    <p class="note">Nothing here by that name.</p>
  {:else}
    <p class="note">This mode plays a title. Find the one to play.</p>
  {/if}
</Sheet>

<style>
  .q {
    width: 100%;
    padding: 0.6rem 0.75rem;
    border-radius: 9px;
    border: 1px solid var(--border);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-size: 0.95rem;
  }
  .note { margin: 0.75rem 0 0; color: var(--muted); font-size: 0.88rem; }
  .note.err { color: var(--err); }
  .res { list-style: none; margin: 0.6rem 0 0; padding: 0; display: flex; flex-direction: column; gap: 2px; }
  .pick {
    width: 100%;
    display: flex;
    align-items: center;
    gap: 0.75rem;
    padding: 0.45rem 0.5rem;
    border-radius: 8px;
    text-align: left;
    color: var(--text);
  }
  .pick:hover, .pick:focus-visible { background: color-mix(in srgb, var(--text) 7%, transparent); }
  .art {
    width: 2.2rem;
    aspect-ratio: 2 / 3;
    border-radius: 4px;
    overflow: hidden;
    flex: none;
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--muted);
  }
  .art.wide { width: 3.6rem; aspect-ratio: 16 / 9; }
  .art.square { aspect-ratio: 1; }
  .art img { width: 100%; height: 100%; object-fit: cover; }
  .txt { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .t { font-size: 0.92rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .s { font-size: 0.8rem; color: var(--muted); }
</style>
