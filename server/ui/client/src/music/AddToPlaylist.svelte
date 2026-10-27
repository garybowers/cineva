<script lang="ts">
  // "Add to playlist" (server/docs/mix.md, "Adding to a playlist"): the
  // viewer's manual playlists, the ones added to most recently first with
  // the last three as large chips, a filter box when there are many, a
  // tick where the track is already in, and a toast with Undo. Mounted
  // once by App and opened through addToPlaylist() (actions.svelte.ts)
  // from any track, album, artist or the queue.
  import { ListMusic, Plus, X, Check } from '@lucide/svelte'
  import { listPlaylists, createPlaylist, addToPlaylist as addTo, removeLastFromPlaylist, type Playlist } from '../lib/api'
  import { music, picker } from './actions.svelte'

  let lists: Playlist[] = $state([])
  let loading = $state(false)
  let busy = $state(false)
  let name = $state('')
  let filter = $state('')
  let filterBox = $state<HTMLInputElement | null>(null)
  const open = $derived(picker.ids.length > 0)
  // One track being added: the picker can say which playlists hold it.
  const single = $derived(picker.ids.length === 1 ? picker.ids[0] : '')

  const MANY = 8
  const shown = $derived.by(() => {
    const q = filter.trim().toLowerCase()
    return q ? lists.filter((p) => p.name.toLowerCase().includes(q)) : lists
  })
  // The last three used, as chips, when there is something to prefer.
  const chips = $derived(filter.trim() ? [] : lists.filter((p) => p.lastAddedAt).slice(0, 3))
  const rest = $derived(chips.length > 0 ? shown.filter((p) => !chips.some((c) => c.id === p.id)) : shown)

  $effect(() => {
    if (!open) return
    loading = true
    name = picker.suggest
    filter = ''
    listPlaylists(single || undefined)
      .then((all) => {
        lists = all.filter((p) => !p.smart)
        if (lists.length > MANY) setTimeout(() => filterBox?.focus(), 30)
      })
      .catch((err) => music().notify('error', err instanceof Error ? err.message : String(err)))
      .finally(() => (loading = false))
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  })

  function close() {
    picker.ids = []
    picker.label = ''
    picker.suggest = ''
  }

  async function choose(p: Playlist) {
    if (busy) return
    busy = true
    const ids = picker.ids
    try {
      const before = p.trackCount
      const d = await addTo(p.id, ids)
      const added = d.playlist.trackCount - before
      const text = added === 1 ? `Added to ${p.name}` : `Added ${added} tracks to ${p.name}`
      music().notify('info', text, added > 0 ? {
        label: 'Undo',
        run: () => {
          removeLastFromPlaylist(p.id, added)
            .then(() => music().notify('info', `Removed from ${p.name}.`))
            .catch((err) => music().notify('error', err instanceof Error ? err.message : String(err)))
        },
      } : undefined)
      close()
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    } finally {
      busy = false
    }
  }

  async function createAndAdd(e: SubmitEvent) {
    e.preventDefault()
    if (!name.trim() || busy) return
    busy = true
    try {
      const p = await createPlaylist(name.trim())
      busy = false
      await choose(p)
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    } finally {
      busy = false
    }
  }

  // Enter in the filter adds to the first match.
  function onFilterSubmit(e: SubmitEvent) {
    e.preventDefault()
    const first = chips[0] ?? rest[0]
    if (first) void choose(first)
  }
</script>

{#if open}
  <div class="atp-back" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
    <div class="atp" role="dialog" aria-modal="true" aria-labelledby="atp-title">
      <header>
        <h2 id="atp-title">Add to playlist</h2>
        <button class="atp-x" onclick={close} aria-label="Close"><X size={18} /></button>
      </header>
      <p class="atp-what">{picker.label}</p>
      {#if chips.length > 0}
        <div class="atp-chips" data-nav-group="atp-chips">
          {#each chips as p (p.id)}
            <button class="atp-chip" class:holds={p.holds} disabled={busy} onclick={() => choose(p)} title={p.holds ? `Already in ${p.name}: add again` : `Add to ${p.name}`}>
              {#if p.covers[0]}
                <img src={p.covers[0]} alt="" loading="lazy" />
              {:else}
                <span class="atp-ph"><ListMusic size={18} /></span>
              {/if}
              <span class="atp-chipname">{p.name}</span>
              {#if p.holds}<span class="atp-tick" title="Already in this playlist"><Check size={14} /></span>{/if}
            </button>
          {/each}
        </div>
      {/if}
      {#if lists.length > MANY}
        <form class="atp-filter" onsubmit={onFilterSubmit}>
          <input bind:this={filterBox} bind:value={filter} placeholder="Find a playlist" aria-label="Find a playlist" />
        </form>
      {/if}
      <form class="atp-new" onsubmit={createAndAdd}>
        <input bind:value={name} placeholder="New playlist name" aria-label="New playlist name" maxlength="120" />
        <button type="submit" disabled={!name.trim() || busy}><Plus size={15} /> Create</button>
      </form>
      <div class="atp-list" data-nav-group="atp-list">
        {#if loading}
          <p class="atp-empty">Loading…</p>
        {:else if lists.length === 0}
          <p class="atp-empty">No playlists yet — name one above.</p>
        {:else if rest.length === 0 && chips.length === 0}
          <p class="atp-empty">Nothing matches “{filter}”.</p>
        {:else}
          {#each rest as p (p.id)}
            <button class="atp-row" disabled={busy} onclick={() => choose(p)} title={p.holds ? `Already in ${p.name}: add again` : undefined}>
              {#if p.covers[0]}
                <img src={p.covers[0]} alt="" loading="lazy" />
              {:else}
                <span class="atp-ph"><ListMusic size={18} /></span>
              {/if}
              <span class="atp-name">{p.name}</span>
              {#if p.holds}
                <span class="atp-again"><Check size={13} /> Add again</span>
              {:else}
                <span class="atp-n">{p.trackCount}</span>
              {/if}
            </button>
          {/each}
        {/if}
      </div>
    </div>
  </div>
{/if}

<style>
  .atp-back {
    position: fixed; inset: 0; z-index: 200;
    background: color-mix(in srgb, var(--bg) 70%, transparent);
    display: grid; place-items: center; padding: 1rem;
  }
  .atp {
    width: min(26rem, 100%); max-height: min(38rem, 90vh); display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--border); border-radius: 14px; padding: 1rem;
    box-shadow: 0 16px 48px color-mix(in srgb, var(--bg) 80%, transparent);
  }
  header { display: flex; align-items: center; justify-content: space-between; }
  h2 { font-size: 1.05rem; margin: 0; }
  .atp-x { display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 50%; color: var(--muted); }
  .atp-x:hover { color: var(--text); background: var(--bg); }
  .atp-what { color: var(--muted); font-size: 0.85rem; margin: 0.15rem 0 0.8rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .atp-chips { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 0.5rem; margin-bottom: 0.75rem; }
  .atp-chip {
    position: relative; display: flex; flex-direction: column; align-items: center; gap: 0.4rem;
    padding: 0.6rem 0.4rem; border-radius: 10px; border: 1px solid var(--border); background: var(--bg);
    min-height: 3rem;
  }
  .atp-chip:hover { border-color: var(--accent); }
  .atp-chip img, .atp-chip .atp-ph { width: 3rem; height: 3rem; border-radius: 8px; object-fit: cover; }
  .atp-chipname { font-size: 0.82rem; font-weight: 600; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .atp-tick { position: absolute; top: 0.35rem; right: 0.35rem; display: grid; place-items: center; width: 1.3rem; height: 1.3rem; border-radius: 50%; background: var(--ok); color: var(--on-accent); }
  .atp-filter { margin-bottom: 0.5rem; }
  .atp-filter input, .atp-new input {
    flex: 1; min-width: 0; width: 100%; box-sizing: border-box; background: var(--bg); color: var(--text); border: 1px solid var(--border);
    border-radius: 8px; padding: 0.5rem 0.7rem; font: inherit; font-size: 0.9rem;
  }
  .atp-filter input:focus, .atp-new input:focus { outline: none; border-color: var(--accent); }
  .atp-new { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
  .atp-new button {
    display: inline-flex; align-items: center; gap: 0.3rem; padding: 0.5rem 0.8rem; border-radius: 8px;
    border: 1px solid var(--accent); background: color-mix(in srgb, var(--accent) 18%, transparent); font-size: 0.88rem; flex: none;
  }
  .atp-new button:disabled { opacity: 0.5; cursor: default; }
  .atp-list { overflow-y: auto; display: flex; flex-direction: column; gap: 0.15rem; }
  .atp-row {
    display: flex; align-items: center; gap: 0.7rem; padding: 0.45rem; border-radius: 8px; text-align: left;
  }
  .atp-row:hover { background: var(--bg); }
  .atp-row img, .atp-ph { width: 2.6rem; height: 2.6rem; border-radius: 6px; object-fit: cover; flex: none; }
  .atp-ph { display: grid; place-items: center; background: var(--bg); color: var(--muted); border: 1px solid var(--border); }
  .atp-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .atp-n { color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; }
  .atp-again { display: inline-flex; align-items: center; gap: 0.25rem; color: var(--ok); font-size: 0.78rem; white-space: nowrap; }
  .atp-empty { color: var(--muted); font-size: 0.88rem; padding: 0.8rem 0.2rem; }
  :global(html.tv) .atp-row:focus, :global(html.tv) .atp-chip:focus { outline: none; background: var(--accent); color: var(--on-accent); }
  @media (max-width: 30rem) {
    .atp-back { place-items: end stretch; padding: 0; }
    .atp { width: 100%; border-radius: 14px 14px 0 0; max-height: 80vh; }
  }
</style>
