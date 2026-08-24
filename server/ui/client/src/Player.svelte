<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import {
    nextUp, postProgress, stopSession, QUALITIES,
    type Item, type PlayInfo, type PlayQuality,
  } from './lib/api'

  let {
    item,
    info,
    quality,
    onclose,
    onnext,
    onquality,
  }: {
    item: Item
    info: PlayInfo
    quality: PlayQuality
    onclose: () => void
    onnext: (next: Item) => void
    onquality: (q: PlayQuality) => void
  } = $props()

  // v1 credits window heuristic — replaced by credits *detection* later.
  const CREDITS_WINDOW = 40

  let box: HTMLElement
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
    video.play().catch(() => {})
    box.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
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

  function toggleFullscreen() {
    // Fullscreen the box (not the video) so the up-next card stays visible.
    if (document.fullscreenElement) document.exitFullscreen?.()
    else box.requestFullscreen?.().catch(() => {})
  }

  function changeQuality(e: Event) {
    const q = (e.target as HTMLSelectElement).value as PlayQuality
    reportProgress(true) // so the new session resumes at the same spot
    onquality(q)
  }

  const modeLabel = () =>
    info.mode === 'direct' ? 'Direct Play' : `Transcoding${info.reason ? ` — ${info.reason}` : ''}`

  const epCode = (i: Item) => `S${String(i.seasonNum).padStart(2, '0')}E${String(i.episodeNum).padStart(2, '0')}`
</script>

<section class="nowplaying" bind:this={box}>
  <div class="bar">
    <span class="bartitle">
      {item.title}
      {#if item.kind === 'episode'}<span class="sub">{epCode(item)}</span>{/if}
      <span class="mode" title={info.reason ?? ''}>{modeLabel()}</span>
    </span>
    <select class="quality" value={quality} onchange={changeQuality} title="Playback quality">
      {#each QUALITIES as q (q.id)}
        <option value={q.id}>{q.label}</option>
      {/each}
    </select>
    <button class="ctl" onclick={toggleFullscreen} title="Fullscreen">⛶</button>
    <button class="ctl" onclick={onclose} title="Stop playback">✕</button>
  </div>
  <div class="stage">
    <!-- svelte-ignore a11y_media_has_caption -->
    <video bind:this={video} controls playsinline ontimeupdate={onTimeUpdate} onended={onEnded}></video>

    {#if showNext && next}
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

  .bar {
    display: flex;
    align-items: center;
    gap: 0.25rem;
    padding: 0.55rem 0.9rem;
    border-bottom: 1px solid var(--border);
  }
  .nowplaying:fullscreen .bar {
    background: rgba(16, 18, 24, 0.9);
    border-bottom: none;
    color: #fff;
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
  .quality {
    background: var(--bg);
    color: var(--text);
    border: 1px solid var(--border);
    border-radius: 8px;
    font: inherit;
    font-size: 0.82rem;
    padding: 0.25rem 0.4rem;
    margin-right: 0.35rem;
  }
  .nowplaying:fullscreen .quality { background: rgba(0, 0, 0, 0.5); color: #fff; }
  .ctl {
    color: var(--muted);
    font-size: 0.95rem;
    padding: 0.3rem 0.5rem;
    border-radius: 6px;
    flex: none;
  }
  .ctl:hover { background: var(--bg); color: var(--text); }
  .nowplaying:fullscreen .ctl { color: rgba(255, 255, 255, 0.85); }

  .stage {
    position: relative;
    background: #000;
  }
  video {
    display: block;
    width: 100%;
    max-height: 62vh;
    aspect-ratio: 16 / 9;
  }

  .upnext {
    position: absolute;
    right: 1rem;
    bottom: 4.25rem;
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
