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
    ChevronRight, ChevronLeft, ArrowUpRight, ShieldCheck, Upload, Sun, Moon, Check, Copy,
  } from '@lucide/svelte'
  import {
    AVATAR_COLOURS, QUALITIES, changePassword, createAPIKey, deleteAPIKey, listAPIKeys, nameDevice,
    removeAvatar, revokeDevice, revokeOtherDevices, setAvatarColour, setPIN, updateMe,
    type APIKey, type AvatarColour, type Device, type PlayQuality, type User,
  } from './lib/api'
  import { pref, setPref } from './lib/prefs'
  import type { ThemeInfo } from './lib/theme'

  type MusicQuality = 'original' | 'opus-128' | 'opus-64'

  let {
    me,
    section,
    onsection,
    onme,
    themes,
    theme,
    onsettheme,
    devices,
    onreloaddevices,
    onpickavatar,
    avatarBusy,
    avatarMsg,
    autoplay,
    onautoplay,
    musicQuality,
    onmusicquality,
  }: {
    me: User
    /** The open section's id, or '' for none (the list, on a phone). */
    section: string
    onsection: (id: string) => void
    /** The user as the server now has them, after a change made here. */
    onme: (u: User) => void
    themes: ThemeInfo[]
    theme: string
    onsettheme: (id: string) => void
    devices: Device[]
    onreloaddevices: () => void
    /** A photo was picked: App frames it (AvatarCrop) and uploads it. */
    onpickavatar: (file: File) => void
    avatarBusy: boolean
    avatarMsg: string
    autoplay: boolean
    onautoplay: (on: boolean) => void
    musicQuality: MusicQuality
    onmusicquality: (q: MusicQuality) => void
  } = $props()

  const SECTIONS = [
    { id: 'profile', label: 'Profile', icon: UserIcon },
    { id: 'playback', label: 'Playback', icon: MonitorPlay },
    { id: 'appearance', label: 'Appearance', icon: Palette },
    { id: 'security', label: 'Sign-in and security', icon: Lock },
    { id: 'devices', label: 'Devices', icon: MonitorSmartphone },
    { id: 'keys', label: 'API keys', icon: KeyRound },
    { id: 'about', label: 'About', icon: Info },
  ]
  // On a wide screen something is always open; on a phone '' is the list.
  const open = $derived(SECTIONS.some((s) => s.id === section) ? section : '')
  const shown = $derived(open || 'profile')
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

  // ---- Playback ----
  // Read when a title starts (quality, by App) or by the server when it
  // picks tracks (languages, subtitles), so setting one is all there is.
  let quality = $state(pref('quality', 'auto') as PlayQuality)
  let audioLang = $state(pref('audio-lang', ''))
  let subMode = $state(pref('sub-mode', 'off'))
  let subLang = $state(pref('sub-lang', ''))
  function save(key: string, value: string) {
    try {
      setPref(key, value)
    } catch {
      /* private browsing: the server copy is what matters */
    }
  }
  const QUALITY_HINTS: Record<PlayQuality, string> = {
    auto: 'Plays the file as it is when this device can, and converts it only when it must.',
    original: 'Always the untouched video. Some files will not play in some browsers.',
    '1080p': 'Always converts, to save bandwidth or suit a slow connection.',
    '720p': 'Always converts, to save bandwidth or suit a slow connection.',
    '480p': 'Always converts, to save bandwidth or suit a slow connection.',
  }
  const SUB_MODES = [
    { id: 'off', label: 'Off', hint: 'Switched on by hand in the player.' },
    { id: 'forced', label: 'Signs and foreign dialogue', hint: 'Only the short tracks that translate signs and lines in another language.' },
    { id: 'foreign', label: 'When the audio is in another language', hint: 'Full subtitles when a film is not in your language; otherwise only signs and foreign dialogue.' },
    { id: 'always', label: 'Always', hint: 'Full subtitles whenever there are some in your language.' },
  ]
  const LANGUAGES = [
    ['ar', 'Arabic'], ['zh', 'Chinese'], ['cs', 'Czech'], ['da', 'Danish'], ['nl', 'Dutch'], ['en', 'English'],
    ['fi', 'Finnish'], ['fr', 'French'], ['de', 'German'], ['el', 'Greek'], ['he', 'Hebrew'], ['hi', 'Hindi'],
    ['hu', 'Hungarian'], ['is', 'Icelandic'], ['id', 'Indonesian'], ['ga', 'Irish'], ['it', 'Italian'],
    ['ja', 'Japanese'], ['ko', 'Korean'], ['ms', 'Malay'], ['no', 'Norwegian'], ['pl', 'Polish'],
    ['pt', 'Portuguese'], ['ro', 'Romanian'], ['ru', 'Russian'], ['sk', 'Slovak'], ['es', 'Spanish'],
    ['sv', 'Swedish'], ['th', 'Thai'], ['tr', 'Turkish'], ['uk', 'Ukrainian'], ['vi', 'Vietnamese'], ['cy', 'Welsh'],
  ] as const
  const MUSIC_QUALITIES: { id: MusicQuality; label: string }[] = [
    { id: 'original', label: 'Original' }, { id: 'opus-128', label: 'High (Opus 128k)' }, { id: 'opus-64', label: 'Data saver (Opus 64k)' },
  ]

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
      await nameDevice(deviceName.trim())
      renaming = false
      deviceMsg = ''
      onreloaddevices()
    } catch (err) {
      deviceMsg = errText(err)
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
      {#if me.role === 'admin'}
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
      <div class="card">
        <div class="srow">
          <label class="sl" for="s-quality">
            <span class="st">Video quality</span>
            <span class="sd">{QUALITY_HINTS[quality]} Changing it in the player changes it here too.</span>
          </label>
          <div class="sc">
            <select id="s-quality" bind:value={quality} onchange={() => save('quality', quality)}>
              {#each QUALITIES as q (q.id)}<option value={q.id}>{q.id === 'auto' ? 'Auto (recommended)' : q.label}</option>{/each}
            </select>
          </div>
        </div>
        <div class="srow">
          <label class="sl" for="s-audio">
            <span class="st">Audio language</span>
            <span class="sd">Played when a file has it; otherwise the file's own choice.</span>
          </label>
          <div class="sc">
            <select id="s-audio" bind:value={audioLang} onchange={() => save('audio-lang', audioLang)}>
              <option value="">The file's default</option>
              {#each LANGUAGES as [code, name] (code)}<option value={code}>{name}</option>{/each}
            </select>
          </div>
        </div>
        <div class="srow">
          <label class="sl" for="s-subs">
            <span class="st">Subtitles</span>
            <span class="sd">{SUB_MODES.find((m) => m.id === subMode)?.hint}</span>
          </label>
          <div class="sc">
            <select id="s-subs" bind:value={subMode} onchange={() => save('sub-mode', subMode)}>
              {#each SUB_MODES as m (m.id)}<option value={m.id}>{m.label}</option>{/each}
            </select>
          </div>
        </div>
        {#if subMode !== 'off'}
          <div class="srow">
            <label class="sl" for="s-sublang">
              <span class="st">Subtitle language</span>
              <span class="sd">Picture-based subtitles, common on disc copies, are drawn into the video, which means converting it.</span>
            </label>
            <div class="sc">
              <select id="s-sublang" bind:value={subLang} onchange={() => save('sub-lang', subLang)}>
                <option value="">{audioLang ? 'Same as audio' : 'Any'}</option>
                {#each LANGUAGES as [code, name] (code)}<option value={code}>{name}</option>{/each}
              </select>
            </div>
          </div>
        {/if}
        <div class="srow tog">
          <div class="sl">
            <span class="st" id="s-autoplay">Play the next episode</span>
            <span class="sd">Starts the next episode when one ends, after a short countdown you can stop.</span>
          </div>
          <div class="sc">
            <button class="switch" role="switch" aria-checked={autoplay} aria-labelledby="s-autoplay" onclick={() => onautoplay(!autoplay)}><span></span></button>
          </div>
        </div>
        <div class="srow">
          <label class="sl" for="s-music">
            <span class="st">Music quality</span>
            <span class="sd">Data saver streams a smaller copy, for phones on mobile data. From the next track.</span>
          </label>
          <div class="sc">
            <select id="s-music" value={musicQuality} onchange={(e) => onmusicquality((e.currentTarget as HTMLSelectElement).value as MusicQuality)}>
              {#each MUSIC_QUALITIES as q (q.id)}<option value={q.id}>{q.label}</option>{/each}
            </select>
          </div>
        </div>
      </div>

    {:else if shown === 'appearance'}
      <h2>Appearance</h2>
      <div class="card">
        <div class="srow col">
          <div class="sl">
            <span class="st">Theme</span>
            <span class="sd">Follows you to every device you sign in on.</span>
          </div>
          <div class="themes" role="radiogroup" aria-label="Theme">
            {#each themes as t (t.id)}
              <button class="theme" class:sel={theme === t.id} role="radio" aria-checked={theme === t.id} onclick={() => onsettheme(t.id)}>
                {#if t.appearance === 'light'}<Sun size={18} />{:else}<Moon size={18} />{/if}
                <span>{t.name}</span>
                {#if theme === t.id}<Check size={15} class="tick" />{/if}
              </button>
            {/each}
          </div>
        </div>
      </div>

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
      <p class="lead">Every browser signed in as you. Sign one out if it is lost or not yours.</p>
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

{#snippet deviceRow(d: Device)}
  <div class="dev">
    {#if isPhone(d)}<Smartphone size={20} />{:else}<MonitorSmartphone size={20} />{/if}
    <div class="dev-text">
      <span class="dev-name">{deviceTitle(d)}{#if d.online}<span class="tag on">open now</span>{/if}</span>
      <span class="sd">{[d.name ? d.client : '', where(d), d.online ? '' : `last used ${ago(d.lastSeenAt)}`].filter(Boolean).join(' · ')}</span>
    </div>
    <button class="btn quiet" onclick={() => signOut(d)}>Sign out</button>
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

  /* ---- Appearance ---- */
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
</style>
