<script lang="ts">
  // An artist's page: picture and biography, their most played tracks, their
  // albums by year and what else they appear on. Reached from the music
  // library's artist rail, album pages, track menus and search.
  import { onDestroy } from 'svelte'
  import { Play, Shuffle, ListPlus, ListMusic, MicVocal, Music as MusicIcon, ExternalLink } from '@lucide/svelte'
  import { artistPage, itemDetail, type ArtistPage, type Item } from '../lib/api'
  import { music, addToPlaylist, trackTime, totalTime, plays } from './actions.svelte'
  import TrackMenu from './TrackMenu.svelte'

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

  // Every track of theirs, album by album, for Play and Shuffle.
  async function allTracks(): Promise<Item[]> {
    if (!page) return []
    const lists = await Promise.all(page.albums.map((al) => itemDetail(al.id).then((d) => d.children.filter((c) => c.kind === 'track'))))
    return lists.flat()
  }
  async function playAll(shuffle = false) {
    try {
      const tracks = await allTracks()
      if (tracks.length) music().play(tracks, shuffle ? Math.floor(Math.random() * tracks.length) : 0, shuffle)
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
  async function queueAll() {
    try {
      const tracks = await allTracks()
      music().queue(tracks)
      music().notify('info', `Queued ${tracks.length} tracks by ${a.title}.`)
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }

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
      music().play((await itemDetail(al.id)).children.filter((c) => c.kind === 'track'), 0)
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
  const albumOf = (t: Item) => page?.albums.find((al) => al.id === t.parentId) ?? page?.appearsOn.find((al) => al.id === t.parentId)
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
        <button class="primary" onclick={() => playAll()} disabled={!page?.albums.length}><Play size={16} /> Play</button>
        <button class="btn" onclick={() => playAll(true)} disabled={!page?.albums.length}><Shuffle size={15} /> Shuffle</button>
        <button class="btn icon" onclick={queueAll} disabled={!page?.albums.length} title="Add to queue" aria-label="Add to queue"><ListPlus size={16} /></button>
        <button class="btn icon" onclick={() => addToPlaylist([a.id], a.title)} disabled={!page?.albums.length} title="Add to playlist" aria-label="Add to playlist"><ListMusic size={16} /></button>
        <select class="btn dlsel" title="Download (ZIP)" aria-label="Download" onchange={(e) => { const s = e.target as HTMLSelectElement; music().download(a.id, s.value); s.value = '' }}>
          <option value="">Download…</option>
          <option value="original">Original (ZIP)</option>
          <option value="mp3-320">MP3 320 (ZIP)</option>
          <option value="mp3-192">MP3 192 (ZIP)</option>
          <option value="ogg-192">OGG 192 (ZIP)</option>
        </select>
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
      <ol class="tracks">
        {#each page.topTracks as t, i (t.id)}
          {@const al = albumOf(t)}
          <li>
            <button class="trow" onclick={() => page && music().play(page.topTracks, i)}>
              <span class="rank">{i + 1}</span>
              {#if t.poster}<img src={t.poster} alt="" loading="lazy" />{:else}<span class="thumb ph"><MusicIcon size={14} /></span>{/if}
              <span class="tmain">
                <span class="ttitle">{t.title}</span>
                <span class="tsub">{al?.title ?? t.artist}</span>
              </span>
              <span class="tplays" title="Your plays">{t.playCount ? plays(t.playCount) : ''}</span>
              <span class="ttime">{trackTime(t.runtimeSecs)}</span>
            </button>
            <TrackMenu track={t} artist={false} />
          </li>
        {/each}
      </ol>
    </section>
  {/if}

  {#if page}
    {#if page.albums.length > 0}
      <section>
        <h2>Albums</h2>
        <div class="grid">
          {#each page.albums as al (al.id)}
            {@render albumCard(al)}
          {/each}
        </div>
      </section>
    {/if}
    {#if page.appearsOn.length > 0}
      <section>
        <h2>Appears on</h2>
        <div class="grid">
          {#each page.appearsOn as al (al.id)}
            {@render albumCard(al, true)}
          {/each}
        </div>
      </section>
    {/if}
  {/if}
</div>

{#snippet albumCard(al: Item, showArtist = false)}
  <div class="card">
    <div class="cwrap">
      <button class="cover" onclick={() => music().openAlbum(al)} aria-label={al.title}>
        {#if al.poster}<img src={al.poster} alt="" loading="lazy" />{:else}<span class="ph"><MusicIcon size={30} /></span>{/if}
      </button>
      <button class="cardplay" title="Play {al.title}" aria-label="Play {al.title}" onclick={() => playAlbum(al)}>
        <Play size={16} />
      </button>
    </div>
    <button class="cname" onclick={() => music().openAlbum(al)}>{al.title}</button>
    <span class="csub">{showArtist ? al.artist : ''}{showArtist && al.year ? ' · ' : ''}{al.year || ''}</span>
  </div>
{/snippet}

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
  .dlsel { font: inherit; font-size: 0.85rem; padding: 0.45rem 0.8rem; max-width: 9rem; color: var(--muted); }

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
  .tracks { list-style: none; display: grid; gap: 0.1rem 2rem; }
  .tracks li { display: flex; align-items: center; min-width: 0; border-radius: 8px; }
  .tracks li:hover { background: var(--panel); }
  .trow { flex: 1; min-width: 0; display: flex; align-items: center; gap: 0.7rem; padding: 0.35rem 0.4rem; text-align: left; }
  .rank { width: 1.3rem; text-align: right; color: var(--muted); font-size: 0.82rem; font-variant-numeric: tabular-nums; flex: none; }
  .trow img, .thumb { width: 2.6rem; height: 2.6rem; border-radius: 5px; object-fit: cover; flex: none; }
  .thumb.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); border: 1px solid var(--border); }
  .tmain { flex: 1; min-width: 0; display: flex; flex-direction: column; }
  .ttitle, .tsub { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttitle { font-size: 0.92rem; }
  .tsub { font-size: 0.78rem; color: var(--muted); }
  .trow:hover .ttitle { color: var(--accent-2); }
  .tplays { color: var(--muted); font-size: 0.78rem; white-space: nowrap; flex: none; }
  .ttime { color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; flex: none; width: 2.8rem; text-align: right; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(8rem, 1fr)); gap: 1rem 0.9rem; }
  .card { display: flex; flex-direction: column; gap: 0.3rem; min-width: 0; }
  .cwrap { position: relative; }
  .cover { display: block; width: 100%; padding: 0; border-radius: 10px; overflow: hidden; aspect-ratio: 1; border: 1px solid var(--border); background: var(--panel); }
  .cover img { width: 100%; height: 100%; object-fit: cover; display: block; transition: transform 0.15s ease; }
  .cover .ph { width: 100%; height: 100%; display: grid; place-items: center; color: var(--muted); }
  .card:hover .cover { border-color: var(--accent); }
  .card:hover .cover img { transform: scale(1.03); }
  .cardplay {
    position: absolute; right: 0.45rem; bottom: 0.45rem; width: 2.4rem; height: 2.4rem;
    display: grid; place-items: center; border-radius: 50%;
    background: color-mix(in srgb, var(--accent) 85%, var(--panel)); color: var(--text);
    opacity: 0; transition: opacity 0.15s ease;
  }
  .card:hover .cardplay, .cardplay:focus-visible { opacity: 1; }
  @media (hover: none) { .cardplay { display: none; } }
  .cname { text-align: left; padding: 0; font-size: 0.86rem; line-height: 1.25; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; line-clamp: 2; -webkit-box-orient: vertical; }
  .cname:hover { color: var(--accent-2); }
  .csub { font-size: 0.75rem; color: var(--muted); }

  @media (min-width: 48rem) {
    .hero { flex-direction: row; align-items: flex-end; text-align: left; gap: 1.6rem; padding-top: 1rem; }
    .portrait { width: 13rem; height: 13rem; }
    .hero-body { align-items: flex-start; }
    h1 { font-size: 2.6rem; }
    .acts { justify-content: flex-start; }
    .bio { margin-left: 0; }
    .tracks { grid-template-columns: 1fr 1fr; }
    .grid { grid-template-columns: repeat(auto-fill, minmax(9.5rem, 1fr)); }
  }
  @media (max-width: 767px) {
    .artbg { margin: -1rem -1rem 0; }
  }
  @media (max-width: 30rem) {
    .tplays { display: none; }
  }
</style>
