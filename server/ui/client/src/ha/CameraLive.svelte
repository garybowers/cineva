<!--
  A camera's live view: Home Assistant's own HLS stream, proxied by
  Cineva, played with hls.js (or natively, on Safari). A camera that cannot
  stream, or whose stream stops, shows its snapshot fetched every two
  seconds instead. Nothing is recorded.
-->
<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import { Maximize, Minimize } from '@lucide/svelte'
  import { haSnapshotURL, haStreamURL, type HAEntity } from '../lib/api'
  import Sheet from '../Sheet.svelte'

  let { entity, onclose }: { entity: HAEntity; onclose: () => void } = $props()

  let frame: HTMLDivElement
  let video: HTMLVideoElement | undefined = $state()
  let hls: Hls | null = null
  // svelte-ignore state_referenced_locally
  let pictures = $state(!entity.stream) // snapshots rather than video
  let note = $state('')
  let src = $state('')
  let fullscreen = $state(false)
  let retries = 0
  let retryTimer: ReturnType<typeof setTimeout> | undefined
  let destroyed = false

  function startStream() {
    if (!video) return
    const url = haStreamURL(entity.entityId)
    if (Hls.isSupported()) {
      hls?.destroy()
      hls = new Hls({ liveDurationInfinity: true })
      hls.on(Hls.Events.ERROR, (_e, data) => {
        if (!data.fatal || destroyed) return
        if (data.type === Hls.ErrorTypes.MEDIA_ERROR) {
          hls?.recoverMediaError()
          return
        }
        // Home Assistant drops idle streams and Cineva asks for a new one;
        // a moment later the same address answers again.
        if (retries < 3) {
          retries += 1
          clearTimeout(retryTimer)
          retryTimer = setTimeout(startStream, 2000)
          return
        }
        fallBack()
      })
      hls.on(Hls.Events.FRAG_LOADED, () => (retries = 0))
      hls.loadSource(url)
      hls.attachMedia(video)
    } else if (video.canPlayType('application/vnd.apple.mpegurl')) {
      video.src = url
      video.onerror = fallBack
    } else {
      fallBack()
      return
    }
    video.play().catch(() => {})
  }

  function fallBack() {
    hls?.destroy()
    hls = null
    note = 'The live view is not available, so this shows a new picture every two seconds.'
    pictures = true
  }

  // Snapshots: every two seconds while the tab is in view, swapped in once
  // loaded so the picture never blanks.
  let tabShown = $state(!document.hidden)
  function refresh() {
    const next = `${haSnapshotURL(entity.entityId)}?t=${Date.now()}`
    const img = new Image()
    img.onload = () => (src = next)
    img.src = next
  }
  $effect(() => {
    if (!pictures || !tabShown) return
    refresh()
    const t = setInterval(refresh, 2000)
    return () => clearInterval(t)
  })

  function toggleFullscreen() {
    if (document.fullscreenElement === frame) document.exitFullscreen?.().catch(() => {})
    else frame.requestFullscreen?.().catch(() => {})
  }

  onMount(() => {
    if (!pictures) startStream()
    const onVis = () => (tabShown = !document.hidden)
    const onFs = () => (fullscreen = document.fullscreenElement === frame)
    document.addEventListener('visibilitychange', onVis)
    document.addEventListener('fullscreenchange', onFs)
    return () => {
      document.removeEventListener('visibilitychange', onVis)
      document.removeEventListener('fullscreenchange', onFs)
    }
  })

  onDestroy(() => {
    destroyed = true
    clearTimeout(retryTimer)
    hls?.destroy()
    hls = null
    if (video) {
      video.removeAttribute('src')
      video.load()
    }
    if (frame && document.fullscreenElement === frame) document.exitFullscreen?.().catch(() => {})
  })
</script>

<Sheet title={entity.name} wide flush {onclose}>
  {#snippet actions()}
    <button class="fs" onclick={toggleFullscreen} title={fullscreen ? 'Leave fullscreen' : 'Fullscreen'}
      aria-label={fullscreen ? 'Leave fullscreen' : 'Fullscreen'} aria-pressed={fullscreen}>
      {#if fullscreen}<Minimize size={17} />{:else}<Maximize size={17} />{/if}
    </button>
  {/snippet}
  <div class="frame" bind:this={frame} class:fullscreen>
    {#if pictures}
      {#if src}<img {src} alt="{entity.name}, latest picture" />{/if}
    {:else}
      <video bind:this={video} muted autoplay playsinline aria-label="{entity.name}, live"></video>
    {/if}
    {#if fullscreen}
      <button class="fs over" onclick={toggleFullscreen} aria-label="Leave fullscreen" title="Leave fullscreen"><Minimize size={18} /></button>
    {/if}
  </div>
  {#if note}<p class="note">{note}</p>{/if}
</Sheet>

<style>
  .frame {
    position: relative;
    width: 100%;
    aspect-ratio: 16 / 9;
    max-height: calc(100dvh - 8rem);
    background: var(--bg);
    display: grid;
    place-items: center;
  }
  .frame.fullscreen { aspect-ratio: auto; max-height: none; height: 100%; }
  video, img { width: 100%; height: 100%; object-fit: contain; display: block; }
  .fs {
    display: grid;
    place-items: center;
    width: 2.25rem;
    height: 2.25rem;
    border-radius: 8px;
    color: var(--muted);
    flex: none;
  }
  .fs:hover { color: var(--text); background: color-mix(in srgb, var(--text) 8%, transparent); }
  .fs.over {
    position: absolute;
    top: 0.75rem;
    right: 0.75rem;
    color: var(--text);
    background: color-mix(in srgb, var(--panel) 70%, transparent);
  }
  .note { margin: 0; padding: 0.6rem 1rem 0.9rem; color: var(--muted); font-size: 0.85rem; }
</style>
