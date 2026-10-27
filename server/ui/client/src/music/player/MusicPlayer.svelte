<script lang="ts">
  // The music engine, with no screen of its own: it plays whatever the
  // queue store (queue.svelte.ts) says is current and reports back into
  // it; the mini bar and the now-playing screen are views over the store.
  //
  // Tracks are decoded with Web Audio and the next one is scheduled
  // sample-accurately at the current one's end, so albums play gapless (a
  // plain <audio> leaves an audible gap between files). A silent <audio>
  // element holds the browser's media session so lock screens and hardware
  // keys work while Web Audio does the actual playing. Stage 5 of the music
  // overhaul splits this into an element engine and a buffer engine.
  import { onDestroy, onMount, untrack } from 'svelte'
  import { pref } from '../../lib/prefs'
  import { playItem, postProgress, stopPlayback, beaconStop, TAKEN_OVER, PLAYBACK_STOPPED, type Item } from '../../lib/api'
  import { mq, advanced, notePosition } from './queue.svelte'

  let {
    onstopped,
  }: {
    /** An administrator stopped playback, or another device took it over. */
    onstopped: (why: 'admin' | 'takeover') => void
  } = $props()

  let paused = $state(true)
  let seeking = false
  let muted = false
  let volume = $state(readVolume())

  const track = $derived(mq.items[mq.index] as Item | undefined)

  // ---- Streaming quality (Settings page) ----
  // 'original' streams the file; otherwise a server-side Opus transcode.
  // Whatever the choice, a decode failure falls back to AAC, which every
  // browser can decode (Safari cannot do Ogg/Opus in Web Audio).
  type MusicQuality = 'original' | 'opus-128' | 'opus-64'
  function readMusicQuality(): MusicQuality {
    try {
      return (pref('music-quality', 'original') as MusicQuality)
    } catch {
      return 'original'
    }
  }
  function streamURL(id: string, fallback = false): string {
    const base = `/api/v1/items/${id}/stream`
    if (fallback) return `${base}?format=aac&quality=192`
    const q = readMusicQuality()
    if (q === 'original') return base
    const [format, kbps] = q.split('-')
    return `${base}?format=${format}&quality=${kbps}`
  }
  /** "FLAC 24/96", "MP3 320k", "Opus 128k": what is actually playing. */
  function qualityLabel(item: Item, fallback: boolean): string {
    const q = readMusicQuality()
    if (fallback) return 'AAC 192k'
    if (q !== 'original') return `Opus ${q.split('-')[1]}k`
    const codec = (item.audioCodec || item.container || '').toUpperCase()
    if (item.bitDepth && item.sampleRate) return `${codec} ${item.bitDepth}/${Math.round(item.sampleRate / 1000)}`
    if (item.bitrate) return `${codec} ${item.bitrate}k`
    return codec
  }

  function readVolume(): number {
    try {
      return Number(localStorage.getItem('cineva-volume') ?? 1)
    } catch {
      return 1
    }
  }

  // ---- Web Audio engine ----
  let ctx: AudioContext | null = null
  let gain: GainNode | null = null
  interface Playing {
    src: AudioBufferSourceNode
    buffer: AudioBuffer
    startedAt: number // ctx time the buffer's `offset` second began
    offset: number
    id: string
    stopped: boolean // true when we stopped it ourselves (seek/skip): ignore onended
  }
  let cur: Playing | null = null
  // The pre-scheduled next track (decoded, source started at cur's end).
  let sched: { id: string; src: AudioBufferSourceNode; buffer: AudioBuffer; at: number; cancelled: boolean } | null = null
  let prepareGen = 0 // invalidates in-flight prepare() calls
  const buffers = new Map<string, Promise<AudioBuffer>>() // small decoded cache
  const fellBack = new Set<string>() // tracks that needed the AAC fallback

  function engine(): AudioContext {
    if (!ctx) {
      ctx = new AudioContext()
      gain = ctx.createGain()
      gain.gain.value = muted ? 0 : volume
      gain.connect(ctx.destination)
    }
    return ctx
  }

  async function decode(item: Item): Promise<AudioBuffer> {
    const key = item.id
    let p = buffers.get(key)
    if (!p) {
      p = (async () => {
        const ac = engine()
        const fetchAndDecode = async (url: string) => ac.decodeAudioData(await (await fetch(url)).arrayBuffer())
        try {
          return await fetchAndDecode(streamURL(item.id))
        } catch {
          // Undecodable here (wma/alac/ogg-on-Safari…): the universal fallback.
          fellBack.add(item.id)
          return await fetchAndDecode(streamURL(item.id, true))
        }
      })()
      buffers.set(key, p)
      p.catch(() => buffers.delete(key))
      // Only the current and the next stay decoded: a FLAC album is
      // hundreds of MB as PCM.
      for (const k of [...buffers.keys()].slice(0, Math.max(0, buffers.size - 2))) buffers.delete(k)
    }
    return p
  }

  function position(): number {
    if (!cur || !ctx) return mq.positionSecs
    return Math.min(cur.buffer.duration, Math.max(0, ctx.currentTime - cur.startedAt + cur.offset))
  }

  function cancelScheduled() {
    if (sched) {
      sched.cancelled = true
      try { sched.src.stop() } catch { /* not started yet is fine */ }
      sched = null
    }
    prepareGen++
  }

  /** The index of the entry after i, by the store's order and repeat, or
      null at the end. */
  function after(i: number): number | null {
    if (mq.repeat === 'one') return i
    if (i + 1 < mq.items.length) return i + 1
    return mq.repeat === 'all' && mq.items.length > 0 ? 0 : null
  }

  // Start the entry at i from `offset` seconds right now. A seek is not a
  // new play: history and Activity hear of a track once per queue entry.
  async function play(i: number, offset = 0, reason: 'start' | 'seek' = 'start') {
    const ac = engine()
    i = Math.max(0, Math.min(mq.items.length - 1, i))
    const item = mq.items[i]
    if (!item) return
    cancelScheduled()
    if (cur) {
      cur.stopped = true
      try { cur.src.stop() } catch { /* already ended */ }
      if (cur.id !== item.id) stopPlayback(cur.id).catch(() => {})
      cur = null
    }
    if (mq.index !== i) advanced(i)
    mq.positionSecs = offset
    mq.duration = item.runtimeSecs ?? 0
    mq.loading = true
    if (reason === 'start') playItem(item.id).catch(() => {}) // history + admin Activity
    const gen = ++prepareGen
    let buffer: AudioBuffer
    try {
      buffer = await decode(item)
    } catch {
      mq.loading = false
      next() // skip what we cannot play
      return
    }
    if (gen !== prepareGen) return // superseded by another play()
    mq.loading = false
    mq.quality = qualityLabel(item, fellBack.has(item.id))
    if (ac.state === 'suspended' && !paused) await ac.resume()
    const src = ac.createBufferSource()
    src.buffer = buffer
    src.connect(gain!)
    const startAt = ac.currentTime + 0.02
    src.start(startAt, Math.min(offset, buffer.duration))
    const me: Playing = { src, buffer, startedAt: startAt, offset, id: item.id, stopped: false }
    cur = me
    mq.duration = buffer.duration
    src.onended = () => onSourceEnded(me)
    prepare(me)
    updateMediaSession()
  }

  // Decode the following track and schedule it to start exactly when the
  // current one ends, when the gapless preference allows. Lives on `me` so
  // a superseded play() can't schedule against the wrong track.
  async function prepare(me: Playing) {
    if (pref('gapless', 'on') === 'off') return
    const n = after(mq.index)
    if (n === null) return
    const item = mq.items[n]
    const gen = prepareGen
    let buffer: AudioBuffer
    try {
      buffer = await decode(item)
    } catch {
      return // onended will try to move on and skip it then
    }
    if (gen !== prepareGen || cur !== me || !ctx || !gain) return
    const endAt = me.startedAt + (me.buffer.duration - me.offset)
    if (endAt <= ctx.currentTime + 0.05) return // too late; onended handles it
    const src = ctx.createBufferSource()
    src.buffer = buffer
    src.connect(gain)
    src.start(endAt)
    sched = { id: item.id, src, buffer, at: endAt, cancelled: false }
  }

  function onSourceEnded(me: Playing) {
    if (me.stopped || cur !== me) return
    markPlayed(me.id)
    const n = after(mq.index)
    if (sched && !sched.cancelled && ctx && n !== null && mq.items[n]?.id === sched.id) {
      // Seamless hand-over: the scheduled source is already sounding.
      const s = sched
      sched = null
      const handed: Playing = { src: s.src, buffer: s.buffer, startedAt: s.at, offset: 0, id: s.id, stopped: false }
      cur = handed
      advanced(n)
      mq.duration = s.buffer.duration
      s.src.onended = () => onSourceEnded(handed)
      if (me.id !== s.id) stopPlayback(me.id).catch(() => {})
      playItem(s.id).catch(() => {})
      mq.quality = qualityLabel(mq.items[n], fellBack.has(s.id))
      updateMediaSession()
      prepare(handed)
      return
    }
    cancelScheduled()
    cur = null
    next()
  }

  function next() {
    const n = after(mq.index)
    if (n === null) {
      // End of the queue: stay on the last track, stopped.
      if (cur) { cur.stopped = true; try { cur.src.stop() } catch { /* ended */ } }
      cur = null
      paused = true
      mq.positionSecs = 0
      ctx?.suspend()
      updateMediaSession()
    } else {
      paused = false
      play(n)
    }
  }

  function prev() {
    if (position() > 3 || mq.index === 0) seekTo(0)
    else { paused = false; play(mq.index - 1) }
  }

  function seekTo(secs: number) {
    if (!cur) {
      mq.positionSecs = Math.max(0, secs)
      return
    }
    play(mq.index, Math.max(0, Math.min(secs, cur.buffer.duration - 0.05)), 'seek')
  }

  function togglePlay() {
    if (!ctx || !cur) { paused = false; play(mq.index, mq.positionSecs, cur ? 'seek' : 'start'); return }
    if (paused) { paused = false; ctx.resume() } else { paused = true; ctx.suspend() }
    updateMediaSession()
  }

  // The order changed under us (shuffle, repeat, a move): the pre-scheduled
  // next track may be wrong now.
  function reschedule() {
    cancelScheduled()
    if (cur) prepare(cur)
  }

  function setVolume(v: number) {
    volume = Math.max(0, Math.min(1, v))
    mq.volume = volume
    if (volume > 0) muted = false
    if (gain) gain.gain.value = muted ? 0 : volume
    try {
      localStorage.setItem('cineva-volume', String(volume))
    } catch { /* private browsing */ }
  }

  function markPlayed(id: string) {
    const it = mq.items.find((t) => t.id === id)
    postProgress(id, it?.runtimeSecs ?? 0).catch(() => {})
  }

  // ---- The store drives the engine ----
  // gen: start the entry at index from offset (or sit on it paused, after a
  // reload); orderGen: the queue was rearranged.
  $effect(() => {
    void mq.gen
    untrack(() => {
      if (!mq.active) return
      if (mq.autoplay) {
        paused = false
        play(mq.index, mq.offset)
      } else {
        if (cur) { cur.stopped = true; try { cur.src.stop() } catch { /* ended */ } }
        cancelScheduled()
        cur = null
        paused = true
        mq.positionSecs = mq.offset
        mq.duration = mq.items[mq.index]?.runtimeSecs ?? 0
        mq.quality = ''
        updateMediaSession()
      }
    })
  })
  $effect(() => {
    void mq.orderGen
    untrack(reschedule)
  })
  $effect(() => {
    mq.paused = paused
  })

  // ---- Clock: position readout, admin heartbeat, media session ----
  const tick = setInterval(() => {
    if (!cur || paused) return
    if (!seeking) mq.positionSecs = position()
    if ('mediaSession' in navigator && mq.duration > 0) {
      try { navigator.mediaSession.setPositionState({ duration: mq.duration, position: Math.min(mq.positionSecs, mq.duration), playbackRate: 1 }) } catch { /* unsupported */ }
    }
  }, 250)
  const heartbeat = setInterval(() => {
    if (!track || !ctx) return
    const at = Math.floor(position())
    notePosition(at)
    postProgress(track.id, at, paused).catch((err: unknown) => {
      // An administrator ended this playback: stop, as the video player does.
      if (err instanceof Error && err.message === PLAYBACK_STOPPED) {
        if (!paused) togglePlay()
        onstopped('admin')
      }
    })
  }, 15000)

  // ---- Media Session (lock screen / hardware keys) ----
  // Browsers only surface media controls for a playing HTMLMediaElement, so
  // a looping silent clip runs alongside Web Audio purely to hold the session.
  let holder: HTMLAudioElement
  function silentWav(): string {
    const rate = 8000, secs = 10, n = rate * secs
    const buf = new ArrayBuffer(44 + n)
    const v = new DataView(buf)
    const str = (o: number, s: string) => { for (let i = 0; i < s.length; i++) v.setUint8(o + i, s.charCodeAt(i)) }
    str(0, 'RIFF'); v.setUint32(4, 36 + n, true); str(8, 'WAVE'); str(12, 'fmt ')
    v.setUint32(16, 16, true); v.setUint16(20, 1, true); v.setUint16(22, 1, true)
    v.setUint32(24, rate, true); v.setUint32(28, rate, true); v.setUint16(32, 1, true); v.setUint16(34, 8, true)
    str(36, 'data'); v.setUint32(40, n, true)
    new Uint8Array(buf, 44).fill(128)
    return URL.createObjectURL(new Blob([buf], { type: 'audio/wav' }))
  }
  function updateMediaSession() {
    if (!('mediaSession' in navigator)) return
    const t = mq.items[mq.index]
    if (!t) return
    try {
      navigator.mediaSession.metadata = new MediaMetadata({
        title: t.title, artist: t.artist ?? '', album: t.album ?? '',
        artwork: t.poster ? [{ src: t.poster, sizes: '512x512', type: 'image/jpeg' }] : [],
      })
      navigator.mediaSession.playbackState = paused ? 'paused' : 'playing'
    } catch { /* metadata unsupported */ }
    if (holder) {
      if (paused) holder.pause()
      else holder.play().catch(() => {})
    }
  }
  function installMediaHandlers() {
    if (!('mediaSession' in navigator)) return
    const ms = navigator.mediaSession
    const set = (a: MediaSessionAction, h: MediaSessionActionHandler) => { try { ms.setActionHandler(a, h) } catch { /* unsupported action */ } }
    set('play', () => { if (paused) togglePlay() })
    set('pause', () => { if (!paused) togglePlay() })
    set('previoustrack', prev)
    set('nexttrack', next)
    set('seekto', (d) => { if (d.seekTime !== undefined) seekTo(d.seekTime) })
    set('seekbackward', (d) => seekTo(position() - (d.seekOffset ?? 10)))
    set('seekforward', (d) => seekTo(position() + (d.seekOffset ?? 10)))
    set('stop', () => onstopped('admin'))
  }

  onMount(() => {
    holder.src = silentWav()
    holder.loop = true
    holder.volume = 0.01 // inaudible; some browsers drop the session at 0
    installMediaHandlers()
    mq.volume = volume
    mq.controls = { toggle: togglePlay, next, prev, seek: seekTo, setVolume }
  })

  const onHide = () => { if (track && cur) beaconStop(track.id) }

  // Another device is continuing this track: stop here, and say where.
  function onTakenOver(e: Event) {
    const id = (e as CustomEvent<{ itemId: string }>).detail.itemId
    if (!track || track.id !== id) return
    const at = Math.floor(position())
    if (ctx && !paused) {
      paused = true
      ctx.suspend()
      updateMediaSession()
    }
    if (ctx) postProgress(track.id, at, true).catch(() => {})
    onstopped('takeover')
  }
  window.addEventListener(TAKEN_OVER, onTakenOver)
  window.addEventListener('pagehide', onHide)

  onDestroy(() => {
    clearInterval(tick)
    clearInterval(heartbeat)
    window.removeEventListener(TAKEN_OVER, onTakenOver)
    window.removeEventListener('pagehide', onHide)
    cancelScheduled()
    if (cur) { cur.stopped = true; try { cur.src.stop() } catch { /* ended */ } }
    if (track && cur) stopPlayback(track.id).catch(() => {})
    holder?.pause()
    if (holder?.src) URL.revokeObjectURL(holder.src)
    ctx?.close()
    mq.controls = null
    if ('mediaSession' in navigator) {
      try { navigator.mediaSession.metadata = null; navigator.mediaSession.playbackState = 'none' } catch { /* ignore */ }
    }
  })
</script>

<audio bind:this={holder} aria-hidden="true"></audio>
