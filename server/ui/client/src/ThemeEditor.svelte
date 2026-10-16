<!--
  Making or changing a custom theme (docs/themes.md): its colours, a
  background picture and how much the background colour covers it, and an
  effect. The draft is shown on the whole app behind the dialogue as it is
  edited, and taken away again on Cancel. Contrast is checked as you go:
  a theme is allowed to be hard to read, but never by accident.
-->
<script lang="ts">
  import { onDestroy } from 'svelte'
  import { Check, TriangleAlert, ImagePlus, Trash2 } from '@lucide/svelte'
  import Sheet from './Sheet.svelte'
  import {
    TOKENS, contrast, createTheme, updateTheme, deleteTheme, uploadThemeBackground, removeThemeBackground,
    type Effect, type ThemeInfo,
  } from './lib/theme'

  let {
    themes,
    editing,
    startFrom,
    admin,
    onclose,
    onsaved,
  }: {
    themes: ThemeInfo[]
    /** The theme being changed; null to make a new one. */
    editing: ThemeInfo | null
    /** A new theme starts as a copy of this one. */
    startFrom: string
    admin: boolean
    onclose: () => void
    /** Saved (theme) or deleted (null). */
    onsaved: (t: ThemeInfo | null) => void
  } = $props()

  const hex = /^#[0-9a-f]{6}$/
  // Colours a theme file may write in other notations are taken only when
  // they are plain #rrggbb; anything else starts from the default theme's.
  function tokensOf(t: ThemeInfo | undefined): Record<string, string> {
    const fallback = themes.find((x) => x.id === 'midnight')?.tokens ?? {}
    const out: Record<string, string> = {}
    for (const { key } of TOKENS) {
      const v = (t?.tokens?.[key] ?? '').toLowerCase()
      out[key] = hex.test(v) ? v : (fallback[key] ?? '#808080').toLowerCase()
    }
    return out
  }

  // Seeded once from what the dialogue was opened with; edits are the
  // draft's own from then on.
  // svelte-ignore state_referenced_locally
  const base = editing ?? themes.find((t) => t.id === startFrom)
  // svelte-ignore state_referenced_locally
  let name = $state(editing?.name ?? '')
  let tokens = $state(tokensOf(base))
  let dim = $state(base?.dim ?? 70)
  let fx = $state<Effect>(base?.effect ?? '')
  // svelte-ignore state_referenced_locally
  let household = $state(editing?.household ?? false)
  // The picture: the saved one, a newly chosen file, or taken away.
  // svelte-ignore state_referenced_locally
  let picture = $state(editing?.background ?? '')
  let file: File | null = $state(null)
  let removePicture = $state(false)
  let more = $state(false)
  let busy = $state(false)
  let err = $state('')
  let confirmDelete = $state(false)

  function pick(e: Event) {
    const f = (e.currentTarget as HTMLInputElement).files?.[0]
    if (!f) return
    if (picture.startsWith('blob:')) URL.revokeObjectURL(picture)
    file = f
    removePicture = false
    picture = URL.createObjectURL(f)
  }
  function dropPicture() {
    if (picture.startsWith('blob:')) URL.revokeObjectURL(picture)
    file = null
    picture = ''
    removePicture = true
  }

  // ---- Live preview on the page behind ----
  const root = document.documentElement
  const previewed = [...TOKENS.map((t) => `--${t.key}`), '--bg-image', '--bg-dim']
  $effect(() => {
    for (const { key } of TOKENS) if (hex.test(tokens[key])) root.style.setProperty(`--${key}`, tokens[key])
    root.style.setProperty('--bg-image', picture ? `url("${picture.replace(/["\\\\]/g, '')}")` : 'none')
    root.style.setProperty('--bg-dim', String(dim / 100))
  })
  onDestroy(() => {
    for (const p of previewed) root.style.removeProperty(p)
    if (picture.startsWith('blob:')) URL.revokeObjectURL(picture)
  })

  // ---- Readability ----
  const checks = $derived([
    { label: 'Text on the background', a: tokens.text, b: tokens.bg, min: 4.5 },
    { label: 'Text on panels', a: tokens.text, b: tokens.panel, min: 4.5 },
    { label: 'Quieter text on panels', a: tokens.muted, b: tokens.panel, min: 4.5 },
    { label: 'Text on the accent', a: tokens['on-accent'], b: tokens.accent, min: 4.5 },
    { label: 'Links on the background', a: tokens['accent-2'], b: tokens.bg, min: 3 },
  ].map((c) => ({ ...c, ratio: hex.test(c.a) && hex.test(c.b) ? contrast(c.a, c.b) : 0 })))

  async function save(e: SubmitEvent) {
    e.preventDefault()
    if (!name.trim()) {
      err = 'Give the theme a name.'
      return
    }
    busy = true
    err = ''
    try {
      const draft = { name: name.trim(), tokens: { ...tokens }, dim, effect: fx, household }
      let t = editing ? await updateTheme(editing.id, draft) : await createTheme(draft)
      if (file) t = await uploadThemeBackground(t.id, file)
      else if (removePicture && editing?.background) t = await removeThemeBackground(t.id)
      onsaved(t)
    } catch (ex) {
      err = ex instanceof Error ? ex.message : String(ex)
    } finally {
      busy = false
    }
  }

  async function remove() {
    if (!editing) return
    busy = true
    try {
      await deleteTheme(editing.id)
      onsaved(null)
    } catch (ex) {
      err = ex instanceof Error ? ex.message : String(ex)
      busy = false
    }
  }

  const effects: { id: Effect; label: string }[] = [
    { id: '', label: 'None' }, { id: 'snow', label: 'Snow' }, { id: 'leaves', label: 'Leaves' }, { id: 'blossom', label: 'Blossom' },
  ]
</script>

<Sheet title={editing ? `Edit ${editing.name}` : 'Make a theme'} onclose={onclose}>
  <form class="ed" onsubmit={save}>
    <label class="field">
      <span class="fl">Name</span>
      <input bind:value={name} maxlength="60" placeholder="Cinema red" required />
    </label>

    {#if !editing}
      <label class="field">
        <span class="fl">Start from</span>
        <select value={startFrom} onchange={(e) => {
          const t = themes.find((x) => x.id === (e.currentTarget as HTMLSelectElement).value)
          tokens = tokensOf(t)
          dim = t?.dim ?? 70
          fx = t?.effect ?? ''
        }}>
          {#each themes as t (t.id)}<option value={t.id}>{t.name}</option>{/each}
        </select>
      </label>
    {/if}

    <fieldset class="field">
      <legend class="fl">Colours</legend>
      <div class="colours">
        {#each TOKENS.filter((t) => more || !t.more) as t (t.key)}
          <label class="colour">
            <input type="color" bind:value={tokens[t.key]} aria-label={t.label} />
            <span class="cl">{t.label}</span>
            <span class="cv">{tokens[t.key]}</span>
          </label>
        {/each}
      </div>
      <button type="button" class="linkish" onclick={() => (more = !more)}>{more ? 'Fewer colours' : 'More colours'}</button>
    </fieldset>

    <ul class="checks" aria-label="Readability">
      {#each checks as c (c.label)}
        <li class:low={c.ratio < c.min}>
          {#if c.ratio >= c.min}<Check size={15} />{:else}<TriangleAlert size={15} />{/if}
          <span>{c.label}</span>
          <span class="ratio">{c.ratio.toFixed(1)}:1{#if c.ratio < c.min}{' '}— hard to read{/if}</span>
        </li>
      {/each}
    </ul>

    <fieldset class="field">
      <legend class="fl">Background picture</legend>
      <div class="pic">
        <span class="thumb theme-backdrop" aria-hidden="true"></span>
        <div class="picacts">
          <label class="btn upload">
            <ImagePlus size={15} /> {picture ? 'Choose another' : 'Choose a picture'}
            <input type="file" accept="image/jpeg,image/png,image/gif,image/webp" onchange={pick} />
          </label>
          {#if picture}<button type="button" class="btn quiet" onclick={dropPicture}>Remove</button>{/if}
        </div>
      </div>
      <label class="dim">
        <span>Covered by the background colour</span>
        <input type="range" min="0" max="95" step="5" bind:value={dim} />
        <span class="ratio">{dim}%</span>
      </label>
      <span class="hint">More covering keeps text and posters easy to read; less shows more of the picture.</span>
    </fieldset>

    <fieldset class="field">
      <legend class="fl">Effect</legend>
      <div class="seg" role="radiogroup" aria-label="Effect">
        {#each effects as f (f.id)}
          <button type="button" role="radio" aria-checked={fx === f.id} class:on={fx === f.id} onclick={() => (fx = f.id)}>{f.label}</button>
        {/each}
      </div>
    </fieldset>

    {#if admin}
      <label class="check">
        <input type="checkbox" bind:checked={household} />
        <span>Share with everyone<span class="hint block">Everyone can pick it, and it can be the household's default (Admin, Defaults).</span></span>
      </label>
    {/if}

    {#if err}<p class="err" role="alert">{err}</p>{/if}

    <div class="acts">
      {#if editing}
        {#if confirmDelete}
          <span class="hint">Delete {editing.name}? Anyone using it goes back to the default.</span>
          <button type="button" class="btn danger" disabled={busy} onclick={remove}><Trash2 size={15} /> Delete</button>
          <button type="button" class="btn quiet" onclick={() => (confirmDelete = false)}>Keep it</button>
        {:else}
          <button type="button" class="btn quiet danger-text" onclick={() => (confirmDelete = true)}><Trash2 size={15} /> Delete</button>
        {/if}
      {/if}
      <span class="spacer"></span>
      <button type="button" class="btn" onclick={onclose}>Cancel</button>
      <button type="submit" class="btn primary" disabled={busy}>{busy ? 'Saving…' : editing ? 'Save' : 'Make it'}</button>
    </div>
  </form>
</Sheet>

<style>
  .ed { display: flex; flex-direction: column; gap: 1rem; }
  .field { display: flex; flex-direction: column; gap: 0.45rem; border: 0; padding: 0; margin: 0; min-width: 0; }
  .fl { font-size: 0.85rem; color: var(--muted); padding: 0; }
  input:not([type]), select {
    background: var(--bg); color: var(--text); border: 1px solid var(--border); border-radius: 9px;
    padding: 0.5rem 0.7rem; font: inherit;
  }
  .colours { display: grid; grid-template-columns: repeat(auto-fill, minmax(11rem, 1fr)); gap: 0.4rem 0.8rem; }
  .colour { display: grid; grid-template-columns: auto 1fr; grid-template-rows: auto auto; column-gap: 0.6rem; align-items: center; }
  .colour input {
    grid-row: span 2; width: 2.4rem; height: 2.4rem; padding: 0; border: 1px solid var(--border); border-radius: 9px;
    background: none; cursor: pointer;
  }
  .cl { font-size: 0.88rem; }
  .cv { font-size: 0.75rem; color: var(--muted); font-variant-numeric: tabular-nums; }
  .linkish { align-self: flex-start; color: var(--accent-2); font-size: 0.85rem; padding: 0.2rem 0; }
  .checks { list-style: none; margin: 0; padding: 0.6rem 0.8rem; display: flex; flex-direction: column; gap: 0.3rem; border: 1px solid var(--border); border-radius: 10px; }
  .checks li { display: flex; align-items: center; gap: 0.5rem; font-size: 0.85rem; color: var(--ok); }
  .checks li span:first-of-type { flex: 1; color: var(--text); }
  .checks li.low { color: var(--err); }
  .ratio { font-size: 0.8rem; color: var(--muted); font-variant-numeric: tabular-nums; }
  .checks li.low .ratio { color: var(--err); }
  .pic { display: flex; align-items: center; gap: 0.9rem; flex-wrap: wrap; }
  .thumb { width: 8rem; aspect-ratio: 16 / 9; border-radius: 9px; border: 1px solid var(--border); flex: none; }
  .picacts { display: flex; gap: 0.4rem; flex-wrap: wrap; }
  .upload { position: relative; overflow: hidden; cursor: pointer; }
  .upload input { position: absolute; inset: 0; opacity: 0; cursor: pointer; }
  .dim { display: grid; grid-template-columns: 1fr auto; gap: 0.3rem 0.8rem; align-items: center; font-size: 0.88rem; }
  .dim input { grid-column: 1; accent-color: var(--accent); }
  .dim .ratio { grid-column: 2; grid-row: 2; }
  .hint { font-size: 0.8rem; color: var(--muted); }
  .hint.block { display: block; }
  .seg { display: flex; flex-wrap: wrap; gap: 0.4rem; }
  .seg button { padding: 0.4rem 0.9rem; border-radius: 999px; border: 1px solid var(--border); color: var(--muted); }
  .seg button.on { border-color: var(--accent); color: var(--text); background: color-mix(in srgb, var(--accent) 16%, transparent); }
  .check { display: flex; gap: 0.6rem; align-items: flex-start; font-size: 0.9rem; }
  .check input { margin-top: 0.2rem; accent-color: var(--accent); }
  .err { color: var(--err); font-size: 0.88rem; margin: 0; }
  .acts { display: flex; align-items: center; gap: 0.5rem; flex-wrap: wrap; border-top: 1px solid var(--border); padding-top: 0.9rem; }
  .spacer { flex: 1; }
  .btn {
    display: inline-flex; align-items: center; gap: 0.4rem; white-space: nowrap;
    padding: 0.45rem 0.9rem; border-radius: 9px; font-size: 0.88rem;
    border: 1px solid var(--border); color: var(--text); background: var(--bg);
  }
  .btn:disabled { opacity: 0.5; cursor: default; }
  .btn.primary { background: var(--accent); border-color: var(--accent); color: var(--on-accent); }
  .btn.quiet { background: none; border-color: transparent; color: var(--muted); }
  .btn.danger { border-color: var(--err); color: var(--err); }
  .btn.danger-text { color: var(--err); }
</style>
