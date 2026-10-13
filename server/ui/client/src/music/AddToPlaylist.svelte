<script lang="ts">
  // "Add to playlist": the viewer's manual playlists to choose from, or a
  // new one named on the spot. Mounted once by App and opened through
  // addToPlaylist() (actions.svelte.ts) from any track, album or artist.
  import { ListMusic, Plus, X } from '@lucide/svelte'
  import { listPlaylists, createPlaylist, addToPlaylist as addTo, type Playlist } from '../lib/api'
  import { music, picker } from './actions.svelte'

  let lists: Playlist[] = $state([])
  let loading = $state(false)
  let busy = $state(false)
  let name = $state('')
  const open = $derived(picker.ids.length > 0)

  $effect(() => {
    if (!open) return
    loading = true
    name = ''
    listPlaylists()
      .then((all) => (lists = all.filter((p) => !p.smart)))
      .catch((err) => music().notify('error', err instanceof Error ? err.message : String(err)))
      .finally(() => (loading = false))
    const esc = (e: KeyboardEvent) => e.key === 'Escape' && close()
    document.addEventListener('keydown', esc)
    return () => document.removeEventListener('keydown', esc)
  })

  function close() {
    picker.ids = []
    picker.label = ''
  }

  async function choose(p: Playlist) {
    if (busy) return
    busy = true
    try {
      const before = p.trackCount
      const d = await addTo(p.id, picker.ids)
      const added = d.playlist.trackCount - before
      music().notify('info', added === 1 ? `Added to ${p.name}.` : `Added ${added} tracks to ${p.name}.`)
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
</script>

{#if open}
  <div class="atp-back" role="presentation" onclick={(e) => e.target === e.currentTarget && close()}>
    <div class="atp" role="dialog" aria-modal="true" aria-labelledby="atp-title">
      <header>
        <h2 id="atp-title">Add to playlist</h2>
        <button class="atp-x" onclick={close} aria-label="Close"><X size={18} /></button>
      </header>
      <p class="atp-what">{picker.label}</p>
      <form class="atp-new" onsubmit={createAndAdd}>
        <input bind:value={name} placeholder="New playlist name" aria-label="New playlist name" maxlength="120" />
        <button type="submit" disabled={!name.trim() || busy}><Plus size={15} /> Create</button>
      </form>
      <div class="atp-list">
        {#if loading}
          <p class="atp-empty">Loading…</p>
        {:else if lists.length === 0}
          <p class="atp-empty">No playlists yet — name one above.</p>
        {:else}
          {#each lists as p (p.id)}
            <button class="atp-row" disabled={busy} onclick={() => choose(p)}>
              {#if p.covers[0]}
                <img src={p.covers[0]} alt="" loading="lazy" />
              {:else}
                <span class="atp-ph"><ListMusic size={18} /></span>
              {/if}
              <span class="atp-name">{p.name}</span>
              <span class="atp-n">{p.trackCount}</span>
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
    width: min(26rem, 100%); max-height: min(36rem, 90vh); display: flex; flex-direction: column;
    background: var(--panel); border: 1px solid var(--border); border-radius: 14px; padding: 1rem;
    box-shadow: 0 16px 48px color-mix(in srgb, var(--bg) 80%, transparent);
  }
  header { display: flex; align-items: center; justify-content: space-between; }
  h2 { font-size: 1.05rem; margin: 0; }
  .atp-x { display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 50%; color: var(--muted); }
  .atp-x:hover { color: var(--text); background: var(--bg); }
  .atp-what { color: var(--muted); font-size: 0.85rem; margin: 0.15rem 0 0.8rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .atp-new { display: flex; gap: 0.5rem; margin-bottom: 0.75rem; }
  .atp-new input {
    flex: 1; min-width: 0; background: var(--bg); color: var(--text); border: 1px solid var(--border);
    border-radius: 8px; padding: 0.5rem 0.7rem; font: inherit; font-size: 0.9rem;
  }
  .atp-new input:focus { outline: none; border-color: var(--accent); }
  .atp-new button {
    display: inline-flex; align-items: center; gap: 0.3rem; padding: 0.5rem 0.8rem; border-radius: 8px;
    border: 1px solid var(--accent); background: color-mix(in srgb, var(--accent) 18%, transparent); font-size: 0.88rem;
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
  .atp-empty { color: var(--muted); font-size: 0.88rem; padding: 0.8rem 0.2rem; }
  @media (max-width: 30rem) {
    .atp-back { place-items: end stretch; padding: 0; }
    .atp { width: 100%; border-radius: 14px 14px 0 0; max-height: 80vh; }
  }
</style>
