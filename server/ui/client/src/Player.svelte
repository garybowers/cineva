<script lang="ts">
  import { onDestroy, onMount } from 'svelte'
  import Hls from 'hls.js'
  import {
    nextUp, prevEpisode, postProgress, stopPlayback, beaconStop, stopSession, QUALITIES,
    PLAYBACK_STOPPED, itemStorage, streamProblem,
    type Item, type PlayInfo, type PlayQuality, type AudioTrack, type PlaySource, type Chapter,
    type SubtitleTrack, type Device,
  } from './lib/api'
  import { onDismiss } from './lib/dismiss'
  import { pref } from './lib/prefs'
  import {
    Play, Pause, RotateCcw, RotateCw, Volume2, VolumeX, X, Maximize, Expand, Shrink,
    Maximize2, SkipBack, SkipForward, Info, Captions, Languages, Settings, List, Gauge, Cast,
    MonitorSmartphone,
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
    onburnsubtitle,
    onexpand,
    ontime,
    devices = [],
    ondevices,
    onplayon,
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
    /** Restart playback with an image subtitle painted in. */
    onburnsubtitle: (streamIndex: number) => void
    onexpand: () => void
    ontime?: (secs: number) => void
    /** The user's other devices that have the app open: where "Play on…"
        can send what is playing. */
    devices?: Device[]
    /** The "Play on…" menu opened: the host refreshes devices. */
    ondevices?: () => void
    /** Send what is playing — the host's queue, or this title — to another
        device, which carries on from the place saved here. The host closes
        this player once it is sent, and throws if it could not be. */
    onplayon?: (d: Device) => Promise<void>
  } = $props()

  // Where the up-next card appears: at the episode's detected credits, or,
  // for an episode with none detected, this many seconds from the end.
  const CREDITS_WINDOW = 40
  // With detected credits the next episode starts this long after they do,
  // rather than after the whole credit roll; without, it starts at the end
  // of the file, so a guessed window can never cut a scene short.
  const CREDITS_COUNTDOWN = 15

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
  // Set once autoplay has moved on, so the next timeupdate cannot do it twice.
  let advanced = false
  let lastPosted = -100
  let idleTimer: ReturnType<typeof setTimeout> | undefined

  const textSubs = info.subtitles?.filter((t) => t.url) ?? []
  // Bitmap tracks cannot be overlaid; they are offered separately because
  // choosing one restarts playback as a re-encode.
  const burnSubs = info.subtitles?.filter((t) => t.burnIn) ?? []
  const subLabel = (t: SubtitleTrack) =>
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
      // hls.js retries a 503 for the best part of a minute before giving
      // up, and keeps only the status. The server answers 503 when the
      // session's storage has stopped answering, so the first one is
      // reason enough to ask why rather than sit through the retries.
      hls.on(Hls.Events.ERROR, (_e, data) => {
        if (data.fatal || data.response?.code === 503) explainFailure(data.fatal)
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

  // Set when the server answers a heartbeat with 409: an administrator ended
  // this playback. The video is paused and the viewer told, rather than the
  // picture simply freezing with no explanation.
  let stoppedByAdmin = $state(false)

  // ---- Storage failures ----
  // Media usually lives on a network share, and a share that stops
  // answering does not fail — reads just stop arriving. The server ends a
  // transcode whose source has stalled and says why; a direct play just
  // stops receiving data. Either way the viewer is told which library's
  // storage is not responding and what to do, instead of watching a frozen
  // picture.

  // A playing video whose time has not moved, with too little buffered to
  // move, for this long is asked about. Long enough for a NAS disk to spin
  // up, short enough that nobody is left wondering for a minute.
  const STALL_CHECK_SECS = 15

  let failure: { title: string; body: string } | null = $state(null)
  let explaining = false
  // Set as the player is torn down, when releasing the stream can itself
  // raise an error that is not worth explaining.
  let destroyed = false

  function fail(title: string, body: string) {
    if (failure || stoppedByAdmin) return
    reportProgress(true) // keep the place before the stream is released
    failure = { title, body }
    if (video) {
      video.pause()
      video.removeAttribute('src')
      video.load() // aborts any request still waiting on the server
    }
    hls?.destroy()
    hls = null
  }

  /** Find out why the stream stopped and, if the server knows, say so.
      fatal: playback cannot carry on by itself, so something is shown
      whether or not the server has a reason. */
  async function explainFailure(fatal: boolean) {
    if (explaining || failure || stoppedByAdmin || destroyed) return
    explaining = true
    try {
      let reason: string | null = null
      if (info.mode === 'hls') reason = await streamProblem(info.url).catch(() => null)
      if (!reason) {
        const st = await itemStorage(item.id).catch(() => null)
        if (st && !st.available) reason = st.message ?? null
      }
      if (destroyed) return
      if (reason) fail('Storage not responding', reason)
      else if (fatal) fail('Playback stopped', 'The stream stopped and could not be recovered. Try again in a moment.')
    } finally {
      explaining = false
    }
  }

  let stalledSecs = 0
  let stallLastTime = -1
  const stallWatch = setInterval(() => {
    if (!video || failure || stoppedByAdmin) return
    const stuck = !video.paused && !video.ended && !seeking &&
      video.readyState < HTMLMediaElement.HAVE_FUTURE_DATA && video.currentTime === stallLastTime
    stallLastTime = video.currentTime
    stalledSecs = stuck ? stalledSecs + 1 : 0
    // Asked again every so often while it stays stuck: the share may only
    // be found down on a later check.
    if (stalledSecs > 0 && stalledSecs % STALL_CHECK_SECS === 0) explainFailure(false)
  }, 1000)

  function retry() {
    failure = null
    onquality(quality) // the host starts a fresh playback at the saved place
  }

  function onProgressError(err: unknown) {
    if (!(err instanceof Error) || err.message !== PLAYBACK_STOPPED || stoppedByAdmin) return
    stoppedByAdmin = true
    if (video) {
      video.pause()
      // Release the stream as well as stopping the picture: a direct play
      // holds its range request open, which is the thing the administrator
      // was trying to reclaim.
      video.removeAttribute('src')
      video.load()
    }
    hls?.destroy()
  }

  // Heartbeat keeps the admin Activity entry alive while paused (no
  // timeupdate events fire then); the server drops silent clients.
  const heartbeat = setInterval(() => {
    if (video && !stoppedByAdmin && !failure) postProgress(item.id, Math.floor(video.currentTime), video.paused).catch(onProgressError)
  }, 15000)

  const onHide = () => beaconStop(item.id)
  window.addEventListener('pagehide', onHide)

  onDestroy(() => {
    destroyed = true
    clearInterval(heartbeat)
    clearInterval(stallWatch)
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
    // Once the stream has been released, currentTime reads 0: the place
    // was saved as it was released.
    if (!video || stoppedByAdmin || failure) return
    const t = Math.floor(video.currentTime)
    if (force || Math.abs(t - lastPosted) >= 10) {
      lastPosted = t
      postProgress(item.id, t, video.paused).catch(onProgressError)
    }
  }

  function onTimeUpdate() {
    if (failure) return // the released stream reads 0, which is not the place
    if (!seeking) current = video.currentTime
    ontime?.(video.currentTime)
    if (video.duration && isFinite(video.duration)) duration = video.duration
    if (video.buffered.length > 0) bufferedEnd = video.buffered.end(video.buffered.length - 1)
    reportProgress()
    if (!duration) return
    const left = duration - current
    const creditsAt = credits ? credits.startSecs : duration - CREDITS_WINDOW
    if (next && !dismissed && current >= creditsAt) {
      showNext = true
      const startsAt = credits ? Math.min(duration, credits.startSecs + CREDITS_COUNTDOWN) : duration
      remaining = Math.max(0, Math.ceil(startsAt - current))
      // A file without detected credits rolls on from onEnded instead.
      if (credits && remaining === 0 && left > 1 && !advanced && autoplayNext()) {
        advanced = true
        reportProgress(true)
        onnext(next)
      }
    } else {
      showNext = false
    }
  }

  function onEnded() {
    reportProgress(true)
    if (advanced) return
    if (next && !dismissed && autoplayNext()) {
      onnext(next)
    } else {
      onended()
    }
  }

  function togglePlay() {
    if (video.paused) {
      video.play().catch(() => {})
      showFlash()
    } else {
      video.pause()
    }
  }

  // A brief play icon in the middle of the picture as playback resumes —
  // pausing needs none, since the paused button appears in the same place.
  // Keyed, so pressing again restarts the animation.
  let flash = $state(0)
  let flashTimer: ReturnType<typeof setTimeout> | undefined
  function showFlash() {
    if (compact) return
    clearTimeout(flashTimer)
    flash++
    flashTimer = setTimeout(() => (flash = 0), 700)
  }

  // ---- Skip intro ----
  // Offered while playback is inside the episode's detected intro, and gone
  // a couple of seconds before its end, when skipping would save nothing.
  const intro = $derived(info.segments?.find((s) => s.kind === 'intro'))
  const inIntro = $derived(!!intro && current >= intro.startSecs && current < intro.endSecs - 2)
  function skipIntro() {
    if (intro) seekTo(intro.endSecs)
  }
  const credits = $derived(info.segments?.find((s) => s.kind === 'credits'))

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

  // Fullscreen is the page's, not this player's: the next episode is a new
  // player, and a fullscreen element that leaves the page takes fullscreen
  // with it. With the page fullscreen the player fills it (the same layout
  // as filling the window), and a player that starts while the page is
  // already fullscreen — the next episode — starts filling it too.
  let fullscreen = $state(typeof document !== 'undefined' && document.fullscreenElement === document.documentElement)
  $effect(() => {
    const sync = () => (fullscreen = document.fullscreenElement === document.documentElement)
    document.addEventListener('fullscreenchange', sync)
    return () => document.removeEventListener('fullscreenchange', sync)
  })
  function toggleFullscreen() {
    if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {})
    else document.documentElement.requestFullscreen?.().catch(() => {})
  }

  // ---- Fill the window ----
  // Between the inline player and fullscreen: the video takes the whole
  // browser window, with the tabs, the address bar and the rest of the
  // desktop still there. The page behind it does not scroll while it is up.
  let windowed = $state(false)
  function toggleWindowed() {
    windowed = !windowed
    wake()
  }
  $effect(() => {
    if (!windowed && !fullscreen) return
    const before = document.body.style.overflow
    document.body.style.overflow = 'hidden'
    document.body.classList.add('player-windowed')
    return () => {
      document.body.style.overflow = before
      document.body.classList.remove('player-windowed')
    }
  })

  // ---- Controls over the picture ----
  // They fade three seconds after the pointer last moved, while playing.
  // Paused, with a menu open, or with the pointer over the controls
  // themselves, they stay.
  let overControls = false
  function wake() {
    idle = false
    clearTimeout(idleTimer)
    idleTimer = setTimeout(() => {
      if (!compact && video && !video.paused && openMenu === null && !overControls) idle = true
    }, 3000)
  }
  const holdControls = (on: boolean) => () => {
    overControls = on
    wake()
  }
  // A tap on a touch screen, where there is no hover, brings hidden
  // controls back rather than pausing behind them.
  function onVideoClick() {
    if (compact) return onexpand()
    if (idle) return wake()
    togglePlay()
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
      case 't':
        toggleWindowed()
        break
      case 'Escape':
        // Fullscreen handles its own Escape; this one leaves the window mode.
        if (windowed && !document.fullscreenElement) windowed = false
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

  /**
   * Picking a bitmap subtitle cannot just switch a text track on: it has to
   * be painted into the picture, which means restarting playback as a
   * re-encode. Handled by the host, like a quality change.
   */
  function selectBurnIn(track: SubtitleTrack) {
    openMenu = null
    reportProgress(true) // the new session resumes at the same spot
    onburnsubtitle(track.streamIndex ?? 0)
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

  // ---- Seek previews (trickplay) ----
  //
  // One sprite sheet plus a WebVTT index of regions within it. Parsed once
  // per item; a 404 simply means the server has not generated them yet (the
  // request itself starts that off), in which case the scrub bar behaves as
  // it always did.
  type TrickCue = { from: number; to: number; x: number; y: number; w: number; h: number }
  let trickCues: TrickCue[] = $state([])
  let hoverSecs = $state(-1)
  let hoverX = $state(0)
  let seekEl: HTMLElement | undefined = $state()

  const trickSheet = $derived(`/api/v1/items/${item.id}/trickplay.jpg`)

  $effect(() => {
    const id = item.id
    trickCues = []
    fetch(`/api/v1/items/${id}/trickplay.vtt`)
      // 202 means the sheet is being generated now; there are no cues to
      // read yet and the next load will have them.
      .then((r) => (r.ok && r.status !== 202 ? r.text() : null))
      .then((text) => {
        if (text && item.id === id) trickCues = parseTrickVTT(text)
      })
      .catch(() => {})
  })

  function parseTrickVTT(text: string): TrickCue[] {
    const out: TrickCue[] = []
    const lines = text.split('\n')
    for (let i = 0; i < lines.length; i++) {
      const range = lines[i].match(/^(\d\d):(\d\d):(\d\d)\.\d+ --> (\d\d):(\d\d):(\d\d)\.\d+/)
      if (!range) continue
      const xywh = lines[i + 1]?.match(/#xywh=(\d+),(\d+),(\d+),(\d+)/)
      if (!xywh) continue
      const secs = (h: string, m: string, sec: string) => +h * 3600 + +m * 60 + +sec
      out.push({
        from: secs(range[1], range[2], range[3]),
        to: secs(range[4], range[5], range[6]),
        x: +xywh[1], y: +xywh[2], w: +xywh[3], h: +xywh[4],
      })
    }
    return out
  }

  /** The cue covering a time, or the nearest one before it. */
  const cueAt = (t: number) => {
    let found: TrickCue | null = null
    for (const c of trickCues) {
      if (c.from <= t) found = c
      else break
    }
    return found
  }
  const hoverCue = $derived(hoverSecs >= 0 ? cueAt(hoverSecs) : null)

  function onSeekHover(e: PointerEvent) {
    if (!seekEl || !duration) return
    const r = seekEl.getBoundingClientRect()
    const frac = Math.min(1, Math.max(0, (e.clientX - r.left) / r.width))
    hoverSecs = frac * duration
    hoverX = frac * r.width
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

  // Many rips name their chapters with a timestamp ("00:08:30.260"), which
  // next to the actual start time reads as the same number printed twice.
  // A title that is only a timestamp carries no information, so it is
  // treated as no title at all.
  const timestampOnly = /^\d{1,2}:\d{2}(:\d{2})?([.,]\d+)?$/
  const chapterLabel = (c: Chapter, i: number) => {
    const t = c.title?.trim() ?? ''
    if (t === '' || timestampOnly.test(t)) return `Chapter ${i + 1}`
    return t
  }

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
  type Menu = 'cc' | 'audio' | 'quality' | 'chapters' | 'speed' | 'cast'
  let openMenu: Menu | null = $state(null)
  const toggleMenu = (m: Menu) => {
    openMenu = openMenu === m ? null : m
    if (openMenu === 'cast') ondevices?.()
  }

  // ---- Play on another device ----
  // The place is saved before the queue goes, so the other device resumes
  // where this one is rather than at the last heartbeat; this one pauses
  // while it is sent and stops once it has gone. Fullscreen is the page's,
  // so it is left too: there is nothing here to watch any more.
  let sending = $state('')
  const deviceTitle = (d: Device) => d.name || d.client || 'Unknown browser'
  async function playOn(d: Device) {
    if (!onplayon || sending) return
    openMenu = null
    sending = d.id
    const wasPaused = video?.paused ?? true
    video?.pause()
    try {
      if (video && !stoppedByAdmin && !failure) {
        lastPosted = Math.floor(video.currentTime)
        await postProgress(item.id, lastPosted, true).catch(() => {})
      }
      await onplayon(d)
      if (document.fullscreenElement) document.exitFullscreen?.().catch(() => {})
    } catch {
      // The host has said why; carry on here as if nothing happened.
      if (!wasPaused && !destroyed) video?.play().catch(() => {})
    } finally {
      sending = ''
    }
  }

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

  // ---- Frame delivery ----
  // Whether the browser is keeping up with the picture. When it is not, the
  // sound carries on and the picture falls behind until a pause lets it
  // catch up, which looks like a sync fault in the stream but is not one.
  // Sampled once a second while the overlay is open: shown frames per
  // second against the source rate says it at a glance.
  let shownFps = $state(0)
  let droppedFrames = $state(0)
  let totalFrames = $state(0)
  $effect(() => {
    if (!showInfo || !video?.getVideoPlaybackQuality) return
    let last = video.getVideoPlaybackQuality()
    let lastAt = performance.now()
    const id = setInterval(() => {
      const q = video.getVideoPlaybackQuality()
      const now = performance.now()
      const shown = q.totalVideoFrames - q.droppedVideoFrames - (last.totalVideoFrames - last.droppedVideoFrames)
      shownFps = video.paused ? 0 : Math.round((shown * 1000) / (now - lastAt))
      droppedFrames = q.droppedVideoFrames
      totalFrames = q.totalVideoFrames
      last = q
      lastAt = now
    }, 1000)
    return () => clearInterval(id)
  })

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

<section class="nowplaying" class:idle class:compact class:windowed={windowed || fullscreen} bind:this={box} onmousemove={wake} onpointerdown={wake}>
  {#if compact}
    <div class="mini-progress"><div style="width: {pct(current)}%"></div></div>
  {/if}

  {#if !compact}
    <div class="bar" role="toolbar" tabindex="-1" onmouseenter={holdControls(true)} onmouseleave={holdControls(false)}>
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
      onpause={() => { paused = true; idle = false }}
      onerror={() => explainFailure(true)}
      onclick={onVideoClick}
      ondblclick={() => !compact && toggleFullscreen()}
    >
      {#each textSubs as t (t.id)}
        <track id={t.id} kind="subtitles" src={t.url} label={subLabel(t)} srclang={t.language || 'und'} />
      {/each}
    </video>

    {#if paused && !compact}
      <button class="bigplay" onclick={togglePlay} aria-label="Play"><Play size={34} fill="currentColor" strokeWidth={1.5} /></button>
    {/if}
    {#if flash && !paused}
      {#key flash}
        <div class="flash" aria-hidden="true"><Play size={30} fill="currentColor" strokeWidth={1.5} /></div>
      {/key}
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
          {#if totalFrames > 0}
            <dt>Frames</dt>
            <dd>
              {shownFps} fps shown
              · {droppedFrames} dropped ({((droppedFrames / totalFrames) * 100).toFixed(1)}%)
            </dd>
          {/if}
          <dt>Buffer</dt>
          <dd>
            {Math.max(0, Math.round(bufferedEnd - current))}s ahead
            {#if bitrateKbps > 0}· ~{(bitrateKbps / 1000).toFixed(1)} Mbps{/if}
          </dd>
        </dl>
      </div>
    {/if}

    {#if stoppedByAdmin}
      <div class="stopped" role="alert">
        <div class="stopped-title">Playback stopped</div>
        <p class="stopped-body">An administrator ended this stream. Your place has been saved.</p>
        <button class="primary" onclick={onclose}>Close</button>
      </div>
    {:else if failure && !compact}
      <div class="stopped" role="alert">
        <div class="stopped-title">{failure.title}</div>
        <p class="stopped-body">{failure.body} Your place has been saved.</p>
        <div class="stopped-actions">
          <button class="primary" onclick={retry}><RotateCw size={15} /> Try again</button>
          <button class="ghost" onclick={onclose}>Close</button>
        </div>
      </div>
    {/if}
    {#if inIntro && !compact}
      <button class="skipintro" onclick={skipIntro}>Skip intro</button>
    {/if}
    {#if showNext && next && !compact}
      <div class="upnext">
        <div class="upnext-label">Up next{#if autoplayNext()} · starts in {remaining}s{/if}</div>
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
      {#if failure}
        <!-- Too small for the explanation; expanding shows it in full. -->
        <button class="time failed" onclick={onexpand} title={failure.body}>{failure.title}</button>
      {:else}
        <span class="time">{fmt(current)} / {fmt(duration)}</span>
      {/if}
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
    <div class="controls" role="toolbar" tabindex="-1" onmouseenter={holdControls(true)} onmouseleave={holdControls(false)}>
      <div
        class="seekwrap"
        bind:this={seekEl}
        onpointermove={onSeekHover}
        onpointerleave={() => (hoverSecs = -1)}
      >
        {#if hoverCue}
          <!-- Clamped to the bar so the preview never hangs off the edge at
               the very start or end of a film. -->
          <div
            class="preview"
            style="left: {Math.min(Math.max(hoverX, hoverCue.w / 2), (seekEl?.clientWidth ?? 0) - hoverCue.w / 2)}px; width: {hoverCue.w}px; height: {hoverCue.h}px;"
          >
            <div
              class="preview-img"
              style="width: {hoverCue.w}px; height: {hoverCue.h}px; background-image: url({trickSheet}); background-position: -{hoverCue.x}px -{hoverCue.y}px;"
            ></div>
            <span class="preview-time">{fmt(hoverSecs)}</span>
          </div>
        {/if}
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
        {#if textSubs.length > 0 || burnSubs.length > 0}
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
                {#if burnSubs.length > 0}
                  <div class="menu-head">Burned in</div>
                  {#each burnSubs as t (t.id)}
                    <button
                      class="menu-item"
                      role="menuitem"
                      title="Painted into the picture — restarts playback"
                      onclick={() => selectBurnIn(t)}>{subLabel(t)}</button>
                  {/each}
                {/if}
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
              <div class="menu chapters" role="menu">
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
        {#if onplayon}
          <div class="menuwrap">
            <button
              class="ctl" class:on={openMenu === 'cast' || !!sending}
              onclick={() => toggleMenu('cast')}
              disabled={!!sending}
              title="Play on another device" aria-label="Play on another device"
              aria-haspopup="menu" aria-expanded={openMenu === 'cast'}><Cast size={16} /></button>
            {#if openMenu === 'cast'}
              <div class="menu cast" role="menu">
                <div class="menu-head">Play on</div>
                {#each devices as d (d.id)}
                  <button class="menu-item dev-row" role="menuitem" onclick={() => playOn(d)}>
                    <MonitorSmartphone size={15} />
                    <span class="dev-name">{deviceTitle(d)}</span>
                  </button>
                {:else}
                  <p class="menu-note">No other device has Cineva open. Open it on the TV or phone you want to watch on, signed in as you.</p>
                {/each}
              </div>
            {/if}
          </div>
        {/if}
        <button
          class="ctl"
          class:on={showInfo}
          onclick={() => (showInfo = !showInfo)}
          title="Playback info"
          aria-label="Playback info"
          aria-pressed={showInfo}><Info size={16} /></button>
        <button class="ctl" onclick={toggleWindowed} title={windowed ? 'Back to the page (t)' : 'Fill the window (t)'} aria-pressed={windowed}>
          {#if windowed}<Shrink size={16} />{:else}<Expand size={16} />{/if}
        </button>
        <button class="ctl" onclick={toggleFullscreen} title={fullscreen ? 'Leave fullscreen (f)' : 'Fullscreen (f)'} aria-pressed={fullscreen}><Maximize size={16} /></button>
      </div>
    </div>
  {/if}
</section>

<style>
  /* Sits over the picture, dark enough to read against a bright frame
     without hiding it. */
  .info {
    position: absolute;
    top: 3.25rem; /* below the title bar */
    left: 0.75rem;
    max-width: min(22rem, calc(100% - 1.5rem));
    padding: 0.6rem 0.75rem;
    border-radius: 10px;
    border: 1px solid rgb(255 255 255 / 0.14);
    background: rgb(10 12 16 / 0.82);
    -webkit-backdrop-filter: blur(6px);
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
  .ch-row { display: flex; align-items: center; gap: 0.5rem; }
  /* Chapter rows carry a name and a time, so the menu is given room for
     both rather than the narrow width the single-column menus need. */
  .menu.chapters { min-width: 15rem; }
  /* Device rows carry an icon and a name, which ellipsises. The note for
     when there are none wraps, so the menu keeps a sensible width. */
  .menu.cast { width: max-content; min-width: 13rem; max-width: min(18rem, calc(100vw - 2rem)); }
  .dev-row { display: flex; align-items: center; gap: 0.5rem; }
  .dev-row::before { display: none; }
  .dev-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .menu-note {
    margin: 0;
    padding: 0.2rem 0.55rem 0.4rem;
    font-size: 0.78rem;
    line-height: 1.4;
    color: rgb(255 255 255 / 0.7);
    white-space: normal;
  }
  /* A chapter row is a name and its timestamp, the name taking the slack. */
  .ch-name { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ch-at { opacity: 0.6; font-variant-numeric: tabular-nums; margin-left: 0.75rem; }

  /* The hover preview sits above the scrub bar, pointer-transparent so it
     never swallows the drag it is describing. */
  .preview {
    position: absolute;
    bottom: calc(100% + 0.5rem);
    transform: translateX(-50%);
    pointer-events: none;
    border-radius: 6px;
    overflow: hidden;
    border: 1px solid rgb(255 255 255 / 0.2);
    box-shadow: 0 6px 18px rgb(0 0 0 / 0.5);
    background: #000;
    z-index: 5;
  }
  .preview-img { background-repeat: no-repeat; }
  .preview-time {
    position: absolute;
    left: 0;
    right: 0;
    bottom: 0;
    text-align: center;
    font-size: 0.7rem;
    font-variant-numeric: tabular-nums;
    color: #fff;
    background: linear-gradient(to top, rgb(0 0 0 / 0.8), transparent);
    padding: 0.5rem 0 0.15rem;
  }

  .menuwrap { position: relative; display: flex; }
  .menu {
    position: absolute;
    bottom: calc(100% + 0.5rem);
    right: 0;
    min-width: 11rem;
    max-height: 15rem;
    overflow-y: auto;
    /* Rows ellipsise rather than widening the menu; without this a long
       chapter name gave the popup a horizontal scrollbar. */
    overflow-x: hidden;
    padding: 0.3rem;
    border-radius: 10px;
    border: 1px solid rgb(255 255 255 / 0.14);
    background: rgb(10 12 16 / 0.92);
    -webkit-backdrop-filter: blur(6px);
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
    border-radius: var(--surface-radius, 16px); /* as the sidebar's */
    overflow: hidden;
    margin-bottom: 1.5rem;
  }
  /* ---- Controls over the picture (every mode but compact) ----
     The title bar and the controls float over the video on dark gradients,
     and fade when idle. Their colours are fixed rather than themed: they sit
     on the picture, which is never the theme's background. */
  .nowplaying:not(.compact) { position: relative; background: #000; }
  .nowplaying:not(.compact) .bar,
  .nowplaying:not(.compact) .controls {
    position: absolute;
    left: 0;
    right: 0;
    z-index: 3;
    color: #fff;
    transition: opacity 0.25s ease;
  }
  .nowplaying:not(.compact) .bar {
    top: 0;
    border-bottom: none;
    padding-bottom: 1.6rem;
    background: linear-gradient(to bottom, rgb(0 0 0 / 0.72), transparent);
  }
  /* The controls float in a rounded glass box clear of the picture's edges.
     Fixed dark tints rather than theme tokens, as everywhere over the video:
     the picture behind is any colour, whatever the theme. */
  .nowplaying:not(.compact) .controls {
    left: 1rem;
    right: 1rem;
    bottom: 1rem;
    padding: 0.55rem 1rem 0.6rem;
    border-radius: 16px;
    background: rgb(12 14 20 / 0.5);
    -webkit-backdrop-filter: blur(18px) saturate(1.4);
    backdrop-filter: blur(18px) saturate(1.4);
    border: 1px solid rgb(255 255 255 / 0.12);
    box-shadow: 0 10px 34px rgb(0 0 0 / 0.35);
  }
  @media (max-width: 40rem) {
    .nowplaying:not(.compact) .controls { left: 0.5rem; right: 0.5rem; bottom: 0.5rem; padding: 0.4rem 0.6rem 0.5rem; border-radius: 12px; }
  }
  .nowplaying:not(.compact) .ctl { color: rgb(255 255 255 / 0.88); }
  .nowplaying:not(.compact) .ctl:hover { background: rgb(255 255 255 / 0.14); color: #fff; }
  .nowplaying:not(.compact) .time,
  .nowplaying:not(.compact) .sub { color: rgb(255 255 255 / 0.75); }
  .nowplaying:not(.compact) .mode { color: rgb(255 255 255 / 0.75); border-color: rgb(255 255 255 / 0.3); }
  .nowplaying:not(.compact) .seekwrap::before { background: rgb(255 255 255 / 0.25); }
  .nowplaying:not(.compact) .seek-buffered { background: rgb(255 255 255 / 0.4); }
  .nowplaying.idle .bar,
  .nowplaying.idle .controls { opacity: 0; pointer-events: none; }
  .nowplaying.idle { cursor: none; }

  /* Filling the window, and fullscreen: the picture takes all the room. */
  .nowplaying.windowed {
    position: fixed;
    inset: 0;
    z-index: 200;
    margin: 0;
    border: none;
    border-radius: 0;
    display: flex;
    flex-direction: column;
  }
  /* <main> isolates its own stacking context (for the page backdrop), which
     would trap the player under the app's header however high its z-index;
     lifting <main> itself while the player fills the window frees it. */
  :global(body.player-windowed main) { z-index: 300; }
  .nowplaying:fullscreen {
    border: none;
    border-radius: 0;
    display: flex;
    flex-direction: column;
  }
  .nowplaying.windowed .stage,
  .nowplaying:fullscreen .stage { flex: 1; min-height: 0; }
  .nowplaying.windowed video,
  .nowplaying:fullscreen video { max-height: none; height: 100%; aspect-ratio: auto; object-fit: contain; }

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

  /* The paused button and the play/pause flash share the controls' glass:
     a dark frosted disc with a hairline edge, the icon solid white. Fixed
     tints rather than theme tokens, as everywhere over the picture. */
  .bigplay, .flash {
    position: absolute;
    inset: 0;
    margin: auto;
    display: grid;
    place-items: center;
    border-radius: 50%;
    color: #fff;
    background: rgb(12 14 20 / 0.42);
    -webkit-backdrop-filter: blur(16px) saturate(1.4);
    backdrop-filter: blur(16px) saturate(1.4);
    border: 1px solid rgb(255 255 255 / 0.18);
    box-shadow: 0 10px 34px rgb(0 0 0 / 0.35);
  }
  .bigplay {
    width: 5.25rem;
    height: 5.25rem;
    z-index: 2;
    padding-left: 0.3rem; /* the triangle's weight sits left of its box */
    animation: bigplay-in 0.2s ease-out;
    transition: transform 0.15s ease, background 0.15s ease, border-color 0.15s ease;
  }
  .bigplay:hover {
    transform: scale(1.06);
    background: rgb(12 14 20 / 0.55);
    border-color: rgb(255 255 255 / 0.32);
  }
  .bigplay:active { transform: scale(0.97); }
  .bigplay:focus-visible { outline: 2px solid var(--accent); outline-offset: 3px; }
  .flash {
    width: 4.25rem;
    height: 4.25rem;
    z-index: 2;
    pointer-events: none;
    animation: flash 0.7s ease-out forwards;
  }
  .flash :global(svg) { margin-left: 0.15rem; }
  @keyframes bigplay-in {
    from { opacity: 0; transform: scale(0.88); }
    to { opacity: 1; transform: scale(1); }
  }
  @keyframes flash {
    0% { opacity: 0; transform: scale(0.8); }
    20% { opacity: 1; transform: scale(1); }
    100% { opacity: 0; transform: scale(1.25); }
  }
  @media (max-width: 40rem) {
    .bigplay { width: 4.25rem; height: 4.25rem; }
  }
  @media (prefers-reduced-motion: reduce) {
    .bigplay, .flash { animation: none; transition: none; }
    .flash { display: none; }
  }

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
  /* At phone width the row would run off the edge, taking the last buttons
     — fullscreen, "Play on…" — with it. The volume slider goes (a phone has
     buttons for that, and mute stays), and the rest wrap onto a second
     row rather than being cut off. */
  @media (max-width: 40rem) {
    .buttons { flex-wrap: wrap; gap: 0.1rem; }
    .vol { display: none; }
    .ctl { padding: 0.3rem 0.4rem; }
    /* A menu hung from a button near the left edge would open off the
       screen; at this width every menu spans the control box instead. */
    .menuwrap { position: static; }
    .menu, .menu.cast, .menu.chapters { left: 0; right: 0; width: auto; min-width: 0; max-width: none; }
  }
  .ctl {
    color: var(--muted);
    font-size: 0.95rem;
    padding: 0.3rem 0.5rem;
    border-radius: 6px;
    flex: none;
  }
  .ctl:disabled { opacity: 0.35; cursor: default; }
  .ctl:hover { background: var(--bg); color: var(--text); }
  .playpause { font-size: 1.15rem; }
  .time { color: var(--muted); font-size: 0.82rem; font-variant-numeric: tabular-nums; margin-left: 0.3rem; }
  .spacer { flex: 1; }
  .vol { width: 5.5rem; accent-color: var(--accent); }

  .upnext {
    position: absolute;
    right: 1rem;
    bottom: 6.5rem; /* clear of the floating controls */
    background: rgba(16, 18, 24, 0.92);
    border: 1px solid rgba(255, 255, 255, 0.15);
    border-radius: 12px;
    padding: 0.9rem 1.1rem;
    color: #fff;
    max-width: 19rem;
  }
  /* Where the up-next card sits: the two never show together, since one
     belongs to the start of an episode and the other to its end. */
  .skipintro {
    position: absolute;
    right: 1rem;
    bottom: 6.5rem; /* clear of the floating controls */
    padding: 0.6rem 1.2rem;
    border-radius: 10px;
    border: 1px solid var(--border);
    background: color-mix(in srgb, var(--panel) 88%, transparent);
    color: var(--text);
    font-weight: 600;
  }
  .skipintro:hover { border-color: var(--accent); }
  /* Centred rather than tucked in a corner like the up-next card: this one
     is not an offer, it explains why the picture stopped. */
  .stopped {
    position: absolute;
    inset: 0;
    display: grid;
    place-content: center;
    justify-items: center;
    gap: 0.2rem;
    text-align: center;
    padding: 1.5rem;
    background: rgba(16, 18, 24, 0.86);
    color: #fff;
  }
  .stopped-title { font-size: 1.1rem; font-weight: 600; }
  .stopped-body { color: rgba(255, 255, 255, 0.75); margin: 0.3rem 0 0.9rem; max-width: 28rem; }
  .upnext-label { font-size: 0.78rem; color: rgba(255, 255, 255, 0.6); }
  .upnext-title { font-weight: 600; margin: 0.35rem 0 0.7rem; }
  .sub2 { color: rgba(255, 255, 255, 0.65); font-weight: 400; margin-left: 0.4rem; }
  .upnext-actions { display: flex; gap: 0.5rem; }
  .stopped-actions { display: flex; flex-wrap: wrap; justify-content: center; gap: 0.5rem; }
  .stopped .primary { display: inline-flex; align-items: center; gap: 0.35rem; }
  .mini-info .failed { color: var(--err); text-align: left; padding: 0; }
  .upnext .primary,
  .stopped-actions .primary {
    background: var(--accent, #7c5cff);
    color: #fff;
    padding: 0.4rem 0.9rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .upnext .ghost,
  .stopped-actions .ghost {
    color: rgba(255, 255, 255, 0.75);
    padding: 0.4rem 0.9rem;
    border: 1px solid rgba(255, 255, 255, 0.25);
    border-radius: 8px;
  }
</style>
