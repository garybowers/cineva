<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, libraryItems, itemDetail, itemSubtitles, playItem, continueWatching, downloadURL, searchItems,
    type Health, type Library, type Item, type ItemDetail, type PlayInfo, type ResumeItem, type PlayQuality, type SubtitleTrack,
  } from './lib/api'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'
  import Player from './Player.svelte'
  import MusicPlayer from './MusicPlayer.svelte'
  import {
    House, Library as LibraryIcon, Settings as SettingsIcon, User, Clapperboard, Tv,
    Music as MusicIcon, Headphones, Podcast, BookOpen, Play, Shuffle, Download,
    RotateCcw,
  } from '@lucide/svelte'

  type Tab = 'home' | 'libraries' | 'settings'
  const tabs = [
    { id: 'home' as Tab, label: 'Home', icon: House },
    { id: 'libraries' as Tab, label: 'Libraries', icon: LibraryIcon },
    { id: 'settings' as Tab, label: 'Settings', icon: SettingsIcon },
  ]

  let tab: Tab = $state('home')
  let health: Health | null = $state(null)
  let offline = $state(false)
  let routing = false // true while applyRoute drives state (suppresses pushState)

  // ---- URL routing: /libraries/<Library>/<Title>/... (names, not ids) ----
  function buildPath(): string {
    if (tab === 'home') return '/'
    if (tab === 'settings') return '/settings'
    let p = '/libraries'
    if (currentLib) p += '/' + encodeURIComponent(currentLib.name)
    if (detail) {
      for (const a of trail) p += '/' + encodeURIComponent(a.title)
      p += '/' + encodeURIComponent(detail.item.title)
    }
    return p
  }

  function syncURL() {
    if (routing) return
    const p = buildPath()
    if (p !== location.pathname) history.pushState({}, '', p)
  }

  function setTab(t: Tab) {
    tab = t
    syncURL()
  }

  async function applyRoute(rawPath: string) {
    routing = true
    try {
      const parts = rawPath.split('/').filter(Boolean).map(decodeURIComponent)
      if (parts.length === 0) {
        tab = 'home'
        return
      }
      if (parts[0] === 'settings') {
        tab = 'settings'
        return
      }
      if (parts[0] !== 'libraries') {
        tab = 'home'
        return
      }
      tab = 'libraries'
      if (libraries.length === 0) {
        try {
          libraries = await listLibraries()
        } catch {
          return
        }
      }
      const lib = parts[1] ? libraries.find((l) => l.name === parts[1]) : undefined
      if (!lib) {
        currentLib = null
        detail = null
        trail = []
        return
      }
      await openLibrary(lib)
      const segs = parts.slice(2)
      if (segs.length === 0) return

      // Resolve title segments against the library's item tree.
      const items = await libraryItems(lib.id)
      let target: Item | null = null
      const ancestors: Item[] = []
      for (const seg of segs) {
        const found = items.find((i) =>
          i.title === seg &&
          (target ? i.parentId === target.id : !i.parentId || i.kind === 'movie' || i.kind === 'series' || i.kind === 'artist'),
        )
        if (!found) break
        if (target) ancestors.push(target)
        target = found
      }
      if (!target) return
      // Seasons are the last navigation level: an episode URL resolves to
      // its season with the episode selected in-page.
      let selectEp: Item | null = null
      if (target.kind === 'episode' && target.parentId) {
        selectEp = target
        target = items.find((i) => i.id === selectEp!.parentId) ?? target
        ancestors.pop()
      }
      detail = await itemDetail(target.id)
      trail = ancestors
      epDetail = null
      detailSubs = []
      if (detail.item.kind === 'movie') {
        itemSubtitles(target.id).then((s) => (detailSubs = s)).catch(() => {})
      }
      if (selectEp) selectEpisode(selectEp)
    } finally {
      routing = false
    }
  }

  const typeLabels: Record<string, { label: string; icon: typeof House }> = {
    movies: { label: 'Movies', icon: Clapperboard },
    tv: { label: 'TV Shows', icon: Tv },
    music: { label: 'Music', icon: MusicIcon },
    audiobooks: { label: 'Audiobooks', icon: Headphones },
    podcasts: { label: 'Podcasts', icon: Podcast },
    books: { label: 'Books', icon: BookOpen },
  }
  const libraryTree = $derived(
    Object.entries(typeLabels)
      .map(([type, meta]) => ({ type, ...meta, libs: libraries.filter((l) => l.mediaType === type) }))
      .filter((g) => g.libs.length > 0),
  )
  let themes: ThemeInfo[] = $state([])
  let theme = $state(currentTheme())

  // Libraries browse state: a simple drill-down stack.
  let libraries: Library[] = $state([])
  let currentLib: Library | null = $state(null)
  let gridItems: Item[] = $state([])
  let detail: ItemDetail | null = $state(null)
  let detailSubs: SubtitleTrack[] = $state([])
  let trail: Item[] = $state([]) // breadcrumb ancestors of the current detail
  let resume: ResumeItem[] = $state([])
  let playing: { item: Item; info: PlayInfo; quality: PlayQuality; gen: number } | null = $state(null)
  let music: { queue: Item[]; startIndex: number; shuffle: boolean; gen: number } | null = $state(null)
  let autoplay = $state(readAutoplay())
  let playGen = 0 // bumps on quality change so the player remounts

  // Video and music playback are mutually exclusive.
  function startMusic(tracks: Item[], startIndex: number, shuffleAll = false) {
    playing = null
    music = { queue: tracks, startIndex, shuffle: shuffleAll, gen: ++playGen }
  }

  // Open the album containing a track (from the music bar or search).
  async function openAlbumOf(track: Item) {
    if (!track.parentId) return
    tab = 'libraries'
    const lib = libraries.find((l) => l.id === track.libraryId)
    if (lib && lib.id !== currentLib?.id) {
      currentLib = lib
      libraryItems(lib.id).then((items) => {
        gridItems = items.filter(gridFilter(lib))
      })
    }
    await openItem(track.parentId)
  }

  const gridFilter = (lib: Library) => (i: Item) =>
    lib.mediaType === 'music' ? i.kind === 'album' : i.kind === 'movie' || i.kind === 'series'

  // ---- Music view: artist rail + album shelves ----
  let musicAll: Item[] = $state([])
  let selArtist = $state('ALL') // artist item id | 'ALL' | 'COMP'
  let selAlbumId: string | null = $state(null) // expanded album panel

  const mArtists = $derived(
    musicAll.filter((i) => i.kind === 'artist').toSorted((a, b) => a.title.localeCompare(b.title)),
  )
  const mAlbums = $derived(musicAll.filter((i) => i.kind === 'album'))
  const mTracks = $derived(musicAll.filter((i) => i.kind === 'track'))
  const albumById = $derived(new Map(mAlbums.map((a) => [a.id, a])))
  // Compilations: tagged Various Artists, or tracks by 2+ different artists.
  const compAlbumIds = $derived.by(() => {
    const artistsPer = new Map<string, Set<string>>()
    for (const t of mTracks) {
      if (!t.parentId) continue
      const s = artistsPer.get(t.parentId) ?? new Set<string>()
      s.add(t.artist ?? '')
      artistsPer.set(t.parentId, s)
    }
    const ids = new Set<string>()
    for (const a of mAlbums) {
      if ((a.artist ?? '').toLowerCase().startsWith('various') || (artistsPer.get(a.id)?.size ?? 0) > 1) {
        ids.add(a.id)
      }
    }
    return ids
  })
  const byYear = (a: Item, b: Item) => (a.year ?? 0) - (b.year ?? 0) || a.title.localeCompare(b.title)

  // Shelf groups: one per artist for "All", a single group otherwise.
  const shelfGroups = $derived.by(() => {
    if (selArtist === 'COMP') {
      return [{ id: 'COMP', title: 'Compilations', albums: mAlbums.filter((a) => compAlbumIds.has(a.id)).toSorted(byYear) }]
    }
    if (selArtist !== 'ALL') {
      const a = mArtists.find((x) => x.id === selArtist)
      return [{ id: selArtist, title: a?.title ?? '', albums: mAlbums.filter((al) => al.parentId === selArtist).toSorted(byYear) }]
    }
    return mArtists
      .map((a) => ({ id: a.id, title: a.title, albums: mAlbums.filter((al) => al.parentId === a.id).toSorted(byYear) }))
      .filter((g) => g.albums.length > 0)
  })
  const shelfAlbumCount = $derived(shelfGroups.reduce((n, g) => n + g.albums.length, 0))

  const tracksOf = (albumId: string) =>
    mTracks
      .filter((t) => t.parentId === albumId)
      .toSorted((x, y) => (x.discNum ?? 0) - (y.discNum ?? 0) || (x.trackNum ?? 0) - (y.trackNum ?? 0))

  const shelfQueue = () => shelfGroups.flatMap((g) => g.albums.flatMap((a) => tracksOf(a.id)))

  function selectArtist(id: string) {
    selArtist = id
    selAlbumId = null
    musicFilter = ''
  }

  // Filter within the current artist/album scope: albums by title or by
  // containing a matching song; matching songs listed for direct play.
  let musicFilter = $state('')
  const filterQ = $derived(musicFilter.trim().toLowerCase())
  const filteredShelfGroups = $derived.by(() => {
    if (!filterQ) return shelfGroups
    return shelfGroups
      .map((g) => ({
        ...g,
        albums: g.albums.filter(
          (a) =>
            a.title.toLowerCase().includes(filterQ) ||
            tracksOf(a.id).some((t) => t.title.toLowerCase().includes(filterQ)),
        ),
      }))
      .filter((g) => g.albums.length > 0)
  })
  const matchedSongs = $derived.by(() => {
    if (!filterQ) return []
    const out: { t: Item; album: Item; idx: number; tracks: Item[] }[] = []
    for (const g of shelfGroups) {
      for (const a of g.albums) {
        const ts = tracksOf(a.id)
        ts.forEach((t, i) => {
          if (t.title.toLowerCase().includes(filterQ)) out.push({ t, album: a, idx: i, tracks: ts })
        })
      }
    }
    return out.slice(0, 60)
  })

  const trackTime = (secs?: number) =>
    secs ? `${Math.floor(secs / 60)}:${String(secs % 60).padStart(2, '0')}` : ''

  // Search
  let searchQ = $state('')
  let searchResults: Item[] = $state([])
  let searchTimer: ReturnType<typeof setTimeout> | undefined

  function onSearchInput() {
    clearTimeout(searchTimer)
    const q = searchQ.trim()
    if (q.length < 2) {
      searchResults = []
      return
    }
    searchTimer = setTimeout(async () => {
      try {
        searchResults = await searchItems(q)
      } catch {
        searchResults = []
      }
    }, 250)
  }

  function clearSearch() {
    searchQ = ''
    searchResults = []
  }

  // The full player shows on the playing item's own page; anywhere else it
  // collapses to the bottom now-playing bar (Plex-style).
  const playerCompact = $derived(
    !!playing &&
      !(tab === 'libraries' && detail &&
        (detail.item.id === playing.item.id || detail.item.id === playing.item.parentId)),
  )

  // Jump to an item's detail page, restoring library context.
  async function showItem(target: Item) {
    if (target.kind === 'track') return openAlbumOf(target)
    tab = 'libraries'
    const lib = libraries.find((l) => l.id === target.libraryId)
    if (lib && lib.id !== currentLib?.id) {
      currentLib = lib
      libraryItems(lib.id).then((items) => {
        gridItems = items.filter(gridFilter(lib))
      })
    }
    await openItem(target.id)
  }

  function readQuality(): PlayQuality {
    try {
      return (localStorage.getItem('cineva-quality') as PlayQuality) || 'auto'
    } catch {
      return 'auto'
    }
  }

  function readAutoplay(): boolean {
    try {
      return localStorage.getItem('cineva-autoplay') !== 'off'
    } catch {
      return true
    }
  }

  function setAutoplay(on: boolean) {
    autoplay = on
    try {
      localStorage.setItem('cineva-autoplay', on ? 'on' : 'off')
    } catch {
      /* private browsing */
    }
  }

  function setTheme(id: string) {
    theme = id
    applyTheme(id)
  }

  async function poll() {
    try {
      health = await getHealth()
      offline = false
      libraries = await listLibraries()
      resume = await continueWatching()
    } catch {
      health = null
      offline = true
    }
  }

  async function startPlay(item: Item, quality: PlayQuality = readQuality(), fromStart = false) {
    try {
      const info = await playItem(item.id, quality, fromStart)
      music = null // video and music are mutually exclusive
      playing = { item, info, quality, gen: ++playGen }
      const inContext = detail && (detail.item.id === item.id || detail.item.id === item.parentId)
      if (!inContext) showItem(item) // full player lives on the item's (or its season's) page
    } catch (e) {
      console.error('play failed', e)
    }
  }

  function closePlayer() {
    playing = null
    poll() // refresh continue-watching
  }

  function playNext(next: Item) {
    playing = null
    startPlay(next)
  }

  function changeQuality(q: PlayQuality) {
    try {
      localStorage.setItem('cineva-quality', q)
    } catch {
      /* private browsing */
    }
    if (playing) startPlay(playing.item, q)
  }

  async function openLibrary(lib: Library) {
    currentLib = lib
    detail = null
    trail = []
    const items = await libraryItems(lib.id)
    if (lib.mediaType === 'music') {
      musicAll = items
      selArtist = 'ALL'
      selAlbumId = null
      gridItems = []
    } else {
      gridItems = items.filter(gridFilter(lib))
    }
    syncURL()
  }

  // fromChild: opened via the current detail's children list (drill-down);
  // otherwise the trail is rebuilt by walking the parent chain, so direct
  // jumps (resume, search) still show the full path.
  async function openItem(id: string, fromChild = false) {
    const parent = fromChild && detail ? detail.item : null
    let d = await itemDetail(id)
    let ancestors: Item[] | null = null
    // Seasons are the last navigation level: opening an episode opens its
    // season with the episode selected in-page.
    let selectEp: Item | null = null
    if (d.item.kind === 'episode' && d.item.parentId) {
      selectEp = d.item
      d = await itemDetail(d.item.parentId)
    }
    detail = d
    epDetail = null
    if (parent && !selectEp) {
      trail = [...trail, parent]
    } else {
      ancestors = []
      let parentId = detail.item.parentId
      while (parentId && ancestors.length < 4) {
        try {
          const p = (await itemDetail(parentId)).item
          ancestors.unshift(p)
          parentId = p.parentId
        } catch {
          break
        }
      }
      trail = ancestors
    }
    detailSubs = []
    if (detail.item.kind === 'movie') {
      itemSubtitles(detail.item.id).then((s) => (detailSubs = s)).catch(() => {})
    }
    if (selectEp) selectEpisode(selectEp)
    syncURL()
  }

  // ---- Episode selection within a season page (accordion panel) ----
  let epDetail: ItemDetail | null = $state(null)
  let epSubs: SubtitleTrack[] = $state([])

  async function selectEpisode(ep: Item) {
    if (epDetail?.item.id === ep.id) {
      epDetail = null // toggle off
      return
    }
    epSubs = []
    epDetail = await itemDetail(ep.id)
    itemSubtitles(ep.id).then((s) => (epSubs = s)).catch(() => {})
  }

  const canResume = (d: ItemDetail | null) =>
    !!d?.playState && d.playState.positionSecs > 10 && !d.playState.watched
  const minsLeft = (d: ItemDetail | null) =>
    d && d.playState && d.item.runtimeSecs
      ? Math.max(1, Math.round((d.item.runtimeSecs - d.playState.positionSecs) / 60))
      : 0

  const resumable = $derived(canResume(detail))
  const minutesLeft = $derived(minsLeft(detail))

  function toLibraries() {
    currentLib = null
    detail = null
    epDetail = null
    trail = []
    gridItems = []
    tab = 'libraries'
    syncURL()
  }

  function toGrid() {
    detail = null
    epDetail = null
    trail = []
    syncURL()
  }

  async function toCrumb(index: number) {
    const target = trail[index]
    detail = await itemDetail(target.id)
    trail = trail.slice(0, index)
    epDetail = null
    syncURL()
  }

  function runtime(secs?: number): string {
    if (!secs) return ''
    const m = Math.round(secs / 60)
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`
  }

  onMount(() => {
    poll()
    listThemes().then((t) => (themes = t)).catch(() => {})
    applyRoute(location.pathname)
    const onPop = () => applyRoute(location.pathname)
    window.addEventListener('popstate', onPop)
    const id = setInterval(poll, 5000)
    return () => {
      clearInterval(id)
      window.removeEventListener('popstate', onPop)
    }
  })
</script>

{#snippet poster(item: Item)}
  {#if item.poster}
    <img class="poster" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'} src={item.poster} alt={item.title} loading="lazy" />
  {:else}
    <div class="poster placeholder" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'}><span>{#if item.kind === 'album' || item.kind === 'track'}<MusicIcon size={30} />{:else}{item.title.slice(0, 1)}{/if}</span></div>
  {/if}
{/snippet}

<div class="shell">
  <div class="topbar">
    <button class="logo" onclick={() => setTab('home')}>Cineva</button>
    <div class="search">
      <input
        type="search"
        placeholder="Search…"
        bind:value={searchQ}
        oninput={onSearchInput}
        onkeydown={(e) => e.key === 'Escape' && clearSearch()}
      />
      {#if searchResults.length > 0}
        <div class="search-results">
          {#each searchResults as r (r.id)}
            <button class="sr" onclick={() => { showItem(r); clearSearch() }}>
              {#if r.poster}
                <img src={r.poster} alt="" />
              {:else}
                <span class="sr-ph">{r.title.slice(0, 1)}</span>
              {/if}
              <span class="sr-text">
                <span class="sr-title">{r.title}</span>
                <span class="sr-sub">
                  {r.kind}{r.year ? ` · ${r.year}` : ''}{r.kind === 'episode' ? ` · S${String(r.seasonNum).padStart(2, '0')}E${String(r.episodeNum).padStart(2, '0')}` : ''}
                </span>
              </span>
            </button>
          {/each}
        </div>
      {/if}
    </div>
    <span class="status" class:offline title={offline ? 'Server unreachable' : `Connected · ${health?.version ?? ''}`}></span>
    <button class="topbtn" class:active={tab === 'settings'} onclick={() => setTab('settings')} title="Settings"><SettingsIcon size={17} /></button>
    <button class="avatar" title="Local user — accounts arrive with milestone 4"><User size={17} /></button>
  </div>

  <div class="body">
  <aside class="side">
    <button class="side-item" class:active={tab === 'home'} onclick={() => setTab('home')}><House size={16} /> Home</button>
    <div class="tree">
      {#each libraryTree as group (group.type)}
        {@const TypeIcon = group.icon}
        <div class="type-label"><TypeIcon size={15} /> {group.label}</div>
        {#each group.libs as lib (lib.id)}
          <button
            class="side-item leaf"
            class:active={tab === 'libraries' && currentLib?.id === lib.id}
            onclick={() => { tab = 'libraries'; openLibrary(lib) }}
          >{lib.name}</button>
        {/each}
      {/each}
      {#if libraries.length === 0}
        <div class="type-label">No libraries yet</div>
      {/if}
    </div>
    <div class="side-status side-bottom">
      <span class="status" class:offline></span>
      <span>{offline ? 'offline' : health?.version ?? ''}</span>
    </div>
  </aside>

  <div class="content">
  <main class:withbar={(playing && playerCompact) || !!music}>
    {#if tab === 'libraries' && currentLib}
      <nav class="crumbs" aria-label="Breadcrumb">
        <button class="crumb" onclick={toLibraries}>Libraries</button>
        <span class="sep">›</span>
        {#if detail}
          <button class="crumb" onclick={toGrid}>{currentLib.name}</button>
          {#each trail as ancestor, i (ancestor.id)}
            <span class="sep">›</span>
            <button class="crumb" onclick={() => toCrumb(i)}>{ancestor.title}</button>
          {/each}
          <span class="sep">›</span>
          <span class="crumb-current">{detail.item.title}</span>
        {:else}
          <span class="crumb-current">{currentLib.name}</span>
        {/if}
      </nav>
    {/if}
    {#if music}
      {#key music.gen}
        <MusicPlayer
          queue={music.queue}
          startIndex={music.startIndex}
          startShuffle={music.shuffle}
          onclose={() => (music = null)}
          onlocate={openAlbumOf}
        />
      {/key}
    {/if}
    {#if playing}
      {#key playing.gen}
        <Player
          item={playing.item}
          info={playing.info}
          quality={playing.quality}
          compact={playerCompact}
          onclose={closePlayer}
          onnext={playNext}
          onquality={changeQuality}
          onexpand={() => playing && showItem(playing.item)}
        />
      {/key}
    {/if}
    {#if tab === 'home'}
      <h1>Home</h1>
      <section>
        <h2>Continue Watching</h2>
        {#if resume.length === 0}
          <div class="empty">Nothing in progress — start something from your libraries.</div>
        {:else}
          <div class="grid">
            {#each resume as r (r.id)}
              <button class="cell" onclick={() => startPlay(r)}>
                {@render poster(r)}
                <div class="progress"><div class="progress-fill" style="width: {Math.min(100, (r.positionSecs / (r.runtimeSecs || 1)) * 100)}%"></div></div>
                <span class="cell-title">{r.title}</span>
                {#if r.kind === 'episode'}<span class="cell-sub">S{String(r.seasonNum).padStart(2, '0')}E{String(r.episodeNum).padStart(2, '0')}</span>{/if}
              </button>
            {/each}
          </div>
        {/if}
      </section>
      <section>
        <h2>Libraries</h2>
        {#if libraries.length === 0}
          <div class="empty">No libraries yet — create one in the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="librow">
            {#each libraries as lib (lib.id)}
              <button class="libcard" onclick={() => { tab = 'libraries'; openLibrary(lib) }}>
                <strong>{lib.name}</strong>
                <span>{lib.itemCount} items</span>
              </button>
            {/each}
          </div>
        {/if}
      </section>
    {:else if tab === 'libraries'}
      {#if detail}
        <div class="detail">
          {@render poster(detail.item)}
          <div class="detail-body">
            <h1>
              {detail.item.title}
              {#if detail.item.year}<span class="muted">({detail.item.year})</span>{/if}
            </h1>
            {#if detail.item.kind === 'album'}
              <p class="meta">{detail.item.artist} · {detail.children.length} tracks</p>
              <div class="actions">
                <button class="play" onclick={() => detail && startMusic(detail.children, 0)}><Play size={15} /> Play</button>
                <button class="dl" onclick={() => detail && startMusic(detail.children, 0, true)}><Shuffle size={14} /> Shuffle</button>
              </div>
            {/if}
            <p class="meta">
              {#if detail.item.kind === 'episode'}S{String(detail.item.seasonNum).padStart(2, '0')}E{String(detail.item.episodeNum).padStart(2, '0')} · {/if}
              {runtime(detail.item.runtimeSecs)}
              {#if detail.item.width}· {detail.item.width}x{detail.item.height} {detail.item.videoCodec}{/if}
            </p>
            {#if detail.item.overview}<p class="overview">{detail.item.overview}</p>{/if}
            {#if detail.item.kind === 'movie' || detail.item.kind === 'episode'}
              <div class="actions">
                <button class="play" onclick={() => detail && startPlay(detail.item)}>
                  <Play size={15} /> {resumable ? 'Resume' : 'Play'}
                </button>
                {#if resumable}
                  <span class="muted">{minutesLeft} min left</span>
                  <button class="dl" onclick={() => detail && startPlay(detail.item, undefined, true)}>
                    <RotateCcw size={14} /> Start over
                  </button>
                {/if}
                <a class="dl" href={downloadURL(detail.item.id)} download><Download size={14} /> Download</a>
              </div>
              <dl class="tech">
                <div><dt>Video</dt><dd>{detail.item.height}p ({detail.item.videoCodec})</dd></div>
                <div><dt>Audio</dt><dd>{(detail.item.audioCodec ?? '').toUpperCase()}</dd></div>
                <div>
                  <dt>Subtitles</dt>
                  <dd>{detailSubs.length ? detailSubs.map((s) => s.title || s.language || s.codec).join(' · ') : 'None'}</dd>
                </div>
              </dl>
            {/if}
          </div>
        </div>
        {#if detail.children.length > 0}
          {#if detail.item.kind === 'series'}
            <!-- Seasons as a poster grid -->
            <div class="grid seasons">
              {#each detail.children as child (child.id)}
                <button class="cell" onclick={() => openItem(child.id, true)}>
                  {@render poster(child)}
                  <span class="cell-title">{child.title}</span>
                </button>
              {/each}
            </div>
          {:else if detail.item.kind === 'season'}
            <!-- Episodes: terminal level — rows select an inline panel -->
            <ul class="eplist">
              {#each detail.children as child (child.id)}
                <li>
                  <button class="eprow" class:sel={epDetail?.item.id === child.id} onclick={() => selectEpisode(child)}>
                    {#if child.poster}
                      <img class="epthumb" src={child.poster} alt="" loading="lazy" />
                    {:else}
                      <span class="epthumb ph"><Tv size={20} /></span>
                    {/if}
                    <span class="epmain">
                      <span><span class="epnum">{child.episodeNum}.</span> {child.title}</span>
                    </span>
                    <span class="muted">{runtime(child.runtimeSecs)}</span>
                  </button>
                  {#if epDetail && epDetail.item.id === child.id}
                    <div class="eppanel">
                      <p class="meta">
                        S{String(epDetail.item.seasonNum).padStart(2, '0')}E{String(epDetail.item.episodeNum).padStart(2, '0')}
                        · {runtime(epDetail.item.runtimeSecs)}
                        {#if epDetail.item.width}· {epDetail.item.width}x{epDetail.item.height} {epDetail.item.videoCodec}{/if}
                        {#if epSubs.length}· CC: {epSubs.map((s) => s.language || s.codec).join(', ')}{/if}
                      </p>
                      {#if epDetail.item.overview}<p class="overview">{epDetail.item.overview}</p>{/if}
                      <div class="actions">
                        <button class="play" onclick={() => epDetail && startPlay(epDetail.item)}>
                          <Play size={15} /> {canResume(epDetail) ? 'Resume' : 'Play'}
                        </button>
                        {#if canResume(epDetail)}
                          <span class="muted">{minsLeft(epDetail)} min left</span>
                          <button class="dl" onclick={() => epDetail && startPlay(epDetail.item, undefined, true)}>
                            <RotateCcw size={14} /> Start over
                          </button>
                        {/if}
                        <a class="dl" href={downloadURL(epDetail.item.id)} download><Download size={14} /> Download</a>
                      </div>
                    </div>
                  {/if}
                </li>
              {/each}
            </ul>
          {:else}
            <ul class="children">
              {#each detail.children as child, ci (child.id)}
                <li>
                  {#if child.kind === 'track'}
                    <button onclick={() => detail && startMusic(detail.children, ci)}>
                      <span>
                        <span class="epnum">{child.trackNum || ci + 1}.</span>
                        {child.title}
                        {#if child.artist && child.artist !== detail.item.artist}<span class="muted"> — {child.artist}</span>{/if}
                      </span>
                      <span class="muted">{child.runtimeSecs ? `${Math.floor(child.runtimeSecs / 60)}:${String(child.runtimeSecs % 60).padStart(2, '0')}` : ''}</span>
                    </button>
                  {:else}
                    <button onclick={() => openItem(child.id, true)}>
                      <span>
                        {child.title}
                        {#if child.kind === 'album' && child.year}<span class="muted"> ({child.year})</span>{/if}
                      </span>
                      <span class="muted">{runtime(child.runtimeSecs)}</span>
                    </button>
                  {/if}
                </li>
              {/each}
            </ul>
          {/if}
        {/if}
      {:else if currentLib && currentLib.mediaType === 'music'}
        <h1>{currentLib.name}</h1>
        {#if musicAll.length === 0}
          <div class="empty">Nothing here yet — run a scan from the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="music">
            <aside class="mrail">
              <button class="rail-item" class:sel={selArtist === 'ALL'} onclick={() => selectArtist('ALL')}>
                All Artists <span class="rail-n">{mArtists.length}</span>
              </button>
              {#if compAlbumIds.size > 0}
                <button class="rail-item" class:sel={selArtist === 'COMP'} onclick={() => selectArtist('COMP')}>
                  Compilations <span class="rail-n">{compAlbumIds.size}</span>
                </button>
              {/if}
              <div class="rail-div"></div>
              {#each mArtists as a (a.id)}
                <button class="rail-item" class:sel={selArtist === a.id} onclick={() => selectArtist(a.id)}>{a.title}</button>
              {/each}
            </aside>

            <section class="mcontent">
              <header class="mhead">
                <div>
                  <h2 class="mtitle">
                    {selArtist === 'ALL' ? 'All Artists' : selArtist === 'COMP' ? 'Compilations' : shelfGroups[0]?.title}
                  </h2>
                  <span class="muted">{shelfAlbumCount} albums</span>
                </div>
                <div class="mactions">
                  <input
                    class="mfilter"
                    type="search"
                    placeholder="Filter songs & albums…"
                    bind:value={musicFilter}
                    onkeydown={(e) => e.key === 'Escape' && (musicFilter = '')}
                  />
                  <button class="play" onclick={() => startMusic(shelfQueue(), 0)}><Play size={15} /> Play</button>
                  <button class="dl" onclick={() => startMusic(shelfQueue(), 0, true)}><Shuffle size={14} /> Shuffle</button>
                </div>
              </header>

              {#if filterQ && matchedSongs.length > 0}
                <div class="msongs">
                  <div class="msongs-h">Songs</div>
                  {#each matchedSongs as m (m.t.id)}
                    <button class="msong" onclick={() => startMusic(m.tracks, m.idx)}>
                      <span class="tname">{m.t.title}</span>
                      <span class="muted">{m.album.artist} — {m.album.title}</span>
                      <span class="muted ttm">{trackTime(m.t.runtimeSecs)}</span>
                    </button>
                  {/each}
                </div>
              {:else if filterQ && filteredShelfGroups.length === 0}
                <div class="empty">No matches for “{musicFilter}”.</div>
              {/if}

              {#each filteredShelfGroups as group (group.id)}
                {#if selArtist === 'ALL'}
                  <button class="shelf-h" onclick={() => selectArtist(group.id)}>{group.title} <span class="muted">· {group.albums.length} albums</span></button>
                {/if}
                <div class="shelf">
                  {#each group.albums as album (album.id)}
                    <button class="acard" class:sel={selAlbumId === album.id} onclick={() => (selAlbumId = selAlbumId === album.id ? null : album.id)}>
                      {#if album.poster}
                        <img class="acover" src={album.poster} alt={album.title} loading="lazy" />
                      {:else}
                        <span class="acover ph"><MusicIcon size={30} /></span>
                      {/if}
                      <span class="aname">{album.title}</span>
                      <span class="asub">{album.year || ''}</span>
                    </button>
                  {/each}
                </div>
                {#if selAlbumId && group.albums.some((a) => a.id === selAlbumId)}
                  {@const album = albumById.get(selAlbumId)}
                  {@const tracks = tracksOf(selAlbumId)}
                  {#if album}
                    <div class="apanel">
                      {#if album.poster}
                        <img class="apanel-cover" src={album.poster} alt="" />
                      {:else}
                        <span class="apanel-cover ph"><MusicIcon size={40} /></span>
                      {/if}
                      <div class="apanel-body">
                        <div class="apanel-head">
                          <div>
                            <strong>{album.title}</strong>
                            <span class="muted"> — {album.artist}{album.year ? ` · ${album.year}` : ''} · {tracks.length} tracks</span>
                          </div>
                          <div class="mactions">
                            <button class="play" onclick={() => startMusic(tracks, 0)}><Play size={15} /> Play</button>
                            <button class="dl" onclick={() => startMusic(tracks, 0, true)}><Shuffle size={14} /></button>
                          </div>
                        </div>
                        <ul class="tlist">
                          {#each tracks as t, ti (t.id)}
                            <li>
                              <button onclick={() => startMusic(tracks, ti)}>
                                <span class="tno">{t.trackNum || ti + 1}</span>
                                <span class="tname">{t.title}{#if t.artist && t.artist !== album.artist}<span class="muted"> — {t.artist}</span>{/if}</span>
                                <span class="muted ttm">{trackTime(t.runtimeSecs)}</span>
                              </button>
                            </li>
                          {/each}
                        </ul>
                      </div>
                    </div>
                  {/if}
                {/if}
              {/each}
            </section>
          </div>
        {/if}
      {:else if currentLib}
        <h1>{currentLib.name}</h1>
        {#if gridItems.length === 0}
          <div class="empty">Nothing here yet — run a scan from the <a href="/admin/">admin UI</a>.</div>
        {:else}
          <div class="grid">
            {#each gridItems as item (item.id)}
              <button class="cell" onclick={() => openItem(item.id)}>
                {@render poster(item)}
                <span class="cell-title">{item.title}</span>
                <span class="cell-sub">
                  {#if item.kind === 'album'}{item.artist}{#if item.year} · {item.year}{/if}{:else if item.year}{item.year}{/if}
                </span>
              </button>
            {/each}
          </div>
        {/if}
      {:else}
        <h1>Libraries</h1>
        {#if libraries.length === 0}
          <div class="empty tall">
            <p><strong>No libraries yet.</strong></p>
            <p>Libraries are created in the <a href="/admin/">admin UI</a> — one media type each.</p>
          </div>
        {:else}
          <div class="librow">
            {#each libraries as lib (lib.id)}
              <button class="libcard" onclick={() => openLibrary(lib)}>
                <strong>{lib.name}</strong>
                <span>{lib.mediaType} · {lib.itemCount} items</span>
              </button>
            {/each}
          </div>
        {/if}
      {/if}
    {:else}
      <h1>Settings</h1>
      <section class="card">
        <div class="row">
          <span>Theme</span>
          <div class="theme-picker">
            {#each themes as t (t.id)}
              <button class="chip" class:sel={theme === t.id} onclick={() => setTheme(t.id)}>{t.name}</button>
            {/each}
          </div>
        </div>
        <div class="row">
          <span>Server</span>
          <span class="muted">{offline ? 'unreachable' : `${health?.service} ${health?.version}`}</span>
        </div>
        <div class="row">
          <span>Next-episode autoplay</span>
          <div class="theme-picker">
            <button class="chip" class:sel={autoplay} onclick={() => setAutoplay(true)}>On</button>
            <button class="chip" class:sel={!autoplay} onclick={() => setAutoplay(false)}>Off</button>
          </div>
        </div>
      </section>
    {/if}
  </main>

  <nav class="tabbar">
    {#each tabs as t (t.id)}
      {@const TabIcon = t.icon}
      <button class:active={tab === t.id} onclick={() => setTab(t.id)}>
        <TabIcon size={19} />
        <span>{t.label}</span>
      </button>
    {/each}
  </nav>
  </div>
  </div>
</div>


<style>
  .shell { min-height: 100vh; display: flex; flex-direction: column; }
  .body { flex: 1; display: flex; }
  .content { flex: 1; min-width: 0; display: flex; flex-direction: column; }

  /* ---- Persistent top bar: logo, universal search, profile ---- */
  :root { --topbar-h: 3.4rem; }
  .topbar {
    position: sticky;
    top: 0;
    z-index: 50;
    display: flex;
    align-items: center;
    gap: 0.9rem;
    height: var(--topbar-h);
    padding: 0 1rem;
    background: var(--panel);
    border-bottom: 1px solid var(--border);
  }
  .topbar .logo { flex: none; }
  .topbar .status { margin-left: auto; } /* pushes status + profile to the right edge */
  .search { position: relative; flex: 1; max-width: 30rem; }
  .search input {
    width: 100%;
    background: var(--bg);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    font: inherit;
    font-size: 0.88rem;
    padding: 0.45rem 1rem;
  }
  .search input:focus { outline: none; border-color: var(--accent); }
  .search-results {
    position: absolute;
    top: calc(100% + 0.4rem);
    left: 0;
    right: 0;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    box-shadow: 0 10px 30px rgba(0, 0, 0, 0.4);
    overflow: hidden;
    max-height: 60vh;
    overflow-y: auto;
  }
  .sr {
    display: flex;
    align-items: center;
    gap: 0.7rem;
    width: 100%;
    padding: 0.5rem 0.8rem;
    text-align: left;
  }
  .sr:hover { background: var(--bg); }
  .sr img, .sr-ph {
    width: 2rem;
    height: 3rem;
    border-radius: 4px;
    object-fit: cover;
    flex: none;
  }
  .sr-ph {
    display: grid;
    place-items: center;
    background: var(--bg);
    color: var(--muted);
    font-weight: 700;
  }
  .sr-text { display: flex; flex-direction: column; min-width: 0; }
  .sr-title { font-size: 0.9rem; white-space: nowrap; overflow: hidden; text-overflow: ellipsis; }
  .sr-sub { font-size: 0.76rem; color: var(--muted); }
  .avatar, .topbtn {
    width: 2.1rem;
    height: 2.1rem;
    border-radius: 50%;
    background: var(--bg);
    border: 1px solid var(--border);
    display: grid;
    place-items: center;
    font-size: 1rem;
    flex: none;
    color: var(--muted);
  }
  .avatar:hover, .topbtn:hover { border-color: var(--accent); color: var(--text); }
  .topbtn.active { border-color: var(--accent); color: var(--accent-2); }

  /* ---- Sidebar: desktop only; mobile keeps the bottom tab bar ---- */
  .side { display: none; }
  @media (min-width: 768px) {
    .side {
      display: flex;
      flex-direction: column;
      width: 15rem;
      flex: none;
      position: sticky;
      top: var(--topbar-h);
      height: calc(100vh - var(--topbar-h));
      overflow-y: auto;
      background: var(--panel);
      border-right: 1px solid var(--border);
      padding: 0.9rem 0.8rem;
      gap: 0.15rem;
    }
    .tabbar { display: none !important; }
  }
  main.withbar { padding-bottom: 6.5rem; }
  .side-item {
    display: flex;
    align-items: center;
    gap: 0.5rem;
    text-align: left;
    padding: 0.5rem 0.65rem;
    border-radius: 8px;
    color: var(--muted);
    font-size: 0.92rem;
  }
  .side-item:hover { background: var(--bg); color: var(--text); }
  .side-item.active { background: var(--bg); color: var(--accent-2); }
  .tree { display: flex; flex-direction: column; gap: 0.1rem; margin-top: 0.75rem; flex: 1; }
  .type-label {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    color: var(--muted);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.7rem 0.65rem 0.25rem;
  }
  .type-icon { margin-right: 0.35rem; font-size: 1.15rem; vertical-align: -0.2rem; }
  .side-item.leaf { padding-left: 1.9rem; }
  .side-bottom { margin-top: auto; }
  .side-status {
    display: flex;
    align-items: center;
    gap: 0.45rem;
    padding: 0.6rem 0.65rem 0;
    color: var(--muted);
    font-size: 0.78rem;
  }
  .logo {
    font-size: 1.35rem;
    font-weight: 700;
    letter-spacing: -0.02em;
    background: linear-gradient(120deg, var(--accent), var(--accent-2));
    -webkit-background-clip: text;
    background-clip: text;
    color: transparent;
  }
  .status {
    width: 10px; height: 10px; border-radius: 50%;
    background: var(--ok);
    box-shadow: 0 0 8px var(--ok);
  }
  .status.offline { background: var(--err); box-shadow: 0 0 8px var(--err); }

  main {
    flex: 1;
    padding: 1.25rem 1.5rem;
    padding-bottom: 5rem;
    width: 100%;
  }
  h1 { font-size: 1.4rem; margin-bottom: 1rem; }
  h2 { font-size: 1rem; color: var(--muted); margin: 1.25rem 0 0.5rem; }
  .muted { color: var(--muted); }

  .empty {
    background: var(--panel);
    border: 1px dashed var(--border);
    border-radius: 12px;
    padding: 1.5rem;
    color: var(--muted);
    font-size: 0.95rem;
  }
  .empty.tall { padding: 2.5rem 1.5rem; text-align: center; }
  .empty p + p { margin-top: 0.5rem; }
  .empty a, a { color: var(--accent-2); }

  .librow { display: grid; grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr)); gap: 0.75rem; }
  .libcard {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.1rem 1.2rem;
    display: flex;
    flex-direction: column;
    gap: 0.3rem;
    text-align: left;
  }
  .libcard span { color: var(--muted); font-size: 0.82rem; }
  .libcard:hover { border-color: var(--accent); }

  .crumbs {
    display: flex;
    flex-wrap: wrap;
    align-items: center;
    gap: 0.35rem;
    margin-bottom: 0.9rem;
    font-size: 0.88rem;
  }
  .crumb { color: var(--accent-2); padding: 0.15rem 0.1rem; }
  .crumb:hover { text-decoration: underline; }
  .crumb-current { color: var(--muted); padding: 0.15rem 0.1rem; }
  .sep { color: var(--muted); user-select: none; }

  .grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(7.5rem, 1fr)); gap: 0.9rem; }
  .cell { display: flex; flex-direction: column; gap: 0.35rem; text-align: left; }
  .poster {
    width: 100%;
    aspect-ratio: 2 / 3;
    object-fit: cover;
    border-radius: 10px;
    border: 1px solid var(--border);
    background: var(--panel);
  }
  .poster.placeholder {
    display: grid;
    place-items: center;
    font-size: 2.2rem;
    font-weight: 700;
    color: var(--muted);
  }
  .poster.square { aspect-ratio: 1 / 1; }

  /* ---- Music view: artist rail + album shelves ---- */
  .music { display: flex; gap: 1.25rem; align-items: flex-start; }
  .mrail {
    flex: none;
    width: 12.5rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.5rem;
    position: sticky;
    top: calc(var(--topbar-h, 3.4rem) + 1rem);
    max-height: calc(100vh - var(--topbar-h, 3.4rem) - 2rem);
    overflow-y: auto;
  }
  .rail-item {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 0.5rem;
    width: 100%;
    text-align: left;
    padding: 0.45rem 0.7rem;
    border-radius: 8px;
    font-size: 0.9rem;
    color: var(--text);
  }
  .rail-item:hover { background: var(--bg); }
  .rail-item.sel { background: color-mix(in srgb, var(--accent) 18%, transparent); color: var(--accent-2); font-weight: 600; }
  .rail-n { color: var(--muted); font-size: 0.75rem; font-weight: 400; }
  .rail-div { height: 1px; background: var(--border); margin: 0.4rem 0.3rem; }

  .mcontent { flex: 1; min-width: 0; }
  .mhead {
    display: flex;
    align-items: flex-end;
    justify-content: space-between;
    gap: 1rem;
    margin-bottom: 1rem;
    flex-wrap: wrap;
  }
  .mtitle { font-size: 1.6rem; margin: 0; }
  .mactions { display: flex; gap: 0.5rem; align-items: center; flex-wrap: wrap; }
  .mfilter {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 999px;
    color: var(--text);
    font: inherit;
    font-size: 0.86rem;
    padding: 0.4rem 0.9rem;
    width: 15rem;
  }
  .mfilter:focus { outline: none; border-color: var(--accent); }

  .msongs {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.5rem;
    margin-bottom: 1rem;
  }
  .msongs-h {
    color: var(--muted);
    font-size: 0.72rem;
    text-transform: uppercase;
    letter-spacing: 0.06em;
    padding: 0.3rem 0.6rem;
  }
  .msong {
    display: flex;
    align-items: baseline;
    gap: 0.8rem;
    width: 100%;
    text-align: left;
    padding: 0.35rem 0.6rem;
    border-radius: 8px;
    font-size: 0.88rem;
  }
  .msong:hover { background: var(--bg); color: var(--accent-2); }
  .msong .tname { flex: none; max-width: 40%; }

  .shelf-h {
    display: block;
    font-size: 1.05rem;
    font-weight: 600;
    color: var(--text);
    padding: 0;
    margin: 1.4rem 0 0.6rem;
  }
  .shelf-h:hover { color: var(--accent-2); }
  .shelf {
    display: grid;
    grid-template-columns: repeat(auto-fill, minmax(8.5rem, 1fr));
    gap: 0.9rem;
    margin-bottom: 0.75rem;
  }
  .acard { display: flex; flex-direction: column; gap: 0.3rem; text-align: left; border-radius: 10px; }
  .acover {
    width: 100%;
    aspect-ratio: 1 / 1;
    object-fit: cover;
    border-radius: 10px;
    border: 1px solid var(--border);
    background: var(--panel);
    transition: transform 0.12s ease;
  }
  .acover.ph { display: grid; place-items: center; font-size: 2rem; color: var(--muted); }
  .acard:hover .acover { transform: scale(1.03); border-color: var(--accent); }
  .acard.sel .acover { outline: 2px solid var(--accent); outline-offset: 2px; }
  .aname { font-size: 0.85rem; line-height: 1.25; overflow: hidden; display: -webkit-box; -webkit-line-clamp: 2; -webkit-box-orient: vertical; }
  .asub { font-size: 0.75rem; color: var(--muted); }

  .apanel {
    display: flex;
    gap: 1.1rem;
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1.1rem;
    margin: 0.25rem 0 1.25rem;
  }
  .apanel-cover {
    width: 11rem;
    height: 11rem;
    border-radius: 10px;
    object-fit: cover;
    flex: none;
    border: 1px solid var(--border);
  }
  .apanel-cover.ph { display: grid; place-items: center; font-size: 3rem; color: var(--muted); background: var(--bg); }
  .apanel-body { flex: 1; min-width: 0; }
  .apanel-head {
    display: flex;
    justify-content: space-between;
    align-items: center;
    gap: 1rem;
    margin-bottom: 0.6rem;
    flex-wrap: wrap;
  }
  .tlist { list-style: none; columns: 2; column-gap: 2rem; }
  .tlist li { break-inside: avoid; }
  .tlist button {
    display: flex;
    align-items: baseline;
    gap: 0.6rem;
    width: 100%;
    text-align: left;
    padding: 0.32rem 0.5rem;
    border-radius: 6px;
    font-size: 0.88rem;
  }
  .tlist button:hover { background: var(--bg); color: var(--accent-2); }
  .tno { color: var(--muted); font-size: 0.78rem; width: 1.3rem; flex: none; font-variant-numeric: tabular-nums; }
  .tname { flex: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .ttm { flex: none; font-size: 0.8rem; font-variant-numeric: tabular-nums; }

  @media (max-width: 767px) {
    .music { flex-direction: column; }
    .mrail {
      position: static;
      width: 100%;
      max-height: none;
      display: flex;
      overflow-x: auto;
      gap: 0.25rem;
      padding: 0.4rem;
    }
    .rail-item { width: auto; white-space: nowrap; }
    .rail-div { display: none; }
    .tlist { columns: 1; }
    .apanel { flex-direction: column; }
    .apanel-cover { width: 8rem; height: 8rem; }
  }
  .cell:hover .poster { border-color: var(--accent); }
  .cell-title { font-size: 0.86rem; line-height: 1.25; }
  .cell-sub { font-size: 0.75rem; color: var(--muted); }

  .detail { display: flex; gap: 1.25rem; align-items: flex-start; }
  .detail .poster { width: 9rem; flex: none; }
  .detail-body { flex: 1; min-width: 0; }
  .meta { color: var(--muted); font-size: 0.88rem; margin: 0.4rem 0 0.8rem; }
  .overview { font-size: 0.95rem; line-height: 1.5; margin-bottom: 1rem; }
  .actions { display: flex; gap: 0.6rem; align-items: center; }
  .play {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    background: var(--accent);
    color: white;
    padding: 0.55rem 1.4rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .dl {
    display: inline-flex;
    align-items: center;
    gap: 0.4rem;
    color: var(--accent-2);
    border: 1px solid var(--border);
    border-radius: 8px;
    padding: 0.55rem 1rem;
    text-decoration: none;
    font-size: 0.9rem;
  }
  .progress {
    height: 4px;
    background: var(--border);
    border-radius: 2px;
    overflow: hidden;
    margin-top: -0.1rem;
  }
  .progress-fill { height: 100%; background: var(--accent); }
  .tech {
    margin-top: 1.1rem;
    display: flex;
    flex-direction: column;
    gap: 0.35rem;
    font-size: 0.88rem;
  }
  .tech div { display: flex; gap: 0.75rem; }
  .tech dt { color: var(--muted); width: 5.5rem; flex: none; }

  .grid.seasons { margin-top: 1.25rem; grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr)); }

  /* ---- Episode list (season page, terminal level) ---- */
  .eplist { list-style: none; margin-top: 1.25rem; }
  .eplist li { border-bottom: 1px solid var(--border); }
  .eplist li:last-child { border-bottom: none; }
  .eprow {
    display: flex;
    align-items: center;
    gap: 0.9rem;
    width: 100%;
    text-align: left;
    padding: 0.5rem 0.4rem;
    border-radius: 10px;
  }
  .eprow:hover { background: var(--panel); }
  .eprow.sel { background: var(--panel); }
  .epthumb {
    width: 7.5rem;
    aspect-ratio: 16 / 9;
    object-fit: cover;
    border-radius: 8px;
    border: 1px solid var(--border);
    flex: none;
  }
  .epthumb.ph { display: grid; place-items: center; background: var(--panel); color: var(--muted); }
  .epmain { flex: 1; min-width: 0; }
  .eppanel {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 1rem 1.2rem;
    margin: 0.35rem 0 0.85rem;
  }
  .eppanel .overview { margin: 0.5rem 0 0.9rem; }
  .eppanel .meta { margin: 0; }

  .children { list-style: none; margin-top: 1.25rem; }
  .children li { border-bottom: 1px solid var(--border); }
  .children li:last-child { border-bottom: none; }
  .children button {
    width: 100%;
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.75rem 0.25rem;
    text-align: left;
  }
  .children button:hover { color: var(--accent-2); }
  .epnum { color: var(--muted); }

  .card {
    background: var(--panel);
    border: 1px solid var(--border);
    border-radius: 12px;
    padding: 0.25rem 1.25rem;
  }
  .row {
    display: flex;
    justify-content: space-between;
    gap: 1rem;
    padding: 0.9rem 0;
    border-bottom: 1px solid var(--border);
  }
  .row:last-child { border-bottom: none; }
  .theme-picker { display: flex; gap: 0.4rem; flex-wrap: wrap; justify-content: flex-end; }
  .chip {
    padding: 0.3rem 0.75rem;
    border-radius: 999px;
    border: 1px solid var(--border);
    color: var(--muted);
    font-size: 0.85rem;
  }
  .chip.sel {
    border-color: var(--accent);
    color: var(--text);
    background: color-mix(in srgb, var(--accent) 18%, transparent);
  }

  .tabbar {
    position: fixed;
    bottom: 0; left: 0; right: 0;
    display: flex;
    background: var(--panel);
    border-top: 1px solid var(--border);
  }
  .tabbar button {
    flex: 1;
    display: flex;
    flex-direction: column;
    align-items: center;
    gap: 0.15rem;
    padding: 0.6rem 0 0.7rem;
    color: var(--muted);
    font-size: 0.75rem;
  }
  .tabbar button .icon { font-size: 1.15rem; }
  .tabbar button.active { color: var(--accent-2); }

  @media (min-width: 768px) {
    main { padding-bottom: 1.25rem; }
  }
</style>
