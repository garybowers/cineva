<script lang="ts">
  // The now-playing screen (server/docs/music.md, part 2): one screen in
  // three sizes. On a phone it fills the screen, with Now playing, Up next
  // and Lyrics as tabs at the bottom; on a desktop it is a panel on the
  // right over the page, Up next beside the cover; on a TV it fills the
  // screen and takes the remote. A view over the queue store; the engine
  // (MusicPlayer) does the playing.
  import { onDestroy } from 'svelte'
  import {
    Shuffle, SkipBack, SkipForward, Play, Pause, Repeat, Repeat1, X, Music as MusicIcon,
    Cast, MonitorSmartphone, Lamp, ListMusic, MicVocal, Disc3,
  } from '@lucide/svelte'
  import type { Device, Item } from '../../lib/api'
  import { ownKeys, tv } from '../../lib/tv.svelte'
  import { onDismiss } from '../../lib/dismiss'
  import { currentRoom, ha } from '../../lib/homeassistant.svelte'
  import RoomPanel from '../../ha/RoomPanel.svelte'
  import Stars from '../Stars.svelte'
  import TrackMenu from '../TrackMenu.svelte'
  import { music, trackTime, addToPlaylist, picker } from '../actions.svelte'
  import { mq, current, setShuffle, cycleRepeat, clear } from './queue.svelte'
  import UpNext from './UpNext.svelte'
  import Lyrics from './Lyrics.svelte'

  let {
    onclose,
    onlocate,
    devices = [],
    onplayon,
  }: {
    onclose: () => void
    /** Go to the album. */
    onlocate: (track: Item) => void
    /** The user's other devices that have the app open, for "Play on…". */
    devices?: Device[]
    onplayon?: (d: Device) => void
  } = $props()

  const track = $derived(current())
  type Pane = 'now' | 'next' | 'lyrics'
  // A phone opens on the cover; a desktop or TV shows the cover beside
  // Up next from the start.
  const wide = () => tv.on || window.matchMedia('(min-width: 768px)').matches
  let pane = $state<Pane>(wide() ? 'next' : 'now')
  let seekPreview = $state<number | null>(null)
  const shown = $derived(seekPreview ?? mq.positionSecs)
  let devMenu = $state(false)
  $effect(() => {
    if (!devMenu) return
    return onDismiss('.np-devwrap', () => (devMenu = false))
  })
  let roomOpen = $state(false)
  const hasRoom = $derived(!!ha.rooms?.configured && !!currentRoom())

  const BACK_KEYS = new Set(['Escape', 'Backspace', 'GoBack', 'BrowserBack'])
  let box = $state<HTMLElement | null>(null)
  // On a TV the screen has the remote: Back closes it, play/pause plays
  // and pauses, left and right skip within the track while the play
  // button has the focus (elsewhere the arrows move between the controls).
  function tvKey(e: KeyboardEvent): boolean {
    if (roomOpen) return false
    if (BACK_KEYS.has(e.key)) {
      onclose()
      return true
    }
    if (e.key === 'MediaPlayPause' || e.key === 'MediaPlay' || e.key === 'MediaPause') {
      mq.controls?.toggle()
      return true
    }
    const onPlay = document.activeElement?.classList.contains('playpause')
    if (onPlay && (e.key === 'ArrowLeft' || e.key === 'ArrowRight')) {
      mq.controls?.seek(mq.positionSecs + (e.key === 'ArrowLeft' ? -10 : 10))
      return true
    }
    return false
  }
  $effect(() => {
    if (!tv.on) return
    ownKeys(tvKey)
    setTimeout(() => box?.querySelector<HTMLElement>('.playpause')?.focus(), 50)
    return () => ownKeys(null)
  })
  function onKey(e: KeyboardEvent) {
    if (tv.on) return
    if (e.key === 'Escape' && !roomOpen && !devMenu) onclose()
    // p opens the playlist picker for the playing track, unless typing.
    const typing = (e.target as HTMLElement | null)?.closest('input, textarea, [contenteditable]')
    const t = current()
    if (e.key === 'p' && !e.ctrlKey && !e.metaKey && !e.altKey && !typing && t && picker.ids.length === 0) {
      e.preventDefault()
      addToPlaylist([t.id], t.title)
    }
  }
  window.addEventListener('keydown', onKey)
  onDestroy(() => window.removeEventListener('keydown', onKey))

  function onSeekPreview(e: Event) {
    seekPreview = Number((e.target as HTMLInputElement).value)
  }
  function onSeekCommit(e: Event) {
    const v = Number((e.target as HTMLInputElement).value)
    seekPreview = null
    mq.controls?.seek(v)
  }
  const deviceTitle = (d: Device) => d.name || d.client || 'Unknown browser'
</script>

{#if track}
  <div class="np" class:tv={tv.on} role="dialog" aria-modal="true" aria-label="Now playing" data-nav-scope bind:this={box}>
    {#if track.poster}
      <div class="npbg" style="background-image: url({track.poster})" aria-hidden="true"></div>
    {/if}
    <header class="nphead">
      <span class="eyebrow">Now playing</span>
      <span class="spacer"></span>
      <button class="ctl" onclick={onclose} title="Close" aria-label="Close now playing"><X size={18} /></button>
    </header>

    <div class="npbody" class:lyricsopen={pane === 'lyrics'}>
      <div class="side" class:hidden={pane !== 'now'}>
        {#if track.poster}
          <img class="npcover" src={track.poster} alt="" />
        {:else}
          <div class="npcover ph"><MusicIcon size={64} /></div>
        {/if}
        <div class="titles">
          <button class="nptitle" onclick={() => onlocate(track)} title="Go to album">{track.title}</button>
          <div class="npsub">
            {#if track.parentId}
              <button class="link" onclick={() => track.parentId && music().openArtist({ id: track.parentId, libraryId: track.libraryId })}>{track.artist}</button>
            {:else}<span>{track.artist}</span>{/if}
            {#if track.album}<span class="muted"> · </span><button class="link" onclick={() => onlocate(track)}>{track.album}</button>{/if}
          </div>
          <div class="npstars">
            <Stars {track} size={18} />
            <button class="chip" onclick={() => addToPlaylist([track.id], track.title)} title="Add to playlist (p)"><ListMusic size={14} /> Add to playlist</button>
          </div>
        </div>

        <div class="timeline">
          <span class="time">{trackTime(Math.floor(shown))}</span>
          <input type="range" class="seek" min="0" max={mq.duration || 0} step="0.5" value={shown} oninput={onSeekPreview} onchange={onSeekCommit} aria-label="Seek" />
          <span class="time">{trackTime(Math.floor(mq.duration))}</span>
        </div>

        <div class="transport" data-nav-group="np-transport">
          <button class="ctl" class:on={mq.shuffle} onclick={() => setShuffle(!mq.shuffle)} title="Shuffle" aria-pressed={mq.shuffle}><Shuffle size={20} /></button>
          <button class="ctl" onclick={() => mq.controls?.prev()} title="Previous" aria-label="Previous"><SkipBack size={24} /></button>
          <button class="ctl playpause" onclick={() => mq.controls?.toggle()} title={mq.paused ? 'Play' : 'Pause'} aria-label={mq.paused ? 'Play' : 'Pause'}>
            {#if mq.paused}<Play size={30} />{:else}<Pause size={30} />{/if}
          </button>
          <button class="ctl" onclick={() => mq.controls?.next()} title="Next" aria-label="Next"><SkipForward size={24} /></button>
          <button class="ctl" class:on={mq.repeat !== 'off'} onclick={cycleRepeat} title="Repeat: {mq.repeat}" aria-label="Repeat: {mq.repeat}">
            {#if mq.repeat === 'one'}<Repeat1 size={20} />{:else}<Repeat size={20} />{/if}
          </button>
        </div>

        <div class="small-acts" data-nav-group="np-acts">
          {#if !tv.on}
            <input class="vol" type="range" min="0" max="1" step="0.05" value={mq.volume} oninput={(e) => mq.controls?.setVolume(Number((e.target as HTMLInputElement).value))} aria-label="Volume" />
          {/if}
          {#if mq.quality}<span class="quality" title="What is playing">{mq.quality}</span>{/if}
          {#if mq.loading}<span class="muted small">loading…</span>{/if}
          {#if onplayon && devices.length > 0}
            <span class="np-devwrap">
              <button class="chip" aria-haspopup="menu" aria-expanded={devMenu} onclick={() => (devMenu = !devMenu)}><Cast size={14} /> Play on…</button>
              {#if devMenu}
                <div class="devmenu" role="menu">
                  {#each devices as d (d.id)}
                    <button class="um-item" role="menuitem" onclick={() => { devMenu = false; onplayon(d) }}><MonitorSmartphone size={15} /> {deviceTitle(d)}</button>
                  {/each}
                </div>
              {/if}
            </span>
          {/if}
          {#if hasRoom && !tv.on}
            <button class="chip" onclick={() => (roomOpen = true)} title="The room's lights and modes"><Lamp size={14} /> Room</button>
          {/if}
          <TrackMenu {track} />
          <button class="chip" onclick={clear} title="Stop and clear the queue"><X size={14} /> Stop</button>
        </div>
      </div>

      <div class="main" class:hidden={pane === 'now'}>
        {#if pane === 'next'}
          <h2 class="panelh"><ListMusic size={16} /> Up next</h2>
          <UpNext />
        {:else if pane === 'lyrics'}
          <h2 class="panelh"><MicVocal size={16} /> Lyrics</h2>
          <Lyrics {track} />
        {/if}
      </div>
    </div>

    <nav class="panes" aria-label="Now playing views" data-nav-group="np-panes">
      <button class="pane" class:on={pane === 'now'} onclick={() => (pane = 'now')} aria-current={pane === 'now' ? 'page' : undefined}><Disc3 size={16} /> Now playing</button>
      <button class="pane" class:on={pane === 'next'} onclick={() => (pane = 'next')} aria-current={pane === 'next' ? 'page' : undefined}><ListMusic size={16} /> Up next</button>
      <button class="pane" class:on={pane === 'lyrics'} onclick={() => (pane = 'lyrics')} aria-current={pane === 'lyrics' ? 'page' : undefined}><MicVocal size={16} /> Lyrics</button>
    </nav>

    {#if roomOpen && track}
      <RoomPanel itemId={track.id} onclose={() => (roomOpen = false)} />
    {/if}
  </div>
{/if}

<style>
  /* Phone and TV: the whole screen. Desktop: a panel on the right. */
  .np {
    position: fixed; inset: 0; z-index: 60; display: flex; flex-direction: column;
    background: var(--bg); color: var(--text); overflow: hidden;
  }
  .npbg {
    position: absolute; inset: -10%; background-size: cover; background-position: center;
    filter: blur(40px) saturate(0.8); opacity: 0.35; pointer-events: none;
  }
  .np > :not(.npbg) { position: relative; }
  .nphead { display: flex; align-items: center; gap: 0.5rem; padding: 0.7rem 1rem 0; }
  .eyebrow { font-size: 0.72rem; text-transform: uppercase; letter-spacing: 0.08em; color: var(--muted); }
  .spacer { flex: 1; }
  .npbody { flex: 1; min-height: 0; display: flex; flex-direction: column; padding: 0.5rem 1rem; overflow: hidden; }
  .side { display: flex; flex-direction: column; align-items: center; gap: 0.8rem; min-height: 0; flex: 1; justify-content: center; }
  .side.hidden, .main.hidden { display: none; }
  .main { flex: 1; min-height: 0; overflow-y: auto; }
  .npcover {
    width: min(78vw, 42vh, 22rem); aspect-ratio: 1; object-fit: cover; border-radius: 16px;
    border: 1px solid var(--border); box-shadow: 0 24px 70px rgb(0 0 0 / 0.45);
  }
  .npcover.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); }
  .titles { text-align: center; max-width: 100%; }
  .nptitle { font-size: 1.3rem; font-weight: 600; color: var(--text); padding: 0; max-width: 100%; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; display: block; }
  .nptitle:hover { color: var(--accent-2); }
  .npsub { font-size: 0.92rem; color: var(--muted); margin-top: 0.1rem; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .link { color: var(--muted); padding: 0; font-size: inherit; }
  .link:hover { color: var(--accent-2); }
  .npstars { margin-top: 0.3rem; display: flex; justify-content: center; align-items: center; gap: 0.6rem; flex-wrap: wrap; }
  .timeline { display: flex; align-items: center; gap: 0.6rem; width: min(100%, 30rem); }
  .seek { flex: 1; accent-color: var(--accent); }
  .time { color: var(--muted); font-size: 0.8rem; font-variant-numeric: tabular-nums; }
  .transport { display: flex; align-items: center; justify-content: center; gap: 0.6rem; }
  .ctl { color: var(--text); padding: 0.5rem; border-radius: 50%; display: grid; place-items: center; }
  .ctl:hover { background: var(--panel); }
  .ctl.on { color: var(--accent-2); }
  .playpause { background: var(--accent); color: var(--on-accent); width: 4rem; height: 4rem; }
  .playpause:hover { background: var(--accent); filter: brightness(1.1); }
  .small-acts { display: flex; align-items: center; justify-content: center; gap: 0.5rem; flex-wrap: wrap; }
  .vol { width: 6rem; accent-color: var(--accent); }
  .quality { color: var(--muted); font-size: 0.78rem; border: 1px solid var(--border); border-radius: 6px; padding: 0.1rem 0.4rem; }
  .chip { display: inline-flex; align-items: center; gap: 0.35rem; border-radius: 999px; font-size: 0.82rem; padding: 0.35rem 0.8rem; border: 1px solid var(--border); background: var(--panel); color: var(--text); }
  .chip:hover { border-color: var(--accent); }
  .np-devwrap { position: relative; }
  .devmenu { position: absolute; bottom: calc(100% + 0.3rem); left: 0; z-index: 5; min-width: 12rem; background: var(--panel); border: 1px solid var(--border); border-radius: 10px; padding: 0.35rem; display: flex; flex-direction: column; }
  .um-item { display: flex; align-items: center; gap: 0.5rem; padding: 0.5rem 0.6rem; border-radius: 6px; text-align: left; font-size: 0.88rem; color: var(--text); white-space: nowrap; }
  .um-item:hover { background: var(--bg); }
  .muted { color: var(--muted); }
  .small { font-size: 0.8rem; }
  .panelh { display: flex; align-items: center; gap: 0.4rem; font-size: 1rem; margin: 0.4rem 0 0.8rem; }
  .panes { display: flex; border-top: 1px solid var(--border); background: var(--panel); }
  .pane { flex: 1; display: flex; align-items: center; justify-content: center; gap: 0.4rem; padding: 0.8rem 0.5rem; font-size: 0.82rem; color: var(--muted); }
  .pane.on { color: var(--accent-2); }

  /* Desktop: a panel on the right over the page, the cover and controls
     beside Up next or the lyrics. */
  @media (min-width: 768px) {
    .np { left: auto; width: min(64rem, 92vw); top: var(--topbar-h, 3.4rem); border-left: 1px solid var(--border); box-shadow: -12px 0 40px rgb(0 0 0 / 0.35); }
    .npbody { flex-direction: row; gap: 1.5rem; padding: 0.5rem 1.25rem 1rem; }
    .side { flex: none; width: 22rem; justify-content: flex-start; padding-top: 0.5rem; }
    .side.hidden { display: flex; }
    .main.hidden { display: block; }
    .npcover { width: 18rem; }
    .panes { order: -1; border-top: 0; border-bottom: 1px solid var(--border); background: none; justify-content: flex-end; padding: 0 1rem; }
    .pane { flex: none; padding: 0.5rem 0.8rem; }
    .pane:first-child { display: none; }
  }
  .np.tv { left: 0; width: auto; top: 0; border: 0; box-shadow: none; }
  .np.tv .npbody { flex-direction: row; }
  .np.tv .side.hidden { display: flex; }
  .np.tv .main.hidden { display: block; }
  .np.tv .pane:first-child { display: none; }
  :global(html.tv) .ctl:focus, :global(html.tv) .chip:focus, :global(html.tv) .pane:focus { outline: none; background: var(--accent); color: var(--on-accent); }
  :global(html.tv) .playpause:focus { box-shadow: 0 0 0 0.25rem var(--accent-2); }
</style>
