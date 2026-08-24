<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import {
    nextUp, postProgress, stopSession, QUALITIES,
    type Item, type PlayInfo, type PlayQuality,
  } from './lib/api'
  import {
    Play, Pause, RotateCcw, RotateCw, Volume2, VolumeX, X, Maximize,
    Maximize2,
  } from '@lucide/svelte'

  let {
    item,
    info,
    quality,
    compact = false,
    onclose,
    onnext,
    onquality,
    onexpand,
    ontime,
  }: {
    item: Item
    info: PlayInfo
    quality: PlayQuality
    compact?: boolean
    onclose: () => void
    onnext: (next: Item) => void
    onquality: (q: PlayQuality) => void
    onexpand: () => void
    ontime?: (secs: number) => void
  } = $props()

  // v1 credits window heuristic — replaced by credits *detection* later.
  const CREDITS_WINDOW = 40

  let box: HTMLElement
  let video: HTMLVideoElement
  let hls: Hls | null = null
  let next: Item | null = $state(null)
  let showNext = $state(false)
  let remaining = $state(0)
  let subtitle = $state('off')

  // Transport state for the custom controls.
  let paused = $state(true)
  let current = $state(0)
  let duration = $state(info.durationSecs || 0)
  let bufferedEnd = $state(0)
  let muted = $state(false)
  let volume = $state(readVolume())
  let idle = $state(false)
  let seeking = $state(false) // true while the seek thumb is being dragged

  let dismissed = false
  let lastPosted = -100
  let idleTimer: ReturnType<typeof setTimeout> | undefined

  const textSubs = info.subtitles?.filter((t) => t.url) ?? []
  const subLabel = (t: (typeof textSubs)[number]) =>
    t.title || t.language?.toUpperCase() || t.codec

  function readVolume(): number {
    try {
      return Number(localStorage.getItem('cineva-volume') ?? 1)
    } catch {
      return 1
    }
  }

  const autoplayNext = () => {
    try {
      return localStorage.getItem('cineva-autoplay') !== 'off'
    } catch {
      return true
    }
  }

  onMount(() => {
    if (info.mode === 'hls' && Hls.isSupported()) {
      // startPosition (not a pre-metadata currentTime seek): a currentTime
      // seek before the manifest loads makes hls.js first fetch segment 0,
      // thrashing the server-side transcoder with a restart-to-zero.
      hls = new Hls({ startPosition: info.positionSecs > 0 ? info.positionSecs : -1 })
      hls.loadSource(info.url)
      hls.attachMedia(video)
    } else {
      video.src = info.url // direct play, or Safari's native HLS
      if (info.positionSecs > 0) video.currentTime = info.positionSecs
    }
    for (const track of video.textTracks) track.mode = 'hidden' // off until picked
    video.volume = volume
    video.play().catch(() => {})
    if (!compact) box.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    if (item.kind === 'episode') {
      nextUp(item.id).then((n) => (next = n)).catch(() => {})
    }
  })

  onDestroy(() => {
    reportProgress(true)
    hls?.destroy()
    if (info.sessionId) stopSession(info.sessionId)
    clearTimeout(idleTimer)
  })

  function reportProgress(force = false) {
    if (!video) return
    const t = Math.floor(video.currentTime)
    if (force || Math.abs(t - lastPosted) >= 10) {
      lastPosted = t
      postProgress(item.id, t).catch(() => {})
    }
  }

  function onTimeUpdate() {
    if (!seeking) current = video.currentTime
    ontime?.(video.currentTime)
    if (video.duration && isFinite(video.duration)) duration = video.duration
    if (video.buffered.length > 0) bufferedEnd = video.buffered.end(video.buffered.length - 1)
    reportProgress()
    if (!duration) return
    const left = duration - current
    if (next && !dismissed && left <= CREDITS_WINDOW) {
      showNext = true
      remaining = Math.max(0, Math.ceil(left))
    } else {
      showNext = false
    }
  }

  function onEnded() {
    reportProgress(true)
    if (next && !dismissed && autoplayNext()) {
      onnext(next)
    } else {
      onclose()
    }
  }

  function togglePlay() {
    if (video.paused) video.play().catch(() => {})
    else video.pause()
  }

  function skip(secs: number) {
    video.currentTime = Math.max(0, Math.min(duration || Infinity, video.currentTime + secs))
  }

  // Preview while dragging; the real seek fires once on release. Per-tick
  // seeks would thrash the decoder — and for HLS, every drag tick can mean
  // a server-side transcoder restart.
  function onSeekPreview(e: Event) {
    seeking = true
    current = Number((e.target as HTMLInputElement).value)
  }

  function onSeekCommit(e: Event) {
    const t = Number((e.target as HTMLInputElement).value)
    video.currentTime = t
    current = t
    seeking = false
  }

  function onVolumeInput(e: Event) {
    volume = Number((e.target as HTMLInputElement).value)
    video.volume = volume
    if (volume > 0) video.muted = muted = false
    try {
      localStorage.setItem('cineva-volume', String(volume))
    } catch { /* private browsing */ }
  }

  function toggleMute() {
    muted = !muted
    video.muted = muted
  }

  function toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen?.()
    else box.requestFullscreen?.().catch(() => {})
  }

  function wake() {
    idle = false
    clearTimeout(idleTimer)
    idleTimer = setTimeout(() => {
      if (document.fullscreenElement && !video.paused) idle = true
    }, 3000)
  }

  function onKeydown(e: KeyboardEvent) {
    const target = e.target as HTMLElement
    if (['INPUT', 'SELECT', 'TEXTAREA'].includes(target.tagName)) return
    switch (e.key) {
      case ' ':
        e.preventDefault()
        togglePlay()
        break
      case 'ArrowLeft':
        skip(-10)
        break
      case 'ArrowRight':
        skip(10)
        break
      case 'f':
        toggleFullscreen()
        break
      case 'm':
        toggleMute()
        break
    }
    wake()
  }

  function selectSubtitle(e: Event) {
    subtitle = (e.target as HTMLSelectElement).value
    for (const track of video.textTracks) {
      track.mode = track.id === subtitle ? 'showing' : 'hidden'
    }
  }

  function changeQuality(e: Event) {
    const q = (e.target as HTMLSelectElement).value as PlayQuality
    reportProgress(true) // so the new session resumes at the same spot
    onquality(q)
  }

  const modeLabel = () =>
    info.mode === 'direct' ? 'Direct Play' : `Transcoding${info.reason ? ` — ${info.reason}` : ''}`

  function fmt(secs: number): string {
    if (!isFinite(secs) || secs < 0) secs = 0
    const s = Math.floor(secs)
    const h = Math.floor(s / 3600)
    const m = Math.floor((s % 3600) / 60)
    const sec = String(s % 60).padStart(2, '0')
    return h > 0 ? `${h}:${String(m).padStart(2, '0')}:${sec}` : `${m}:${sec}`
  }

  const epCode = (i: Item) => `S${String(i.seasonNum).padStart(2, '0')}E${String(i.episodeNum).padStart(2, '0')}`
  const pct = (v: number) => (duration > 0 ? (v / duration) * 100 : 0)
</script>

<svelte:window onkeydown={onKeydown} />

<section class="nowplaying" class:idle class:compact bind:this={box} onmousemove={wake}>
  {#if compact}
    <div class="mini-progress"><div style="width: {pct(current)}%"></div></div>
  {/if}

  {#if !compact}
    <div class="bar">
      <span class="bartitle">
        {item.title}
        {#if item.kind === 'episode'}<span class="sub">{epCode(item)}</span>{/if}
        <span class="mode" title={info.reason ?? ''}>{modeLabel()}</span>
      </span>
      {#if textSubs.length > 0}
        <select class="picker" value={subtitle} onchange={selectSubtitle} title="Subtitles">
          <option value="off">CC Off</option>
          {#each textSubs as t (t.id)}
            <option value={t.id}>{subLabel(t)}</option>
          {/each}
        </select>
      {/if}
      <select class="picker" value={quality} onchange={changeQuality} title="Playback quality">
        {#each QUALITIES as q (q.id)}
          <option value={q.id}>{q.label}</option>
        {/each}
      </select>
      <button class="ctl" onclick={onclose} title="Stop playback"><X size={17} /></button>
    </div>
  {/if}

  <div class="stage">
    <video
      bind:this={video}
      playsinline
      crossorigin="anonymous"
      ontimeupdate={onTimeUpdate}
      onended={onEnded}
      onplay={() => { paused = false; wake() }}
      onpause={() => (paused = true)}
      onclick={() => (compact ? onexpand() : togglePlay())}
      ondblclick={() => !compact && toggleFullscreen()}
    >
      {#each textSubs as t (t.id)}
        <track id={t.id} kind="subtitles" src={t.url} label={subLabel(t)} srclang={t.language || 'und'} />
      {/each}
    </video>

    {#if paused && !compact}
      <button class="bigplay" onclick={togglePlay} aria-label="Play"><Play size={30} /></button>
    {/if}

    {#if showNext && next && !compact}
      <div class="upnext">
        <div class="upnext-label">Up next · starts in {remaining}s</div>
        <div class="upnext-title">
          {next.title}
          <span class="sub2">{epCode(next)}</span>
        </div>
        <div class="upnext-actions">
          <button class="primary" onclick={() => next && onnext(next)}>Play now</button>
          <button class="ghost" onclick={() => { dismissed = true; showNext = false }}>Dismiss</button>
        </div>
      </div>
    {/if}
  </div>

  {#if compact}
    <div class="mini-info">
      <button class="mini-title" onclick={onexpand} title="Go to item">
        {item.title}
        {#if item.kind === 'episode'}<span class="sub">{epCode(item)}</span>{/if}
      </button>
      <span class="time">{fmt(current)} / {fmt(duration)}</span>
    </div>
    <div class="mini-transport">
      <button class="ctl" onclick={() => skip(-10)} title="Back 10s"><RotateCcw size={16} /></button>
      <button class="ctl playpause" onclick={togglePlay}>{#if paused}<Play size={19} />{:else}<Pause size={19} />{/if}</button>
      <button class="ctl" onclick={() => skip(10)} title="Forward 10s"><RotateCw size={16} /></button>
    </div>
    <div class="mini-actions">
      <button class="ctl" onclick={onexpand} title="Expand"><Maximize2 size={16} /></button>
      <button class="ctl" onclick={onclose} title="Stop playback"><X size={17} /></button>
    </div>
  {:else}
    <div class="controls">
      <div class="seekwrap">
        <div class="seek-buffered" style="width: {pct(bufferedEnd)}%"></div>
        <div class="seek-played" style="width: {pct(current)}%"></div>
        <input
          class="seek"
          type="range"
          min="0"
          max={duration || 0}
          step="0.5"
          value={current}
          oninput={onSeekPreview}
          onchange={onSeekCommit}
          aria-label="Seek"
        />
      </div>
      <div class="buttons">
        <button class="ctl" onclick={() => skip(-10)} title="Back 10s (←)"><RotateCcw size={16} /></button>
        <button class="ctl playpause" onclick={togglePlay} title="Play/pause (space)">{#if paused}<Play size={19} />{:else}<Pause size={19} />{/if}</button>
        <button class="ctl" onclick={() => skip(10)} title="Forward 10s (→)"><RotateCw size={16} /></button>
        <span class="time">{fmt(current)} / {fmt(duration)}</span>
        <span class="spacer"></span>
        <button class="ctl" onclick={toggleMute} title="Mute (m)">{#if muted || volume === 0}<VolumeX size={17} />{:else}<Volume2 size={17} />{/if}</button>
        <input class="vol" type="range" min="0" max="1" step="0.05" value={muted ? 0 : volume} oninput={onVolumeInput} aria-label="Volume" />
        <button class="ctl" onclick={toggleFullscreen} title="Fullscreen (f)"><Maximize size={16} /></button>
      </div>
    </div>
  {/if}
</section>

<style>
  .nowplaying {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    overflow: hidden;
    margin-bottom: 1.5rem;
  }
  .nowplaying:fullscreen {
    border: none;
    border-radius: 0;
    display: flex;
    flex-direction: column;
    background: #000;
  }
  .nowplaying:fullscreen .stage { flex: 1; min-height: 0; }
  .nowplaying:fullscreen video { max-height: none; height: 100%; }
  .nowplaying:fullscreen .bar { background: rgba(16, 18, 24, 0.9); border-bottom: none; color: #fff; }
  .nowplaying:fullscreen .controls { background: rgba(16, 18, 24, 0.9); }
  .nowplaying:fullscreen.idle .bar,
  .nowplaying:fullscreen.idle .controls { opacity: 0; pointer-events: none; }
  .nowplaying:fullscreen.idle { cursor: none; }
  .bar, .controls { transition: opacity 0.25s ease; }

  /* ---- Compact: Plex-style bottom now-playing bar ---- */
  .nowplaying.compact {
    position: fixed;
    left: 0;
    right: 0;
    bottom: 0;
    z-index: 45;
    display: flex;
    align-items: center;
    gap: 0.9rem;
    height: 4.6rem;
    margin: 0;
    border-radius: 0;
    border: none;
    border-top: 1px solid var(--border);
    padding-right: 0.75rem;
  }
  @media (max-width: 767px) {
    .nowplaying.compact { bottom: 3.35rem; } /* clear the mobile tab bar */
  }
  .mini-progress {
    position: absolute;
    top: 0;
    left: 0;
    right: 0;
    height: 3px;
    background: var(--border);
  }
  .mini-progress div { height: 100%; background: var(--accent); }
  .nowplaying.compact .stage { width: 8rem; height: 100%; flex: none; }
  .nowplaying.compact video {
    width: 100%;
    height: 100%;
    max-height: none;
    aspect-ratio: auto;
    object-fit: cover;
  }
  .mini-info { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.1rem; }
  .mini-title {
    text-align: left;
    font-weight: 600;
    font-size: 0.92rem;
    color: var(--text);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    padding: 0;
  }
  .mini-title:hover { color: var(--accent-2); }
  .mini-transport { display: flex; align-items: center; gap: 0.2rem; }
  .mini-actions { display: flex; align-items: center; gap: 0.2rem; }

  .bar {
    display: flex;
    align-items: center;
    gap: 0.4rem;
    padding: 0.55rem 0.9rem;
    border-bottom: 1px solid var(--border);
  }
  .bartitle {
    flex: 1;
    min-width: 0;
    font-weight: 600;
    font-size: 0.92rem;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub { color: var(--muted); font-weight: 400; margin-left: 0.4rem; }
  .mode {
    color: var(--muted);
    font-weight: 400;
    font-size: 0.78rem;
    margin-left: 0.6rem;
    border: 1px solid var(--border);
    border-radius: 999px;
    padding: 0.1rem 0.55rem;
  }
  .picker {
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 8px;
    font: inherit;
    font-size: 0.82rem;
    padding: 0.25rem 0.4rem;
  }
  .nowplaying:fullscreen .picker { background: rgba(0, 0, 0, 0.5); color: #fff; }

  .stage { position: relative; background: #000; }
  video {
    display: block;
    width: 100%;
    max-height: 62vh;
    aspect-ratio: 16 / 9;
    cursor: pointer;
  }

  .bigplay {
    position: absolute;
    inset: 0;
    margin: auto;
    width: 4.5rem;
    height: 4.5rem;
    border-radius: 50%;
    background: color-mix(in srgb, var(--accent) 85%, black);
    color: #fff;
    font-size: 1.6rem;
    padding-left: 0.3rem;
    box-shadow: 0 4px 24px rgba(0, 0, 0, 0.5);
  }
  .bigplay:hover { background: var(--accent); }

  .controls { padding: 0.35rem 0.9rem 0.55rem; }
  .seekwrap { position: relative; height: 14px; display: flex; align-items: center; }
  .seek-buffered, .seek-played {
    position: absolute;
    left: 0;
    height: 4px;
    border-radius: 2px;
    pointer-events: none;
  }
  .seekwrap::before {
    content: '';
    position: absolute;
    left: 0;
    right: 0;
    height: 4px;
    border-radius: 2px;
    background: var(--border);
  }
  .seek-buffered { background: color-mix(in srgb, var(--muted) 40%, transparent); }
  .seek-played { background: var(--accent); z-index: 1; }
  .seek {
    position: absolute;
    inset: 0;
    width: 100%;
    appearance: none;
    background: transparent;
    margin: 0;
    z-index: 2;
    cursor: pointer;
  }
  .seek::-webkit-slider-thumb {
    appearance: none;
    width: 12px;
    height: 12px;
    border-radius: 50%;
    background: var(--accent-2);
  }
  .seek::-moz-range-thumb {
    width: 12px;
    height: 12px;
    border: none;
    border-radius: 50%;
    background: var(--accent-2);
  }

  .buttons { display: flex; align-items: center; gap: 0.3rem; margin-top: 0.2rem; }
  .ctl {
    color: var(--muted);
    font-size: 0.95rem;
    padding: 0.3rem 0.5rem;
    border-radius: 6px;
    flex: none;
  }
  .ctl:hover { background: var(--bg); color: var(--text); }
  .nowplaying:fullscreen .ctl { color: rgba(255, 255, 255, 0.85); }
  .playpause { font-size: 1.15rem; }
  .time { color: var(--muted); font-size: 0.82rem; font-variant-numeric: tabular-nums; margin-left: 0.3rem; }
  .spacer { flex: 1; }
  .vol { width: 5.5rem; accent-color: var(--accent); }

  .upnext {
    position: absolute;
    right: 1rem;
    bottom: 1rem;
    background: rgba(16, 18, 24, 0.92);
    border: 1px solid rgba(255, 255, 255, 0.15);
    border-radius: 12px;
    padding: 0.9rem 1.1rem;
    color: #fff;
    max-width: 19rem;
  }
  .upnext-label { font-size: 0.78rem; color: rgba(255, 255, 255, 0.6); }
  .upnext-title { font-weight: 600; margin: 0.35rem 0 0.7rem; }
  .sub2 { color: rgba(255, 255, 255, 0.65); font-weight: 400; margin-left: 0.4rem; }
  .upnext-actions { display: flex; gap: 0.5rem; }
  .upnext .primary {
    background: var(--accent, #7c5cff);
    color: #fff;
    padding: 0.4rem 0.9rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .upnext .ghost {
    color: rgba(255, 255, 255, 0.75);
    padding: 0.4rem 0.9rem;
    border: 1px solid rgba(255, 255, 255, 0.25);
    border-radius: 8px;
  }
</style>
