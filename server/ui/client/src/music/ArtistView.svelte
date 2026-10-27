<script lang="ts">
  // An artist's page: picture and biography, their most played tracks, their
  // albums by year and what else they appear on. Reached from the music
  // library's artist rail, album pages, track menus and search.
  import { onDestroy } from 'svelte'
  import { Play, Shuffle, ListPlus, ListStart, ListMusic, MicVocal, ExternalLink, Sparkles } from '@lucide/svelte'
  import { artistPage, itemTracks, type ArtistPage, type Item } from '../lib/api'
  import { music, addToPlaylist, totalTime } from './actions.svelte'
  import { mq, play, playNext, append } from './player/queue.svelte'
  import TrackList from './TrackList.svelte'
  import DownloadMenu from './DownloadMenu.svelte'
  import AlbumGrid from './library/AlbumGrid.svelte'

  let { artist }: { artist: Item } = $props()

  let page = $state<ArtistPage | null>(null)
  let error = $state('')
  let bioOpen = $state(false)
  let retry: ReturnType<typeof setTimeout> | undefined
  let attempts = 0

  async function load(id: string) {
    try {
      const p = await artistPage(id)
      if (p.artist.id !== artist.id) return // navigated on meanwhile
      page = p
      error = ''
      // A picture and biography are being looked up: ask again shortly, a
      // few times, rather than leave the page without them until a reload.
      clearTimeout(retry)
      if (p.lookingUp && attempts < 4) {
        attempts++
        retry = setTimeout(() => load(id), 3000 + attempts * 2000)
      }
    } catch (err) {
      error = err instanceof Error ? err.message : String(err)
    }
  }

  $effect(() => {
    const id = artist.id
    page = null
    bioOpen = false
    attempts = 0
    load(id)
  })
  onDestroy(() => clearTimeout(retry))

  const a = $derived(page?.artist ?? artist)
  // No picture of the artist: their newest album's cover stands in.
  const fallback = $derived(page ? [...page.albums].reverse().find((al) => al.poster)?.poster ?? '' : '')
  const image = $derived(a.poster || fallback)
  const portrait = $derived(!!a.poster)

  // Every track of theirs, album by album in release order, in one request.
  const source = $derived({ kind: 'artist', id: artist.id, title: artist.title })
  const allTracks = () => itemTracks(artist.id)
  const fail = (err: unknown) => music().notify('error', err instanceof Error ? err.message : String(err))
  async function playAll(shuffle = false) {
    try {
      const tracks = await allTracks()
      if (tracks.length) play(tracks, 0, { shuffle, source })
    } catch (err) {
      fail(err)
    }
  }
  async function queueAll(next = false) {
    try {
      const tracks = await allTracks()
      if (next) playNext(tracks)
      else append(tracks)
      music().notify('info', `${next ? 'Playing next' : 'Queued'}: ${tracks.length} tracks by ${a.title}.`)
    } catch (err) {
      fail(err)
    }
  }
  const playing = $derived(mq.active ? mq.items[mq.index]?.id : undefined)

  const stats = $derived.by(() => {
    if (!page) return ''
    const parts = [
      page.albums.length === 1 ? '1 album' : `${page.albums.length} albums`,
      page.stats.tracks === 1 ? '1 track' : `${page.stats.tracks} tracks`,
    ]
    if (page.stats.secs) parts.push(totalTime(page.stats.secs))
    if (page.stats.plays) parts.push(`you've played ${page.stats.plays === 1 ? 'once' : `${page.stats.plays} times`}`)
    return parts.join(' · ')
  })
  async function playAlbum(al: Item) {
    try {
      play(await itemTracks(al.id), 0, { source: { kind: 'album', id: al.id, title: al.title } })
    } catch (err) {
      fail(err)
    }
  }
</script>

<div class="artist">
  {#if image}
    <div class="artbg" style="background-image: url({image})" aria-hidden="true"></div>
  {/if}
  <header class="hero">
    {#if image}
      <img class="portrait" class:round={portrait} src={image} alt={a.title} />
    {:else}
      <span class="portrait round ph"><MicVocal size={48} /></span>
    {/if}
    <div class="hero-body">
      <span class="eyebrow">Artist</span>
      <h1>{a.title}</h1>
      {#if stats}<p class="stats">{stats}</p>{/if}
      <div class="acts">
        <button class="play primary" onclick={() => playAll()} disabled={!page?.albums.length}><Play size={16} /> Play</button>
        <button class="btn" onclick={() => playAll(true)} disabled={!page?.albums.length}><Shuffle size={15} /> Shuffle</button>
        <button class="btn" onclick={() => music().mix(a)} disabled={!page?.albums.length} title="A mix from this artist: their best-known songs, then similar artists"><Sparkles size={15} /> Mix</button>
        <button class="btn icon" onclick={() => queueAll(true)} disabled={!page?.albums.length} title="Play next" aria-label="Play next"><ListStart size={16} /></button>
        <button class="btn icon" onclick={() => queueAll()} disabled={!page?.albums.length} title="Add to queue" aria-label="Add to queue"><ListPlus size={16} /></button>
        <button class="btn icon" onclick={() => addToPlaylist([a.id], a.title)} disabled={!page?.albums.length} title="Add to playlist" aria-label="Add to playlist"><ListMusic size={16} /></button>
        <DownloadMenu id={a.id} compact />
      </div>
    </div>
  </header>

  {#if error}
    <p class="note err">{error}</p>
  {/if}

  {#if a.overview}
    <section class="bio">
      <p class:clamped={!bioOpen}>{a.overview}</p>
      <div class="bio-foot">
        <button class="link" onclick={() => (bioOpen = !bioOpen)}>{bioOpen ? 'Less' : 'More'}</button>
        {#if page?.bioUrl}
          <a href={page.bioUrl} target="_blank" rel="noopener noreferrer">From Wikipedia <ExternalLink size={12} /></a>
        {/if}
      </div>
    </section>
  {:else if page?.lookingUp}
    <p class="note">Looking for a picture and biography…</p>
  {/if}

  {#if page && page.topTracks.length > 0}
    <section>
      <h2>Top tracks</h2>
      <TrackList tracks={page.topTracks} mode="list" currentId={playing} menuArtist={false}
        onplay={(i) => page && play(page.topTracks, i, { source: { kind: 'artist', id: artist.id, title: `${artist.title}: top tracks` } })} />
    </section>
  {/if}

  {#if page}
    {#if page.albums.length > 0}
      <section>
        <h2>Albums</h2>
        <AlbumGrid items={page.albums} showArtist={false} onopen={(al) => music().openAlbum(al)} onplay={playAlbum} />
      </section>
    {/if}
    {#if page.appearsOn.length > 0}
      <section>
        <h2>Appears on</h2>
        <AlbumGrid items={page.appearsOn} onopen={(al) => music().openAlbum(al)} onplay={playAlbum} />
      </section>
    {/if}
  {/if}
</div>

<style>
  .artist { position: relative; }
  .artist > :not(.artbg) { position: relative; }
  .artbg {
    position: absolute; top: 0; left: 0; right: 0; height: 26rem;
    margin: -1.25rem -1.5rem 0;
    background-size: cover; background-position: center 30%;
    opacity: 0.28; filter: saturate(0.8) blur(24px); transform: scale(1.06);
    /* A mask reads only alpha: any opaque token fades it out the same. */
    -webkit-mask-image: linear-gradient(to bottom, var(--bg) 40%, transparent);
    mask-image: linear-gradient(to bottom, var(--bg) 40%, transparent);
  }
  .hero { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 1rem; padding: 0.5rem 0 1rem; }
  .portrait {
    width: 11rem; height: 11rem; object-fit: cover; flex: none; border-radius: 12px;
    border: 1px solid var(--border); background: var(--panel);
    box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 60%, transparent);
  }
  .portrait.round { border-radius: 50%; }
  .portrait.ph { display: grid; place-items: center; color: var(--muted); }
  .hero-body { min-width: 0; display: flex; flex-direction: column; align-items: center; gap: 0.35rem; }
  .eyebrow { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); }
  h1 { font-size: 1.9rem; line-height: 1.1; margin: 0; overflow-wrap: anywhere; }
  .stats { color: var(--muted); font-size: 0.88rem; }
  .acts { display: flex; flex-wrap: wrap; justify-content: center; gap: 0.5rem; margin-top: 0.6rem; }
  .primary, .btn {
    display: inline-flex; align-items: center; gap: 0.4rem; border-radius: 999px; font-size: 0.9rem;
    padding: 0.5rem 1.1rem; border: 1px solid var(--border); background: var(--panel); color: var(--text);
  }
  .primary { border-color: var(--accent); background: color-mix(in srgb, var(--accent) 22%, var(--panel)); font-weight: 600; }
  .primary:hover { background: color-mix(in srgb, var(--accent) 34%, var(--panel)); }
  .btn:hover { border-color: var(--accent); }
  .btn.icon { padding: 0.5rem 0.65rem; }
  .primary:disabled, .btn:disabled { opacity: 0.5; cursor: default; }

  .note { color: var(--muted); font-size: 0.88rem; text-align: center; margin: 0.5rem 0; }
  .note.err { color: var(--err); }

  .bio { max-width: 52rem; margin: 0.5rem auto 0.5rem; }
  .bio p { font-size: 0.95rem; line-height: 1.55; }
  .bio p.clamped { overflow: hidden; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; }
  .bio-foot { display: flex; gap: 1rem; align-items: center; margin-top: 0.35rem; font-size: 0.82rem; }
  .bio-foot a { color: var(--muted); display: inline-flex; align-items: center; gap: 0.25rem; text-decoration: none; }
  .bio-foot a:hover { color: var(--accent-2); }
  .link { color: var(--accent-2); font-size: 0.85rem; padding: 0; }

  h2 { font-size: 1.1rem; color: var(--text); margin: 1.6rem 0 0.7rem; }

  @media (min-width: 48rem) {
    .hero { flex-direction: row; align-items: flex-end; text-align: left; gap: 1.6rem; padding-top: 1rem; }
    .portrait { width: 13rem; height: 13rem; }
    .hero-body { align-items: flex-start; }
    h1 { font-size: 2.6rem; }
    .acts { justify-content: flex-start; }
    .bio { margin-left: 0; }
  }
  @media (max-width: 767px) {
    .artbg { margin: -1rem -1rem 0; }
  }
</style>
