<!--
  One Home Assistant entity's control: a light's switch and brightness, a
  blind's open, stop and close, an amplifier's power, volume and input, a
  scene's Run. Shared by the Rooms page and the player's room panel.

  A change shows at once and the event that follows corrects it (see act
  in lib/homeassistant). Sliders send when they are let go, not on every
  step of a drag: each send is a call to Home Assistant.
-->
<script lang="ts">
  import {
    Lightbulb, Power, Fan, ToggleRight, Blinds, Speaker, Sparkles, ScrollText,
    ArrowUp, ArrowDown, Square, Volume2, VolumeX, Play, Pause, Check,
  } from '@lucide/svelte'
  import type { HAAction, HAEntity } from '../lib/api'
  import { act } from '../lib/homeassistant.svelte'

  let { entity, disabled = false }: {
    entity: HAEntity
    /** Home Assistant is unreachable: shown, but nothing can be worked. */
    disabled?: boolean
  } = $props()

  const off = $derived(disabled || !entity.available)
  let err = $state('')
  let busy = $state(false)
  let ran = $state(false)
  // A slider's place while it is being dragged; null shows the entity's own.
  let brightDraft = $state<number | null>(null)
  let volDraft = $state<number | null>(null)

  const icons = {
    light: Lightbulb, switch: Power, fan: Fan, input_boolean: ToggleRight, cover: Blinds,
    media_player: Speaker, scene: Sparkles, script: ScrollText, camera: Power,
  }
  const Icon = $derived(icons[entity.domain] ?? Power)
  const toggles = $derived(['light', 'switch', 'fan', 'input_boolean', 'media_player'].includes(entity.domain))

  async function send(action: HAAction, expect: Partial<HAEntity> = {}) {
    if (off) return
    err = ''
    busy = true
    try {
      await act(entity, action, expect)
    } catch (e) {
      err = e instanceof Error ? e.message : String(e)
    } finally {
      busy = false
    }
  }

  function toggle() {
    const on = !entity.on
    void send({ service: on ? 'turn_on' : 'turn_off' }, { on, state: on ? 'on' : 'off' })
  }

  function setBrightness(e: Event) {
    const pct = Number((e.currentTarget as HTMLInputElement).value)
    brightDraft = null
    void send({ service: 'turn_on', brightnessPct: pct }, { brightnessPct: pct, on: pct > 0, state: pct > 0 ? 'on' : 'off' })
  }

  function setVolume(e: Event) {
    const v = Number((e.currentTarget as HTMLInputElement).value)
    volDraft = null
    void send({ service: 'volume_set', volume: v }, { volume: v })
  }

  function playPause() {
    const playing = entity.state === 'playing'
    void send({ service: 'media_play_pause' }, { state: playing ? 'paused' : 'playing' })
  }

  async function run() {
    await send({ service: 'turn_on' })
    if (!err) {
      ran = true
      setTimeout(() => (ran = false), 2000)
    }
  }

  // What the row says under the name.
  const line = $derived.by(() => {
    if (!entity.available) return 'Unavailable'
    switch (entity.domain) {
      case 'light':
        if (!entity.on) return 'Off'
        return entity.dimmable && entity.brightnessPct !== undefined ? `On · ${entity.brightnessPct}%` : 'On'
      case 'cover': {
        const s = coverState[entity.state] ?? entity.state
        return entity.position !== undefined && entity.state !== 'closed' ? `${s} · ${entity.position}%` : s
      }
      case 'media_player':
        if (!entity.on) return 'Off'
        return [entity.mediaTitle, entity.source].filter(Boolean).join(' · ') || stateWord(entity.state)
      case 'scene':
        return 'Scene'
      case 'script':
        return entity.state === 'on' ? 'Running' : 'Script'
      default:
        return entity.on ? 'On' : 'Off'
    }
  })
  const coverState: Record<string, string> = {
    open: 'Open', closed: 'Closed', opening: 'Opening', closing: 'Closing', stopped: 'Stopped',
  }
  const stateWord = (s: string) => (s ? s[0].toUpperCase() + s.slice(1).replace(/_/g, ' ') : '')
</script>

<div class="ent" class:unavailable={!entity.available}>
  <div class="ent-row">
    <span class="ent-icon" class:lit={entity.on && entity.available}><Icon size={18} /></span>
    <div class="ent-text">
      <span class="ent-name">{entity.name}</span>
      <span class="ent-line">{line}</span>
    </div>
    {#if toggles}
      <button class="switch" role="switch" aria-checked={!!entity.on} aria-label={entity.name}
        disabled={off} onclick={toggle}><span></span></button>
    {:else if entity.domain === 'cover'}
      <div class="btns">
        <button class="ib" title="Open" aria-label="Open {entity.name}" disabled={off} onclick={() => send({ service: 'open_cover' }, { state: 'opening' })}><ArrowUp size={16} /></button>
        <button class="ib" title="Stop" aria-label="Stop {entity.name}" disabled={off} onclick={() => send({ service: 'stop_cover' })}><Square size={14} /></button>
        <button class="ib" title="Close" aria-label="Close {entity.name}" disabled={off} onclick={() => send({ service: 'close_cover' }, { state: 'closing' })}><ArrowDown size={16} /></button>
      </div>
    {:else if entity.domain === 'scene' || entity.domain === 'script'}
      <button class="runbtn" disabled={off || busy} onclick={run}>
        {#if ran}<Check size={15} /> Done{:else}Run{/if}
      </button>
    {/if}
  </div>

  {#if entity.domain === 'light' && entity.dimmable && entity.on}
    <label class="slider">
      <span class="sr">Brightness of {entity.name}</span>
      <input type="range" min="1" max="100" step="1" disabled={off}
        value={brightDraft ?? entity.brightnessPct ?? 100}
        oninput={(e) => (brightDraft = Number((e.currentTarget as HTMLInputElement).value))}
        onchange={setBrightness} />
      <span class="val">{brightDraft ?? entity.brightnessPct ?? 100}%</span>
    </label>
  {/if}

  {#if entity.domain === 'media_player' && entity.on}
    <div class="media">
      {#if entity.volume !== undefined}
        <div class="slider">
          <button class="ib" title={entity.muted ? 'Unmute' : 'Mute'} aria-label="{entity.muted ? 'Unmute' : 'Mute'} {entity.name}"
            aria-pressed={!!entity.muted} disabled={off}
            onclick={() => send({ service: 'volume_mute', muted: !entity.muted }, { muted: !entity.muted })}>
            {#if entity.muted}<VolumeX size={16} />{:else}<Volume2 size={16} />{/if}
          </button>
          <input type="range" min="0" max="1" step="0.01" disabled={off} aria-label="Volume of {entity.name}"
            value={volDraft ?? entity.volume}
            oninput={(e) => (volDraft = Number((e.currentTarget as HTMLInputElement).value))}
            onchange={setVolume} />
          <span class="val">{Math.round((volDraft ?? entity.volume) * 100)}%</span>
        </div>
      {/if}
      <div class="media-row">
        <button class="ib" title={entity.state === 'playing' ? 'Pause' : 'Play'} aria-label="{entity.state === 'playing' ? 'Pause' : 'Play'} {entity.name}"
          disabled={off} onclick={playPause}>
          {#if entity.state === 'playing'}<Pause size={16} />{:else}<Play size={16} />{/if}
        </button>
        {#if entity.sources && entity.sources.length > 0}
          <select aria-label="Input of {entity.name}" disabled={off} value={entity.source ?? ''}
            onchange={(e) => {
              const source = (e.currentTarget as HTMLSelectElement).value
              if (source) void send({ service: 'select_source', source }, { source })
            }}>
            {#if !entity.source}<option value="">Choose an input</option>{/if}
            {#each entity.sources as s (s)}<option value={s}>{s}</option>{/each}
          </select>
        {/if}
      </div>
    </div>
  {/if}

  {#if err}<p class="ent-err" role="alert">{err}</p>{/if}
</div>

<style>
  .ent {
    display: flex;
    flex-direction: column;
    gap: 0.5rem;
    padding: 0.7rem 0.9rem;
    min-width: 0;
  }
  .ent.unavailable { opacity: 0.5; }
  .ent-row { display: flex; align-items: center; gap: 0.75rem; min-width: 0; }
  .ent-icon {
    display: grid;
    place-items: center;
    width: 2.25rem;
    height: 2.25rem;
    border-radius: 50%;
    flex: none;
    color: var(--muted);
    background: color-mix(in srgb, var(--text) 6%, transparent);
  }
  .ent-icon.lit { color: var(--accent-2); background: color-mix(in srgb, var(--accent-2) 16%, transparent); }
  .ent-text { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 0.1rem; }
  .ent-name {
    color: var(--text);
    font-size: 0.95rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .ent-line {
    color: var(--muted);
    font-size: 0.8rem;
    overflow: hidden;
    text-overflow: ellipsis;
    white-space: nowrap;
  }
  .btns, .media-row { display: flex; gap: 0.35rem; align-items: center; flex: none; }
  .media-row { flex-wrap: wrap; }
  .media { display: flex; flex-direction: column; gap: 0.5rem; }
  .ib {
    display: grid;
    place-items: center;
    width: 2.4rem;
    height: 2.4rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    color: var(--text);
    flex: none;
  }
  .ib:hover:not(:disabled) { border-color: var(--accent); }
  .ib:disabled, .runbtn:disabled, select:disabled { opacity: 0.5; cursor: default; }
  .runbtn {
    display: inline-flex;
    align-items: center;
    gap: 0.3rem;
    padding: 0.45rem 1rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    color: var(--accent-2);
    font-size: 0.88rem;
    flex: none;
  }
  .runbtn:hover:not(:disabled) { border-color: var(--accent); }
  select {
    flex: 1;
    min-width: 0;
    max-width: 16rem;
    padding: 0.5rem 0.6rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: var(--bg);
    color: var(--text);
    font: inherit;
    font-size: 0.88rem;
  }
  .slider { display: flex; align-items: center; gap: 0.6rem; min-width: 0; }
  .slider input[type='range'] { flex: 1; min-width: 0; accent-color: var(--accent); height: 1.75rem; }
  .slider input:disabled { opacity: 0.5; }
  .val { color: var(--muted); font-size: 0.8rem; width: 2.6rem; text-align: right; flex: none; }
  .sr {
    position: absolute;
    width: 1px;
    height: 1px;
    overflow: hidden;
    clip-path: inset(50%);
    white-space: nowrap;
  }
  /* The switch, as Settings draws it. */
  .switch {
    width: 2.6rem; height: 1.5rem; border-radius: 999px; padding: 2px; flex: none;
    background: var(--border); display: flex; transition: background 0.15s;
  }
  .switch span { width: calc(1.5rem - 4px); height: calc(1.5rem - 4px); border-radius: 50%; background: var(--text); transition: transform 0.15s; }
  .switch[aria-checked='true'] { background: var(--accent); }
  .switch[aria-checked='true'] span { transform: translateX(1.1rem); }
  .switch:disabled { opacity: 0.5; cursor: default; }
  .ent-err { margin: 0; color: var(--err); font-size: 0.82rem; }
  @media (prefers-reduced-motion: reduce) {
    .switch, .switch span { transition: none; }
  }
</style>
