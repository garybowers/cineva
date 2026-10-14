<script lang="ts">
  // The top of a music library: what the viewer played lately, their
  // playlists, and their most played tracks — Plexamp's way in, before the
  // whole catalogue.
  import { Play, Music as MusicIcon, Plus } from '@lucide/svelte'
  import { libraryListening, listPlaylists, itemDetail, type Listening, type Playlist, type Item } from '../lib/api'
  import { music, trackTime, plays, playedWhen } from './actions.svelte'
  import Mosaic from './Mosaic.svelte'
  import TrackMenu from './TrackMenu.svelte'
  import Stars from './Stars.svelte'

  let { libraryId, onplaylists }: { libraryId: string; onplaylists: () => void } = $props()

  let rows = $state<Listening | null>(null)
  let lists: Playlist[] = $state([])

  $effect(() => {
    const id = libraryId
    rows = null
    libraryListening(id)
      .then((r) => { if (id === libraryId) rows = r })
      .catch(() => (rows = null))
    listPlaylists()
      .then((p) => (lists = p))
      .catch(() => (lists = []))
  })

  async function playAlbum(al: Item) {
    try {
      music().play((await itemDetail(al.id)).children.filter((c) => c.kind === 'track'), 0)
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
  const top = $derived(rows?.mostPlayed.slice(0, 10) ?? [])
</script>

{#if rows && rows.recentlyPlayed.length > 0}
  <section class="lrow">
    <h3>Recently played</h3>
    <div class="scroller">
      {#each rows.recentlyPlayed as al (al.id)}
        <div class="card">
          <div class="cwrap">
            <button class="cover" onclick={() => music().openAlbum(al)} aria-label={al.title}>
              {#if al.poster}<img src={al.poster} alt="" loading="lazy" />{:else}<span class="ph"><MusicIcon size={26} /></span>{/if}
            </button>
            <button class="cardplay" title="Play {al.title}" aria-label="Play {al.title}" onclick={() => playAlbum(al)}><Play size={15} /></button>
          </div>
          <button class="cname" onclick={() => music().openAlbum(al)}>{al.title}</button>
          <span class="csub">{al.artist}{al.lastPlayedAt ? ` · ${playedWhen(al.lastPlayedAt)}` : ''}</span>
        </div>
      {/each}
    </div>
  </section>
{/if}

<section class="lrow">
  <div class="lhead">
    <h3>Playlists</h3>
    <button class="see" onclick={onplaylists}>{lists.length ? 'See all' : 'New playlist'}{#if !lists.length}<Plus size={14} />{/if}</button>
  </div>
  {#if lists.length > 0}
    <div class="scroller">
      {#each lists as p (p.id)}
        <div class="card">
          <button class="mcover" onclick={() => music().openPlaylist(p.id)} aria-label={p.name}>
            <Mosaic covers={p.covers} smart={!!p.smart} />
          </button>
          <button class="cname" onclick={() => music().openPlaylist(p.id)}>{p.name}</button>
          <span class="csub">{p.trackCount === 1 ? '1 track' : `${p.trackCount} tracks`}</span>
        </div>
      {/each}
    </div>
  {:else}
    <p class="hint">Collect tracks from any album or artist with “Add to playlist”, or let a smart playlist gather them by genre, plays or date added.</p>
  {/if}
</section>

{#if top.length > 0}
  <section class="lrow">
    <h3>Most played</h3>
    <ol class="tracks">
      {#each top as t, i (t.id)}
        <li class="trackstars">
          <button class="trow" onclick={() => music().play(top, i)}>
            <span class="rank">{i + 1}</span>
            {#if t.poster}<img src={t.poster} alt="" loading="lazy" />{:else}<span class="thumb"><MusicIcon size={14} /></span>{/if}
            <span class="tmain"><span class="ttitle">{t.title}</span><span class="tsub">{t.artist}</span></span>
            <span class="tplays">{plays(t.playCount)}</span>
            <span class="ttime">{trackTime(t.runtimeSecs)}</span>
          </button>
          <Stars track={t} />
          <TrackMenu track={t} />
        </li>
      {/each}
    </ol>
  </section>
{/if}

<style>
  .lrow { margin-bottom: 1.2rem; }
  .lhead { display: flex; align-items: baseline; justify-content: space-between; gap: 1rem; }
  h3 { font-size: 1.05rem; font-weight: 600; margin: 0.4rem 0 0.6rem; }
  .see { display: inline-flex; align-items: center; gap: 0.25rem; color: var(--accent-2); font-size: 0.85rem; }
  .hint { color: var(--muted); font-size: 0.86rem; max-width: 40rem; }
  .scroller {
    display: grid; grid-auto-flow: column; grid-auto-columns: 8.5rem; gap: 0.9rem;
    overflow-x: auto; overscroll-behavior-x: contain; scroll-snap-type: x mandatory;
    padding-bottom: 0.3rem; scrollbar-width: none;
  }
  .scroller::-webkit-scrollbar { display: none; }
  .scroller > * { scroll-snap-align: start; }
  .card { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
  .cwrap { position: relative; }
  .cover, .mcover { display: block; width: 100%; padding: 0; border-radius: 10px; overflow: hidden; }
  .cover { aspect-ratio: 1; border: 1px solid var(--border); background: var(--panel); }
  .cover img { width: 100%; height: 100%; object-fit: cover; display: block; }
  .cover .ph { width: 100%; height: 100%; display: grid; place-items: center; color: var(--muted); }
  .card:hover .cover { border-color: var(--accent); }
  .cardplay {
    position: absolute; right: 0.4rem; bottom: 0.4rem; width: 2.2rem; height: 2.2rem; display: grid; place-items: center;
    border-radius: 50%; background: color-mix(in srgb, var(--accent) 85%, var(--panel)); color: var(--text);
    opacity: 0; transition: opacity 0.15s ease;
  }
  .card:hover .cardplay, .cardplay:focus-visible { opacity: 1; }
  @media (hover: none) { .cardplay { display: none; } }
  .cname { text-align: left; padding: 0; font-size: 0.85rem; line-height: 1.25; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .cname:hover { color: var(--accent-2); }
  .csub { font-size: 0.74rem; color: var(--muted); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }

  .tracks { list-style: none; display: grid; gap: 0.1rem 2rem; }
  .tracks li { display: flex; align-items: center; min-width: 0; border-radius: 8px; }
  .tracks li:hover { background: var(--panel); }
  .trow { flex: 1; min-width: 0; display: flex; align-items: center; gap: 0.65rem; padding: 0.3rem 0.4rem; text-align: left; }
  .rank { width: 1.3rem; text-align: right; color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; flex: none; }
  .trow img, .thumb { width: 2.4rem; height: 2.4rem; border-radius: 5px; object-fit: cover; flex: none; }
  .thumb { display: grid; place-items: center; background: var(--panel); color: var(--muted); border: 1px solid var(--border); }
  .tmain { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .ttitle, .tsub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttitle { font-size: 0.9rem; }
  .tsub { font-size: 0.76rem; color: var(--muted); }
  .trow:hover .ttitle { color: var(--accent-2); }
  .tplays { color: var(--muted); font-size: 0.76rem; white-space: nowrap; flex: none; }
  .ttime { color: var(--muted); font-size: 0.78rem; font-variant-numeric: tabular-nums; flex: none; width: 2.6rem; text-align: right; }
  @media (min-width: 64rem) { .tracks { grid-template-columns: 1fr 1fr; } }
  @media (max-width: 30rem) { .scroller { grid-auto-columns: 7.25rem; gap: 0.7rem; } .tplays { display: none; } }
</style>
