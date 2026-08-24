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

  function playNextNow() {
    if (next) onnext(next)
  }

  function dismiss() {
    dismissed = true
    showNext = false
  }

  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') onclose()
  }
</script>

<svelte:window onkeydown={onKeydown} />

<div class="player">
  <button class="close" onclick={onclose} aria-label="Close player">✕</button>
  <div class="title">
    {item.title}
    {#if item.kind === 'episode'}
      <span class="sub">S{String(item.seasonNum).padStart(2, '0')}E{String(item.episodeNum).padStart(2, '0')}</span>
    {/if}
  </div>
  <!-- svelte-ignore a11y_media_has_caption -->
  <video bind:this={video} controls playsinline ontimeupdate={onTimeUpdate} onended={onEnded}></video>

  {#if showNext && next}
    <div class="upnext">
      <div class="upnext-label">Up next · starts in {remaining}s</div>
      <div class="upnext-title">
        {next.title}
        <span class="sub">S{String(next.seasonNum).padStart(2, '0')}E{String(next.episodeNum).padStart(2, '0')}</span>
      </div>
      <div class="upnext-actions">
        <button class="primary" onclick={playNextNow}>Play now</button>
        <button class="ghost" onclick={dismiss}>Dismiss</button>
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
  .close {
    position: absolute;
    top: 0.9rem;
    left: 0.9rem;
    z-index: 2;
    color: #fff;
    font-size: 1.2rem;
    background: rgba(0, 0, 0, 0.45);
    border-radius: 50%;
    width: 2.4rem;
    height: 2.4rem;
  }
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
