<script lang="ts">
  // The music bar: under the top bar, across the window, whenever a queue
  // exists. A view over the queue store; the engine (MusicPlayer) does the
  // playing. On a phone it is a compact strip that opens the now-playing
  // screen when tapped; on desktop it carries the stars, the timeline and
  // the volume as well.
  import { Shuffle, SkipBack, SkipForward, Play, Pause, Repeat, Repeat1, X, Music as MusicIcon, ListMusic, ChevronDown } from '@lucide/svelte'
  import { tv } from '../../lib/tv.svelte'
  import Stars from '../Stars.svelte'
  import TrackMenu from '../TrackMenu.svelte'
  import { mq, current, setShuffle, cycleRepeat, clear } from './queue.svelte'
  import { trackTime, addToPlaylist } from '../actions.svelte'

  let {
    onlocate,
    onexpand,
    expanded = false,
    at = 'top',
  }: {
    /** The title was pressed: go to the album. */
    onlocate: () => void
    /** Open or close the now-playing screen. */
    onexpand: () => void
    expanded?: boolean
    /** Where the bar sits: the music-bar preference. */
    at?: 'top' | 'bottom'
  } = $props()

  const track = $derived(current())
  let seekPreview = $state<number | null>(null)
  const shown = $derived(seekPreview ?? mq.positionSecs)
  const pct = $derived(mq.duration > 0 ? (shown / mq.duration) * 100 : 0)

  function onSeekPreview(e: Event) {
    seekPreview = Number((e.target as HTMLInputElement).value)
  }
  function onSeekCommit(e: Event) {
    const v = Number((e.target as HTMLInputElement).value)
    seekPreview = null
    mq.controls?.seek(v)
  }
</script>

{#if track}
  <div class="musicbar" class:expanded class:bottom={at === 'bottom'} role="region" aria-label="Now playing" data-nav-group="musicbar">
    <div class="mb-progress"><div style="width: {pct}%"></div></div>

    <button class="mb-cover" onclick={onexpand} aria-label={expanded ? 'Close now playing' : 'Open now playing'}>
      {#if track.poster}
        <img class="cover" src={track.poster} alt="" />
      {:else}
        <span class="cover ph"><MusicIcon size={22} /></span>
      {/if}
    </button>

    <div class="info">
      <button class="mb-title" onclick={onlocate} title="Go to album">{track.title}</button>
      <span class="mb-artist">{track.artist}{track.album ? ` · ${track.album}` : ''}{mq.loading ? ' · loading…' : ''}</span>
    </div>
    <span class="mb-stars"><Stars {track} size={15} /></span>
    <button class="ctl mb-add" onclick={() => addToPlaylist([track.id], track.title)} title="Add to playlist" aria-label="Add to playlist"><ListMusic size={16} /></button>

    <div class="transport">
      <button class="ctl" class:on={mq.shuffle} onclick={() => setShuffle(!mq.shuffle)} title="Shuffle" aria-pressed={mq.shuffle}><Shuffle size={16} /></button>
      <button class="ctl" onclick={() => mq.controls?.prev()} title="Previous" aria-label="Previous"><SkipBack size={17} /></button>
      <button class="ctl big playpause" onclick={() => mq.controls?.toggle()} title={mq.paused ? 'Play' : 'Pause'} aria-label={mq.paused ? 'Play' : 'Pause'}>
        {#if mq.paused}<Play size={22} />{:else}<Pause size={22} />{/if}
      </button>
      <button class="ctl" onclick={() => mq.controls?.next()} title="Next" aria-label="Next"><SkipForward size={17} /></button>
      <button class="ctl" class:on={mq.repeat !== 'off'} onclick={cycleRepeat} title="Repeat: {mq.repeat}" aria-label="Repeat: {mq.repeat}">
        {#if mq.repeat === 'one'}<Repeat1 size={16} />{:else}<Repeat size={16} />{/if}
      </button>
    </div>

    <div class="seekarea">
      <span class="time">{trackTime(Math.floor(shown))}</span>
      <input type="range" class="seek" min="0" max={mq.duration || 0} step="0.5" value={shown} oninput={onSeekPreview} onchange={onSeekCommit} aria-label="Seek" />
      <span class="time">{trackTime(Math.floor(mq.duration))}</span>
    </div>

    <span class="qpos" title="Place in the queue">{mq.index + 1}/{mq.items.length}</span>
    {#if mq.quality}<span class="quality" title="What is playing">{mq.quality}</span>{/if}
    {#if !tv.on}
      <input class="vol" type="range" min="0" max="1" step="0.05" value={mq.volume} oninput={(e) => mq.controls?.setVolume(Number((e.target as HTMLInputElement).value))} aria-label="Volume" />
    {/if}
    <span class="mb-menu"><TrackMenu {track} /></span>
    <button class="ctl" onclick={onexpand} title={expanded ? 'Close now playing' : 'Now playing'} aria-label={expanded ? 'Close now playing' : 'Now playing'}>
      {#if expanded}<ChevronDown size={17} />{:else}<ListMusic size={17} />{/if}
    </button>
    <button class="ctl" onclick={clear} title="Stop" aria-label="Stop"><X size={17} /></button>
  </div>
{/if}

<style>
  /* Under the top bar, across the window: the page's --music-bar makes
     room for it (App.svelte). The music-bar preference puts it at the
     bottom instead, above a phone's tab bar, where the page pads the
     bottom as it does for the video player's compact bar. */
  .musicbar {
    position: fixed;
    left: 0;
    right: 0;
    top: var(--topbar-h, 3.4rem);
    z-index: 45;
    display: flex;
    align-items: center;
    gap: 0.8rem;
    height: 4.6rem;
    padding: 0 0.9rem;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
  }
  .musicbar.bottom {
    top: auto;
    bottom: 0;
    border-bottom: none;
    border-top: 1px solid var(--border);
  }
  /* The progress strip sits on the bar's edge nearest the page. */
  .musicbar.bottom .mb-progress { bottom: auto; top: 0; }
  .mb-progress {
    position: absolute;
    bottom: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: var(--border);
  }
  .mb-progress div { height: 100%; background: var(--accent); }

  .mb-cover { padding: 0; flex: none; border-radius: 8px; }
  .cover {
    width: 3.1rem;
    height: 3.1rem;
    border-radius: 8px;
    object-fit: cover;
    flex: none;
    display: block;
  }
  .cover.ph {
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--muted);
  }
  .info { display: flex; flex-direction: column; min-width: 0; width: 14rem; flex: none; }
  .mb-title {
    text-align: left;
    font-weight: 600;
    font-size: 0.9rem;
    color: var(--text);
    padding: 0;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .mb-title:hover { color: var(--accent-2); }
  .mb-artist { font-size: 0.78rem; color: var(--muted); white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .mb-stars, .mb-menu { flex: none; }

  .transport { display: flex; align-items: center; gap: 0.1rem; flex: none; }
  .ctl {
    color: var(--muted);
    padding: 0.3rem 0.45rem;
    border-radius: 6px;
    display: grid;
    place-items: center;
  }
  .ctl:hover { background: var(--bg); color: var(--text); }
  .ctl.on { color: var(--accent-2); }
  .ctl.big { padding: 0.35rem 0.5rem; }

  .seekarea { flex: 1; display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .seek { flex: 1; accent-color: var(--accent); }
  .time { color: var(--muted); font-size: 0.78rem; font-variant-numeric: tabular-nums; flex: none; }
  .qpos, .quality { color: var(--muted); font-size: 0.78rem; flex: none; white-space: nowrap; }
  .quality { border: 1px solid var(--border); border-radius: 6px; padding: 0.1rem 0.4rem; }
  .vol { width: 5rem; accent-color: var(--accent); flex: none; }

  @media (max-width: 767px) {
    .musicbar { height: 3.6rem; gap: 0.5rem; padding: 0 0.6rem; }
    .musicbar.bottom { bottom: 3.35rem; } /* above the tab bar */
    .cover { width: 2.5rem; height: 2.5rem; }
    .info { width: auto; flex: 1; }
    /* The strip carries position; the screen has the rest. */
    .seekarea, .vol, .mb-stars, .mb-add, .qpos, .quality, .mb-menu,
    .transport .ctl:not(.playpause):not(:nth-child(4)) { display: none; }
  }
  :global(html.tv) .ctl:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
