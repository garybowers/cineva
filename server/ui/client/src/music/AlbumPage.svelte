<script lang="ts">
  // An album's page: cover, title, the artist as a link, the information
  // line ("2013 · 12 tracks · 41 min · FLAC 24-bit 96 kHz"), the actions,
  // the track list with disc headings, and the artist's other albums.
  import { Play, Shuffle, ListPlus, ListStart, ListMusic, Music as MusicIcon, Sparkles } from '@lucide/svelte'
  import { libraryAlbums, itemTracks, type Item } from '../lib/api'
  import { music, addToPlaylist, totalTime } from './actions.svelte'
  import { mq, play, playNext, append } from './player/queue.svelte'
  import TrackList from './TrackList.svelte'
  import DownloadMenu from './DownloadMenu.svelte'
  import AlbumGrid from './library/AlbumGrid.svelte'

  let {
    album,
    tracks,
    highlightId,
  }: {
    album: Item
    /** The album's tracks, in disc and track order (item detail's children). */
    tracks: Item[]
    /** A track to pick out: the album was opened on a song. */
    highlightId?: string
  } = $props()

  const source = $derived({ kind: 'album', id: album.id, title: album.title })
  const secs = $derived(tracks.reduce((n, t) => n + (t.runtimeSecs ?? 0), 0))

  /** "FLAC 24-bit 96 kHz", "MP3 320 kbit/s": what the files are, from the first track. */
  const format = $derived.by(() => {
    const t = tracks[0]
    if (!t) return ''
    const codec = (t.audioCodec || t.container || '').toUpperCase()
    if (t.bitDepth && t.sampleRate) return `${codec} ${t.bitDepth}-bit ${t.sampleRate % 1000 === 0 ? t.sampleRate / 1000 : (t.sampleRate / 1000).toFixed(1)} kHz`
    if (t.bitrate) return `${codec} ${t.bitrate} kbit/s`
    return codec
  })
  const info = $derived([
    album.year ? String(album.year) : '',
    tracks.length === 1 ? '1 track' : `${tracks.length} tracks`,
    secs ? totalTime(secs) : '',
    format,
  ].filter(Boolean).join(' · '))

  // The artist's other albums, newest first.
  let others = $state<Item[]>([])
  $effect(() => {
    const id = album.id
    const artist = album.parentId
    others = []
    if (!artist || album.compilation) return
    libraryAlbums(album.libraryId, { artist, sort: 'year', size: 20 })
      .then((p) => { if (album.id === id) others = p.items.filter((a) => a.id !== id) })
      .catch(() => (others = []))
  })

  const playing = $derived(mq.active ? mq.items[mq.index]?.id : undefined)
  async function playOther(al: Item) {
    try {
      play(await itemTracks(al.id), 0, { source: { kind: 'album', id: al.id, title: al.title } })
    } catch (err) {
      music().notify('error', err instanceof Error ? err.message : String(err))
    }
  }
</script>

<div class="albumpage">
  {#if album.poster}
    <div class="artbg" style="background-image: url({album.poster})" aria-hidden="true"></div>
  {/if}
  <header class="hero">
    {#if album.poster}
      <img class="cover" src={album.poster} alt={album.title} />
    {:else}
      <span class="cover ph"><MusicIcon size={48} /></span>
    {/if}
    <div class="hero-body">
      <span class="eyebrow">{album.compilation ? 'Compilation' : 'Album'}</span>
      <h1>{album.title}</h1>
      <p class="by">
        {#if album.parentId}
          <button class="artistlink" onclick={() => album.parentId && music().openArtist({ id: album.parentId, libraryId: album.libraryId })}>{album.artist}</button>
        {:else}{album.artist}{/if}
      </p>
      {#if info}<p class="info">{info}</p>{/if}
      <div class="actions acts">
        <button class="play primary" onclick={() => play(tracks, 0, { source })} disabled={tracks.length === 0}><Play size={16} /> Play</button>
        <button class="btn" onclick={() => play(tracks, 0, { source, shuffle: true })} disabled={tracks.length === 0}><Shuffle size={15} /> Shuffle</button>
        <button class="btn" onclick={() => music().mix(album)} disabled={tracks.length === 0} title="A mix from this album: its best, then what belongs beside it"><Sparkles size={15} /> Mix</button>
        <button class="btn icon" onclick={() => playNext(tracks)} disabled={tracks.length === 0} title="Play next" aria-label="Play next"><ListStart size={16} /></button>
        <button class="btn icon" onclick={() => append(tracks)} disabled={tracks.length === 0} title="Add to queue" aria-label="Add to queue"><ListPlus size={16} /></button>
        <button class="btn icon" onclick={() => addToPlaylist([album.id], album.title)} disabled={tracks.length === 0} title="Add to playlist" aria-label="Add to playlist"><ListMusic size={16} /></button>
        <DownloadMenu id={album.id} compact />
      </div>
    </div>
  </header>

  {#if album.overview}<p class="overview">{album.overview}</p>{/if}

  <TrackList {tracks} mode="album" albumArtist={album.artist} currentId={playing} {highlightId}
    onplay={(i) => play(tracks, i, { source, shuffle: mq.shuffle && mq.source?.id === album.id, startHere: true })} />

  {#if others.length > 0}
    <section class="more">
      <h2>More by {album.artist}</h2>
      <AlbumGrid items={others} showArtist={false} small onopen={(al) => music().openAlbum(al)} onplay={playOther} />
    </section>
  {/if}
</div>

<style>
  .albumpage { position: relative; }
  .albumpage > :not(.artbg) { position: relative; }
  .artbg {
    position: absolute; top: 0; left: 0; right: 0; height: 24rem;
    margin: -1.25rem -1.5rem 0;
    background-size: cover; background-position: center 30%;
    opacity: 0.25; filter: saturate(0.7) blur(26px); transform: scale(1.06);
    -webkit-mask-image: linear-gradient(to bottom, var(--bg) 35%, transparent);
    mask-image: linear-gradient(to bottom, var(--bg) 35%, transparent);
  }
  .hero { display: flex; flex-direction: column; align-items: center; text-align: center; gap: 1rem; padding: 0.5rem 0 1rem; }
  .cover {
    width: 12rem; height: 12rem; object-fit: cover; flex: none; border-radius: 12px;
    border: 1px solid var(--border); background: var(--panel);
    box-shadow: 0 12px 32px color-mix(in srgb, var(--bg) 60%, transparent);
  }
  .cover.ph { display: grid; place-items: center; color: var(--muted); }
  .hero-body { min-width: 0; display: flex; flex-direction: column; align-items: center; gap: 0.3rem; }
  .eyebrow { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); }
  h1 { font-size: 1.8rem; line-height: 1.1; margin: 0; overflow-wrap: anywhere; }
  .by { font-size: 1rem; margin: 0; }
  .artistlink { padding: 0; color: var(--text); text-decoration: underline; text-decoration-color: var(--border); text-underline-offset: 3px; font-size: inherit; }
  .artistlink:hover { color: var(--accent-2); text-decoration-color: currentColor; }
  .info { color: var(--muted); font-size: 0.88rem; margin: 0; }
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
  .overview { max-width: 52rem; font-size: 0.95rem; line-height: 1.55; margin: 0.4rem auto 1rem; }
  h2 { font-size: 1.1rem; margin: 1.8rem 0 0.7rem; }
  @media (min-width: 48rem) {
    .hero { flex-direction: row; align-items: flex-end; text-align: left; gap: 1.6rem; padding-top: 1rem; }
    .cover { width: 14rem; height: 14rem; }
    .hero-body { align-items: flex-start; }
    h1 { font-size: 2.4rem; }
    .acts { justify-content: flex-start; }
    .overview { margin-left: 0; }
  }
  @media (max-width: 767px) { .artbg { margin: -1rem -1rem 0; } }
  :global(html.tv) .primary:focus, :global(html.tv) .btn:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
