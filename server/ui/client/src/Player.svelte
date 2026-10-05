<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import {
    nextUp, prevEpisode, postProgress, stopPlayback, beaconStop, stopSession, QUALITIES,
    type Item, type PlayInfo, type PlayQuality, type AudioTrack, type PlaySource, type Chapter,
  } from './lib/api'
  import { onDismiss } from './lib/dismiss'
  import { pref } from './lib/prefs'
  import {
    Play, Pause, RotateCcw, RotateCw, Volume2, VolumeX, X, Maximize,
    Maximize2, SkipBack, SkipForward, Info, Captions, Languages, Settings, List, Gauge,
  } from '@lucide/svelte'

  let {
    item,
    info,
    quality,
    compact = false,
    onclose,
    onnext,
    onended,
    onquality,
    onaudiotrack,
    onexpand,
    ontime,
  }: {
    item: Item
    info: PlayInfo
    quality: PlayQuality
    compact?: boolean
    onclose: () => void
    onnext: (next: Item) => void
    /** The film finished and there is no next episode to roll into. The
        host decides what follows — the next thing in the play queue, or
        closing the player. */
    onended: () => void
    onquality: (q: PlayQuality) => void
    onaudiotrack: (streamIndex: number) => void
    onexpand: () => void
    ontime?: (secs: number) => void
  } = $props()

  // v1 credits window heuristic — replaced by credits *detection* later.
  const CREDITS_WINDOW = 40

  let box: HTMLElement
  let video: HTMLVideoElement
  let hls: Hls | null = null
  let next: Item | null = $state(null)
  let prev: Item | null = $state(null)
  let showNext = $state(false)
  let remaining = $state(0)
  let subtitle = $state('off')
  let showInfo = $state(false)
  // The track in play. The server resolves the file's default, so this
  // starts from what it actually chose rather than guessing.
  let audioTrack = $state(info.audioTrack ?? -1)

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
      return pref('autoplay', 'on') !== 'off'
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
      hls.on(Hls.Events.FRAG_LOADED, (_e, data) => {
        noteSegment(data.frag.stats?.total ?? 0, data.frag.duration)
      })
      hls.loadSource(info.url)
      hls.attachMedia(video)
    } else {
      video.src = info.url // direct play, or Safari's native HLS
      if (info.positionSecs > 0) video.currentTime = info.positionSecs
      // Nothing is being segmented, so the file's own average is the honest
      // figure rather than a measurement that cannot be taken.
      if (item.sizeBytes && item.runtimeSecs) {
        bitrateKbps = Math.round((item.sizeBytes * 8) / item.runtimeSecs / 1000)
      }
    }
    for (const track of video.textTracks) track.mode = 'hidden' // off until picked
    video.volume = volume
    video.play().catch(() => {})
    if (!compact) box.scrollIntoView({ behavior: 'smooth', block: 'nearest' })
    if (item.kind === 'episode') {
      nextUp(item.id).then((n) => (next = n)).catch(() => {})
      prevEpisode(item.id).then((p) => (prev = p)).catch(() => {})
    }
  })

  // Heartbeat keeps the admin Activity entry alive while paused (no
  // timeupdate events fire then); the server drops silent clients.
  const heartbeat = setInterval(() => {
    if (video) postProgress(item.id, Math.floor(video.currentTime), video.paused).catch(() => {})
  }, 15000)

  const onHide = () => beaconStop(item.id)
  window.addEventListener('pagehide', onHide)

  onDestroy(() => {
    clearInterval(heartbeat)
    window.removeEventListener('pagehide', onHide)
    reportProgress(true) // reads currentTime, so it runs before the teardown
    hls?.destroy()
    // hls.destroy() only covers the HLS path. A direct-play <video> keeps its
    // range request open after the element leaves the DOM — one abandoned
    // 4K stream held a connection for ~55 s and 100 MB, and with the event
    // stream and admin polling also in flight that exhausted the browser's
    // six-per-origin limit, stalling the next title's segment fetches.
    if (video) {
      video.pause()
      video.removeAttribute('src')
      video.load() // aborts the in-flight request
    }
    if (info.sessionId) stopSession(info.sessionId)
    stopPlayback(item.id).catch(() => {})
    clearTimeout(idleTimer)
  })

  function reportProgress(force = false) {
    if (!video) return
    const t = Math.floor(video.currentTime)
    if (force || Math.abs(t - lastPosted) >= 10) {
      lastPosted = t
      postProgress(item.id, t, video.paused).catch(() => {})
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
      onended()
    }
  }

  function togglePlay() {
    if (video.paused) video.play().catch(() => {})
    else video.pause()
  }

  function skip(secs: number) {
    seekTo(video.currentTime + secs)
  }

  /** Absolute seek, clamped to the media. */
  function seekTo(t: number) {
    if (!video) return
    const target = Math.max(0, Math.min(duration || Infinity, t))
    video.currentTime = target
    current = target
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
    if (e.shiftKey && (e.key === 'N' || e.key === 'P')) {
      const ep = e.key === 'N' ? next : prev
      if (ep) { e.preventDefault(); onnext(ep) }
      return
    }
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
      // Chapter skip, matching the bracket keys most desktop players use.
      case '[':
        e.preventDefault()
        skipChapter(-1)
        break
      case ']':
        e.preventDefault()
        skipChapter(1)
        break
      case '<':
      case ',':
        setSpeed(SPEEDS[Math.max(0, SPEEDS.indexOf(speed) - 1)])
        break
      case '>':
      case '.':
        setSpeed(SPEEDS[Math.min(SPEEDS.length - 1, SPEEDS.indexOf(speed) + 1)])
        break
    }
    wake()
  }

  function selectSubtitle(id: string) {
    subtitle = id
    for (const track of video.textTracks) {
      track.mode = track.id === subtitle ? 'showing' : 'hidden'
    }
    openMenu = null
  }

  function changeQuality(q: PlayQuality) {
    openMenu = null
    if (q === quality) return
    reportProgress(true) // so the new session resumes at the same spot
    onquality(q)
  }

  // ---- Chapters ----
  // Chapters come from the container itself, so the list is whatever the
  // file carries — often nothing, in which case the control is not shown.
  const chapters = $derived(info.chapters ?? [])
  /** The chapter the playhead is inside, or -1. */
  const currentChapter = $derived.by(() => {
    for (let i = chapters.length - 1; i >= 0; i--) {
      if (current >= chapters[i].startSecs) return i
    }
    return chapters.length > 0 ? 0 : -1
  })

  function seekToChapter(c: Chapter) {
    seekTo(c.startSecs)
    openMenu = null
  }

  /** Skip by whole chapters; falls back to nothing when the file has none. */
  function skipChapter(delta: number) {
    if (chapters.length === 0) return
    // Going back within the first few seconds of a chapter means "the
    // previous one", which is how every disc player behaves.
    let target = currentChapter + delta
    if (delta < 0 && current - (chapters[currentChapter]?.startSecs ?? 0) > 3) target = currentChapter
    if (target < 0 || target >= chapters.length) return
    seekTo(chapters[target].startSecs)
  }

  const chapterLabel = (c: Chapter, i: number) => c.title?.trim() || `Chapter ${i + 1}`

  // ---- Playback speed ----
  // Audiobook-style speeds are not useful for film, but 1.25x/1.5x are for
  // getting through something slow, and 0.75x helps with dense dialogue.
  const SPEEDS = [0.75, 1, 1.25, 1.5, 2]
  let speed = $state(1)
  function setSpeed(v: number) {
    speed = v
    if (video) video.playbackRate = v
    openMenu = null
  }
  // Reapplied whenever the element is replaced (a quality change rebuilds
  // the <video>), or the rate silently reverts to 1.
  $effect(() => {
    if (video && video.playbackRate !== speed) video.playbackRate = speed
  })

  // ---- Control-bar menus ----
  // One menu open at a time, closing on a pick, on Escape, or on a click
  // anywhere else — the usual expectations for a popup over a video.
  type Menu = 'cc' | 'audio' | 'quality' | 'chapters' | 'speed'
  let openMenu: Menu | null = $state(null)
  const toggleMenu = (m: Menu) => (openMenu = openMenu === m ? null : m)

  $effect(() => {
    if (openMenu === null) return
    return onDismiss('.menuwrap', () => (openMenu = null))
  })

  // The info overlay is a panel over the picture, so it dismisses like the
  // menus rather than only via its own close button.
  $effect(() => {
    if (!showInfo) return
    return onDismiss('.info, .ctl', () => (showInfo = false))
  })


  // "English · 5.1 · EAC3", skipping whatever the file does not say.
  function audioLabel(a: AudioTrack): string {
    const parts: string[] = []
    if (a.title) parts.push(a.title)
    else if (a.language) parts.push(languageName(a.language))
    else parts.push(`Track ${a.streamIndex}`)
    if (a.channels) parts.push(channelLabel(a.channels))
    if (a.codec) parts.push(a.codec.toUpperCase())
    return parts.join(' · ')
  }

  const channelLabel = (n: number) =>
    n === 1 ? 'Mono' : n === 2 ? 'Stereo' : n === 6 ? '5.1' : n === 8 ? '7.1' : `${n}ch`

  // Three-letter ISO codes are what media files carry; show the few that
  // come up often in full and fall back to the code itself.
  const LANGUAGES: Record<string, string> = {
    eng: 'English', fre: 'French', fra: 'French', ger: 'German', deu: 'German',
    spa: 'Spanish', ita: 'Italian', por: 'Portuguese', dut: 'Dutch', nld: 'Dutch',
    jpn: 'Japanese', kor: 'Korean', chi: 'Chinese', zho: 'Chinese', rus: 'Russian',
    pol: 'Polish', swe: 'Swedish', nor: 'Norwegian', dan: 'Danish', fin: 'Finnish',
    hin: 'Hindi', ara: 'Arabic', tur: 'Turkish', tha: 'Thai', vie: 'Vietnamese',
  }
  const languageName = (code: string) => LANGUAGES[code.toLowerCase()] ?? code.toUpperCase()

  const hdrLabel = (src: PlaySource) =>
    src.dvProfile ? `Dolby Vision (profile ${src.dvProfile})` : src.hdrFormat === 'pq' ? 'HDR10' : 'HLG'

  // What is happening to the video, in the viewer's terms.
  function videoLine(): string {
    const src = info.source
    if (!src) return info.mode === 'direct' ? 'Direct play' : 'Transcoding'
    if (src.video === 'direct') return 'Direct play — untouched'
    if (src.video === 'remux') return 'Copied 1:1 — container changed only'
    const parts = [`Re-encoded to ${src.outHeight ? `${src.outHeight}p ` : ''}H.264`]
    // Which path is doing the work is the first thing anyone asks when a
    // transcode is struggling.
    if (src.accel) parts.push(src.accel === 'none' ? 'on the CPU' : `on ${src.accel}`)
    if (src.hdrToneMapped) parts.push('HDR tone mapped to SDR')
    return parts.join(' · ')
  }

  // ---- Live bitrate ----
  // Measured from the segments hls.js actually fetched: bytes over the media
  // time they cover. That is the number that says whether the link will keep
  // up, and it needs no extra request. Direct play has no segments, so the
  // file's own average is used instead — it is a fixed property of the file,
  // not something that can vary mid-stream.
  let bitrateKbps = $state(0)

  /** Feed one fetched segment into the rolling average. */
  function noteSegment(bytes: number, seconds: number) {
    if (bytes <= 0 || seconds <= 0) return
    const kbps = (bytes * 8) / seconds / 1000
    // A short window: a single slow segment should show, but one outlier
    // should not define the reading.
    bitrateKbps = bitrateKbps === 0 ? Math.round(kbps) : Math.round(bitrateKbps * 0.7 + kbps * 0.3)
  }

  function audioLine(): string {
    const src = info.source
    const chosen = info.audio?.find((a) => a.streamIndex === audioTrack)
    const from = chosen ? audioLabel(chosen) : (item.audioCodec ?? '')
    if (!src || src.audio === 'direct') return `${from} — untouched`
    return `${from} → ${(src.audioCodec ?? 'aac').toUpperCase()}`
  }

  function pickAudio(idx: number) {
    openMenu = null
    if (idx === audioTrack) return
    audioTrack = idx
    reportProgress(true) // the new session resumes at the same spot
    onaudiotrack(idx)
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

    {#if showInfo}
      <div class="info" role="status">
        <div class="info-head">
          Playback info
          <button class="info-x" onclick={() => (showInfo = false)} aria-label="Close playback info"><X size={13} /></button>
        </div>
        <dl>
          <dt>Source</dt>
          <dd>
            {info.source?.width ?? item.width}×{info.source?.height ?? item.height}
            {info.source?.videoCodec ?? item.videoCodec ?? ''}
            {#if info.source?.hdrFormat}· {hdrLabel(info.source)}{/if}
            {#if info.source?.container}· {info.source.container}{/if}
          </dd>
          <dt>Video</dt>
          <dd>{videoLine()}</dd>
          <dt>Audio</dt>
          <dd>{audioLine()}</dd>
          {#if info.reason}
            <dt>Why</dt>
            <dd>{info.reason}</dd>
          {/if}
          <dt>Buffer</dt>
          <dd>
            {Math.max(0, Math.round(bufferedEnd - current))}s ahead
            {#if bitrateKbps > 0}· ~{(bitrateKbps / 1000).toFixed(1)} Mbps{/if}
          </dd>
        </dl>
      </div>
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
      {#if item.kind === 'episode'}
        <button class="ctl" onclick={() => prev && onnext(prev)} disabled={!prev} title={prev ? `Previous episode · ${epCode(prev)}` : 'First episode'}><SkipBack size={16} /></button>
      {/if}
      <button class="ctl" onclick={() => skip(-10)} title="Back 10s"><RotateCcw size={16} /></button>
      <button class="ctl playpause" onclick={togglePlay}>{#if paused}<Play size={19} />{:else}<Pause size={19} />{/if}</button>
      <button class="ctl" onclick={() => skip(10)} title="Forward 10s"><RotateCw size={16} /></button>
      {#if item.kind === 'episode'}
        <button class="ctl" onclick={() => next && onnext(next)} disabled={!next} title={next ? `Next episode · ${epCode(next)}` : 'Last episode'}><SkipForward size={16} /></button>
      {/if}
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
        {#if item.kind === 'episode'}
          <button class="ctl" onclick={() => prev && onnext(prev)} disabled={!prev} title={prev ? `Previous episode · ${epCode(prev)} (shift+P)` : 'First episode'}><SkipBack size={16} /></button>
        {/if}
        <button class="ctl" onclick={() => skip(-10)} title="Back 10s (←)"><RotateCcw size={16} /></button>
        <button class="ctl playpause" onclick={togglePlay} title="Play/pause (space)">{#if paused}<Play size={19} />{:else}<Pause size={19} />{/if}</button>
        <button class="ctl" onclick={() => skip(10)} title="Forward 10s (→)"><RotateCw size={16} /></button>
        {#if item.kind === 'episode'}
          <button class="ctl" onclick={() => next && onnext(next)} disabled={!next} title={next ? `Next episode · ${epCode(next)} (shift+N)` : 'Last episode'}><SkipForward size={16} /></button>
        {/if}
        <span class="time">{fmt(current)} / {fmt(duration)}</span>
        <span class="spacer"></span>
        <button class="ctl" onclick={toggleMute} title="Mute (m)">{#if muted || volume === 0}<VolumeX size={17} />{:else}<Volume2 size={17} />{/if}</button>
        <input class="vol" type="range" min="0" max="1" step="0.05" value={muted ? 0 : volume} oninput={onVolumeInput} aria-label="Volume" />
        {#if info.audio && info.audio.length > 1}
          <div class="menuwrap">
            <button
              class="ctl" class:on={openMenu === 'audio'}
              onclick={() => toggleMenu('audio')}
              title="Audio track" aria-label="Audio track"
              aria-haspopup="menu" aria-expanded={openMenu === 'audio'}><Languages size={16} /></button>
            {#if openMenu === 'audio'}
              <div class="menu" role="menu">
                <div class="menu-head">Audio</div>
                {#each info.audio as a (a.streamIndex)}
                  <button
                    class="menu-item" class:sel={a.streamIndex === audioTrack}
                    role="menuitemradio" aria-checked={a.streamIndex === audioTrack}
                    onclick={() => pickAudio(a.streamIndex)}>{audioLabel(a)}</button>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        {#if textSubs.length > 0}
          <div class="menuwrap">
            <button
              class="ctl" class:on={openMenu === 'cc' || subtitle !== 'off'}
              onclick={() => toggleMenu('cc')}
              title="Subtitles" aria-label="Subtitles"
              aria-haspopup="menu" aria-expanded={openMenu === 'cc'}><Captions size={16} /></button>
            {#if openMenu === 'cc'}
              <div class="menu" role="menu">
                <div class="menu-head">Subtitles</div>
                <button
                  class="menu-item" class:sel={subtitle === 'off'}
                  role="menuitemradio" aria-checked={subtitle === 'off'}
                  onclick={() => selectSubtitle('off')}>Off</button>
                {#each textSubs as t (t.id)}
                  <button
                    class="menu-item" class:sel={subtitle === t.id}
                    role="menuitemradio" aria-checked={subtitle === t.id}
                    onclick={() => selectSubtitle(t.id)}>{subLabel(t)}</button>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        {#if chapters.length > 0}
          <div class="menuwrap">
            <button
              class="ctl" class:on={openMenu === 'chapters'}
              onclick={() => toggleMenu('chapters')}
              title="Chapters" aria-label="Chapters"
              aria-haspopup="menu" aria-expanded={openMenu === 'chapters'}><List size={16} /></button>
            {#if openMenu === 'chapters'}
              <div class="menu" role="menu">
                <div class="menu-head">Chapters</div>
                {#each chapters as c, i (c.startSecs)}
                  <button
                    class="menu-item ch-row" class:sel={i === currentChapter}
                    role="menuitemradio" aria-checked={i === currentChapter}
                    onclick={() => seekToChapter(c)}>
                    <span class="ch-name">{chapterLabel(c, i)}</span>
                    <span class="ch-at">{fmt(c.startSecs)}</span>
                  </button>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        <div class="menuwrap">
          <button
            class="ctl" class:on={openMenu === 'speed'}
            onclick={() => toggleMenu('speed')}
            title="Playback speed" aria-label="Playback speed"
            aria-haspopup="menu" aria-expanded={openMenu === 'speed'}><Gauge size={16} /></button>
          {#if openMenu === 'speed'}
            <div class="menu" role="menu">
              <div class="menu-head">Speed</div>
              {#each SPEEDS as v (v)}
                <button
                  class="menu-item" class:sel={v === speed}
                  role="menuitemradio" aria-checked={v === speed}
                  onclick={() => setSpeed(v)}>{v === 1 ? 'Normal' : `${v}×`}</button>
              {/each}
            </div>
          {/if}
        </div>
        <div class="menuwrap">
          <button
            class="ctl" class:on={openMenu === 'quality'}
            onclick={() => toggleMenu('quality')}
            title="Playback quality" aria-label="Playback quality"
            aria-haspopup="menu" aria-expanded={openMenu === 'quality'}><Settings size={16} /></button>
          {#if openMenu === 'quality'}
            <div class="menu" role="menu">
              <div class="menu-head">Quality</div>
              {#each QUALITIES as q (q.id)}
                <button
                  class="menu-item" class:sel={q.id === quality}
                  role="menuitemradio" aria-checked={q.id === quality}
                  onclick={() => changeQuality(q.id)}>{q.label}</button>
              {/each}
            </div>
          {/if}
        </div>
        <button
          class="ctl"
          class:on={showInfo}
          onclick={() => (showInfo = !showInfo)}
          title="Playback info"
          aria-label="Playback info"
          aria-pressed={showInfo}><Info size={16} /></button>
        <button class="ctl" onclick={toggleFullscreen} title="Fullscreen (f)"><Maximize size={16} /></button>
      </div>
    </div>
  {/if}
</section>

<style>
  /* Sits over the picture, dark enough to read against a bright frame
     without hiding it. */
  .info {
    position: absolute;
    top: 0.75rem;
    left: 0.75rem;
    max-width: min(22rem, calc(100% - 1.5rem));
    padding: 0.6rem 0.75rem;
    border-radius: 10px;
    border: 1px solid rgb(255 255 255 / 0.14);
    background: rgb(10 12 16 / 0.82);
    backdrop-filter: blur(6px);
    color: #fff;
    font-size: 0.78rem;
    line-height: 1.45;
    z-index: 4;
  }
  .info-head {
    display: flex;
    align-items: center;
    justify-content: space-between;
    gap: 0.75rem;
    font-weight: 600;
    margin-bottom: 0.4rem;
  }
  .info-x {
    border: 0;
    background: none;
    color: inherit;
    opacity: 0.7;
    cursor: pointer;
    padding: 0;
    display: flex;
  }
  .info-x:hover { opacity: 1; }
  .info dl {
    display: grid;
    grid-template-columns: auto 1fr;
    gap: 0.15rem 0.7rem;
    margin: 0;
  }
  .info dt { opacity: 0.6; white-space: nowrap; }
  .info dd { margin: 0; overflow-wrap: anywhere; }

  /* .menu-item is display:block for the plain text rows; a chapter row needs
     two columns, so it opts into flex rather than changing the shared rule. */
  .ch-row { display: flex; align-items: center; }
  /* A chapter row is a name and its timestamp, the name taking the slack. */
  .ch-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ch-at { opacity: 0.6; font-variant-numeric: tabular-nums; margin-left: 0.75rem; }

  .menuwrap { position: relative; display: flex; }
  .menu {
    position: absolute;
    bottom: calc(100% + 0.5rem);
    right: 0;
    min-width: 11rem;
    max-height: 15rem;
    overflow-y: auto;
    padding: 0.3rem;
    border-radius: 10px;
    border: 1px solid rgb(255 255 255 / 0.14);
    background: rgb(10 12 16 / 0.92);
    backdrop-filter: blur(6px);
    z-index: 6;
  }
  .menu-head {
    padding: 0.25rem 0.55rem 0.35rem;
    font-size: 0.68rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    color: rgb(255 255 255 / 0.5);
  }
  .menu-item {
    display: block;
    width: 100%;
    text-align: left;
    padding: 0.35rem 0.55rem;
    border: 0;
    border-radius: 6px;
    background: none;
    color: #fff;
    font: inherit;
    font-size: 0.8rem;
    white-space: nowrap;
    cursor: pointer;
  }
  .menu-item:hover { background: rgb(255 255 255 / 0.1); }
  /* The selected row is marked with a tick rather than colour alone. */
  .menu-item.sel { background: rgb(255 255 255 / 0.08); font-weight: 600; }
  .menu-item.sel::before { content: '✓ '; }
  .menu-item:not(.sel)::before { content: ''; padding-left: 0.85rem; }

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
  .ctl:disabled { opacity: 0.35; cursor: default; }
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
