<script lang="ts">
  // The signed-in user's own settings, in sections: a list beside the open
  // section on a wide screen, and on a phone a list that opens each section
  // as its own screen, like a phone's own settings. The section is in the
  // URL (/settings/devices), so it is linkable and the back button works.
  //
  // What only this page uses lives here; what the rest of the app shares —
  // the user, the device list, the theme, playback choices read at play
  // time — stays with App, which passes it in.
  import {
    User as UserIcon, MonitorPlay, Palette, Lock, MonitorSmartphone, Smartphone, KeyRound, Info,
    ChevronRight, ChevronLeft, ArrowUpRight, ShieldCheck, Upload, Sun, Moon, Check, Copy, SlidersHorizontal,
    Bell, Plus, Pencil,
  } from '@lucide/svelte'
  import {
    AVATAR_COLOURS, changePassword, createAPIKey, deleteAPIKey, listAPIKeys, nameDevice,
    removeAvatar, revokeDevice, revokeOtherDevices, forgetDevice, setAvatarColour, setPIN, updateMe,
    myNotifications, createMyDestination, updateMyDestination, deleteMyDestination, testMyDestination,
    type APIKey, type AvatarColour, type Device, type MyPrefs, type PrefEntry, type PrefOrigin, type User,
    type MyNotifications, type NotifyDestination, type NotifyKind,
  } from './lib/api'
  import { onPrefs, prefState, resetPref, setPref } from './lib/prefs'
  import { tv } from './lib/tv.svelte'
  import type { ThemeInfo } from './lib/theme'
  import { ha, loadHA } from './lib/homeassistant.svelte'
  import Sheet from './Sheet.svelte'
  import ThemeEditor from './ThemeEditor.svelte'
  import { holidayKey, type Occasion } from './lib/theme'

  let {
    me,
    section,
    onsection,
    onme,
    themes,
    theme,
    occasion,
    onthemeschanged,
    onsettheme,
    devices,
    onreloaddevices,
    onpickavatar,
    avatarBusy,
    avatarMsg,
  }: {
    me: User
    /** The open section's id, or '' for none (the list, on a phone). */
    section: string
    onsection: (id: string) => void
    /** The user as the server now has them, after a change made here. */
    onme: (u: User) => void
    themes: ThemeInfo[]
    theme: string
    /** The holiday on now, whose theme shows instead of the chosen one
        while Holiday themes is on. */
    occasion: Occasion | null
    /** A theme was made, changed or deleted here. */
    onthemeschanged: () => void
    onsettheme: (id: string) => void
    devices: Device[]
    onreloaddevices: () => void
    /** A photo was picked: App frames it (AvatarCrop) and uploads it. */
    onpickavatar: (file: File) => void
    avatarBusy: boolean
    avatarMsg: string
  } = $props()

  // Preferences as the server resolved them, with the registry that
  // describes them: the Playback and Appearance sections are drawn from it,
  // so a preference the server adds appears here without a client change.
  let prefs: MyPrefs | null = $state(prefState())
  $effect(() => onPrefs((s) => (prefs = s)))

  const BASE_SECTIONS = [
    { id: 'profile', label: 'Profile', icon: UserIcon },
    { id: 'playback', label: 'Playback', icon: MonitorPlay },
    { id: 'appearance', label: 'Appearance', icon: Palette },
    { id: 'notifications', label: 'Notifications', icon: Bell },
    { id: 'security', label: 'Sign-in and security', icon: Lock },
    { id: 'devices', label: 'Devices', icon: MonitorSmartphone },
    { id: 'keys', label: 'API keys', icon: KeyRound },
    { id: 'about', label: 'About', icon: Info },
  ]
  // A section the registry names that this page does not know yet goes in
  // before the account sections.
  const SECTIONS = $derived.by(() => {
    const known = new Set(BASE_SECTIONS.map((s) => s.id))
    const extra = (prefs?.sections ?? [])
      .filter((s) => !known.has(s.id))
      .map((s) => ({ id: s.id, label: s.label, icon: SlidersHorizontal }))
    const at = BASE_SECTIONS.findIndex((s) => s.id === 'security')
    return [...BASE_SECTIONS.slice(0, at), ...extra, ...BASE_SECTIONS.slice(at)]
  })
  // On a wide screen something is always open; on a phone '' is the list.
  const open = $derived(SECTIONS.some((s) => s.id === section) ? section : '')
  const shown = $derived(open || 'profile')
  const extraSection = $derived(prefs?.sections.find((s) => s.id === shown && !BASE_SECTIONS.some((b) => b.id === s.id)))
  const errText = (err: unknown) => (err instanceof Error ? err.message : String(err))

  // ---- Profile ----
  // svelte-ignore state_referenced_locally
  let nameDraft = $state(me.displayName) // a draft: starts from the name, then is the user's
  let nameMsg = $state('')
  let nameBusy = $state(false)
  const nameChanged = $derived(nameDraft.trim() !== '' && nameDraft.trim() !== me.displayName)
  async function saveName(e: Event) {
    e.preventDefault()
    if (!nameChanged) return
    nameBusy = true
    nameMsg = ''
    try {
      const u = await updateMe(nameDraft.trim())
      onme(u)
      nameDraft = u.displayName
      nameMsg = 'Saved.'
    } catch (err) {
      nameMsg = errText(err)
    } finally {
      nameBusy = false
    }
  }
  let pictureMsg = $state('')
  function pickPhoto(e: Event) {
    const input = e.currentTarget as HTMLInputElement
    const file = input.files?.[0]
    input.value = '' // choosing the same file again should still offer it
    if (file) onpickavatar(file)
  }
  async function dropPhoto() {
    try {
      onme(await removeAvatar())
    } catch (err) {
      pictureMsg = errText(err)
    }
  }
  async function chooseColour(c: AvatarColour | '') {
    try {
      onme(await setAvatarColour(c))
    } catch (err) {
      pictureMsg = errText(err)
    }
  }
  const COLOUR_NAMES: Record<string, string> = {
    'accent-2': 'Sky', 'chart-1': 'Colour one', 'chart-2': 'Colour two', 'chart-3': 'Colour three', ok: 'Green', err: 'Red',
  }

  // ---- Preferences ----
  // Read when a title starts (by App and the player) or by the server when
  // it picks tracks, so setting one is all there is.
  const inSection = (id: string) =>
    (prefs?.registry ?? []).filter((p) => {
      if (p.section !== id || p.hidden) return false
      const hide = p.hideWhen
      return !hide || prefs?.prefs[hide.key]?.value !== hide.value
    })
  const valueOf = (p: PrefEntry) => prefs?.prefs[p.key]?.value ?? p.default
  const choiceLabel = (p: PrefEntry, v: string) =>
    p.type === 'bool' ? (v === 'on' ? 'On' : 'Off') : (p.options?.find((o) => o.value === v)?.label ?? v)
  // A theme's preview colours, as custom properties on its card: only the
  // tokens the mini screen draws with, each from that theme's own set.
  const previewTokens = ['bg', 'panel', 'border', 'text', 'muted', 'accent', 'accent-2']
  const previewStyle = (t: ThemeInfo) =>
    [
      ...previewTokens.filter((k) => t.tokens?.[k]).map((k) => `--p-${k}: ${t.tokens![k]}`),
      // The picture, covered a little less than on the page so it shows
      // at this size. Its URL comes from the server's own paths.
      ...(t.background ? [`--p-image: url("${t.background.replace(/["\\()]/g, '')}")`, `--p-dim: ${((t.dim ?? 70) / 100) * 0.6}`] : []),
    ].join('; ')

  // ---- Themes: holidays, and making your own ----
  const holidayOn = $derived(prefs?.prefs['holiday-themes']?.value !== 'off')
  const holidayTheme = $derived(occasion?.themeId ? themes.find((t) => t.id === occasion?.themeId) : undefined)
  // Set aside this year by choosing another theme while it showed.
  const holidaySkipped = $derived(!!occasion && prefs?.prefs['holiday-skip']?.value === holidayKey(occasion))
  const holidayNow = $derived(holidayOn && !holidaySkipped ? holidayTheme : undefined)
  // Everyday themes, then the holidays', then those people made.
  const themeOrder = (t: ThemeInfo) => (t.custom ? 2 : t.occasion ? 1 : 0)
  const orderedThemes = $derived([...themes].sort((a, b) => themeOrder(a) - themeOrder(b)))
  const untilLabel = (d: string) => new Date(d + 'T12:00:00').toLocaleDateString(undefined, { day: 'numeric', month: 'long' })
  // The editor: null closed, 'new' to make one, or the theme being changed.
  let editingTheme: ThemeInfo | 'new' | null = $state(null)
  const canEdit = (t: ThemeInfo) => !!t.custom && (t.mine || me.role === 'admin')
  const fromAdmin = (o?: PrefOrigin) => o?.source === 'server' || o?.source === 'group'
  function save(key: string, value: string) {
    try {
      setPref(key, value)
    } catch {
      /* private browsing: the server copy is what matters */
    }
  }
  // ---- Sign-in and security ----
  let pwOpen = $state(false)
  let pwCurrent = $state(''), pwNew = $state(''), pwConfirm = $state(''), pwMsg = $state('')
  async function submitPassword(e: Event) {
    e.preventDefault()
    pwMsg = ''
    if (pwNew !== pwConfirm) {
      pwMsg = 'The new passwords do not match.'
      return
    }
    try {
      await changePassword(pwCurrent, pwNew)
      pwCurrent = pwNew = pwConfirm = ''
      pwOpen = false
      pwMsg = 'Password changed. Your other devices have been signed out.'
      onreloaddevices()
    } catch (err) {
      pwMsg = errText(err)
    }
  }
  let pinOpen = $state(false)
  let pinAuth = $state(''), pinNew = $state(''), pinMsg = $state('')
  async function submitPIN(e: Event, clear = false) {
    e.preventDefault()
    pinMsg = ''
    try {
      await setPIN(pinAuth, clear ? '' : pinNew)
      pinMsg = clear ? 'PIN removed.' : 'PIN saved.'
      pinAuth = pinNew = ''
      pinOpen = false
      onme({ ...me, hasPin: !clear })
    } catch (err) {
      pinMsg = errText(err)
    }
  }

  // ---- Devices ----
  // This one first, then what has been used lately; sessions left idle for a
  // week fold away, since a list of old browsers hides the one that matters.
  const STALE_MS = 7 * 24 * 3600 * 1000
  const when = (ts: string) => Date.parse(/[zZ]|[+-]\d\d:?\d\d$/.test(ts) ? ts : ts.replace(' ', 'T') + 'Z')
  let now = $state(Date.now())
  $effect(() => {
    const t = setInterval(() => (now = Date.now()), 60_000)
    return () => clearInterval(t)
  })
  function ago(ts: string): string {
    const t = when(ts)
    if (Number.isNaN(t)) return ts
    const mins = Math.round((now - t) / 60_000)
    if (mins < 1) return 'just now'
    if (mins < 60) return mins === 1 ? 'a minute ago' : `${mins} minutes ago`
    const hours = Math.round(mins / 60)
    if (hours < 24) return hours === 1 ? 'an hour ago' : `${hours} hours ago`
    const days = Math.round(hours / 24)
    if (days < 2) return 'yesterday'
    if (days < 7) return `${days} days ago`
    const d = new Date(t)
    return d.toLocaleDateString('en-GB', { day: 'numeric', month: 'long', year: d.getFullYear() === new Date(now).getFullYear() ? undefined : 'numeric' })
  }
  const loopback = (ip: string) => ip === '::1' || ip.startsWith('127.') || ip.startsWith('::ffff:127.')
  const where = (d: Device) => (!d.clientIp ? '' : loopback(d.clientIp) ? 'on the server itself' : d.clientIp)
  const isPhone = (d: Device) => /iOS|iPadOS|Android/.test(d.client)
  const deviceTitle = (d: Device) => d.name || d.client || 'Unknown browser'
  const thisDevice = $derived(devices.find((d) => d.current))
  const others = $derived(
    devices.filter((d) => !d.current).sort((a, b) => Number(b.online) - Number(a.online) || when(b.lastSeenAt) - when(a.lastSeenAt)),
  )
  const recent = $derived(others.filter((d) => d.online || now - when(d.lastSeenAt) < STALE_MS))
  const older = $derived(others.filter((d) => !recent.includes(d)))
  let showOlder = $state(false)
  let deviceMsg = $state('')
  let renaming = $state(false)
  let deviceName = $state('')
  function startRename() {
    deviceName = thisDevice?.name ?? ''
    renaming = true
  }
  async function saveDeviceName(e: Event) {
    e.preventDefault()
    try {
      await nameDevice({ name: deviceName.trim() })
      renaming = false
      deviceMsg = ''
      onreloaddevices()
    } catch (err) {
      deviceMsg = errText(err)
    }
  }
  // The room this device is in, for Home Assistant: the player shows that
  // room's controls and the modes that play there.
  const haAreas = $derived(
    [...(ha.rooms?.allAreas ?? ha.rooms?.areas.filter((a) => a.id) ?? [])].sort((a, b) => a.name.localeCompare(b.name)),
  )
  let areaBusy = $state(false)
  let areaMsg = $state('')
  async function saveArea(areaId: string) {
    areaBusy = true
    areaMsg = ''
    try {
      await nameDevice({ areaId })
      if (ha.rooms) ha.rooms.currentAreaId = areaId
      void loadHA()
      onreloaddevices()
    } catch (err) {
      areaMsg = errText(err)
    } finally {
      areaBusy = false
    }
  }

  let gone: string[] = $state([]) // signed out here, hidden until the list reloads
  async function signOut(d: Device) {
    gone = [...gone, d.id]
    try {
      await revokeDevice(d.id)
      onreloaddevices()
    } catch (err) {
      gone = gone.filter((id) => id !== d.id)
      deviceMsg = errText(err)
    }
  }
  // Forgetting a device removes it, with its name, room and settings, rather
  // than only signing it out; asked twice, as it cannot be undone.
  let forgetAsk = $state('')
  async function forget(d: Device) {
    if (forgetAsk !== d.id) {
      forgetAsk = d.id
      return
    }
    forgetAsk = ''
    gone = [...gone, d.id]
    try {
      await forgetDevice(d.id)
      onreloaddevices()
    } catch (err) {
      gone = gone.filter((id) => id !== d.id)
      deviceMsg = errText(err)
    }
  }
  let confirmAll = $state(false)
  async function signOutOthers() {
    if (!confirmAll) {
      confirmAll = true
      return
    }
    confirmAll = false
    try {
      const { revoked } = await revokeOtherDevices()
      deviceMsg = revoked === 1 ? 'Signed out 1 other device.' : `Signed out ${revoked} other devices.`
      onreloaddevices()
    } catch (err) {
      deviceMsg = errText(err)
    }
  }

  // ---- API keys ----
  let keys: APIKey[] = $state([])
  let keyName = $state('')
  let revealed = $state('')
  let copied = $state(false)
  let keyMsg = $state('')
  $effect(() => {
    if (shown === 'keys') listAPIKeys().then((k) => (keys = k)).catch(() => {})
  })
  async function addKey(e: Event) {
    e.preventDefault()
    keyMsg = ''
    try {
      const k = await createAPIKey(keyName.trim() || 'API key')
      revealed = k.key
      copied = false
      keyName = ''
      keys = [...keys, k]
    } catch (err) {
      keyMsg = errText(err)
    }
  }
  async function copyKey() {
    try {
      await navigator.clipboard.writeText(revealed)
      copied = true
    } catch {
      keyMsg = 'Copying was refused by the browser; select the key and copy it instead.'
    }
  }
  async function revoke(id: string) {
    const before = keys
    keys = keys.filter((k) => k.id !== id)
    try {
      await deleteAPIKey(id)
    } catch (err) {
      keys = before
      keyMsg = errText(err)
    }
  }

  // ---- Notifications ----
  // The caller's own destinations, for events about them alone. The kinds
  // the server offers carry their settings, so the form is drawn from them.
  let notif = $state<MyNotifications | null>(null)
  let notifMsg = $state('')
  async function loadNotif() {
    try {
      notif = await myNotifications()
    } catch (err) {
      notifMsg = errText(err)
    }
  }
  $effect(() => {
    if (shown === 'notifications') void loadNotif()
  })
  const kindOf = (id: string) => notif?.kinds.find((k) => k.id === id)
  function eventsSummary(d: NotifyDestination): string {
    const all = notif?.events ?? []
    const chosen = all.filter((e) => d.events.includes(e.id))
    if (chosen.length === 0) return 'No events chosen'
    if (chosen.length === all.length) return 'Every event'
    return chosen.map((e) => e.label).join(', ')
  }
  // A test's answer, shown beside its buttons until the next test.
  let testing = $state('')
  let tested: Record<string, { ok: boolean; text: string }> = $state({})
  async function testDest(d: NotifyDestination) {
    testing = d.id
    delete tested[d.id]
    try {
      await testMyDestination(d.id)
      tested[d.id] = { ok: true, text: 'Delivered' }
    } catch (err) {
      tested[d.id] = { ok: false, text: errText(err) }
    } finally {
      testing = ''
      void loadNotif() // the destination now records the attempt
    }
  }
  async function toggleDest(d: NotifyDestination) {
    notifMsg = ''
    try {
      const saved = await updateMyDestination(d.id, { enabled: !d.enabled })
      if (notif) notif.destinations = notif.destinations.map((x) => (x.id === saved.id ? saved : x))
    } catch (err) {
      notifMsg = errText(err)
    }
  }
  // Deleting is asked twice, as Forget is for a device.
  let deleteAsk = $state('')
  async function deleteDest(d: NotifyDestination) {
    if (deleteAsk !== d.id) {
      deleteAsk = d.id
      return
    }
    deleteAsk = ''
    notifMsg = ''
    try {
      await deleteMyDestination(d.id)
      if (notif) notif.destinations = notif.destinations.filter((x) => x.id !== d.id)
    } catch (err) {
      notifMsg = errText(err)
    }
  }

  // The add/edit form. editing is null when closed, '' when adding, else
  // the destination's id. A secret field starts blank: left blank, the
  // saved value is kept, and its mask shows as the placeholder.
  let editing: string | null = $state(null)
  let draft = $state({ kind: '', name: '', values: {} as Record<string, string>, events: [] as string[], enabled: true })
  let formMsg = $state('')
  let formBusy = $state(false)
  const editingDest = $derived(editing ? notif?.destinations.find((d) => d.id === editing) : undefined)
  const draftKind = $derived(kindOf(draft.kind))
  function openAdd() {
    const kinds = notif?.kinds ?? []
    draft = { kind: kinds.length === 1 ? kinds[0].id : '', name: '', values: {}, events: (notif?.events ?? []).map((e) => e.id), enabled: true }
    formMsg = ''
    editing = ''
  }
  function openEdit(d: NotifyDestination) {
    const values: Record<string, string> = {}
    for (const f of kindOf(d.kind)?.fields ?? []) values[f.name] = f.secret ? '' : (d.config[f.name] ?? '')
    draft = { kind: d.kind, name: d.name, values, events: [...d.events], enabled: d.enabled }
    formMsg = ''
    editing = d.id
  }
  function chooseKind(k: NotifyKind) {
    if (draft.kind === k.id) return
    draft.kind = k.id
    draft.values = {}
  }
  function toggleEvent(id: string, on: boolean) {
    draft.events = on ? [...draft.events, id] : draft.events.filter((e) => e !== id)
  }
  async function saveDest(e: Event) {
    e.preventDefault()
    if (!draftKind) {
      formMsg = 'Choose where to send them first.'
      return
    }
    formBusy = true
    formMsg = ''
    const config: Record<string, string> = {}
    for (const f of draftKind.fields) config[f.name] = (draft.values[f.name] ?? '').trim()
    // In the catalogue's order, whatever order they were ticked in.
    const events = (notif?.events ?? []).map((x) => x.id).filter((id) => draft.events.includes(id))
    try {
      const body = { name: draft.name.trim(), config, events, enabled: draft.enabled }
      if (editing) await updateMyDestination(editing, body)
      else await createMyDestination({ kind: draft.kind, ...body })
      editing = null
      await loadNotif()
    } catch (err) {
      formMsg = errText(err)
    } finally {
      formBusy = false
    }
  }
</script>

<div class="settings" class:has-open={open !== ''}>
  <nav class="snav" aria-label="Settings sections">
    <h1>Settings</h1>
    <ul>
      {#each SECTIONS as s (s.id)}
        {@const Icon = s.icon}
        <li>
          <button class="snav-item" class:sel={shown === s.id} aria-current={shown === s.id ? 'page' : undefined} onclick={() => onsection(s.id)}>
            <Icon size={17} />
            <span>{s.label}</span>
            <ChevronRight size={16} class="chev" />
          </button>
        </li>
      {/each}
      <!-- Administration is for a computer or phone, never a TV. -->
      {#if me.role === 'admin' && !tv.on}
        <li class="sep">
          <a class="snav-item" href="/admin/">
            <ShieldCheck size={17} />
            <span>Administration</span>
            <ArrowUpRight size={16} class="out" />
          </a>
        </li>
      {/if}
    </ul>
  </nav>

  <div class="spane">
    <button class="back" onclick={() => onsection('')}><ChevronLeft size={18} /> Settings</button>

    {#if shown === 'profile'}
      <h2>Profile</h2>
      <div class="card">
        <div class="srow pic">
          {#if me.avatar}
            <img class="face" src={me.avatar} alt="" />
          {:else}
            <span class="face" class:tinted={!!me.avatarColour} style:--face={me.avatarColour ? `var(--${me.avatarColour})` : undefined}>{me.displayName.slice(0, 1).toUpperCase()}</span>
          {/if}
          <div class="pic-side">
            <div class="btns">
              <label class="btn upload" class:busy={avatarBusy}>
                <Upload size={15} /> {avatarBusy ? 'Uploading…' : me.avatar ? 'Change photo' : 'Upload a photo'}
                <input type="file" accept="image/*" onchange={pickPhoto} disabled={avatarBusy} />
              </label>
              {#if me.avatar}<button class="btn quiet" onclick={dropPhoto}>Remove photo</button>{/if}
            </div>
            {#if !me.avatar}
              <div class="swatches" role="radiogroup" aria-label="Colour behind your initial">
                <button class="swatch" class:sel={!me.avatarColour} role="radio" aria-checked={!me.avatarColour} aria-label="Default" title="Default" onclick={() => chooseColour('')}></button>
                {#each AVATAR_COLOURS as c (c)}
                  <button class="swatch" class:sel={me.avatarColour === c} role="radio" aria-checked={me.avatarColour === c} aria-label={COLOUR_NAMES[c] ?? c} title={COLOUR_NAMES[c] ?? c} style:--face="var(--{c})" onclick={() => chooseColour(c)}></button>
                {/each}
              </div>
            {/if}
            <span class="hint">Shown on the profile picker and in the top bar.</span>
            {#if avatarMsg || pictureMsg}<span class="msg">{avatarMsg || pictureMsg}</span>{/if}
          </div>
        </div>
        <form class="srow" onsubmit={saveName}>
          <label class="sl" for="s-name">
            <span class="st">Name</span>
            <span class="sd">How you appear to everyone else here. You sign in as <strong>{me.username}</strong>, which stays the same.</span>
          </label>
          <div class="sc inline">
            <input id="s-name" maxlength="40" bind:value={nameDraft} autocomplete="name" />
            <button class="btn" type="submit" disabled={!nameChanged || nameBusy}>Save</button>
          </div>
        </form>
        {#if nameMsg}<p class="msg pad">{nameMsg}</p>{/if}
      </div>

    {:else if shown === 'playback'}
      <h2>Playback</h2>
      {@render prefCard('playback')}

    {:else if shown === 'appearance'}
      {@const themePref = prefs?.prefs.theme}
      <h2>Appearance</h2>
      <div class="card">
        <div class="srow col">
          <div class="sl">
            <span class="st">Theme</span>
            <span class="sd">Follows you to every device you sign in on.</span>
            {#if fromAdmin(themePref)}<span class="sd">The default here is your administrator's choice.</span>{/if}
          </div>
          {#if holidayNow && occasion}
            <p class="holnote">
              It is the {holidayNow.name} theme until {untilLabel(occasion.until)}; yours comes back after.
              Choose another theme to use it instead.
            </p>
          {:else if holidayOn && holidaySkipped && holidayTheme}
            <p class="holnote">
              You chose your own theme over the {holidayTheme.name} one this year.
              <button class="linkish" onclick={() => save('holiday-skip', '')}>Bring {holidayTheme.name} back</button>
            </p>
          {/if}
          <div class="tgrid" role="radiogroup" aria-label="Theme">
            {#each orderedThemes as t (t.id)}
              <div class="tslot">
              <button class="tcard" class:sel={theme === t.id} role="radio" aria-checked={theme === t.id} onclick={() => onsettheme(t.id)}>
                <!-- A small screen of the app in this theme's own colours. -->
                <span class="mini" style={previewStyle(t)} aria-hidden="true">
                  <span class="mini-bar"><span class="mini-logo"></span><span class="mini-line short"></span></span>
                  <span class="mini-body">
                    <span class="mini-line title"></span>
                    <span class="mini-line"></span>
                    <span class="mini-posters"><span></span><span></span><span></span><span></span><span></span></span>
                    <span class="mini-btn"></span>
                  </span>
                </span>
                <span class="tname">
                  {#if t.appearance === 'light'}<Sun size={15} />{:else}<Moon size={15} />{/if}
                  <span>{t.name}</span>
                  {#if theme === t.id}<Check size={15} class="tick" />{/if}
                </span>
                {#if holidayNow?.id === t.id}<span class="tbadge now">On now</span>
                {:else if t.occasion}<span class="tbadge">{t.occasion === 'christmas' ? 'December' : t.occasion === 'halloween' ? 'October' : 'Around Easter'}</span>
                {:else if t.household}<span class="tbadge">Shared</span>
                {:else if t.mine}<span class="tbadge">Yours</span>{/if}
              </button>
              {#if canEdit(t) && !tv.on}
                <button class="tedit" title="Edit {t.name}" aria-label="Edit {t.name}" onclick={() => (editingTheme = t)}><Pencil size={14} /></button>
              {/if}
              </div>
            {/each}
            {#if !tv.on}
              <button class="tcard tnew" onclick={() => (editingTheme = 'new')}>
                <span class="mini plus" aria-hidden="true"><Plus size={26} /></span>
                <span class="tname"><span>Make a theme</span></span>
              </button>
            {/if}
          </div>
        </div>
        {#each inSection('appearance').filter((p) => p.key !== 'theme') as p (p.key)}
          {@render prefRow(p)}
        {/each}
      </div>
      {#if editingTheme}
        <ThemeEditor
          {themes}
          editing={editingTheme === 'new' ? null : editingTheme}
          startFrom={theme}
          admin={me.role === 'admin'}
          onclose={() => (editingTheme = null)}
          onsaved={(t) => {
            const made = editingTheme === 'new'
            editingTheme = null
            onthemeschanged()
            // A theme just made is the one you wanted to see.
            if (t && made) onsettheme(t.id)
          }}
        />
      {/if}

    {:else if shown === 'notifications'}
      <h2>Notifications</h2>
      <p class="lead">
        Be told when your requests are approved, declined or ready to watch, and when new episodes of series you are
        watching arrive. These go only to destinations you set up here, never to a shared channel.
      </p>
      {#if !notif}
        <p class="lead">{notifMsg || 'Loading your notifications…'}</p>
      {:else}
        {#if notif.destinations.length > 0}
          <div class="card">
            {#each notif.destinations as d (d.id)}
              {@const k = kindOf(d.kind)}
              {@const t = tested[d.id]}
              <div class="dest">
                <div class="dest-head">
                  <div class="dev-text">
                    <span class="dev-name" id="nd-{d.id}">{d.name}<span class="tag">{k?.label ?? d.kind}</span></span>
                    <span class="sd">{eventsSummary(d)}</span>
                    {#if !k}
                      <span class="sd">No longer offered here. Delete it, or ask an admin.</span>
                    {:else if d.lastError}
                      <span class="sd bad">Not delivered{d.lastErrorAt ? ` ${ago(d.lastErrorAt)}` : ''}: {d.lastError}</span>
                    {:else if d.lastSentAt}
                      <span class="sd">Last delivered {ago(d.lastSentAt)}</span>
                    {:else}
                      <span class="sd">Nothing sent yet</span>
                    {/if}
                  </div>
                  <button class="switch" role="switch" aria-checked={d.enabled} aria-labelledby="nd-{d.id}"
                    title={d.enabled ? 'On' : 'Off'} onclick={() => toggleDest(d)}><span></span></button>
                </div>
                <div class="btns">
                  <button class="btn" onclick={() => testDest(d)} disabled={testing === d.id}>{testing === d.id ? 'Sending…' : 'Test'}</button>
                  {#if k}<button class="btn quiet" onclick={() => openEdit(d)}>Edit</button>{/if}
                  <button class="btn quiet" class:danger={deleteAsk === d.id} onclick={() => deleteDest(d)}
                    onblur={() => { if (deleteAsk === d.id) deleteAsk = '' }}>{deleteAsk === d.id ? 'Delete it?' : 'Delete'}</button>
                  {#if t}<span class="sd test" class:good={t.ok} class:bad={!t.ok} role="status">{t.text}</span>{/if}
                </div>
              </div>
            {/each}
          </div>
        {/if}
        {#if notif.kinds.length === 0}
          <div class="card"><p class="sd pad first">An admin has not made any kinds of destination available yet.</p></div>
        {:else}
          <div class="btns" class:after={notif.destinations.length > 0}>
            <button class="btn" onclick={openAdd}><Plus size={15} /> Add a destination</button>
          </div>
        {/if}
        {#if notifMsg}<p class="msg">{notifMsg}</p>{/if}
      {/if}

      {#if editing !== null}
        <Sheet title={editing ? `Edit ${editingDest?.name ?? 'destination'}` : 'Add a destination'} onclose={() => (editing = null)}>
          <form class="nform" onsubmit={saveDest}>
            {#if editing}
              <div class="field">
                <span class="st">Sent by</span>
                <span class="sd">{draftKind?.label ?? draft.kind}</span>
              </div>
            {:else}
              <div class="field">
                <span class="st" id="nf-kind">Send them by</span>
                <div class="themes" role="radiogroup" aria-labelledby="nf-kind">
                  {#each notif?.kinds ?? [] as k (k.id)}
                    <button type="button" class="theme" class:sel={draft.kind === k.id} role="radio" aria-checked={draft.kind === k.id} onclick={() => chooseKind(k)}>
                      <span>{k.label}</span>
                      {#if draft.kind === k.id}<Check size={15} class="tick" />{/if}
                    </button>
                  {/each}
                </div>
              </div>
            {/if}
            {#if draftKind}
              <label class="field">
                <span class="st">Name</span>
                <input maxlength="60" placeholder="For example, My phone" bind:value={draft.name} required />
              </label>
              {#each draftKind.fields as f (f.name)}
                {@const masked = editing ? (editingDest?.config[f.name] ?? '') : ''}
                <label class="field">
                  <span class="st">{f.label}{#if !f.required}<span class="opt">optional</span>{/if}</span>
                  <input bind:value={draft.values[f.name]} autocomplete="off" spellcheck="false"
                    placeholder={f.secret && masked ? masked : ''}
                    required={f.required && !(f.secret && masked)} />
                  {#if f.secret && masked}<span class="sd">Leave blank to keep the saved one.</span>{/if}
                  {#if f.hint}<span class="sd">{f.hint}</span>{/if}
                  {#if draftKind.id === 'telegram' && f.name === 'chatId'}
                    <span class="sd">To find yours, send the bot a message, then ask an admin to look it up, or ask @userinfobot.</span>
                  {/if}
                </label>
              {/each}
              <fieldset class="field">
                <legend class="st">Tell me when</legend>
                {#each notif?.events ?? [] as ev (ev.id)}
                  <label class="check">
                    <input type="checkbox" checked={draft.events.includes(ev.id)}
                      onchange={(e) => toggleEvent(ev.id, (e.currentTarget as HTMLInputElement).checked)} />
                    <span>{ev.label}</span>
                  </label>
                {/each}
              </fieldset>
              <div class="field row">
                <span class="st" id="nf-on">Enabled</span>
                <button type="button" class="switch" role="switch" aria-checked={draft.enabled} aria-labelledby="nf-on"
                  onclick={() => (draft.enabled = !draft.enabled)}><span></span></button>
              </div>
            {/if}
            {#if formMsg}<p class="msg bad">{formMsg}</p>{/if}
            <div class="btns">
              <button class="btn primary" type="submit" disabled={formBusy || !draftKind}>{formBusy ? 'Saving…' : editing ? 'Save' : 'Add'}</button>
              <button class="btn quiet" type="button" onclick={() => (editing = null)}>Cancel</button>
            </div>
          </form>
        </Sheet>
      {/if}

    {:else if extraSection}
      <h2>{extraSection.label}</h2>
      {@render prefCard(extraSection.id)}

    {:else if shown === 'security'}
      <h2>Sign-in and security</h2>
      <div class="card">
        <div class="srow" class:col={pwOpen}>
          <div class="sl">
            <span class="st">Password</span>
            <span class="sd">{me.hasPassword ? 'Changing it signs out your other devices.' : 'Not set: you sign in with your PIN.'}</span>
          </div>
          {#if !pwOpen}
            <div class="sc"><button class="btn" onclick={() => { pwOpen = true; pwMsg = '' }}>Change password</button></div>
          {:else}
            <form class="stack" onsubmit={submitPassword}>
              <input type="password" placeholder="Current password" aria-label="Current password" bind:value={pwCurrent} autocomplete="current-password" required />
              <input type="password" placeholder="New password, 8 or more characters" aria-label="New password" bind:value={pwNew} autocomplete="new-password" minlength="8" required />
              <input type="password" placeholder="New password again" aria-label="New password again" bind:value={pwConfirm} autocomplete="new-password" minlength="8" required />
              <div class="btns">
                <button class="btn primary" type="submit">Change password</button>
                <button class="btn quiet" type="button" onclick={() => { pwOpen = false; pwCurrent = pwNew = pwConfirm = '' }}>Cancel</button>
              </div>
            </form>
          {/if}
        </div>
        {#if pwMsg}<p class="msg pad">{pwMsg}</p>{/if}
        <div class="srow" class:col={pinOpen}>
          <div class="sl">
            <span class="st">Quick sign-in PIN</span>
            <span class="sd">{me.hasPin ? 'Set. Tap your picture on the sign-in screen and enter it.' : '4 to 6 digits, to sign in from the profile picker without your password.'}</span>
          </div>
          {#if !pinOpen}
            <div class="sc btns">
              <button class="btn" onclick={() => { pinOpen = true; pinMsg = '' }}>{me.hasPin ? 'Change PIN' : 'Set a PIN'}</button>
            </div>
          {:else}
            <form class="stack" onsubmit={(e) => submitPIN(e)}>
              <input type="password" placeholder={me.hasPassword ? 'Your password' : 'Your current PIN'} aria-label={me.hasPassword ? 'Your password' : 'Your current PIN'} bind:value={pinAuth} autocomplete="current-password" required />
              <input type="password" inputmode="numeric" pattern="[0-9]{'{'}4,6{'}'}" maxlength="6" placeholder="New PIN, 4 to 6 digits" aria-label="New PIN" bind:value={pinNew} />
              <div class="btns">
                <button class="btn primary" type="submit" disabled={pinNew.length < 4}>Save PIN</button>
                {#if me.hasPin && me.hasPassword}<button class="btn quiet" type="button" onclick={(e) => submitPIN(e, true)}>Remove PIN</button>{/if}
                <button class="btn quiet" type="button" onclick={() => { pinOpen = false; pinAuth = pinNew = '' }}>Cancel</button>
              </div>
            </form>
          {/if}
        </div>
        {#if pinMsg}<p class="msg pad">{pinMsg}</p>{/if}
      </div>

    {:else if shown === 'devices'}
      <h2>Devices</h2>
      <p class="lead">Every browser signed in as you. Sign one out if it is lost or not yours; a device keeps its name, room and settings until it is forgotten.</p>
      {#if thisDevice}
        <h3>This device</h3>
        <div class="card">
          <div class="dev">
            {#if isPhone(thisDevice)}<Smartphone size={20} />{:else}<MonitorSmartphone size={20} />{/if}
            {#if renaming}
              <form class="inline grow" onsubmit={saveDeviceName}>
                <!-- svelte-ignore a11y_autofocus -->
                <input maxlength="40" placeholder="For example, Living-room TV" aria-label="Device name" bind:value={deviceName} autofocus />
                <button class="btn primary" type="submit">Save</button>
                <button class="btn quiet" type="button" onclick={() => (renaming = false)}>Cancel</button>
              </form>
            {:else}
              <div class="dev-text">
                <span class="dev-name">{deviceTitle(thisDevice)}</span>
                <span class="sd">{[thisDevice.name ? thisDevice.client : '', where(thisDevice), `signed in ${ago(thisDevice.createdAt)}`].filter(Boolean).join(' · ')}</span>
              </div>
              <button class="btn quiet" onclick={startRename}>{thisDevice.name ? 'Rename' : 'Name it'}</button>
            {/if}
          </div>
          {#if ha.rooms?.configured}
            <div class="srow">
              <div class="sl">
                <label class="st" for="dev-area">This device is in</label>
                <span class="sd">Its room in Home Assistant. The player shows that room's lights and modes.</span>
              </div>
              <div class="sc">
                <select id="dev-area" value={ha.rooms.currentAreaId} disabled={areaBusy}
                  onchange={(e) => saveArea((e.currentTarget as HTMLSelectElement).value)}>
                  <option value="">No room</option>
                  {#each haAreas as a (a.id)}<option value={a.id}>{a.name}</option>{/each}
                </select>
              </div>
            </div>
            {#if areaMsg}<p class="msg pad">{areaMsg}</p>{/if}
          {/if}
          <div class="srow">
            <div class="sl">
              <span class="st">Forget this device</span>
              <span class="sd">Signing out keeps its name, room and settings for whoever signs in next. Forgetting removes them, and signs you out.</span>
            </div>
            <div class="sc">
              <button class="btn quiet" onclick={() => forget(thisDevice)}>{forgetAsk === thisDevice.id ? 'Forget it?' : 'Forget'}</button>
            </div>
          </div>
        </div>
      {/if}
      {#if recent.length > 0}
        <h3>Other devices</h3>
        <div class="card">
          {#each recent.filter((d) => !gone.includes(d.id)) as d (d.id)}
            {@render deviceRow(d)}
          {/each}
        </div>
      {/if}
      {#if older.length > 0}
        <button class="fold" onclick={() => (showOlder = !showOlder)} aria-expanded={showOlder}>
          <ChevronRight size={16} class={showOlder ? 'down' : ''} />
          Not used for over a week ({older.length})
        </button>
        {#if showOlder}
          <div class="card">
            {#each older.filter((d) => !gone.includes(d.id)) as d (d.id)}
              {@render deviceRow(d)}
            {/each}
          </div>
        {/if}
      {/if}
      {#if others.length > 0}
        <div class="btns after">
          <button class="btn" class:danger={confirmAll} onclick={signOutOthers} onblur={() => (confirmAll = false)}>
            {confirmAll ? `Sign out ${others.length} ${others.length === 1 ? 'device' : 'devices'}?` : 'Sign out all other devices'}
          </button>
        </div>
      {/if}
      {#if deviceMsg}<p class="msg">{deviceMsg}</p>{/if}

    {:else if shown === 'keys'}
      <h2>API keys</h2>
      <p class="lead">
        For apps and scripts that use Cineva as you. Send a key as <code>Authorization: Bearer &lt;key&gt;</code>;
        the <a href="/api/docs/" target="_blank" rel="noopener">API reference</a> lists what it can do.
      </p>
      {#if revealed}
        <div class="card reveal">
          <span class="st">Copy your new key now. It will not be shown again.</span>
          <code>{revealed}</code>
          <div class="btns">
            <button class="btn primary" onclick={copyKey}>{#if copied}<Check size={15} /> Copied{:else}<Copy size={15} /> Copy{/if}</button>
            <button class="btn quiet" onclick={() => (revealed = '')}>Done</button>
          </div>
        </div>
      {/if}
      <div class="card">
        {#each keys as k (k.id)}
          <div class="srow">
            <div class="sl">
              <span class="st">{k.name}</span>
              <span class="sd">Created {ago(k.createdAt)} · {k.lastUsedAt ? `last used ${ago(k.lastUsedAt)}` : 'never used'}</span>
            </div>
            <div class="sc"><button class="btn quiet" onclick={() => revoke(k.id)}>Revoke</button></div>
          </div>
        {:else}
          <p class="sd pad">No keys yet.</p>
        {/each}
        <form class="srow" onsubmit={addKey}>
          <label class="sl" for="s-key"><span class="st">New key</span><span class="sd">Name it after where it will be used.</span></label>
          <div class="sc inline">
            <input id="s-key" placeholder="For example, Phone" bind:value={keyName} />
            <button class="btn" type="submit">Create</button>
          </div>
        </form>
      </div>
      {#if keyMsg}<p class="msg">{keyMsg}</p>{/if}

    {:else if shown === 'about'}
      <h2>About</h2>
      <div class="card">
        <div class="srow col">
          <span class="st">Cineva</span>
          <span class="sd">Free software, licensed under the GNU Affero General Public License, version 3 or later.</span>
        </div>
        <!-- Required by TMDB's terms wherever their data is used. -->
        <div class="srow col">
          <span class="st">Metadata</span>
          <span class="sd">
            Film and programme details, artwork and cast come from
            <a href="https://www.themoviedb.org/" target="_blank" rel="noreferrer noopener">TMDB</a>.
            This product uses the TMDB API but is not endorsed or certified by TMDB.
          </span>
        </div>
        <div class="srow col">
          <span class="st">API</span>
          <span class="sd"><a href="/api/docs/" target="_blank" rel="noopener">API reference</a>, for building apps against this server.</span>
        </div>
      </div>
    {/if}
  </div>
</div>

{#snippet prefCard(section: string)}
  {#if prefs}
    <div class="card">
      {#each inSection(section) as p (p.key)}
        {@render prefRow(p)}
      {/each}
    </div>
  {:else}
    <p class="lead">Loading your preferences…</p>
  {/if}
{/snippet}

<!-- One preference, drawn from its registry entry: a switch, a list of
     choices, a number or text. Locked ones stay visible but cannot be
     changed; ones the viewer set say what Reset would go back to. -->
{#snippet prefRow(p: PrefEntry)}
  {@const r = prefs?.prefs[p.key]}
  {@const value = valueOf(p)}
  {@const hint = [p.options?.find((o) => o.value === value)?.description, p.description].filter(Boolean).join(' ')}
  {@const id = `s-${p.key}`}
  {@const locked = !!r?.locked}
  <div class="srow" class:tog={p.type === 'bool'}>
    <div class="sl">
      <label class="st" id="{id}-l" for={id}>
        {p.label}{#if p.scope === 'device'}<span class="tag">This device</span>{/if}
      </label>
      {#if hint}<span class="sd">{hint}</span>{/if}
      {#if locked}
        <span class="sd lock"><Lock size={13} /> Set by your administrator</span>
      {:else if fromAdmin(r)}
        <span class="sd">Your administrator's default.</span>
      {:else if r?.default && r.default.value !== value}
        <span class="sd">
          <button class="reset" onclick={() => resetPref(p.key)}>Reset</button>
          to {fromAdmin(r.default) ? "your administrator's default" : 'the default'}, {choiceLabel(p, r.default.value) || 'none'}
        </span>
      {/if}
    </div>
    <div class="sc">
      {#if p.type === 'bool'}
        <button {id} class="switch" role="switch" aria-checked={value === 'on'} aria-labelledby="{id}-l" disabled={locked}
          onclick={() => save(p.key, value === 'on' ? 'off' : 'on')}><span></span></button>
      {:else if p.type === 'enum'}
        <select {id} {value} disabled={locked} onchange={(e) => save(p.key, (e.currentTarget as HTMLSelectElement).value)}>
          {#each p.options ?? [] as o (o.value)}<option value={o.value}>{o.label}</option>{/each}
        </select>
      {:else if p.type === 'int'}
        <input {id} type="number" {value} min={p.min} max={p.max} disabled={locked}
          onchange={(e) => save(p.key, (e.currentTarget as HTMLInputElement).value)} />
      {:else}
        <input {id} {value} disabled={locked} onchange={(e) => save(p.key, (e.currentTarget as HTMLInputElement).value.trim())} />
      {/if}
    </div>
  </div>
{/snippet}

{#snippet deviceRow(d: Device)}
  <div class="dev">
    {#if isPhone(d)}<Smartphone size={20} />{:else}<MonitorSmartphone size={20} />{/if}
    <div class="dev-text">
      <span class="dev-name">{deviceTitle(d)}{#if d.online}<span class="tag on">open now</span>{/if}</span>
      <span class="sd">{[d.name ? d.client : '', where(d), d.online ? '' : `last used ${ago(d.lastSeenAt)}`].filter(Boolean).join(' · ')}</span>
    </div>
    <button class="btn quiet" onclick={() => signOut(d)}>Sign out</button>
    <button class="btn quiet" onclick={() => forget(d)}>{forgetAsk === d.id ? 'Forget it?' : 'Forget'}</button>
  </div>
{/snippet}

<style>
  .settings {
    display: grid;
    grid-template-columns: 15rem minmax(0, 46rem);
    gap: 2.5rem;
    align-items: start;
    justify-content: center;
    padding-bottom: 3rem;
  }
  h1 { font-size: 1.5rem; margin: 0.2rem 0 1rem 0.6rem; }
  h2 { font-size: 1.3rem; margin: 0.35rem 0 1rem; }
  h3 { font-size: 0.8rem; font-weight: 600; text-transform: uppercase; letter-spacing: 0.05em; color: var(--muted); margin: 1.4rem 0 0.5rem; }
  .lead { color: var(--muted); font-size: 0.9rem; line-height: 1.5; margin: -0.4rem 0 1rem; }
  a { color: var(--accent-2); }
  code { font-size: 0.85em; }

  /* ---- Section list ---- */
  .snav { position: sticky; top: 1rem; }
  .snav ul { list-style: none; display: flex; flex-direction: column; gap: 2px; }
  .snav-item {
    width: 100%; display: flex; align-items: center; gap: 0.7rem;
    padding: 0.55rem 0.75rem; border-radius: 10px; color: var(--muted);
    text-decoration: none; font-size: 0.92rem; text-align: left;
  }
  .snav-item span { flex: 1; }
  .snav-item:hover { color: var(--text); background: color-mix(in srgb, var(--text) 6%, transparent); }
  .snav-item.sel { color: var(--text); background: color-mix(in srgb, var(--accent) 16%, transparent); }
  .snav-item :global(.chev) { display: none; }
  .snav .sep { margin-top: 0.6rem; padding-top: 0.6rem; border-top: 1px solid var(--border); }

  /* ---- Rows ---- */
  .card { background: var(--panel); border: 1px solid var(--border); border-radius: 14px; overflow: hidden; }
  .srow {
    display: flex; align-items: center; gap: 1.5rem; padding: 0.9rem 1.1rem;
  }
  .card > * + * { border-top: 1px solid var(--border); }
  .srow.col { flex-direction: column; align-items: stretch; gap: 0.75rem; }
  .sl { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.2rem; }
  .st { font-size: 0.95rem; color: var(--text); }
  .sd { font-size: 0.82rem; color: var(--muted); line-height: 1.45; }
  .sc { flex: 0 0 auto; display: flex; justify-content: flex-end; }
  .inline { display: flex; gap: 0.5rem; align-items: center; }
  .inline.grow { flex: 1; }
  .inline.grow input { flex: 1; min-width: 0; }
  .stack { display: flex; flex-direction: column; gap: 0.5rem; max-width: 24rem; }
  .btns { display: flex; gap: 0.5rem; flex-wrap: wrap; align-items: center; }
  .btns.after { margin-top: 1rem; }
  .pad { padding: 0 1.1rem 0.9rem; border-top: none !important; }
  .msg { font-size: 0.85rem; color: var(--muted); }
  p.msg:not(.pad) { margin-top: 0.75rem; }
  .hint { font-size: 0.8rem; color: var(--muted); }

  input, select {
    background: var(--bg); border: 1px solid var(--border); border-radius: 9px;
    color: var(--text); padding: 0.5rem 0.7rem; font: inherit; font-size: 0.9rem;
  }
  .sc input { width: 17rem; }
  select { width: 17rem; max-width: 100%; cursor: pointer; }
  input:focus, select:focus { outline: none; border-color: var(--accent); }

  .btn {
    display: inline-flex; align-items: center; gap: 0.4rem; white-space: nowrap;
    padding: 0.45rem 0.9rem; border-radius: 9px; font-size: 0.88rem;
    border: 1px solid var(--border); color: var(--text); background: var(--bg);
  }
  .btn:hover:not(:disabled) { border-color: color-mix(in srgb, var(--text) 30%, var(--border)); }
  .btn:disabled { opacity: 0.45; cursor: default; }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); }
  .btn.quiet { background: none; color: var(--muted); }
  .btn.quiet:hover { color: var(--text); }
  .btn.danger { border-color: var(--err); color: var(--err); }

  /* ---- Profile ---- */
  .pic { align-items: flex-start; }
  .face {
    flex: 0 0 auto; width: 5.5rem; height: 5.5rem; border-radius: 16px; display: grid; place-items: center;
    font-size: 2.2rem; font-weight: 600; color: var(--on-accent); object-fit: cover;
    background: linear-gradient(135deg, var(--accent), var(--accent-2));
  }
  .face.tinted { background: var(--face); color: var(--bg); }
  .pic-side { display: flex; flex-direction: column; gap: 0.7rem; min-width: 0; }
  .upload { position: relative; cursor: pointer; }
  .upload input { position: absolute; inset: 0; opacity: 0; cursor: pointer; width: 100%; }
  .upload.busy { opacity: 0.6; }
  .swatches { display: flex; gap: 0.5rem; flex-wrap: wrap; }
  .swatch {
    width: 1.75rem; height: 1.75rem; border-radius: 50%;
    background: var(--face, linear-gradient(135deg, var(--accent), var(--accent-2)));
    border: 2px solid transparent; outline: 2px solid transparent; outline-offset: 1px;
  }
  .swatch.sel { outline-color: var(--text); }

  /* ---- Appearance ----
     Each theme card is a small screen drawn in that theme's own tokens,
     handed over as --p-* custom properties (previewStyle); the page's own
     tokens stand in for any a theme does not define. At least two to a row,
     even at phone width. */
  .tgrid { display: grid; grid-template-columns: repeat(auto-fill, minmax(min(10rem, calc(50% - 0.4rem)), 1fr)); gap: 0.8rem; }
  .tcard {
    display: flex; flex-direction: column; gap: 0.55rem; padding: 0.5rem 0.5rem 0.6rem; min-width: 0;
    border-radius: 12px; border: 1px solid var(--border); background: var(--bg); color: var(--muted);
    transition: border-color 0.15s ease, transform 0.15s ease;
  }
  .tcard:hover { border-color: color-mix(in srgb, var(--accent) 45%, var(--border)); }
  .tcard.sel { border-color: var(--accent); color: var(--text); background: color-mix(in srgb, var(--accent) 12%, var(--bg)); }
  .tname { display: flex; align-items: center; gap: 0.45rem; padding: 0 0.2rem; font-size: 0.9rem; }
  .tname span { flex: 1; min-width: 0; text-align: left; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
  .tcard :global(.tick) { color: var(--accent); flex: none; }
  /* Sized from the card's width, so it keeps its shape at any size. */
  .mini {
    --p-scrim: color-mix(in srgb, var(--p-bg, var(--bg)) calc(var(--p-dim, 1) * 100%), transparent);
    display: flex; flex-direction: column; overflow: hidden;
    border-radius: 7px; border: 1px solid var(--p-border, var(--border));
    background: linear-gradient(var(--p-scrim), var(--p-scrim)), var(--p-image, none) center / cover, var(--p-bg, var(--bg));
  }
  .mini.plus { aspect-ratio: 16 / 10.4; display: grid; place-items: center; color: var(--muted); border-style: dashed; background: none; }
  .tslot { position: relative; display: flex; min-width: 0; }
  .tslot .tcard { flex: 1; }
  .tbadge {
    align-self: flex-start; margin: -0.2rem 0.2rem 0; padding: 0.05rem 0.45rem; border-radius: 999px;
    font-size: 0.72rem; color: var(--muted); border: 1px solid var(--border);
  }
  .tbadge.now { color: var(--on-accent); background: var(--accent); border-color: var(--accent); }
  .tedit {
    position: absolute; top: 0.9rem; right: 0.9rem; display: grid; place-items: center;
    width: 1.9rem; height: 1.9rem; border-radius: 50%;
    background: var(--panel); color: var(--text); border: 1px solid var(--border);
  }
  .tedit:hover { border-color: var(--accent); }
  .holnote { margin: 0 0 0.2rem; font-size: 0.85rem; color: var(--muted); }
  .holnote .linkish { color: var(--accent-2); font-size: inherit; padding: 0; }
  .mini-bar {
    display: flex; align-items: center; gap: 5%; padding: 4% 7%;
    background: var(--p-panel, var(--panel)); border-bottom: 1px solid var(--p-border, var(--border));
  }
  .mini-logo { flex: none; width: 7%; aspect-ratio: 1; border-radius: 50%; background: var(--p-accent, var(--accent)); }
  .mini-body { display: flex; flex-direction: column; align-items: flex-start; padding: 6% 7% 7%; }
  .mini-body > * + * { margin-top: 5%; }
  .mini-line { height: 0.2rem; width: 55%; border-radius: 999px; background: var(--p-muted, var(--muted)); }
  .mini-line.title { width: 40%; height: 0.3rem; background: var(--p-text, var(--text)); }
  .mini-line.short { width: 30%; }
  .mini-posters { align-self: stretch; display: grid; grid-template-columns: repeat(5, 1fr); gap: 0.2rem; }
  .mini-posters span {
    aspect-ratio: 2 / 3; border-radius: 3px; border: 1px solid var(--p-border, var(--border));
    background: linear-gradient(160deg, color-mix(in srgb, var(--p-accent-2, var(--accent-2)) 40%, var(--p-panel, var(--panel))), var(--p-panel, var(--panel)));
  }
  .mini-btn { width: 28%; aspect-ratio: 4 / 1; border-radius: 999px; background: var(--p-accent, var(--accent)); }
  /* TV mode: the focused card keeps the ring every focus wears there and
     grows a little, as a focused poster does; the row's padding leaves it
     room, so neither is clipped. */
  :global(html.tv) .tcard:focus { outline: 0.18rem solid var(--accent-2); outline-offset: 0.18rem; transform: scale(1.04); }
  :global(html.tv) .tcard:focus .tname { color: var(--accent-2); }
  /* Plain choice cards, as for a notification's kind. */
  .themes { display: grid; grid-template-columns: repeat(auto-fill, minmax(9rem, 1fr)); gap: 0.6rem; }
  .theme {
    display: flex; align-items: center; gap: 0.55rem; padding: 0.75rem 0.9rem;
    border-radius: 11px; border: 1px solid var(--border); background: var(--bg); color: var(--muted);
  }
  .theme span { flex: 1; text-align: left; }
  .theme.sel { border-color: var(--accent); color: var(--text); background: color-mix(in srgb, var(--accent) 14%, var(--bg)); }
  .theme :global(.tick) { color: var(--accent); }

  /* ---- Toggle ---- */
  .switch {
    width: 2.6rem; height: 1.5rem; border-radius: 999px; padding: 2px;
    background: var(--border); display: flex; transition: background 0.15s;
  }
  .switch span { width: calc(1.5rem - 4px); height: calc(1.5rem - 4px); border-radius: 50%; background: var(--text); transition: transform 0.15s; }
  .switch[aria-checked='true'] { background: var(--accent); }
  .switch[aria-checked='true'] span { transform: translateX(1.1rem); }
  .switch:disabled, select:disabled, input:disabled { opacity: 0.5; cursor: not-allowed; }

  /* ---- Preferences ---- */
  label.st { cursor: default; }
  .st .tag { margin-left: 0.5rem; vertical-align: 0.1em; }
  .lock { display: inline-flex; align-items: center; gap: 0.3rem; }
  .reset { color: var(--accent-2); font-size: inherit; text-decoration: underline; text-underline-offset: 2px; }

  /* ---- Devices ---- */
  .dev { display: flex; align-items: center; gap: 0.9rem; padding: 0.8rem 1.1rem; color: var(--muted); }
  .dev-text { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.15rem; }
  .dev-name { color: var(--text); font-size: 0.95rem; display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; }
  .tag { font-size: 0.7rem; padding: 0.05rem 0.45rem; border-radius: 999px; border: 1px solid var(--border); color: var(--muted); }
  .tag.on { border-color: color-mix(in srgb, var(--ok) 50%, transparent); color: var(--ok); }
  .fold { display: flex; align-items: center; gap: 0.4rem; color: var(--muted); font-size: 0.88rem; margin: 1.1rem 0 0.5rem; }
  .fold:hover { color: var(--text); }
  .fold :global(svg) { transition: transform 0.15s; }
  .fold :global(.down) { transform: rotate(90deg); }

  /* ---- API keys ---- */
  .reveal { padding: 1rem 1.1rem; display: flex; flex-direction: column; gap: 0.7rem; margin-bottom: 1rem; border-color: var(--accent); }
  .reveal code { word-break: break-all; background: var(--bg); padding: 0.6rem 0.7rem; border-radius: 8px; border: 1px solid var(--border); }
  .reveal > * + * { border-top: none; }

  /* ---- Notifications ---- */
  .dest { display: flex; flex-direction: column; gap: 0.6rem; padding: 0.85rem 1.1rem; }
  .dest-head { display: flex; align-items: flex-start; gap: 0.9rem; }
  .dest-head .switch { flex: none; margin-top: 0.1rem; }
  .dest .dev-text { overflow-wrap: anywhere; }
  .sd.bad, .msg.bad { color: var(--err); }
  .sd.good { color: var(--ok); }
  .test { flex-basis: 100%; overflow-wrap: anywhere; }
  .pad.first { padding-top: 0.9rem; }
  .nform { display: flex; flex-direction: column; gap: 1rem; }
  .field { display: flex; flex-direction: column; gap: 0.35rem; min-width: 0; }
  .field.row { flex-direction: row; align-items: center; justify-content: space-between; }
  .field input { width: 100%; min-width: 0; }
  .nform .theme span { min-width: 0; overflow-wrap: anywhere; }
  .opt { margin-left: 0.45rem; font-size: 0.78rem; color: var(--muted); }
  fieldset.field { border: none; padding: 0; margin: 0; gap: 0.45rem; }
  fieldset.field legend { padding: 0; margin-bottom: 0.35rem; }
  .check { display: flex; align-items: center; gap: 0.6rem; font-size: 0.9rem; color: var(--text); cursor: pointer; }
  .check input { width: 1.05rem; height: 1.05rem; padding: 0; margin: 0; flex: none; accent-color: var(--accent); }

  .back { display: none; }

  /* ---- Phone: the list, or one section with a way back ---- */
  @media (max-width: 48rem) {
    .settings { grid-template-columns: minmax(0, 1fr); gap: 0; }
    .settings.has-open .snav { display: none; }
    .settings:not(.has-open) .spane { display: none; }
    .snav { position: static; }
    .snav ul { background: var(--panel); border: 1px solid var(--border); border-radius: 14px; overflow: hidden; gap: 0; }
    .snav li + li { border-top: 1px solid var(--border); }
    .snav .sep { margin-top: 0; padding-top: 0; }
    .snav-item { border-radius: 0; padding: 0.85rem 1rem; color: var(--text); font-size: 0.98rem; }
    .snav-item.sel { background: none; }
    .snav-item :global(.chev) { display: block; color: var(--muted); }
    .back { display: inline-flex; align-items: center; gap: 0.2rem; color: var(--accent-2); margin: 0 0 0.4rem -0.3rem; font-size: 0.95rem; }
    .srow { flex-direction: column; align-items: stretch; gap: 0.6rem; }
    .sc { justify-content: flex-start; }
    .sc input, select { width: 100%; min-width: 0; }
    .sc.inline input { flex: 1; width: auto; }
    .pic, .srow.tog { flex-direction: row; align-items: center; }
  }
  /* TV mode: the focused section fills with the accent, like the rail's
     (App.svelte). */
  :global(html.tv) .snav-item { transition: background-color 0.15s ease, color 0.15s ease; }
  :global(html.tv) .snav-item:focus { outline: none; background: var(--accent); color: var(--on-accent); }
</style>
