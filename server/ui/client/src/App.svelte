<script lang="ts">
  import { onMount } from 'svelte'
  import {
    getHealth, listLibraries, libraryItems, itemDetail, itemSubtitles, playItem, continueWatching, downloadURL, searchItems,
    type Health, type Library, type Item, type ItemDetail, type PlayInfo, type ResumeItem, type PlayQuality, type SubtitleTrack,
  } from './lib/api'
  import { applyTheme, currentTheme, listThemes, type ThemeInfo } from './lib/theme'
  import Player from './Player.svelte'
  import MusicPlayer from './MusicPlayer.svelte'

  type Tab = 'home' | 'libraries' | 'settings'
  const tabs: { id: Tab; label: string; icon: string }[] = [
    { id: 'home', label: 'Home', icon: '⌂' },
    { id: 'libraries', label: 'Libraries', icon: '▤' },
    { id: 'settings', label: 'Settings', icon: '⚙' },
  ]

  let tab: Tab = $state('home')
  let health: Health | null = $state(null)
  let offline = $state(false)

  const typeLabels: Record<string, { label: string; icon: string }> = {
    movies: { label: 'Movies', icon: '🎬' },
    tv: { label: 'TV Shows', icon: '📺' },
    music: { label: 'Music', icon: '🎵' },
    audiobooks: { label: 'Audiobooks', icon: '🎧' },
    podcasts: { label: 'Podcasts', icon: '📻' },
    books: { label: 'Books', icon: '📚' },
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
  }

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
    !!playing && !(tab === 'libraries' && detail?.item.id === playing.item.id),
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

  async function startPlay(item: Item, quality: PlayQuality = readQuality()) {
    try {
      const info = await playItem(item.id, quality)
      music = null // video and music are mutually exclusive
      playing = { item, info, quality, gen: ++playGen }
      if (detail?.item.id !== item.id) showItem(item) // full player lives on the item's page
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
      selAlbum = 'ALL'
      gridItems = []
    } else {
      gridItems = items.filter(gridFilter(lib))
    }
  }

  // fromChild: opened via the current detail's children list (drill-down);
  // otherwise the trail is rebuilt by walking the parent chain, so direct
  // jumps (resume, search) still show the full path.
  async function openItem(id: string, fromChild = false) {
    const parent = fromChild && detail ? detail.item : null
    detail = await itemDetail(id)
    if (parent) {
      trail = [...trail, parent]
    } else {
      const ancestors: Item[] = []
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
    if (detail.item.kind === 'movie' || detail.item.kind === 'episode') {
      itemSubtitles(id).then((s) => (detailSubs = s)).catch(() => {})
    }
  }

  const resumable = $derived(
    !!detail?.playState && detail.playState.positionSecs > 10 && !detail.playState.watched,
  )
  const minutesLeft = $derived(
    detail && detail.playState && detail.item.runtimeSecs
      ? Math.max(1, Math.round((detail.item.runtimeSecs - detail.playState.positionSecs) / 60))
      : 0,
  )

  function toLibraries() {
    currentLib = null
    detail = null
    trail = []
    gridItems = []
    tab = 'libraries'
  }

  function toGrid() {
    detail = null
    trail = []
  }

  async function toCrumb(index: number) {
    const target = trail[index]
    detail = await itemDetail(target.id)
    trail = trail.slice(0, index)
  }

  function runtime(secs?: number): string {
    if (!secs) return ''
    const m = Math.round(secs / 60)
    return m >= 60 ? `${Math.floor(m / 60)}h ${m % 60}m` : `${m}m`
  }

  onMount(() => {
    poll()
    listThemes().then((t) => (themes = t)).catch(() => {})
    const id = setInterval(poll, 5000)
    return () => clearInterval(id)
  })
</script>

{#snippet poster(item: Item)}
  {#if item.poster}
    <img class="poster" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'} src={item.poster} alt={item.title} loading="lazy" />
  {:else}
    <div class="poster placeholder" class:square={item.kind === 'album' || item.kind === 'artist' || item.kind === 'track'}><span>{item.kind === 'album' || item.kind === 'track' ? '♪' : item.title.slice(0, 1)}</span></div>
  {/if}
{/snippet}

<div class="shell">
  <div class="topbar">
    <button class="logo" onclick={() => (tab = 'home')}>Cineva</button>
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
    <button class="avatar" title="Local user — accounts arrive with milestone 4">👤</button>
  </div>

  <div class="body">
  <aside class="side">
    <button class="side-item" class:active={tab === 'home'} onclick={() => (tab = 'home')}>⌂ Home</button>
    <div class="tree">
      {#each libraryTree as group (group.type)}
        <div class="type-label"><span class="type-icon">{group.icon}</span> {group.label}</div>
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
    <button class="side-item side-bottom" class:active={tab === 'settings'} onclick={() => (tab = 'settings')}>⚙ Settings</button>
    <div class="side-status">
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
                <button class="play" onclick={() => detail && startMusic(detail.children, 0)}>▶ Play</button>
                <button class="dl" onclick={() => detail && startMusic(detail.children, 0, true)}>🔀 Shuffle</button>
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
                  ▶ {resumable ? 'Resume' : 'Play'}
                </button>
                {#if resumable}<span class="muted">{minutesLeft} min left</span>{/if}
                <a class="dl" href={downloadURL(detail.item.id)} download>↓ Download</a>
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
                      {#if child.kind === 'episode'}<span class="epnum">{child.episodeNum}.</span> {/if}
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
                  <button class="play" onclick={() => startMusic(shelfQueue(), 0)}>▶ Play</button>
                  <button class="dl" onclick={() => startMusic(shelfQueue(), 0, true)}>🔀 Shuffle</button>
                </div>
              </header>

              {#each shelfGroups as group (group.id)}
                {#if selArtist === 'ALL'}
                  <button class="shelf-h" onclick={() => selectArtist(group.id)}>{group.title} <span class="muted">· {group.albums.length} albums</span></button>
                {/if}
                <div class="shelf">
                  {#each group.albums as album (album.id)}
                    <button class="acard" class:sel={selAlbumId === album.id} onclick={() => (selAlbumId = selAlbumId === album.id ? null : album.id)}>
                      {#if album.poster}
                        <img class="acover" src={album.poster} alt={album.title} loading="lazy" />
                      {:else}
                        <span class="acover ph">♪</span>
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
                        <span class="apanel-cover ph">♪</span>
                      {/if}
                      <div class="apanel-body">
                        <div class="apanel-head">
                          <div>
                            <strong>{album.title}</strong>
                            <span class="muted"> — {album.artist}{album.year ? ` · ${album.year}` : ''} · {tracks.length} tracks</span>
                          </div>
                          <div class="mactions">
                            <button class="play" onclick={() => startMusic(tracks, 0)}>▶ Play</button>
                            <button class="dl" onclick={() => startMusic(tracks, 0, true)}>🔀</button>
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
      <button class:active={tab === t.id} onclick={() => (tab = t.id)}>
        <span class="icon">{t.icon}</span>
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
  .avatar {
    width: 2.1rem;
    height: 2.1rem;
    border-radius: 50%;
    background: var(--bg);
    border: 1px solid var(--border);
    display: grid;
    place-items: center;
    font-size: 1rem;
    flex: none;
  }
  .avatar:hover { border-color: var(--accent); }

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
    padding: 1.25rem;
    padding-bottom: 5rem;
    max-width: 60rem;
    width: 100%;
    margin: 0 auto;
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
  .mactions { display: flex; gap: 0.5rem; align-items: center; }

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
    background: var(--accent);
    color: white;
    padding: 0.55rem 1.4rem;
    border-radius: 8px;
    font-weight: 600;
  }
  .dl {
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
