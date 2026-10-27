<script lang="ts">
  // The queue: the current track at the top, what follows below, what has
  // played dimmed above, with "Playing from" over it. Rows jump, move and
  // go; over the list, Clear, Shuffle and Save as playlist. A mix
  // (server/docs/mix.md) shows under its own heading, "Mix · from ...",
  // with what it was built on and a Stop, so what is coming is visible
  // before it plays.
  import { Shuffle, Trash2, ListMusic, Sparkles, X } from '@lucide/svelte'
  import { music, addToPlaylist } from '../actions.svelte'
  import { mq, current, jump, remove, move, setShuffle, clearUpcoming, stopMix, mixFrom, basisWords } from './queue.svelte'
  import TrackList from '../TrackList.svelte'

  const sourceLabel = $derived.by(() => {
    const s = mq.source
    if (!s) return ''
    const kinds: Record<string, string> = { album: 'album', artist: 'artist', playlist: 'playlist', genre: 'genre', library: 'library', search: 'search', mix: 'mix' }
    return s.title ? `${kinds[s.kind] ?? s.kind}: ${s.title}` : (kinds[s.kind] ?? s.kind)
  })
  function openSource() {
    const s = mq.source
    const t = current()
    if (!s?.id || !t) return
    if (s.kind === 'album') music().openAlbum({ id: s.id, libraryId: t.libraryId })
    else if (s.kind === 'artist') music().openArtist({ id: s.id, libraryId: t.libraryId })
    else if (s.kind === 'playlist') music().openPlaylist(s.id)
  }
  const remaining = $derived(Math.max(0, mq.items.length - mq.index - 1))

  // The mix's entries begin at from; before it the queue is the queue.
  const from = $derived(mixFrom())
  const split = $derived(!!mq.mix && from > 0 && from < mq.items.length)
  const mixName = $derived(mq.mix?.title ? `Mix · from ${mq.mix.title}` : 'Mix')
  const before = $derived(split ? mq.items.slice(0, from) : mq.items)
  const after = $derived(split ? mq.items.slice(from) : [])

  function openSeed() {
    const m = mq.mix
    const t = current()
    if (!m || !t) return
    if (m.kind === 'album') music().openAlbum({ id: m.seedId, libraryId: t.libraryId })
    else if (m.kind === 'artist') music().openArtist({ id: m.seedId, libraryId: t.libraryId })
    else music().openAlbum({ id: t.parentId ?? m.seedId, libraryId: t.libraryId })
  }
  function save() {
    const ids = mq.items.map((t) => t.id)
    if (mq.mix) addToPlaylist(ids, 'the mix', mq.mix.title ? `Mix from ${mq.mix.title}` : 'Mix')
    else addToPlaylist(ids, 'the queue')
  }
</script>

<div class="upnext">
  <header class="qhead">
    <div class="from">
      {#if mq.mix && !split}
        <span class="mixtag"><Sparkles size={13} /> {mixName}</span>
        <span class="muted basis">{basisWords(mq.mix.basis)}</span>
      {:else if sourceLabel}
        <span class="muted">Playing from</span>
        {#if mq.source?.id && mq.source.kind !== 'library' && mq.source.kind !== 'genre' && mq.source.kind !== 'mix'}
          <button class="link" onclick={openSource}>{sourceLabel}</button>
        {:else}
          <span>{sourceLabel}</span>
        {/if}
      {/if}
      <span class="muted count">{remaining === 1 ? '1 to come' : `${remaining} to come`}{mq.mixLoading ? ' · finding more…' : ''}</span>
    </div>
    <div class="qacts">
      <button class="btn" class:on={mq.shuffle} onclick={() => setShuffle(!mq.shuffle)} aria-pressed={mq.shuffle} title="Shuffle what is to come"><Shuffle size={14} /> Shuffle</button>
      <button class="btn" onclick={save} title={mq.mix ? 'Save the mix so far as a playlist' : 'Save the queue as a playlist'}><ListMusic size={14} /> Save</button>
      {#if mq.mix}
        <button class="btn" onclick={stopMix} title="Stop the mix: what it added and has not played goes"><X size={14} /> Stop mix</button>
      {/if}
      <button class="btn" onclick={clearUpcoming} disabled={remaining === 0} title="Clear what is to come"><Trash2 size={14} /> Clear</button>
    </div>
  </header>
  {#if split}
    <TrackList tracks={before} mode="queue" currentId={current()?.id} currentIndex={mq.index}
      onplay={jump} onremove={remove} onmove={move} />
    <div class="mixhead">
      <button class="mixtag link" onclick={openSeed} title="Go to where the mix came from"><Sparkles size={13} /> {mixName}</button>
      <span class="muted basis">{basisWords(mq.mix?.basis)}</span>
    </div>
    <TrackList tracks={after} mode="queue" currentId={current()?.id} currentIndex={mq.index - from}
      onplay={(i) => jump(i + from)} onremove={(i) => remove(i + from)} onmove={(a, b) => move(a + from, b + from)} />
  {:else}
    <TrackList tracks={mq.items} mode="queue" currentId={current()?.id} currentIndex={mq.index}
      onplay={jump} onremove={remove} onmove={move} />
  {/if}
</div>

<style>
  .qhead { display: flex; align-items: center; justify-content: space-between; gap: 0.8rem; flex-wrap: wrap; margin-bottom: 0.6rem; }
  .from { display: flex; align-items: baseline; gap: 0.4rem; flex-wrap: wrap; font-size: 0.88rem; min-width: 0; }
  .muted { color: var(--muted); }
  .count::before { content: '·'; margin-right: 0.4rem; }
  .basis::before { content: '·'; margin-right: 0.4rem; }
  .link { color: var(--accent-2); padding: 0; font-size: inherit; }
  .link:hover { text-decoration: underline; }
  .mixtag { display: inline-flex; align-items: center; gap: 0.3rem; font-weight: 600; color: var(--accent-2); }
  .mixhead { display: flex; align-items: baseline; gap: 0.4rem; flex-wrap: wrap; font-size: 0.88rem; margin: 0.9rem 0 0.4rem; padding-top: 0.7rem; border-top: 1px solid var(--border); }
  .qacts { display: flex; gap: 0.4rem; flex-wrap: wrap; }
  .btn { display: inline-flex; align-items: center; gap: 0.35rem; border-radius: 999px; font-size: 0.82rem; padding: 0.35rem 0.8rem; border: 1px solid var(--border); background: var(--panel); color: var(--text); }
  .btn:hover { border-color: var(--accent); }
  .btn.on { color: var(--accent-2); border-color: var(--accent); }
  .btn:disabled { opacity: 0.5; cursor: default; }
  :global(html.tv) .btn:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
