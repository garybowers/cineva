<script lang="ts">
  // The viewer's playlists, and one playlist open. Manual playlists are
  // arranged by hand — dragged on a desktop, moved with buttons on a phone;
  // smart ones are rules the server evaluates each time one is opened.
  import { Play, Shuffle, ListPlus, ListStart, Pencil, Trash2, Plus, Sparkles, GripVertical, ArrowUp, ArrowDown, Music as MusicIcon, ChevronLeft } from '@lucide/svelte'
  import {
    listPlaylists, playlistDetail, createPlaylist, updatePlaylist, deletePlaylist, setPlaylistTracks,
    type Playlist, type PlaylistDetail, type Item, type SmartRules,
  } from '../lib/api'
  import { music, trackTime, totalTime, plays, playedWhen } from './actions.svelte'
  import { describe, blankRules } from './smart'
  import Mosaic from './Mosaic.svelte'
  import TrackMenu from './TrackMenu.svelte'
  import Stars from './Stars.svelte'
  import SmartEditor from './SmartEditor.svelte'
  import Sheet from '../Sheet.svelte'

  let { playlistId, onopen }: { playlistId: string | null; onopen: (id: string | null) => void } = $props()

  let lists: Playlist[] = $state([])
  let loaded = $state(false)
  let open = $state<PlaylistDetail | null>(null)
  let error = $state('')
  let newName = $state('')
  let creating = $state(false)
  let editing = $state<{ name: string; rules: SmartRules; id?: string } | null>(null)
  let renaming = $state(false)
  let renameTo = $state('')
  let confirmDelete = $state(false)

  const fail = (err: unknown) => music().notify('error', err instanceof Error ? err.message : String(err))

  async function loadLists() {
    try {
      lists = await listPlaylists()
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    } finally {
      loaded = true
    }
  }

  $effect(() => {
    const id = playlistId
    renaming = false
    confirmDelete = false
    if (!id) {
      open = null
      loadLists()
      return
    }
    if (open?.playlist.id !== id) open = null
    playlistDetail(id)
      .then((d) => { if (playlistId === id) open = d })
      .catch((err) => { error = err instanceof Error ? err.message : String(err) })
  })

  async function createManual(e: SubmitEvent) {
    e.preventDefault()
    if (!newName.trim()) return
    try {
      const p = await createPlaylist(newName.trim())
      newName = ''
      creating = false
      onopen(p.id)
    } catch (err) {
      fail(err)
    }
  }

  async function saveSmart(name: string, rules: SmartRules) {
    if (editing?.id) {
      open = await updatePlaylist(editing.id, { name, smart: rules })
    } else {
      const p = await createPlaylist(name, rules)
      onopen(p.id)
    }
    editing = null
  }

  async function rename(e: SubmitEvent) {
    e.preventDefault()
    if (!open || !renameTo.trim()) return
    try {
      open = await updatePlaylist(open.playlist.id, { name: renameTo.trim() })
      renaming = false
    } catch (err) {
      fail(err)
    }
  }

  async function remove() {
    if (!open) return
    if (!confirmDelete) {
      confirmDelete = true
      return
    }
    try {
      await deletePlaylist(open.playlist.id)
      music().notify('info', `Deleted ${open.playlist.name}.`)
      onopen(null)
    } catch (err) {
      fail(err)
    }
  }

  // ---- Arranging a manual playlist ----
  // The new order shows at once and is saved shortly after, so a run of
  // moves is one request rather than one each.
  let saveTimer: ReturnType<typeof setTimeout> | undefined
  function arrange(tracks: Item[]) {
    if (!open) return
    open = { ...open, tracks }
    const id = open.playlist.id
    clearTimeout(saveTimer)
    saveTimer = setTimeout(() => {
      setPlaylistTracks(id, tracks.map((t) => t.id))
        .then((d) => { if (open?.playlist.id === id) open = { ...open, playlist: d.playlist } })
        .catch(fail)
    }, 500)
  }
  function move(i: number, delta: -1 | 1) {
    if (!open) return
    const to = i + delta
    if (to < 0 || to >= open.tracks.length) return
    const next = [...open.tracks]
    ;[next[i], next[to]] = [next[to], next[i]]
    arrange(next)
  }
  function removeAt(i: number) {
    if (!open) return
    arrange(open.tracks.filter((_, j) => j !== i))
  }

  let dragFrom = $state<number | null>(null)
  let dragOver = $state<number | null>(null)
  function drop(i: number) {
    if (!open || dragFrom === null || dragFrom === i) {
      dragFrom = dragOver = null
      return
    }
    const next = [...open.tracks]
    const [moved] = next.splice(dragFrom, 1)
    next.splice(i, 0, moved)
    dragFrom = dragOver = null
    arrange(next)
  }

  const manual = $derived(!!open && !open.playlist.smart)
</script>

{#if playlistId}
  {#if open}
    {@const p = open.playlist}
    <button class="back" onclick={() => onopen(null)}><ChevronLeft size={16} /> Playlists</button>
    <header class="hero">
      <span class="art"><Mosaic covers={p.covers} smart={!!p.smart} /></span>
      <div class="hero-body">
        <span class="eyebrow">{p.smart ? 'Smart playlist' : 'Playlist'}</span>
        {#if renaming}
          <form class="rename" onsubmit={rename}>
            <input bind:value={renameTo} aria-label="Playlist name" maxlength="120" />
            <button type="submit" class="btn">Save</button>
            <button type="button" class="btn" onclick={() => (renaming = false)}>Cancel</button>
          </form>
        {:else}
          <h1>{p.name}</h1>
        {/if}
        <p class="stats">{p.trackCount === 1 ? '1 track' : `${p.trackCount} tracks`}{p.durationSecs ? ` · ${totalTime(p.durationSecs)}` : ''}</p>
        {#if p.smart}<p class="rules">{describe(p.smart)}</p>{/if}
        <div class="acts">
          <button class="play primary" disabled={!open.tracks.length} onclick={() => open && music().play(open.tracks, 0, false, { kind: 'playlist', id: p.id, title: p.name })}><Play size={16} /> Play</button>
          <button class="btn" disabled={!open.tracks.length} onclick={() => open && music().play(open.tracks, 0, true, { kind: 'playlist', id: p.id, title: p.name })}><Shuffle size={15} /> Shuffle</button>
          <button class="btn icon" disabled={!open.tracks.length} title="Play next" aria-label="Play next"
            onclick={() => { if (open) { music().playNext(open.tracks); music().notify('info', `Playing next: ${open.tracks.length} tracks.`) } }}><ListStart size={16} /></button>
          <button class="btn icon" disabled={!open.tracks.length} title="Add to queue" aria-label="Add to queue"
            onclick={() => { if (open) { music().queue(open.tracks); music().notify('info', `Queued ${open.tracks.length} tracks.`) } }}><ListPlus size={16} /></button>
          {#if p.smart}
            <button class="btn" onclick={() => open?.playlist.smart && (editing = { name: open.playlist.name, rules: open.playlist.smart, id: open.playlist.id })}><Sparkles size={15} /> Edit rules</button>
          {:else}
            <button class="btn icon" title="Rename" aria-label="Rename" onclick={() => { renameTo = p.name; renaming = true }}><Pencil size={15} /></button>
          {/if}
          <button class="btn" class:danger={confirmDelete} onclick={remove} title="Delete playlist"><Trash2 size={15} />{confirmDelete ? ' Delete?' : ''}</button>
        </div>
      </div>
    </header>

    {#if open.tracks.length === 0}
      <p class="empty">
        {p.smart ? 'Nothing matches these rules yet.' : 'Empty so far — use “Add to playlist” on any track, album or artist.'}
      </p>
    {:else}
      <ol class="tracks" class:manual>
        {#each open.tracks as t, i (`${t.id}-${i}`)}
          <li
            class="trackstars"
            class:over={dragOver === i && dragFrom !== i}
            class:dragging={dragFrom === i}
            draggable={manual}
            ondragstart={(e) => { dragFrom = i; e.dataTransfer?.setData('text/plain', String(i)) }}
            ondragover={(e) => { if (dragFrom !== null) { e.preventDefault(); dragOver = i } }}
            ondragleave={() => { if (dragOver === i) dragOver = null }}
            ondrop={(e) => { e.preventDefault(); drop(i) }}
            ondragend={() => (dragFrom = dragOver = null)}
          >
            {#if manual}<span class="grip" aria-hidden="true"><GripVertical size={15} /></span>{/if}
            <button class="trow" onclick={() => open && music().play(open.tracks, i, false, { kind: 'playlist', id: p.id, title: p.name })}>
              <span class="rank">{i + 1}</span>
              {#if t.poster}<img src={t.poster} alt="" loading="lazy" />{:else}<span class="thumb"><MusicIcon size={14} /></span>{/if}
              <span class="tmain"><span class="ttitle">{t.title}</span><span class="tsub">{t.artist}</span></span>
              <span class="tplays" title={t.lastPlayedAt ? `Last played ${playedWhen(t.lastPlayedAt)}` : 'Not played yet'}>{t.playCount ? plays(t.playCount) : ''}</span>
              <span class="ttime">{trackTime(t.runtimeSecs)}</span>
            </button>
            <Stars track={t} />
            {#if manual}
              <span class="moves">
                <button title="Move up" aria-label="Move {t.title} up" disabled={i === 0} onclick={() => move(i, -1)}><ArrowUp size={15} /></button>
                <button title="Move down" aria-label="Move {t.title} down" disabled={i === open.tracks.length - 1} onclick={() => move(i, 1)}><ArrowDown size={15} /></button>
              </span>
              <TrackMenu track={t} onremove={() => removeAt(i)} onmove={(d) => move(i, d)} />
            {:else}
              <TrackMenu track={t} />
            {/if}
          </li>
        {/each}
      </ol>
    {/if}
  {:else if error}
    <p class="empty">{error}</p>
  {:else}
    <p class="empty">Loading…</p>
  {/if}
{:else}
  <header class="listhead">
    <h1>Playlists</h1>
    <div class="acts">
      <button class="btn" onclick={() => { newName = ''; creating = true }}><Plus size={15} /> New playlist</button>
      <button class="btn" onclick={() => (editing = { name: '', rules: blankRules() })}><Sparkles size={15} /> New smart playlist</button>
    </div>
  </header>
  {#if creating}
    <Sheet title="New playlist" onclose={() => (creating = false)}>
      <form class="newform" onsubmit={createManual}>
        <!-- svelte-ignore a11y_autofocus -->
        <input bind:value={newName} placeholder="Name" aria-label="New playlist name" maxlength="120" autofocus />
        <button type="submit" class="primary" disabled={!newName.trim()}>Create</button>
      </form>
    </Sheet>
  {/if}
  {#if loaded && lists.length === 0}
    <div class="empty">
      <p>No playlists yet.</p>
      <p>Make one here and fill it with “Add to playlist” from any track, album or artist — or make a smart playlist that fills itself: a genre, what you have played most, what you have not played in months.</p>
    </div>
  {:else}
    <div class="grid">
      {#each lists as p (p.id)}
        <button class="card" onclick={() => onopen(p.id)}>
          <Mosaic covers={p.covers} smart={!!p.smart} />
          <span class="cname">{p.name}</span>
          <span class="csub">{p.trackCount === 1 ? '1 track' : `${p.trackCount} tracks`}{p.durationSecs ? ` · ${totalTime(p.durationSecs)}` : ''}</span>
        </button>
      {/each}
    </div>
  {/if}
{/if}

{#if editing}
  <SmartEditor name={editing.name} rules={editing.rules} onsave={saveSmart} onclose={() => (editing = null)} />
{/if}

<style>
  h1 { font-size: 1.6rem; line-height: 1.15; margin: 0; overflow-wrap: anywhere; }
  .back { display: inline-flex; align-items: center; gap: 0.2rem; color: var(--muted); font-size: 0.86rem; margin-bottom: 0.6rem; }
  .back:hover { color: var(--accent-2); }
  .listhead { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 0.8rem; margin-bottom: 1rem; }
  .hero { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 1rem; margin-bottom: 1.2rem; }
  .art { width: 11rem; display: block; box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 60%, transparent); border-radius: 10px; }
  .hero-body { display: flex; flex-direction: column; align-items: center; gap: 0.35rem; min-width: 0; }
  .eyebrow { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); }
  .stats { color: var(--muted); font-size: 0.88rem; }
  .rules { color: var(--muted); font-size: 0.82rem; max-width: 40rem; }
  .acts { display: flex; flex-wrap: wrap; justify-content: center; gap: 0.5rem; margin-top: 0.5rem; }
  .primary, .btn {
    display: inline-flex; align-items: center; gap: 0.4rem; border-radius: 999px; font-size: 0.9rem;
    padding: 0.5rem 1.1rem; border: 1px solid var(--border); background: var(--panel); color: var(--text);
  }
  .primary { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 22%, var(--panel)); font-weight: 600; }
  .primary:hover { background: color-mix(in srgb, var(--accent) 34%, var(--panel)); }
  .btn:hover { border-color: var(--accent); }
  .btn.icon { padding: 0.5rem 0.65rem; }
  .btn.danger { color: var(--err); border-color: var(--err); }
  .primary:disabled, .btn:disabled { opacity: 0.5; cursor: default; }
  .rename, .newform { display: flex; gap: 0.5rem; flex-wrap: wrap; justify-content: center; }
  .newform { justify-content: flex-start; }
  .newform input { max-width: none; }
  .rename input, .newform input {
    background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 8px;
    padding: 0.45rem 0.7rem; font: inherit; font-size: 1rem; min-width: 0; flex: 1 1 12rem; max-width: 24rem;
  }
  .rename input:focus, .newform input:focus { outline: none; border-color: var(--accent); }
  .empty { background: var(--panel); border: 1px solid var(--border); border-radius: 12px; padding: 1.2rem 1.4rem; color: var(--muted); }
  .empty p + p { margin-top: 0.5rem; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr)); gap: 1rem 0.9rem; }
  .card { display: flex; flex-direction: column; gap: 0.3rem; text-align: left; min-width: 0; }
  .card:hover :global(.mosaic) { border-color: var(--accent); }
  .cname { font-size: 0.88rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .card:hover .cname { color: var(--accent-2); }
  .csub { font-size: 0.75rem; color: var(--muted); }

  .tracks { list-style: none; display: flex; flex-direction: column; gap: 0.1rem; }
  .tracks li { display: flex; align-items: center; min-width: 0; border-radius: 8px; border-top: 2px solid transparent; }
  .tracks li:hover { background: var(--panel); }
  .tracks li.over { border-top-color: var(--accent); }
  .tracks li.dragging { opacity: 0.5; }
  .grip { color: var(--muted); cursor: grab; display: grid; place-items: center; padding-left: 0.2rem; }
  .trow { flex: 1; min-width: 0; display: flex; align-items: center; gap: 0.65rem; padding: 0.35rem 0.4rem; text-align: left; }
  .rank { width: 1.6rem; text-align: right; color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; flex: none; }
  .trow img, .thumb { width: 2.5rem; height: 2.5rem; border-radius: 5px; object-fit: cover; flex: none; }
  .thumb { display: grid; place-items: center; background: var(--panel); color: var(--muted); border: 1px solid var(--border); }
  .tmain { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .ttitle, .tsub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttitle { font-size: 0.92rem; }
  .tsub { font-size: 0.77rem; color: var(--muted); }
  .trow:hover .ttitle { color: var(--accent-2); }
  .tplays { color: var(--muted); font-size: 0.76rem; white-space: nowrap; flex: none; }
  .ttime { color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; flex: none; width: 2.8rem; text-align: right; }
  /* Move buttons are for touch, where dragging a row fights scrolling;
     a pointer drags by the grip instead. */
  .moves { display: none; flex: none; }
  .moves button { display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 50%; color: var(--muted); }
  .moves button:hover:not(:disabled) { color: var(--text); background: var(--bg); }
  .moves button:disabled { opacity: 0.3; }
  @media (hover: none), (max-width: 40rem) {
    .moves { display: inline-flex; }
    .grip { display: none; }
    .tplays { display: none; }
    /* Room for the title beside the move buttons. */
    .manual .rank { display: none; }
    .moves button { width: 1.8rem; }
  }

  @media (min-width: 48rem) {
    .hero { flex-direction: row; align-items: flex-end; text-align: left; gap: 1.6rem; }
    .art { width: 12rem; }
    .hero-body { align-items: flex-start; }
    h1 { font-size: 2.3rem; }
    .acts, .rename { justify-content: flex-start; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(10rem, 1fr)); }
  }
</style>
