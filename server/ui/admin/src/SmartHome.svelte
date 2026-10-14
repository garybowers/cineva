<script lang="ts">
  // The Smart home page: which Home Assistant entities Cineva may see and
  // who may use them (Entities), which room each device is in (Rooms), and
  // the modes people run (Modes). Nothing reaches the client until it is
  // exposed here. See docs/homeassistant.md.
  import { onMount } from 'svelte'
  import {
    listHAEntities, putHAEntity, haRooms, listHADevices, putHADeviceArea,
    listHAModes, createHAMode, updateHAMode, deleteHAMode, searchItems, getItem,
    type HAConnection, type HADevice, type HADomain, type HAEntity, type HAEntityAdmin,
    type HAMode, type HAModeRequest, type HAModeStep, type Item, type LibraryAccess, type User, type UserGroup,
  } from './lib/api'
  import Modal from './Modal.svelte'
  import AccessPicker from './AccessPicker.svelte'
  import {
    Lightbulb, ToggleRight, ToggleLeft, Fan, Blinds, Speaker, Sparkles, ScrollText, Cctv,
    Plus, Pencil, Trash, Users, Lock, ChevronUp, ChevronDown, X, Zap, Timer, Play, Copy, Check, TriangleAlert,
  } from '@lucide/svelte'

  let { connection, users, groups }: { connection: HAConnection; users: User[]; groups: UserGroup[] } = $props()

  type Tab = 'entities' | 'rooms' | 'modes'
  let tab: Tab = $state('entities')
  let entities: HAEntityAdmin[] = $state([])
  let areas: { id: string; name: string }[] = $state([])
  let devices: HADevice[] = $state([])
  let modes: HAMode[] = $state([])
  let loaded = $state(false)
  let err = $state('')
  const errText = (e: unknown) => (e instanceof Error ? e.message : String(e))

  const loadEntities = () => listHAEntities().then((e) => (entities = e)).catch((e) => (err = errText(e)))
  const loadAreas = () => haRooms().then((r) => (areas = r.allAreas ?? [])).catch(() => {})
  const loadDevices = () => listHADevices().then((d) => (devices = d)).catch((e) => (err = errText(e)))
  const loadModes = () => listHAModes().then((m) => (modes = m)).catch((e) => (err = errText(e)))
  async function load() {
    await Promise.all([loadEntities(), loadAreas(), loadDevices(), loadModes()])
    loaded = true
  }
  onMount(load)

  /**
   * From the event stream. Changed states patch their rows in place, keeping
   * exposure and access, which a state event does not carry; no data says
   * the connection, exposure, access, rooms or modes changed, so everything
   * is fetched again.
   */
  export function onEvent(data: unknown) {
    const changed = (data as { entities?: HAEntity[] } | null)?.entities
    if (Array.isArray(changed)) {
      const byId = new Map(changed.map((e) => [e.entityId, e]))
      entities = entities.map((e) => {
        const c = byId.get(e.entityId)
        return c ? { ...c, exposed: e.exposed, access: e.access } : e
      })
      return
    }
    load()
  }

  // ---- Names ----
  // Entities with no area, or one Home Assistant no longer lists, come
  // under "Elsewhere", as the server's rooms do.
  const areaName = (id: string) => (id ? areas.find((a) => a.id === id)?.name ?? 'Elsewhere' : 'Elsewhere')
  const areaKey = (id: string) => (id && areas.some((a) => a.id === id) ? id : '')
  const userName = (id: string) => {
    const u = users.find((x) => x.id === id)
    return u ? u.displayName || u.username : id.slice(0, 8)
  }
  const groupName = (id: string) => groups.find((g) => g.id === id)?.name ?? id.slice(0, 8)
  const entityName = (id?: string) => entities.find((e) => e.entityId === id)?.name ?? id ?? ''
  // Several devices often share a browser's name ("Chrome · Linux"), so the
  // choice also says where each is and whether Cineva is open on it now.
  const deviceOption = (d: HADevice) =>
    [`${d.name} (${d.userName})`, d.areaId ? areaName(d.areaId) : '', d.online ? 'open now' : ''].filter(Boolean).join(' · ')
  const deviceLabel = (id?: string) => {
    const d = devices.find((x) => x.id === id)
    return d ? `${d.name} (${d.userName})` : 'a device no longer signed in'
  }
  const open = (): LibraryAccess => ({ restricted: false, userIds: [], groupIds: [] })
  function accessSummary(a?: LibraryAccess): string {
    if (!a || !a.restricted) return 'Everyone'
    const names = [...a.groupIds.map(groupName), ...a.userIds.map(userName)]
    if (names.length === 0) return 'Admins only'
    return names.length <= 2 ? names.join(', ') : `${names.slice(0, 2).join(', ')} and ${names.length - 2} more`
  }

  // ---- Domains ----
  // What each supported domain is called here, and the services a mode may
  // run on it: the server's whitelist, which refuses anything else.
  type Service = { id: string; label: string }
  const onOff: Service[] = [{ id: 'turn_on', label: 'Turn on' }, { id: 'turn_off', label: 'Turn off' }, { id: 'toggle', label: 'Toggle' }]
  const domains: Record<HADomain, { label: string; icon: typeof Lightbulb; services: Service[] }> = {
    light: { label: 'Lights', icon: Lightbulb, services: onOff },
    switch: { label: 'Switches', icon: ToggleRight, services: onOff },
    fan: { label: 'Fans', icon: Fan, services: onOff },
    input_boolean: { label: 'Toggles', icon: ToggleLeft, services: onOff },
    cover: {
      label: 'Blinds and covers', icon: Blinds,
      services: [{ id: 'open_cover', label: 'Open' }, { id: 'close_cover', label: 'Close' }, { id: 'stop_cover', label: 'Stop' }],
    },
    media_player: {
      label: 'Media players', icon: Speaker,
      services: [
        { id: 'turn_on', label: 'Turn on' }, { id: 'turn_off', label: 'Turn off' },
        { id: 'volume_set', label: 'Set the volume' }, { id: 'volume_mute', label: 'Mute or unmute' },
        { id: 'select_source', label: 'Choose the input' }, { id: 'media_play_pause', label: 'Play or pause' },
      ],
    },
    scene: { label: 'Scenes', icon: Sparkles, services: [{ id: 'turn_on', label: 'Activate' }] },
    script: { label: 'Scripts', icon: ScrollText, services: [{ id: 'turn_on', label: 'Run' }] },
    camera: { label: 'Cameras', icon: Cctv, services: [] },
  }
  const domainOrder = Object.keys(domains) as HADomain[]
  const domainOf = (entityId: string) => entityId.split('.')[0] as HADomain
  const servicesFor = (entityId: string) => domains[domainOf(entityId)]?.services ?? []
  const serviceLabel = (entityId: string, service: string) =>
    servicesFor(entityId).find((s) => s.id === service)?.label ?? service

  const cap = (s: string) => (s ? s[0].toUpperCase() + s.slice(1).replaceAll('_', ' ') : '')
  function stateText(e: HAEntity): string {
    if (e.state === 'missing') return 'No longer in Home Assistant'
    if (!e.available) return 'Unavailable'
    switch (e.domain) {
      case 'light':
        return e.on ? (e.dimmable && e.brightnessPct != null ? `On · ${e.brightnessPct}%` : 'On') : 'Off'
      case 'cover':
        return e.position != null ? `${cap(e.state)} · ${e.position}%` : cap(e.state)
      case 'media_player': {
        const parts = [cap(e.state)]
        if (e.source) parts.push(e.source)
        if (e.muted) parts.push('muted')
        else if (e.volume != null) parts.push(`volume ${Math.round(e.volume * 100)}%`)
        return parts.join(' · ')
      }
      case 'camera':
        return e.stream ? 'Live view' : 'Snapshots only'
      // A scene's state is when it last ran, which says nothing useful here.
      case 'scene':
        return 'Scene'
      case 'script':
        return e.state === 'on' ? 'Running' : 'Ready'
      default:
        return e.on != null ? (e.on ? 'On' : 'Off') : cap(e.state)
    }
  }

  // ---- Entities ----
  let query = $state('')
  let domainFilter: HADomain | '' = $state('')
  let exposedOnly = $state(false)
  const exposedCount = $derived(entities.filter((e) => e.exposed).length)
  const domainCounts = $derived(
    domainOrder
      .map((d) => ({ id: d, n: entities.filter((e) => e.domain === d).length }))
      .filter((d) => d.n > 0),
  )
  const shown = $derived.by(() => {
    const q = query.trim().toLowerCase()
    return entities.filter((e) =>
      (!domainFilter || e.domain === domainFilter) &&
      (!exposedOnly || e.exposed) &&
      (!q || e.name.toLowerCase().includes(q) || e.entityId.toLowerCase().includes(q) || areaName(e.areaId).toLowerCase().includes(q)),
    )
  })
  // By area name, Elsewhere last.
  const grouped = $derived.by(() => {
    const by = new Map<string, HAEntityAdmin[]>()
    for (const e of shown) by.set(areaKey(e.areaId), [...(by.get(areaKey(e.areaId)) ?? []), e])
    return [...by]
      .map(([id, list]) => ({ id, name: areaName(id), list }))
      .sort((a, b) => (a.id === '' ? 1 : b.id === '' ? -1 : a.name.localeCompare(b.name)))
  })

  let busyEntities: string[] = $state([])
  const replaceEntity = (saved: HAEntityAdmin) => {
    entities = entities.map((x) => (x.entityId === saved.entityId ? saved : x))
  }
  // Shown at once and put back if refused. No access is sent: the server
  // keeps what an exposed entity had, and restricts a camera exposed for
  // the first time to admins.
  async function setExposed(e: HAEntityAdmin, exposed: boolean) {
    busyEntities = [...busyEntities, e.entityId]
    replaceEntity({ ...e, exposed })
    try {
      replaceEntity(await putHAEntity(e.entityId, { exposed }))
      err = ''
    } catch (x) {
      replaceEntity(e)
      err = `${e.name}: ${errText(x)}`
    } finally {
      busyEntities = busyEntities.filter((id) => id !== e.entityId)
    }
  }

  let accessFor: HAEntityAdmin | null = $state(null)
  let accessDraft: LibraryAccess = $state(open())
  let accessErr = $state('')
  let accessBusy = $state(false)
  function openAccess(e: HAEntityAdmin) {
    const a = e.access ?? open()
    accessDraft = { restricted: a.restricted, userIds: [...a.userIds], groupIds: [...a.groupIds] }
    accessErr = ''
    accessFor = e
  }
  async function saveAccess(ev: Event) {
    ev.preventDefault()
    if (!accessFor) return
    accessBusy = true
    accessErr = ''
    try {
      replaceEntity(await putHAEntity(accessFor.entityId, { exposed: accessFor.exposed, access: accessDraft }))
      accessFor = null
    } catch (x) {
      accessErr = errText(x)
    } finally {
      accessBusy = false
    }
  }

  // ---- Rooms ----
  const byUser = $derived.by(() => {
    const out: { userId: string; userName: string; list: HADevice[] }[] = []
    for (const d of devices) {
      let g = out.find((x) => x.userId === d.userId)
      // A device keeps its room when nobody is signed in on it.
      if (!g) out.push((g = { userId: d.userId, userName: d.userId ? d.userName : 'Nobody signed in', list: [] }))
      g.list.push(d)
    }
    return out
  })
  async function setRoom(d: HADevice, areaId: string) {
    devices = devices.map((x) => (x.id === d.id ? { ...x, areaId } : x))
    try {
      await putHADeviceArea(d.id, areaId)
      err = ''
    } catch (x) {
      devices = devices.map((y) => (y.id === d.id ? { ...y, areaId: d.areaId } : y))
      err = `${d.name}: ${errText(x)}`
    }
  }

  // The automation example names a room that is really in use, when there is one.
  const exampleArea = $derived(devices.find((d) => d.areaId)?.areaId || areas[0]?.id || 'living_room')
  const exampleName = $derived(areas.find((a) => a.id === exampleArea)?.name ?? 'the living room')
  const automation = $derived(`alias: Lights up when Cineva pauses in ${exampleName}
triggers:
  - trigger: event
    event_type: cineva_playback_paused
    event_data:
      area_id: ${exampleArea}
actions:
  - action: light.turn_on
    target:
      area_id: ${exampleArea}
    data:
      brightness_pct: 40
mode: single`)
  let copied = $state(false)
  async function copyAutomation() {
    try {
      await navigator.clipboard.writeText(automation)
      copied = true
      setTimeout(() => (copied = false), 2000)
    } catch {
      /* no clipboard (an insecure origin): the text can still be selected */
    }
  }

  // ---- Modes ----
  const MAX_STEPS = 20
  const MAX_WAIT = 60
  const pad = (n?: number) => String(n ?? 0).padStart(2, '0')
  const itemLabel = (it: Item) =>
    it.kind === 'episode' ? `${it.title} (S${pad(it.seasonNum)}E${pad(it.episodeNum)})` : it.year ? `${it.title} (${it.year})` : it.title
  const kindLabel: Record<string, string> = { movie: 'Film', series: 'Series', season: 'Season', episode: 'Episode', album: 'Album', track: 'Track', artist: 'Artist' }

  // A fixed title's name, fetched once per id for the summaries.
  let titles: Record<string, string> = $state({})
  const asked = new Set<string>()
  $effect(() => {
    for (const m of modes) {
      for (const s of m.steps) {
        const id = s.type === 'play' ? s.itemId : ''
        if (!id || asked.has(id)) continue
        asked.add(id)
        getItem(id).then((r) => (titles = { ...titles, [id]: itemLabel(r.item) })).catch(() => {})
      }
    }
  })

  function stepSummary(s: HAModeStep): string {
    if (s.type === 'wait') return `Wait ${s.seconds ?? 0} s`
    if (s.type === 'play') {
      const what = s.itemId ? titles[s.itemId] ?? 'a chosen title' : 'the title it is started from'
      return `Play ${what} on ${deviceLabel(s.deviceId)}`
    }
    const id = s.entityId ?? ''
    let what = serviceLabel(id, s.service ?? '').toLowerCase()
    if (s.service === 'turn_on' && s.brightnessPct != null) what += ` at ${s.brightnessPct}%`
    if (s.service === 'volume_set' && s.volume != null) what = `volume to ${Math.round(s.volume * 100)}%`
    if (s.service === 'volume_mute') what = s.muted ? 'mute' : 'unmute'
    if (s.service === 'select_source' && s.source) what = `input to ${s.source}`
    return `${entityName(id)}: ${what}`
  }

  // A step as the editor holds it: every field of every kind, so switching
  // service or title keeps what was typed; only the relevant ones are sent.
  type Draft = {
    key: number
    type: HAModeStep['type']
    entityId: string
    service: string
    brightnessPct: number | null
    volumePct: number
    muted: boolean
    source: string
    seconds: number
    deviceId: string
    titleFrom: 'started' | 'fixed'
    itemId: string
    itemName: string
    q: string
  }
  let nextKey = 0
  function draft(s: HAModeStep): Draft {
    return {
      key: nextKey++,
      type: s.type,
      entityId: s.entityId ?? '',
      service: s.service ?? '',
      brightnessPct: s.brightnessPct ?? null,
      volumePct: s.volume != null ? Math.round(s.volume * 100) : 30,
      muted: s.muted ?? true,
      source: s.source ?? '',
      seconds: s.seconds ?? 5,
      deviceId: s.deviceId ?? '',
      titleFrom: s.itemId ? 'fixed' : 'started',
      itemId: s.itemId ?? '',
      itemName: s.itemId ? titles[s.itemId] ?? '' : '',
      q: '',
    }
  }
  function step(d: Draft): HAModeStep {
    if (d.type === 'wait') return { type: 'wait', seconds: d.seconds }
    if (d.type === 'play') return { type: 'play', deviceId: d.deviceId, ...(d.titleFrom === 'fixed' ? { itemId: d.itemId } : {}) }
    const s: HAModeStep = { type: 'action', entityId: d.entityId, service: d.service }
    if (domainOf(d.entityId) === 'light' && d.service === 'turn_on' && d.brightnessPct != null) s.brightnessPct = d.brightnessPct
    if (d.service === 'volume_set') s.volume = d.volumePct / 100
    if (d.service === 'volume_mute') s.muted = d.muted
    if (d.service === 'select_source') s.source = d.source
    return s
  }

  // Entities a mode can work: every supported one bar cameras, exposed or
  // not, since building a mode is the admin's decision.
  const actionEntities = $derived(entities.filter((e) => e.state !== 'missing' && servicesFor(e.entityId).length > 0))
  const actionGroups = $derived(
    domainOrder
      .map((d) => ({ id: d, list: actionEntities.filter((e) => e.domain === d) }))
      .filter((g) => g.list.length > 0),
  )
  const entitySources = (id: string) => entities.find((e) => e.entityId === id)?.sources ?? []

  let editingMode: { id?: string; name: string; steps: Draft[]; access: LibraryAccess } | null = $state(null)
  let modeErr = $state('')
  let modeBusy = $state(false)
  let deletingMode: HAMode | null = $state(null)
  // The server allows one play step: a mode plays one thing.
  const hasPlay = $derived(editingMode ? editingMode.steps.some((d) => d.type === 'play') : false)
  const totalWait = $derived(editingMode ? editingMode.steps.reduce((n, d) => n + (d.type === 'wait' ? Number(d.seconds) || 0 : 0), 0) : 0)

  function newMode() {
    editingMode = { name: '', steps: [], access: open() }
    modeErr = ''
  }
  function editMode(m: HAMode) {
    const a = m.access ?? open()
    editingMode = { id: m.id, name: m.name, steps: m.steps.map(draft), access: { restricted: a.restricted, userIds: [...a.userIds], groupIds: [...a.groupIds] } }
    modeErr = ''
  }
  function addStep(type: HAModeStep['type']) {
    if (!editingMode || editingMode.steps.length >= MAX_STEPS) return
    editingMode.steps = [...editingMode.steps, draft({ type })]
  }
  function moveStep(i: number, delta: number) {
    if (!editingMode) return
    const next = [...editingMode.steps]
    const to = i + delta
    if (to < 0 || to >= next.length) return
    ;[next[i], next[to]] = [next[to], next[i]]
    editingMode.steps = next
  }
  function removeStep(i: number) {
    if (editingMode) editingMode.steps = editingMode.steps.filter((_, x) => x !== i)
  }
  // A new entity may be in a domain without the chosen service; fall back to its first.
  function setEntity(d: Draft, id: string) {
    d.entityId = id
    const services = servicesFor(id)
    if (!services.some((s) => s.id === d.service)) d.service = services[0]?.id ?? ''
    const sources = entitySources(id)
    if (sources.length > 0 && !sources.includes(d.source)) d.source = sources[0]
  }

  // Search for a fixed title, per step, once the typist pauses.
  let results: Record<number, Item[]> = $state({})
  let searchTimer: ReturnType<typeof setTimeout> | undefined
  function onSearch(d: Draft) {
    clearTimeout(searchTimer)
    const q = d.q.trim()
    if (q.length < 2) {
      results = { ...results, [d.key]: [] }
      return
    }
    searchTimer = setTimeout(async () => {
      try {
        const r = await searchItems(q)
        if (d.q.trim() === q) results = { ...results, [d.key]: r }
      } catch (x) {
        modeErr = errText(x)
      }
    }, 250)
  }
  function chooseTitle(d: Draft, it: Item) {
    d.itemId = it.id
    d.itemName = itemLabel(it)
    d.q = ''
    results = { ...results, [d.key]: [] }
  }

  // The spec's limits, checked here so the admin hears about them before
  // the round-trip; the server checks them again.
  function problem(m: { name: string; steps: Draft[] }): string {
    if (!m.name.trim()) return 'Give the mode a name.'
    if (m.steps.length === 0) return 'Add at least one step.'
    if (m.steps.length > MAX_STEPS) return `A mode has at most ${MAX_STEPS} steps.`
    for (const [i, d] of m.steps.entries()) {
      const n = `Step ${i + 1}`
      if (d.type === 'action') {
        if (!d.entityId) return `${n}: choose what it works.`
        if (!servicesFor(d.entityId).some((s) => s.id === d.service)) return `${n}: choose what it does.`
        if (domainOf(d.entityId) === 'light' && d.service === 'turn_on' && d.brightnessPct != null &&
          (!Number.isInteger(d.brightnessPct) || d.brightnessPct < 0 || d.brightnessPct > 100)) return `${n}: brightness is 0 to 100%.`
        if (d.service === 'volume_set' && (d.volumePct < 0 || d.volumePct > 100)) return `${n}: the volume is 0 to 100%.`
        if (d.service === 'select_source' && !d.source.trim()) return `${n}: choose the input.`
      } else if (d.type === 'wait') {
        if (!Number.isInteger(d.seconds) || d.seconds < 1 || d.seconds > 30) return `${n}: a wait is 1 to 30 seconds.`
      } else {
        if (!d.deviceId) return `${n}: choose the device it plays on.`
        if (d.titleFrom === 'fixed' && !d.itemId) return `${n}: choose the title it plays.`
      }
    }
    const wait = m.steps.reduce((t, d) => t + (d.type === 'wait' ? d.seconds : 0), 0)
    if (wait > MAX_WAIT) return `The waits add up to ${wait} seconds; a mode may wait ${MAX_WAIT} seconds at most.`
    return ''
  }
  // A duplicate name is a 409; should it come without a message of its own,
  // say what it means rather than the bare status.
  const modeErrText = (x: unknown) => {
    const t = errText(x)
    return t.startsWith('409') ? 'Another mode already has this name.' : t
  }
  async function saveMode(e: Event) {
    e.preventDefault()
    if (!editingMode) return
    modeErr = problem(editingMode)
    if (modeErr) return
    modeBusy = true
    const body: HAModeRequest = {
      name: editingMode.name.trim(),
      steps: editingMode.steps.map(step),
      access: $state.snapshot(editingMode.access),
    }
    try {
      if (editingMode.id) await updateHAMode(editingMode.id, body)
      else await createHAMode(body)
      editingMode = null
      loadModes()
    } catch (x) {
      modeErr = modeErrText(x)
    } finally {
      modeBusy = false
    }
  }
  async function removeMode(m: HAMode) {
    modeBusy = true
    try {
      await deleteHAMode(m.id)
      modes = modes.filter((x) => x.id !== m.id)
      deletingMode = null
    } catch (x) {
      modeErr = errText(x)
    } finally {
      modeBusy = false
    }
  }
</script>

<h1>Smart home</h1>
<p class="hint intro">
  What Cineva may see of {connection.name || 'Home Assistant'}, who may use it, which room each device is in, and the
  modes people can run. Nothing is shown to anyone until it is exposed here.
</p>
{#if !connection.connected}
  <div class="error">
    <TriangleAlert size={16} />
    <span>
      {connection.name || 'Home Assistant'} is unreachable{#if connection.error}: {connection.error}{/if}. Its
      entities and areas cannot be listed until it is back; exposure, access, rooms and modes are kept.
    </span>
  </div>
{/if}
{#if err}<div class="error"><span>{err}</span><button class="dismiss" aria-label="Dismiss" onclick={() => (err = '')}><X size={15} /></button></div>{/if}

<nav class="lib-tabs" aria-label="Smart home">
  {#each [['entities', 'Entities'], ['rooms', 'Rooms'], ['modes', 'Modes']] as [id, label] (id)}
    <button class="lib-tab" class:on={tab === id} aria-current={tab === id ? 'page' : undefined} onclick={() => (tab = id as Tab)}>
      {label}{#if id === 'entities' && entities.length > 0}<span class="count">{exposedCount}/{entities.length}</span>{/if}{#if id === 'modes' && modes.length > 0}<span class="count">{modes.length}</span>{/if}
    </button>
  {/each}
</nav>

{#if tab === 'entities'}
  <div class="toolbar">
    <input class="filter" type="search" placeholder="Filter by name, entity id or area" bind:value={query} aria-label="Filter entities" />
    <label class="chk"><input type="checkbox" bind:checked={exposedOnly} /> Exposed only</label>
  </div>
  {#if domainCounts.length > 1}
    <div class="chips" role="group" aria-label="Domain">
      <button class="chip" class:sel={domainFilter === ''} onclick={() => (domainFilter = '')}>All <span class="n">{entities.length}</span></button>
      {#each domainCounts as d (d.id)}
        {@const Icon = domains[d.id].icon}
        <button class="chip ibtn" class:sel={domainFilter === d.id} onclick={() => (domainFilter = domainFilter === d.id ? '' : d.id)}>
          <Icon size={14} /> {domains[d.id].label} <span class="n">{d.n}</span>
        </button>
      {/each}
    </div>
  {/if}
  <p class="hint">
    Exposed entities follow the library access model: open to everyone, or only to chosen people and groups.
    A camera starts with admins only when it is first exposed; grant it to people deliberately.
  </p>

  {#if !loaded}
    <div class="empty">Loading…</div>
  {:else if entities.length === 0}
    <div class="empty">{connection.connected ? 'Home Assistant has no entities in the domains Cineva works with: lights, switches, fans, toggles, blinds and covers, media players, scenes, scripts and cameras.' : 'Nothing to list while Home Assistant is unreachable.'}</div>
  {:else if shown.length === 0}
    <div class="empty">Nothing matches the filter.</div>
  {:else}
    {#each grouped as g (g.id)}
      <h2>{g.name} <span class="muted small">{g.list.length}</span></h2>
      <ul class="rows">
        {#each g.list as e (e.entityId)}
          {@const Icon = domains[e.domain]?.icon ?? Zap}
          {@const busy = busyEntities.includes(e.entityId)}
          <li class="erow" class:hidden={!e.exposed}>
            <span class="eicon" title={domains[e.domain]?.label}><Icon size={18} /></span>
            <span class="etext">
              <span class="ename">{e.name}</span>
              <span class="muted small eid">{e.entityId}</span>
            </span>
            <span class="estate small" class:muted={!e.available} class:gone={e.state === 'missing'}>{stateText(e)}</span>
            <span class="ectl">
              <label class="switch">
                <input type="checkbox" role="switch" checked={e.exposed} disabled={busy}
                  onchange={(ev) => setExposed(e, ev.currentTarget.checked)} />
                <span class="track" aria-hidden="true"></span>
                Exposed
              </label>
              <button class="lib-btn ibtn access" disabled={!e.exposed} onclick={() => openAccess(e)}
                title={e.exposed ? 'Who can use it' : 'Expose it first'}>
                {#if e.access?.restricted}<Lock size={14} />{:else}<Users size={14} />{/if}
                {e.exposed ? accessSummary(e.access) : 'Hidden'}
              </button>
            </span>
          </li>
        {/each}
      </ul>
    {/each}
  {/if}
{:else if tab === 'rooms'}
  <p class="hint intro">
    A device in a room shows that room's controls and modes in the player. People set their own device's room
    in the client's settings; any device's can be set here. Areas are Home Assistant's own.
  </p>
  {#if !loaded}
    <div class="empty">Loading…</div>
  {:else if devices.length === 0}
    <div class="empty">No devices yet. One appears when someone signs in on it.</div>
  {:else}
    {#if areas.length === 0}
      <p class="hint warnline">No areas to choose from{connection.connected ? ': Home Assistant has none set up.' : ' while Home Assistant is unreachable.'}</p>
    {/if}
    {#each byUser as u (u.userId)}
      <h2>{u.userName}</h2>
      <ul class="rows">
        {#each u.list as d (d.id)}
          <li class="drow">
            <span class="dot" class:on={d.online} aria-hidden="true"></span>
            <span class="etext">
              <span class="ename">{d.name}</span>
              <span class="muted small">{d.online ? 'Cineva open now' : 'Not open'}</span>
            </span>
            <label class="room">
              <span class="muted small">Room</span>
              <select value={d.areaId} disabled={areas.length === 0 && !d.areaId}
                onchange={(ev) => setRoom(d, ev.currentTarget.value)} aria-label="Room for {d.name}">
                <option value="">No room</option>
                {#if d.areaId && !areas.some((a) => a.id === d.areaId)}<option value={d.areaId}>{d.areaId}</option>{/if}
                {#each areas as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
              </select>
            </label>
          </li>
        {/each}
      </ul>
    {/each}
  {/if}

  <details class="panel events">
    <summary>Playback events in Home Assistant</summary>
    <p class="hint">
      {#if connection.playbackEvents}
        On: Cineva fires <code>cineva_playback_started</code>, <code>_paused</code>, <code>_resumed</code> and
        <code>_stopped</code> on Home Assistant's event bus.
      {:else}
        Off. Turn them on by editing the connection on the Integrations page{#if !connection.isAdmin}; Cineva's
        Home Assistant user must be an administrator there first{/if}.
      {/if}
      Each carries <code>user</code>, <code>device</code>, <code>device_id</code>, <code>area_id</code> (the device's
      room), <code>item_id</code>, <code>title</code>, <code>subtitle</code>, <code>kind</code>,
      <code>position_secs</code> and <code>duration_secs</code>, so a room's automations can follow what plays in it.
    </p>
    <p class="hint">This automation raises the lights in {exampleName} when something is paused there. Paste it into a new automation's YAML editor in Home Assistant.</p>
    <div class="yaml">
      <pre>{automation}</pre>
      <button class="lib-btn ibtn copy" onclick={copyAutomation}>
        {#if copied}<Check size={14} /> Copied{:else}<Copy size={14} /> Copy{/if}
      </button>
    </div>
  </details>
{:else}
  <div class="titlerow">
    <p class="hint">
      A mode runs its steps in order: Home Assistant actions, short waits, and playing something in Cineva. People
      run them from the Rooms page, the player's room panel or a title's page.
    </p>
    <button class="primary ibtn" onclick={newMode}><Plus size={16} /> New mode</button>
  </div>
  {#if !loaded}
    <div class="empty">Loading…</div>
  {:else if modes.length === 0}
    <div class="empty">No modes yet. A "Movie mode" might run a scene, wait for the amplifier, and play the film it was started from.</div>
  {:else}
    <ul class="rows">
      {#each modes as m (m.id)}
        <li class="mrow">
          <span class="etext">
            <span class="ename">{m.name}</span>
            <span class="muted small msum">{m.steps.map(stepSummary).join(' → ')}</span>
            <span class="mtags">
              <span class="tag ibtn">{#if m.access?.restricted}<Lock size={12} />{:else}<Users size={12} />{/if} {accessSummary(m.access)}</span>
              {#if m.areaId}<span class="tag">Plays in {areaName(m.areaId)}</span>{/if}
              {#if m.needsTitle}<span class="tag">Needs a title</span>{/if}
            </span>
          </span>
          <span class="mctl">
            <button class="lib-btn ibtn" onclick={() => editMode(m)}><Pencil size={15} /> Edit</button>
            <button class="lib-btn ibtn danger" onclick={() => { modeErr = ''; deletingMode = m }}><Trash size={15} /> Delete</button>
          </span>
        </li>
      {/each}
    </ul>
  {/if}
{/if}

{#if accessFor}
  {@const ent = accessFor}
  <Modal title="Who can use {ent.name}" onclose={() => (accessFor = null)}>
    <form id="ha-access-form" class="form" onsubmit={saveAccess}>
      <AccessPicker bind:access={accessDraft} {users} {groups} noun={ent.domain === 'camera' ? 'camera' : 'control'} />
      <p class="hint">
        {#if ent.domain === 'camera'}
          Whoever is listed can see this camera's picture and watch it live.
        {:else}
          Whoever is listed sees it in their room and can work it. Modes that use it run for anyone allowed the mode.
        {/if}
        Admins always see everything.
      </p>
      {#if accessErr}<div class="error slim">{accessErr}</div>{/if}
    </form>
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (accessFor = null)}>Cancel</button>
      <button class="primary" type="submit" form="ha-access-form" disabled={accessBusy}>{accessBusy ? 'Saving…' : 'Save'}</button>
    {/snippet}
  </Modal>
{/if}

{#if editingMode}
  <Modal title={editingMode.id ? `Edit ${editingMode.name || 'mode'}` : 'New mode'} onclose={() => (editingMode = null)}>
    <form id="ha-mode-form" class="form" onsubmit={saveMode} novalidate>
      <label>Name
        <input bind:value={editingMode.name} required maxlength="60" placeholder="Movie mode" />
      </label>

      <div class="field">
        <span>Steps</span>
        {#if editingMode.steps.length === 0}<p class="hint">No steps yet. Add them below; they run in order.</p>{/if}
        <ol class="steps">
          {#each editingMode.steps as d, i (d.key)}
            {@const sources = entitySources(d.entityId)}
            <li class="stepbox">
              <div class="stephead">
                <span class="stepname">
                  {#if d.type === 'action'}<Zap size={15} /> Action{:else if d.type === 'wait'}<Timer size={15} /> Wait{:else}<Play size={15} /> Play{/if}
                  <span class="muted small">Step {i + 1}</span>
                </span>
                <span class="stepbtns">
                  <button type="button" class="lib-btn iconly" aria-label="Move step {i + 1} up" disabled={i === 0} onclick={() => moveStep(i, -1)}><ChevronUp size={15} /></button>
                  <button type="button" class="lib-btn iconly" aria-label="Move step {i + 1} down" disabled={i === editingMode.steps.length - 1} onclick={() => moveStep(i, 1)}><ChevronDown size={15} /></button>
                  <button type="button" class="lib-btn iconly danger" aria-label="Remove step {i + 1}" onclick={() => removeStep(i)}><X size={15} /></button>
                </span>
              </div>

              {#if d.type === 'action'}
                <label>Entity
                  <select value={d.entityId} onchange={(ev) => setEntity(d, ev.currentTarget.value)}>
                    <option value="" disabled>Choose…</option>
                    {#if d.entityId && !actionEntities.some((e) => e.entityId === d.entityId)}
                      <option value={d.entityId}>{d.entityId} (not in Home Assistant now)</option>
                    {/if}
                    {#each actionGroups as g (g.id)}
                      <optgroup label={domains[g.id].label}>
                        {#each g.list as e (e.entityId)}<option value={e.entityId}>{e.name} · {areaName(e.areaId)}{e.exposed ? '' : ' (not exposed)'}</option>{/each}
                      </optgroup>
                    {/each}
                  </select>
                </label>
                {#if d.entityId}
                  <label>Does
                    <select bind:value={d.service}>
                      {#each servicesFor(d.entityId) as s (s.id)}<option value={s.id}>{s.label}</option>{/each}
                    </select>
                  </label>
                  {#if domainOf(d.entityId) === 'light' && d.service === 'turn_on'}
                    <label>Brightness (%)
                      <input type="number" min="0" max="100" step="1" bind:value={d.brightnessPct} placeholder="As it was" />
                    </label>
                  {:else if d.service === 'volume_set'}
                    <label>Volume: {d.volumePct}%
                      <input type="range" min="0" max="100" step="1" bind:value={d.volumePct} />
                    </label>
                  {:else if d.service === 'volume_mute'}
                    <label>Sound
                      <select bind:value={d.muted}>
                        <option value={true}>Mute</option>
                        <option value={false}>Unmute</option>
                      </select>
                    </label>
                  {:else if d.service === 'select_source'}
                    <label>Input
                      {#if sources.length > 0}
                        <select bind:value={d.source}>
                          {#if d.source && !sources.includes(d.source)}<option value={d.source}>{d.source}</option>{/if}
                          {#each sources as src (src)}<option value={src}>{src}</option>{/each}
                        </select>
                      {:else}
                        <input bind:value={d.source} placeholder="HDMI 1" />
                      {/if}
                    </label>
                  {/if}
                {/if}
              {:else if d.type === 'wait'}
                <label>Seconds
                  <input type="number" min="1" max="30" step="1" bind:value={d.seconds} />
                </label>
                <p class="hint">For an amplifier or projector that has to wake before it takes an input. 1 to 30 seconds.</p>
              {:else}
                <label>On
                  <select bind:value={d.deviceId}>
                    <option value="" disabled>Choose a device…</option>
                    {#if d.deviceId && !devices.some((x) => x.id === d.deviceId)}<option value={d.deviceId}>A device no longer signed in</option>{/if}
                    {#each devices as dev (dev.id)}<option value={dev.id}>{deviceOption(dev)}</option>{/each}
                  </select>
                </label>
                <p class="hint">It must have Cineva open when the mode runs, and plays as whoever is signed in on it.</p>
                <fieldset class="titlefrom">
                  <legend>Title</legend>
                  <label class="check"><input type="radio" name="title-{d.key}" value="started" bind:group={d.titleFrom} /> The title it is started from</label>
                  <label class="check"><input type="radio" name="title-{d.key}" value="fixed" bind:group={d.titleFrom} /> Always the same title</label>
                  {#if d.titleFrom === 'fixed'}
                    {#if d.itemId}
                      <div class="chosen">
                        <span>{d.itemName || titles[d.itemId] || 'The chosen title'}</span>
                        <button type="button" class="lib-btn" onclick={() => { d.itemId = ''; d.itemName = '' }}>Change</button>
                      </div>
                    {:else}
                      <input type="search" placeholder="Search for a title…" bind:value={d.q} oninput={() => onSearch(d)} aria-label="Search for a title" />
                      {#if (results[d.key] ?? []).length > 0}
                        <ul class="found">
                          {#each results[d.key] as it (it.id)}
                            <li><button type="button" onclick={() => chooseTitle(d, it)}>{itemLabel(it)} <span class="muted small">{kindLabel[it.kind] ?? it.kind}</span></button></li>
                          {/each}
                        </ul>
                      {:else if d.q.trim().length >= 2}
                        <p class="hint">Nothing found yet.</p>
                      {/if}
                    {/if}
                  {/if}
                </fieldset>
              {/if}
            </li>
          {/each}
        </ol>
        <div class="addsteps">
          <button type="button" class="lib-btn ibtn" disabled={editingMode.steps.length >= MAX_STEPS} onclick={() => addStep('action')}><Zap size={15} /> Action</button>
          <button type="button" class="lib-btn ibtn" disabled={editingMode.steps.length >= MAX_STEPS} onclick={() => addStep('wait')}><Timer size={15} /> Wait</button>
          <button type="button" class="lib-btn ibtn" disabled={editingMode.steps.length >= MAX_STEPS || hasPlay} title={hasPlay ? 'A mode plays one thing' : undefined} onclick={() => addStep('play')}><Play size={15} /> Play</button>
        </div>
        <p class="hint" class:over={totalWait > MAX_WAIT}>{editingMode.steps.length} of {MAX_STEPS} steps · {totalWait} of {MAX_WAIT} seconds of waiting</p>
      </div>

      <div class="field">
        <span>Who can run it</span>
        <AccessPicker bind:access={editingMode.access} {users} {groups} noun="mode" />
        <p class="hint">Admins can run every mode. Running one does not give anyone the controls of the entities it uses.</p>
      </div>
      {#if modeErr}<div class="error slim">{modeErr}</div>{/if}
    </form>
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (editingMode = null)}>Cancel</button>
      <button class="primary" type="submit" form="ha-mode-form" disabled={modeBusy}>{modeBusy ? 'Saving…' : editingMode?.id ? 'Save' : 'Create'}</button>
    {/snippet}
  </Modal>
{/if}

{#if deletingMode}
  {@const m = deletingMode}
  <Modal title="Delete {m.name}?" onclose={() => (deletingMode = null)}>
    <p>People can no longer run it. Nothing changes in Home Assistant.</p>
    {#if modeErr}<div class="error slim">{modeErr}</div>{/if}
    {#snippet foot()}
      <button type="button" class="lib-btn" onclick={() => (deletingMode = null)}>Cancel</button>
      <button type="button" class="lib-btn ibtn danger solid" disabled={modeBusy} onclick={() => removeMode(m)}>
        <Trash size={15} /> {modeBusy ? 'Deleting…' : 'Delete'}
      </button>
    {/snippet}
  </Modal>
{/if}

<style>
  h1 { font-size: 1.35rem; margin-bottom: 0.5rem; }
  h2 { font-size: 0.95rem; color: var(--muted); margin: 1.25rem 0 0.5rem; display: flex; gap: 0.5rem; align-items: baseline; }
  .hint { color: var(--muted); font-size: 0.8rem; line-height: 1.5; }
  .hint.over { color: var(--err); }
  .intro { max-width: 52rem; margin-bottom: 0.75rem; }
  .muted { color: var(--muted); }
  .small { font-size: 0.78rem; }
  code { color: var(--accent-2); }
  .error {
    display: flex; align-items: flex-start; gap: 0.6rem;
    background: color-mix(in srgb, var(--err) 15%, transparent);
    border: 1px solid var(--err); border-radius: 8px;
    padding: 0.6rem 1rem; margin-bottom: 1rem; font-size: 0.9rem;
  }
  .error :global(svg) { flex: none; margin-top: 0.15rem; color: var(--err); }
  .error > span { flex: 1; min-width: 0; overflow-wrap: anywhere; }
  .error.slim { margin: 0; padding: 0.4rem 0.7rem; }
  .dismiss { color: var(--muted); }
  .empty {
    background: var(--panel); border: 1px dashed var(--border); border-radius: 12px;
    padding: 1.5rem 1.25rem; color: var(--muted); margin-top: 0.75rem;
  }
  .panel {
    background: var(--panel); border: 1px solid var(--border); border-radius: 12px;
    padding: 1rem 1.25rem; margin: 1.5rem 0 1rem;
  }
  .primary { background: var(--accent); color: var(--on-accent); padding: 0.5rem 1rem; border-radius: 8px; }
  .primary:disabled { opacity: 0.45; cursor: not-allowed; }
  .lib-btn {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.4rem 0.75rem; font: inherit; font-size: 0.875rem;
  }
  .lib-btn:hover { border-color: var(--accent); }
  .lib-btn:disabled { opacity: 0.5; cursor: default; }
  .lib-btn:disabled:hover { border-color: var(--border); }
  .lib-btn.danger { color: var(--err); border-color: color-mix(in srgb, var(--err) 45%, var(--border)); }
  .lib-btn.danger:hover { border-color: var(--err); }
  .lib-btn.danger.solid { background: var(--err); border-color: var(--err); color: var(--bg); }
  .lib-btn.iconly { padding: 0.35rem; display: inline-grid; place-items: center; }
  .ibtn { display: inline-flex; align-items: center; gap: 0.4rem; }
  .tag {
    background: var(--bg); border: 1px solid var(--border); border-radius: 999px;
    padding: 0.15rem 0.6rem; font-size: 0.78rem; white-space: nowrap; display: inline-flex; align-items: center; gap: 0.3rem;
  }
  .titlerow { display: flex; align-items: flex-start; justify-content: space-between; gap: 1rem; margin-bottom: 0.75rem; }
  .titlerow .primary { flex: none; }

  /* Mirrors the library Manage screen's tabs. */
  .lib-tabs {
    display: flex; gap: 0.25rem; border-bottom: 1px solid var(--border);
    margin: 0.75rem 0 1rem; overflow-x: auto; overflow-y: hidden; scrollbar-width: none;
  }
  .lib-tabs::-webkit-scrollbar { display: none; }
  .lib-tab {
    padding: 0.5rem 0.9rem; color: var(--muted); border-bottom: 2px solid transparent;
    margin-bottom: -1px; white-space: nowrap;
  }
  .lib-tab:focus-visible { outline: 2px solid var(--accent-2); outline-offset: -2px; }
  .lib-tab:hover { color: var(--text); }
  .lib-tab.on { color: var(--text); border-bottom-color: var(--accent); font-weight: 600; }
  .count { margin-left: 0.4rem; color: var(--muted); font-size: 0.78rem; font-weight: 400; }

  .toolbar { display: flex; flex-wrap: wrap; align-items: center; gap: 0.6rem 1rem; margin-bottom: 0.75rem; }
  .filter {
    flex: 1 1 16rem; max-width: 28rem; min-width: 0;
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.45rem 0.7rem; font: inherit;
  }
  .chk { display: flex; align-items: center; gap: 0.4rem; font-size: 0.85rem; white-space: nowrap; }
  .chips { display: flex; flex-wrap: wrap; gap: 0.4rem; margin-bottom: 0.75rem; }
  .chip {
    padding: 0.3rem 0.75rem; border-radius: 999px; border: 1px solid var(--border);
    color: var(--muted); font-size: 0.85rem;
  }
  .chip.sel { border-color: var(--accent); color: var(--text); background: color-mix(in srgb, var(--accent) 18%, transparent); }
  .chip .n { color: var(--muted); font-size: 0.75rem; margin-left: 0.15rem; }

  /* One list per area. On a phone each row stacks: icon, name and state,
     then the controls underneath; wider, it is a single line. */
  .rows {
    list-style: none; background: var(--panel); border: 1px solid var(--border); border-radius: 12px;
  }
  .rows > li + li { border-top: 1px solid var(--border); }
  .erow {
    display: grid; grid-template-columns: 2rem minmax(0, 1fr) auto; align-items: center;
    gap: 0.5rem 0.75rem; padding: 0.7rem 1rem;
  }
  .erow.hidden .etext, .erow.hidden .eicon { opacity: 0.6; }
  .eicon {
    display: grid; place-items: center; width: 2rem; height: 2rem; border-radius: 8px;
    background: var(--bg); color: var(--accent-2);
  }
  .etext { display: flex; flex-direction: column; gap: 0.1rem; min-width: 0; }
  .ename { overflow-wrap: anywhere; }
  .eid { overflow-wrap: anywhere; font-family: ui-monospace, SFMono-Regular, Menlo, monospace; }
  .estate { text-align: right; overflow-wrap: anywhere; }
  .estate.gone { color: var(--err); }
  .ectl { grid-column: 2 / -1; display: flex; flex-wrap: wrap; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .access { max-width: 100%; }
  @media (min-width: 48rem) {
    .erow { grid-template-columns: 2rem minmax(0, 1fr) minmax(8rem, 14rem) auto; }
    .ectl { grid-column: auto; flex-wrap: nowrap; justify-content: flex-end; gap: 0.9rem; }
    .access { min-width: 9rem; justify-content: flex-start; }
  }

  /* An on/off switch over a real checkbox, so it keeps the keyboard and
     the screen reader's "switch". */
  .switch { position: relative; display: inline-flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; cursor: pointer; }
  .switch input { position: absolute; opacity: 0; width: 2.4rem; height: 1.35rem; margin: 0; cursor: pointer; }
  .switch .track {
    position: relative; width: 2.4rem; height: 1.35rem; flex: none; border-radius: 999px;
    background: var(--border); transition: background 0.15s;
  }
  .switch .track::after {
    content: ''; position: absolute; top: 0.15rem; left: 0.15rem; width: 1.05rem; height: 1.05rem;
    border-radius: 50%; background: var(--text); transition: transform 0.15s;
  }
  .switch input:checked + .track { background: var(--accent); }
  .switch input:checked + .track::after { transform: translateX(1.05rem); }
  .switch input:focus-visible + .track { outline: 2px solid var(--accent-2); outline-offset: 2px; }
  .switch input:disabled + .track { opacity: 0.5; }

  .drow {
    display: grid; grid-template-columns: auto minmax(0, 1fr); align-items: center;
    gap: 0.5rem 0.75rem; padding: 0.7rem 1rem;
  }
  .dot { width: 0.6rem; height: 0.6rem; border-radius: 50%; background: var(--border); }
  .dot.on { background: var(--ok); }
  .room { grid-column: 2; display: flex; align-items: center; gap: 0.5rem; }
  .room select, .form select, .form input:not([type='checkbox']):not([type='radio']):not([type='range']) {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    color: var(--text); padding: 0.45rem 0.65rem; font: inherit; min-width: 0;
  }
  .room select { flex: 1; max-width: 16rem; font-size: 0.9rem; }
  @media (min-width: 48rem) {
    .drow { grid-template-columns: auto minmax(0, 1fr) auto; }
    .room { grid-column: auto; }
    .room select { width: 14rem; }
  }
  .warnline { margin-bottom: 0.5rem; }

  .events summary { cursor: pointer; font-weight: 600; }
  .events .hint { margin-top: 0.6rem; max-width: 52rem; }
  .yaml { position: relative; margin-top: 0.6rem; }
  .yaml pre {
    background: var(--bg); border: 1px solid var(--border); border-radius: 8px;
    padding: 0.8rem 1rem; padding-right: 6rem; overflow-x: auto; font-size: 0.8rem; line-height: 1.5;
  }
  .copy { position: absolute; top: 0.5rem; right: 0.5rem; }

  .mrow { display: flex; flex-wrap: wrap; align-items: center; gap: 0.75rem 1rem; padding: 0.8rem 1rem; }
  .mrow .etext { flex: 1 1 16rem; gap: 0.3rem; }
  .mrow .ename { font-weight: 600; }
  .msum { overflow-wrap: anywhere; }
  .mtags { display: flex; flex-wrap: wrap; gap: 0.35rem; }
  .mctl { display: flex; gap: 0.5rem; margin-left: auto; }

  /* The mode editor. */
  .form { display: flex; flex-direction: column; gap: 0.75rem; }
  .form label { display: flex; flex-direction: column; gap: 0.3rem; font-size: 0.9rem; color: var(--muted); }
  .form label.check { flex-direction: row; align-items: center; gap: 0.5rem; color: var(--text); }
  .field { display: flex; flex-direction: column; gap: 0.5rem; font-size: 0.9rem; color: var(--muted); }
  .steps { list-style: none; display: flex; flex-direction: column; gap: 0.6rem; }
  .stepbox {
    display: flex; flex-direction: column; gap: 0.6rem;
    border: 1px solid var(--border); border-radius: 10px; padding: 0.7rem 0.8rem; background: var(--bg);
  }
  .stepbox select, .stepbox input:not([type='checkbox']):not([type='radio']):not([type='range']) { background: var(--panel); }
  .stephead { display: flex; align-items: center; justify-content: space-between; gap: 0.5rem; }
  .stepname { display: inline-flex; align-items: center; gap: 0.4rem; color: var(--text); font-weight: 600; }
  .stepname .muted { font-weight: 400; }
  .stepbtns { display: flex; gap: 0.3rem; }
  .stepbtns .lib-btn { background: var(--panel); }
  .titlefrom { border: none; display: flex; flex-direction: column; gap: 0.45rem; }
  .titlefrom legend { font-size: 0.9rem; color: var(--muted); margin-bottom: 0.3rem; }
  .chosen { display: flex; align-items: center; justify-content: space-between; gap: 0.6rem; color: var(--text); }
  .chosen span { overflow-wrap: anywhere; }
  .found { list-style: none; border: 1px solid var(--border); border-radius: 8px; max-height: 14rem; overflow-y: auto; }
  .found li + li { border-top: 1px solid var(--border); }
  .found button { width: 100%; text-align: left; padding: 0.45rem 0.7rem; color: var(--text); }
  .found button:hover, .found button:focus-visible { background: var(--panel); }
  .addsteps { display: flex; flex-wrap: wrap; gap: 0.5rem; }
  input[type='range'] { accent-color: var(--accent); }
</style>
