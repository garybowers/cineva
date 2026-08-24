<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import { nextUp, postProgress, stopSession, type Item, type PlayInfo } from './lib/api'

  let {
    item,
    info,
    onclose,
    onnext,
  }: {
    item: Item
    info: PlayInfo
    onclose: () => void
    onnext: (next: Item) => void
  } = $props()

  // v1 credits window heuristic — replaced by credits *detection* later.
  const CREDITS_WINDOW = 40

  let video: HTMLVideoElement
  let hls: Hls | null = null
  let next: Item | null = $state(null)
  let showNext = $state(false)
  let remaining = $state(0)
  let mini = $state(false)
  let paused = $state(false)
  let dismissed = false
  let lastPosted = -100

  const autoplayNext = () => {
    try {
      return localStorage.getItem('cineva-autoplay') !== 'off'
    } catch {
      return true
    }
  }

  onMount(() => {
    if (info.mode === 'hls' && Hls.isSupported()) {
      hls = new Hls()
      hls.loadSource(info.url)
      hls.attachMedia(video)
    } else {
      video.src = info.url // direct play, or Safari's native HLS
    }
    if (info.positionSecs > 0) video.currentTime = info.positionSecs
    video.play().catch(() => {})
    if (item.kind === 'episode') {
      nextUp(item.id).then((n) => (next = n)).catch(() => {})
    }
  })

  onDestroy(() => {
    reportProgress(true)
    hls?.destroy()
    if (info.sessionId) stopSession(info.sessionId)
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
    reportProgress()
    const duration = video.duration || info.durationSecs
    if (!duration) return
    const left = duration - video.currentTime
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

  function goFullscreen() {
    mini = false
    video.requestFullscreen?.().catch(() => {})
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key !== 'Escape') return
    if (document.fullscreenElement) return // browser handles exiting fullscreen
    if (!mini) mini = true // Esc docks the player; ✕ closes it
  }

  const epCode = (i: Item) => `S${String(i.seasonNum).padStart(2, '0')}E${String(i.episodeNum).padStart(2, '0')}`
</script>

<svelte:window onkeydown={onKeydown} />

<div class="player" class:mini>
  {#if mini}
    <div class="minibar">
      <button class="ctl" onclick={togglePlay} aria-label={paused ? 'Play' : 'Pause'}>{paused ? '▶' : '⏸'}</button>
      <button class="minititle" onclick={() => (mini = false)} title="Expand">
        {item.title}
        {#if item.kind === 'episode'}<span class="sub">{epCode(item)}</span>{/if}
      </button>
      <button class="ctl" onclick={() => (mini = false)} aria-label="Expand" title="Expand">⤢</button>
      <button class="ctl" onclick={goFullscreen} aria-label="Fullscreen" title="Fullscreen">⛶</button>
      <button class="ctl" onclick={onclose} aria-label="Close" title="Close">✕</button>
    </div>
  {:else}
    <button class="close" onclick={onclose} aria-label="Close player">✕</button>
    <button class="dock" onclick={() => (mini = true)} aria-label="Mini player" title="Mini player">⤡</button>
    <div class="title">
      {item.title}
      {#if item.kind === 'episode'}<span class="sub">{epCode(item)}</span>{/if}
    </div>
  {/if}
  <!-- svelte-ignore a11y_media_has_caption -->
  <video
    bind:this={video}
    controls={!mini}
    playsinline
    ontimeupdate={onTimeUpdate}
    onended={onEnded}
    onplay={() => (paused = false)}
    onpause={() => (paused = true)}
    onclick={() => mini && (mini = false)}
  ></video>

  {#if showNext && next && !mini}
    <div class="upnext">
      <div class="upnext-label">Up next · starts in {remaining}s</div>
      <div class="upnext-title">
        {next.title}
        <span class="sub">{epCode(next)}</span>
      </div>
      <div class="upnext-actions">
        <button class="primary" onclick={() => next && onnext(next)}>Play now</button>
        <button class="ghost" onclick={() => { dismissed = true; showNext = false }}>Dismiss</button>
      </div>
    </div>
  {/if}
</div>

<style>
  .player {
    position: fixed;
    inset: 0;
    background: #000;
    z-index: 50;
    display: flex;
    align-items: center;
    justify-content: center;
  }
  video { width: 100%; height: 100%; }

  /* ---- Mini mode: docked card, app stays usable behind it ---- */
  .player.mini {
    inset: auto 1rem 4.75rem auto; /* clears the mobile bottom nav */
    width: min(22rem, calc(100vw - 2rem));
    border-radius: 12px;
    overflow: hidden;
    border: 1px solid var(--border, #232a35);
    box-shadow: 0 8px 30px rgba(0, 0, 0, 0.55);
    display: block;
  }
  .player.mini video {
    aspect-ratio: 16 / 9;
    height: auto;
    display: block;
    cursor: pointer;
  }
  @media (min-width: 768px) {
    .player.mini { inset: auto 1.25rem 1.25rem auto; }
  }

  .minibar {
    display: flex;
    align-items: center;
    gap: 0.15rem;
    padding: 0.35rem 0.5rem;
    background: rgba(16, 18, 24, 0.97);
    color: #fff;
  }
  .minititle {
    flex: 1;
    min-width: 0;
    text-align: left;
    color: #fff;
    font-size: 0.82rem;
    font-weight: 600;
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
    padding: 0.2rem 0.3rem;
  }
  .ctl {
    color: rgba(255, 255, 255, 0.85);
    font-size: 0.9rem;
    padding: 0.25rem 0.4rem;
    border-radius: 6px;
    flex: none;
  }
  .ctl:hover { background: rgba(255, 255, 255, 0.12); }

  /* ---- Full mode chrome ---- */
  .close, .dock {
    position: absolute;
    top: 0.9rem;
    z-index: 2;
    color: #fff;
    font-size: 1.1rem;
    background: rgba(0, 0, 0, 0.45);
    border-radius: 50%;
    width: 2.4rem;
    height: 2.4rem;
  }
  .close { left: 0.9rem; }
  .dock { right: 0.9rem; }
  .title {
    position: absolute;
    top: 1.2rem;
    left: 4rem;
    right: 4rem;
    z-index: 1;
    color: #fff;
    font-weight: 600;
    text-shadow: 0 1px 4px rgba(0, 0, 0, 0.8);
    white-space: nowrap;
    overflow: hidden;
    text-overflow: ellipsis;
  }
  .sub { color: rgba(255, 255, 255, 0.65); font-weight: 400; margin-left: 0.4rem; }

  .upnext {
    position: absolute;
    right: 1.25rem;
    bottom: 5rem;
    z-index: 2;
    background: rgba(16, 18, 24, 0.92);
    border: 1px solid rgba(255, 255, 255, 0.15);
    border-radius: 12px;
    padding: 1rem 1.2rem;
    color: #fff;
    max-width: 20rem;
  }
  .upnext-label { font-size: 0.78rem; color: rgba(255, 255, 255, 0.6); }
  .upnext-title { font-weight: 600; margin: 0.35rem 0 0.75rem; }
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
