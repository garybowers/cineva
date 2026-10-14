<script lang="ts">
  // Music playback engine. Tracks are decoded with Web Audio and the next
  // one is scheduled sample-accurately at the current one's end, so albums
  // play gapless (a plain <audio> leaves an audible gap between files).
  // A silent <audio> element holds the browser's media session so lock
  // screens and hardware keys work while Web Audio does the actual playing.
  import Stars from './music/Stars.svelte'
  import { onDestroy, onMount } from 'svelte'
  import { pref } from './lib/prefs'
  import { playItem, postProgress, stopPlayback, beaconStop, TAKEN_OVER, type Item } from './lib/api'
  import {
    Shuffle, SkipBack, SkipForward, Play, Pause, Repeat, Repeat1,
    X, Music as MusicIcon, Disc3,
  } from '@lucide/svelte'

  let {
    queue,
    startIndex = 0,
    startShuffle = false,
    startOffset = 0,
    onclose,
    onlocate,
    onexpand,
    onchange,
    ontime,
  }: {
    queue: Item[]
    startIndex?: number
    startShuffle?: boolean
    /** Seconds into the first track: continuing from another device. */
    startOffset?: number
    onclose: () => void
    onlocate: (track: Item) => void
    onexpand?: () => void
    onchange?: (track: Item, index: number) => void
    ontime?: (secs: number) => void
  } = $props()

  let index = $state(0)
  let paused = $state(true)
  let loading = $state(false)
  let current = $state(0)
  let duration = $state(0)
  let shuffle = $state(startShuffle)
  let repeat: 'off' | 'all' | 'one' = $state('off')
  let volume = $state(readVolume())
  let muted = $state(false)
  let seeking = $state(false) // true while the seek thumb is being dragged

  const track = $derived(queue[index])

  // ---- Streaming quality (Settings page) ----
  // 'original' streams the file; otherwise a server-side Opus transcode.
  // Whatever the choice, a decode failure falls back to AAC, which every
  // browser can decode (Safari cannot do Ogg/Opus in Web Audio).
  export type MusicQuality = 'original' | 'opus-128' | 'opus-64'
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
    idx: number
    stopped: boolean // true when we stopped it ourselves (seek/skip): ignore onended
  }
  let cur: Playing | null = null
  // The pre-scheduled next track (decoded, source started at cur's end).
  let sched: { idx: number; src: AudioBufferSourceNode; buffer: AudioBuffer; at: number; cancelled: boolean } | null = null
  let prepareGen = 0 // invalidates in-flight prepare() calls
  const buffers = new Map<string, Promise<AudioBuffer>>() // small decoded cache

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
          return await fetchAndDecode(streamURL(item.id, true))
        }
      })()
      buffers.set(key, p)
      p.catch(() => buffers.delete(key))
      // Keep only a handful decoded — a FLAC album is hundreds of MB as PCM.
      for (const k of [...buffers.keys()].slice(0, Math.max(0, buffers.size - 3))) buffers.delete(k)
    }
    return p
  }

  function position(): number {
    if (!cur || !ctx) return 0
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

  // Start `queue[i]` from `offset` seconds right now.
  async function play(i: number, offset = 0) {
    const ac = engine()
    i = Math.max(0, Math.min(queue.length - 1, i))
    const item = queue[i]
    cancelScheduled()
    if (cur) {
      cur.stopped = true
      try { cur.src.stop() } catch { /* already ended */ }
      if (cur.idx !== i || queue[cur.idx].id !== item.id) stopPlayback(queue[cur.idx].id).catch(() => {})
      cur = null
    }
    index = i
    current = offset
    duration = item.runtimeSecs ?? 0
    loading = true
    onchange?.(item, i)
    if (offset === 0) playItem(item.id).catch(() => {}) // history + admin Activity
    const gen = ++prepareGen
    let buffer: AudioBuffer
    try {
      buffer = await decode(item)
    } catch {
      loading = false
      next() // skip what we cannot play
      return
    }
    if (gen !== prepareGen) return // superseded by another play()
    loading = false
    if (ac.state === 'suspended' && !paused) await ac.resume()
    const src = ac.createBufferSource()
    src.buffer = buffer
    src.connect(gain!)
    const startAt = ac.currentTime + 0.02
    src.start(startAt, Math.min(offset, buffer.duration))
    const me: Playing = { src, buffer, startedAt: startAt, offset, idx: i, stopped: false }
    cur = me
    duration = buffer.duration
    src.onended = () => onSourceEnded(me)
    prepare(me)
    updateMediaSession()
  }

  // Decode the following track and schedule it to start exactly when the
  // current one ends. Lives on `me` so a superseded play() can't schedule
  // against the wrong track.
  async function prepare(me: Playing) {
    const n = pickNext()
    if (n === null) return
    const gen = prepareGen
    let buffer: AudioBuffer
    try {
      buffer = await decode(queue[n])
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
    const s = { idx: n, src, buffer, at: endAt, cancelled: false }
    sched = s
    src.onended = () => {
      if (s.cancelled) return
      // Scheduled source finished on its own (it became cur in onSourceEnded).
    }
  }

  function onSourceEnded(me: Playing) {
    if (me.stopped || cur !== me) return
    markPlayed(queue[me.idx])
    if (sched && !sched.cancelled && ctx) {
      // Seamless hand-over: the scheduled source is already sounding.
      const s = sched
      sched = null
      const handed: Playing = { src: s.src, buffer: s.buffer, startedAt: s.at, offset: 0, idx: s.idx, stopped: false }
      cur = handed
      index = s.idx
      current = 0
      duration = s.buffer.duration
      s.src.onended = () => onSourceEnded(handed)
      stopPlayback(queue[me.idx].id).catch(() => {})
      playItem(queue[s.idx].id).catch(() => {})
      onchange?.(queue[s.idx], s.idx)
      updateMediaSession()
      prepare(handed)
      return
    }
    cur = null
    next()
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
      // End of the queue: stay on the last track, stopped.
      if (cur) { cur.stopped = true; try { cur.src.stop() } catch { /* ended */ } }
      cur = null
      paused = true
      current = 0
      ctx?.suspend()
      updateMediaSession()
    } else {
      paused = false
      play(n)
    }
  }

  function prev() {
    if (position() > 3 || index === 0) seekTo(0)
    else { paused = false; play(index - 1) }
  }

  function seekTo(secs: number) {
    if (!cur) return
    play(cur.idx, Math.max(0, Math.min(secs, cur.buffer.duration - 0.05)))
  }

  function togglePlay() {
    if (!ctx) { paused = false; play(index); return }
    if (!cur) { paused = false; play(index, current); return }
    if (paused) { paused = false; ctx.resume() } else { paused = true; ctx.suspend() }
    updateMediaSession()
  }

  function cycleRepeat() {
    repeat = repeat === 'off' ? 'all' : repeat === 'all' ? 'one' : 'off'
    reschedule()
  }
  function toggleShuffle() {
    shuffle = !shuffle
    reschedule()
  }
  // Shuffle/repeat changed: the pre-scheduled next track may be wrong now.
  function reschedule() {
    cancelScheduled()
    if (cur) prepare(cur)
  }

  // Jump to a queue position (used by the listening-session view).
  export function jumpTo(i: number) {
    paused = false
    play(i)
  }

  function markPlayed(item: Item) {
    postProgress(item.id, item.runtimeSecs ?? 0).catch(() => {})
  }

  // Dragging previews the position; the actual seek happens once on release.
  function onSeekPreview(e: Event) {
    seeking = true
    current = Number((e.target as HTMLInputElement).value)
  }
  function onSeekCommit(e: Event) {
    seeking = false
    seekTo(Number((e.target as HTMLInputElement).value))
  }

  function onVolume(e: Event) {
    volume = Number((e.target as HTMLInputElement).value)
    if (volume > 0) muted = false
    if (gain) gain.gain.value = muted ? 0 : volume
    try {
      localStorage.setItem('cineva-volume', String(volume))
    } catch { /* private browsing */ }
  }

  // ---- Clock: position readout, admin heartbeat, media session ----
  const tick = setInterval(() => {
    if (!cur || paused) return
    if (!seeking) current = position()
    ontime?.(current)
    if ('mediaSession' in navigator && duration > 0) {
      try { navigator.mediaSession.setPositionState({ duration, position: Math.min(current, duration), playbackRate: 1 }) } catch { /* unsupported */ }
    }
  }, 250)
  const heartbeat = setInterval(() => {
    if (track && ctx) postProgress(track.id, Math.floor(position()), paused).catch(() => {})
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
    const t = queue[index]
    if (!t) return
    try {
      navigator.mediaSession.metadata = new MediaMetadata({
        title: t.title, artist: t.artist ?? '',
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
    set('stop', onclose)
  }

  onMount(() => {
    holder.src = silentWav()
    holder.loop = true
    holder.volume = 0.01 // inaudible; some browsers drop the session at 0
    installMediaHandlers()
    paused = false
    play(shuffle && queue.length > 1 ? Math.floor(Math.random() * queue.length) : startIndex, startOffset)
  })

  const onHide = () => { if (track) beaconStop(track.id) }

  // Another device is continuing this track: pause, and say where.
  function onTakenOver(e: Event) {
    const id = (e as CustomEvent<{ itemId: string }>).detail.itemId
    if (!track || track.id !== id) return
    if (ctx && !paused) {
      paused = true
      ctx.suspend()
      updateMediaSession()
    }
    if (ctx) postProgress(track.id, Math.floor(position()), true).catch(() => {})
  }
  window.addEventListener(TAKEN_OVER, onTakenOver)
  onDestroy(() => window.removeEventListener(TAKEN_OVER, onTakenOver))
  window.addEventListener('pagehide', onHide)

  onDestroy(() => {
    clearInterval(tick)
    clearInterval(heartbeat)
    window.removeEventListener('pagehide', onHide)
    cancelScheduled()
    if (cur) { cur.stopped = true; try { cur.src.stop() } catch { /* ended */ } }
    if (track) stopPlayback(track.id).catch(() => {})
    holder?.pause()
    if (holder?.src) URL.revokeObjectURL(holder.src)
    ctx?.close()
    if ('mediaSession' in navigator) {
      try { navigator.mediaSession.metadata = null; navigator.mediaSession.playbackState = 'none' } catch { /* ignore */ }
    }
  })

  function fmt(secs: number): string {
    if (!isFinite(secs) || secs < 0) secs = 0
    const s = Math.floor(secs)
    return `${Math.floor(s / 60)}:${String(s % 60).padStart(2, '0')}`
  }

  const pct = (v: number) => (duration > 0 ? (v / duration) * 100 : 0)
</script>

<audio bind:this={holder} aria-hidden="true"></audio>

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
      <span class="mb-artist">{track.artist}{loading ? ' · loading…' : ''}</span>
    </div>
    <span class="mb-stars"><Stars {track} size={15} /></span>

    <div class="transport">
      <button class="ctl" class:on={shuffle} onclick={toggleShuffle} title="Shuffle"><Shuffle size={16} /></button>
      <button class="ctl" onclick={prev} title="Previous"><SkipBack size={17} /></button>
      <button class="ctl big" onclick={togglePlay} title={paused ? 'Play' : 'Pause'}>{#if paused}<Play size={22} />{:else}<Pause size={22} />{/if}</button>
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
    .mb-stars { display: none; } /* rated from the listening view's track menu */
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

  .mb-stars { flex: none; }

  .transport { display: flex; align-items: center; gap: 0.1rem; flex: none; }
  .ctl {
    color: var(--muted);
    font-size: 0.95rem;
    padding: 0.3rem 0.45rem;
    border-radius: 6px;
  }
  .ctl:hover { background: var(--bg); color: var(--text); }
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
