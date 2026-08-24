<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import { postProgress, type Item } from './lib/api'
  import {
    Shuffle, SkipBack, SkipForward, Play, Pause, Repeat, Repeat1,
    Volume2, VolumeX, X, Music as MusicIcon, Disc3, Download,
  } from '@lucide/svelte'

  let {
    queue,
    startIndex = 0,
    startShuffle = false,
    onclose,
    onlocate,
    onexpand,
    onchange,
    ontime,
  }: {
    queue: Item[]
    startIndex?: number
    startShuffle?: boolean
    onclose: () => void
    onlocate: (track: Item) => void
    onexpand?: () => void
    onchange?: (track: Item, index: number) => void
    ontime?: (secs: number) => void
  } = $props()

  let audio: HTMLAudioElement
  let index = $state(0)
  let paused = $state(false)
  let current = $state(0)
  let duration = $state(0)
  let shuffle = $state(startShuffle)
  let repeat: 'off' | 'all' | 'one' = $state('off')
  let volume = $state(readVolume())
  let muted = $state(false)
  let seeking = $state(false) // true while the seek thumb is being dragged

  const track = $derived(queue[index])

  function readVolume(): number {
    try {
      return Number(localStorage.getItem('cineva-volume') ?? 1)
    } catch {
      return 1
    }
  }

  function load(i: number, autoplay = true) {
    index = Math.max(0, Math.min(queue.length - 1, i))
    current = 0
    duration = queue[index].runtimeSecs ?? 0
    audio.src = `/api/v1/items/${queue[index].id}/stream`
    if (autoplay) audio.play().catch(() => {})
    onchange?.(queue[index], index)
  }

  // Jump to a queue position (used by the listening-session view).
  export function jumpTo(i: number) {
    load(i)
  }

  onMount(() => {
    audio.volume = volume
    load(shuffle && queue.length > 1 ? Math.floor(Math.random() * queue.length) : startIndex)
  })

  onDestroy(() => audio?.pause())

  function markPlayed() {
    if (track) postProgress(track.id, track.runtimeSecs ?? 0).catch(() => {})
  }

  function pickNext(): number | null {
    if (repeat === 'one') return index
    if (shuffle && queue.length > 1) {
      let n = index
      while (n === index) n = Math.floor(Math.random() * queue.length)
      return n
    }
    if (index + 1 < queue.length) return index + 1
    return repeat === 'all' ? 0 : null
  }

  function next() {
    const n = pickNext()
    if (n === null) {
      paused = true
      audio.pause()
    } else {
      load(n)
    }
  }

  function prev() {
    if (audio.currentTime > 3 || index === 0) {
      audio.currentTime = 0
    } else {
      load(index - 1)
    }
  }

  function onEnded() {
    markPlayed()
    next()
  }

  function togglePlay() {
    if (audio.paused) audio.play().catch(() => {})
    else audio.pause()
  }

  function cycleRepeat() {
    repeat = repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'
  }

  // Dragging previews the position; the actual seek happens once on release.
  // Seeking on every input event fires dozens of decoder seeks per second
  // (audible crackle) while timeupdate fights the thumb (haywire bar).
  function onSeekPreview(e: Event) {
    seeking = true
    current = Number((e.target as HTMLInputElement).value)
  }

  function onSeekCommit(e: Event) {
    audio.currentTime = Number((e.target as HTMLInputElement).value)
    seeking = false
  }

  function onVolume(e: Event) {
    volume = Number((e.target as HTMLInputElement).value)
    audio.volume = volume
    if (volume > 0) audio.muted = muted = false
    try {
      localStorage.setItem('cineva-volume', String(volume))
    } catch { /* private browsing */ }
  }

  function fmt(secs: number): string {
    if (!isFinite(secs) || secs < 0) secs = 0
    const s = Math.floor(secs)
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
  }

  const pct = (v: number) => (duration > 0 ? (v / duration) * 100 : 0)
</script>

<audio
  bind:this={audio}
  ontimeupdate={() => { if (!seeking) current = audio.currentTime; if (isFinite(audio.duration)) duration = audio.duration; ontime?.(audio.currentTime) }}
  onplay={() => (paused = false)}
  onpause={() => (paused = true)}
  onended={onEnded}
></audio>

{#if track}
  <div class="musicbar">
    <div class="mb-progress"><div style="width: {pct(current)}%"></div></div>

    {#if track.poster}
      <img class="cover" src={track.poster} alt="" />
    {:else}
      <span class="cover ph"><MusicIcon size={22} /></span>
    {/if}

    <div class="info">
      <button class="mb-title" onclick={() => onlocate(track)} title="Go to album">{track.title}</button>
      <span class="mb-artist">{track.artist}</span>
    </div>

    <div class="transport">
      <button class="ctl" class:on={shuffle} onclick={() => (shuffle = !shuffle)} title="Shuffle"><Shuffle size={16} /></button>
      <button class="ctl" onclick={prev} title="Previous"><SkipBack size={17} /></button>
      <button class="ctl big" onclick={togglePlay}>{#if paused}<Play size={22} />{:else}<Pause size={22} />{/if}</button>
      <button class="ctl" onclick={next} title="Next"><SkipForward size={17} /></button>
      <button class="ctl" class:on={repeat !== 'off'} onclick={cycleRepeat} title="Repeat: {repeat}">
        {#if repeat === 'one'}<Repeat1 size={16} />{:else}<Repeat size={16} />{/if}
      </button>
    </div>

    <div class="seekarea">
      <span class="time">{fmt(current)}</span>
      <input type="range" class="seek" min="0" max={duration || 0} step="0.5" value={current} oninput={onSeekPreview} onchange={onSeekCommit} aria-label="Seek" />
      <span class="time">{fmt(duration)}</span>
    </div>

    <span class="qpos">{index + 1}/{queue.length}</span>
    <input class="vol" type="range" min="0" max="1" step="0.05" value={muted ? 0 : volume} oninput={onVolume} aria-label="Volume" />
    <select class="dlsel" title="Download track" onchange={(e) => {
      const v = (e.target as HTMLSelectElement).value
      if (v) {
        const [f, q] = v.split('-')
        const u = f === 'original' ? `/api/v1/items/${track.id}/download` : `/api/v1/items/${track.id}/download?format=${f}&quality=${q}`
        const a = document.createElement('a'); a.href = u; a.download = ''; document.body.appendChild(a); a.click(); a.remove()
      }
      ;(e.target as HTMLSelectElement).value = ''
    }}>
      <option value="">DL…</option>
      <option value="original">Original</option>
      <option value="mp3-320">MP3 320</option>
      <option value="mp3-192">MP3 192</option>
      <option value="ogg-192">OGG 192</option>
    </select>
    {#if onexpand}
      <button class="ctl" onclick={onexpand} title="Listening session"><Disc3 size={17} /></button>
    {/if}
    <button class="ctl" onclick={onclose} title="Stop"><X size={17} /></button>
  </div>
{/if}

<style>
  .musicbar {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 45;
    display: flex;
    align-items: center;
    gap: 0.8rem;
    height: 4.6rem;
    padding: 0 0.9rem;
    background: var(--panel);
    border-top: 1px solid var(--border);
  }
  @media (max-width: 767px) {
    .musicbar { bottom: 3.35rem; }
    .seekarea { display: none; } /* progress strip carries position on phones */
    .vol { display: none; }
  }
  .mb-progress {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: var(--border);
  }
  .mb-progress div { height: 100%; background: var(--accent); }

  .cover {
    width: 3.1rem;
    height: 3.1rem;
    border-radius: 8px;
    object-fit: cover;
    flex: none;
  }
  .cover.ph {
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--muted);
    font-size: 1.4rem;
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

  .transport { display: flex; align-items: center; gap: 0.1rem; flex: none; }
  .ctl {
    color: var(--muted);
    font-size: 0.95rem;
    padding: 0.3rem 0.45rem;
    border-radius: 6px;
  }
  .ctl:hover { background: var(--bg); color: var(--text); }
  .dl-a { display: grid; place-items: center; }
  .dlsel {
    background: var(--bg);
    color: var(--muted);
    border: 1px solid var(--border);
    border-radius: 8px;
    font: inherit;
    font-size: 0.78rem;
    padding: 0.2rem 0.3rem;
    flex: none;
  }
  .ctl.on { color: var(--accent-2); }
  .ctl.big { font-size: 1.3rem; }

  .seekarea { flex: 1; display: flex; align-items: center; gap: 0.5rem; min-width: 0; }
  .seek { flex: 1; accent-color: var(--accent); }
  .time { color: var(--muted); font-size: 0.78rem; font-variant-numeric: tabular-nums; flex: none; }
  .qpos { color: var(--muted); font-size: 0.78rem; flex: none; }
  .vol { width: 5rem; accent-color: var(--accent); flex: none; }
</style>
