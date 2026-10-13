<!--
  Framing a photo before it becomes an avatar. The server keeps the centre
  square of whatever it is sent, which cuts the wrong person out of a group
  photo; here the picture is dragged (and zoomed) inside a square frame, and
  only that square is uploaded, drawn on a canvas at 512 px — twice what the
  server keeps, so its own resize still has detail to work from.

  The same file lives in the client and the admin UI (two separate builds),
  so it uses theme tokens and plain words rather than either UI's icons or
  button classes. Keep the two copies identical.
-->
<script lang="ts">
  import { onMount } from 'svelte'

  let {
    file,
    busy = false,
    oncrop,
    oncancel,
  }: {
    file: File
    /** The upload is in flight: the buttons wait. */
    busy?: boolean
    /** The framed square, as a JPEG. */
    oncrop: (picture: Blob) => void
    oncancel: () => void
  } = $props()

  const OUT = 512 // pixels a side of what is uploaded
  const MAX_ZOOM = 4

  let frame: HTMLDivElement
  let dialog: HTMLDivElement
  let img = $state<HTMLImageElement | null>(null)
  let url = ''
  let failed = $state(false)
  let size = $state(0) // the frame's side in CSS pixels
  let zoom = $state(1) // 1 = the picture just covers the frame
  let x = $state(0) // the picture's top-left corner within the frame
  let y = $state(0)

  // The scale at which the picture's shorter side fills the frame.
  const base = $derived(img && size ? size / Math.min(img.naturalWidth, img.naturalHeight) : 1)
  const scale = $derived(base * zoom)
  const width = $derived(img ? img.naturalWidth * scale : 0)
  const height = $derived(img ? img.naturalHeight * scale : 0)

  onMount(() => {
    url = URL.createObjectURL(file)
    const im = new Image()
    im.onload = () => {
      size = frame?.clientWidth ?? 0
      img = im
      // Centred, as the server would have taken it.
      x = (size - im.naturalWidth * (size / Math.min(im.naturalWidth, im.naturalHeight))) / 2
      y = (size - im.naturalHeight * (size / Math.min(im.naturalWidth, im.naturalHeight))) / 2
    }
    // A format this browser cannot draw (HEIC, say): the file can still go
    // up as it is, and the server says whether it can read it.
    im.onerror = () => (failed = true)
    im.src = url
    dialog?.focus()
    const resize = new ResizeObserver(() => {
      if (!frame || !img) return
      const next = frame.clientWidth
      if (next && next !== size) {
        const k = next / size
        size = next
        x *= k
        y *= k
        clamp()
      }
    })
    resize.observe(frame)
    return () => {
      resize.disconnect()
      URL.revokeObjectURL(url)
    }
  })

  /** Keep the frame covered: the picture's edges never come inside it. */
  function clamp() {
    x = Math.min(0, Math.max(size - width, x))
    y = Math.min(0, Math.max(size - height, y))
  }

  /** Zoom to z, keeping the point (fx, fy) of the frame where it is. */
  function zoomTo(z: number, fx = size / 2, fy = size / 2) {
    const next = Math.min(MAX_ZOOM, Math.max(1, z))
    const k = next / zoom
    x = fx - (fx - x) * k
    y = fy - (fy - y) * k
    zoom = next
    clamp()
  }

  // ---- Dragging, and pinching on a touch screen ----
  const pointers = new Map<number, { x: number; y: number }>()
  let pinch = 0 // distance between two fingers when the pinch began, or 0

  function onPointerDown(e: PointerEvent) {
    if (!img) return
    frame.setPointerCapture(e.pointerId)
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (pointers.size === 2) pinch = spread()
  }
  function onPointerMove(e: PointerEvent) {
    const last = pointers.get(e.pointerId)
    if (!last) return
    if (pointers.size === 1) {
      x += e.clientX - last.x
      y += e.clientY - last.y
      clamp()
    }
    pointers.set(e.pointerId, { x: e.clientX, y: e.clientY })
    if (pointers.size === 2 && pinch > 0) {
      const now = spread()
      const r = frame.getBoundingClientRect()
      const [a, b] = [...pointers.values()]
      zoomTo(zoom * (now / pinch), (a.x + b.x) / 2 - r.left, (a.y + b.y) / 2 - r.top)
      pinch = now
    }
  }
  function onPointerUp(e: PointerEvent) {
    pointers.delete(e.pointerId)
    if (pointers.size < 2) pinch = 0
  }
  function spread(): number {
    const [a, b] = [...pointers.values()]
    return Math.hypot(a.x - b.x, a.y - b.y)
  }

  function onWheel(e: WheelEvent) {
    if (!img) return
    e.preventDefault()
    const r = frame.getBoundingClientRect()
    zoomTo(zoom * Math.exp(-e.deltaY / 400), e.clientX - r.left, e.clientY - r.top)
  }

  // Arrow keys move the picture, + and - zoom; Escape cancels.
  function onKeydown(e: KeyboardEvent) {
    if (e.key === 'Escape') {
      e.preventDefault()
      e.stopPropagation()
      if (!busy) oncancel()
      return
    }
    if (!img || (e.target as HTMLElement).tagName === 'INPUT') return
    const step = e.shiftKey ? 40 : 10
    const moves: Record<string, [number, number]> = {
      ArrowLeft: [step, 0], ArrowRight: [-step, 0], ArrowUp: [0, step], ArrowDown: [0, -step],
    }
    if (moves[e.key]) {
      e.preventDefault()
      x += moves[e.key][0]
      y += moves[e.key][1]
      clamp()
    } else if (e.key === '+' || e.key === '=') {
      zoomTo(zoom * 1.1)
    } else if (e.key === '-') {
      zoomTo(zoom / 1.1)
    }
  }

  function save() {
    if (!img || busy) return
    const canvas = document.createElement('canvas')
    canvas.width = canvas.height = OUT
    const ctx = canvas.getContext('2d')
    if (!ctx) return oncrop(file)
    ctx.imageSmoothingQuality = 'high'
    // The frame's square, in the picture's own pixels.
    const side = size / scale
    ctx.drawImage(img, -x / scale, -y / scale, side, side, 0, 0, OUT, OUT)
    canvas.toBlob((blob) => oncrop(blob ?? file), 'image/jpeg', 0.92)
  }
</script>

<div class="crop-back" role="presentation" onclick={() => !busy && oncancel()}></div>
<div
  class="crop"
  role="dialog"
  aria-modal="true"
  aria-label="Frame your photo"
  tabindex="-1"
  bind:this={dialog}
  onkeydown={onKeydown}
>
  <h2>Frame your photo</h2>
  {#if failed}
    <p class="note">This browser cannot show that picture, so it cannot be framed here. It can go up as it is, and the server keeps its centre.</p>
  {:else}
    <p class="note">Drag the photo to frame it; pinch, scroll or use the slider to zoom.</p>
  {/if}
  <div
    class="frame"
    class:ready={!!img}
    bind:this={frame}
    onpointerdown={onPointerDown}
    onpointermove={onPointerMove}
    onpointerup={onPointerUp}
    onpointercancel={onPointerUp}
    onwheel={onWheel}
    role="img"
    aria-label="The photo inside the frame"
  >
    {#if img}
      <img
        src={img.src}
        alt=""
        draggable="false"
        style="width: {width}px; height: {height}px; transform: translate({x}px, {y}px);"
      />
    {/if}
    <!-- Where the round avatar falls; the corners still go up, for any
         place that draws it square. -->
    <div class="ring" aria-hidden="true"></div>
  </div>
  {#if !failed}
    <label class="zoom">
      <span>Zoom</span>
      <input
        type="range"
        min="1"
        max={MAX_ZOOM}
        step="0.01"
        value={zoom}
        oninput={(e) => zoomTo(Number((e.currentTarget as HTMLInputElement).value))}
        disabled={!img}
      />
    </label>
  {/if}
  <div class="foot">
    <button type="button" class="btn" onclick={oncancel} disabled={busy}>Cancel</button>
    {#if failed}
      <button type="button" class="btn primary" onclick={() => oncrop(file)} disabled={busy}>{busy ? 'Uploading…' : 'Upload as it is'}</button>
    {:else}
      <button type="button" class="btn primary" onclick={save} disabled={busy || !img}>{busy ? 'Uploading…' : 'Use photo'}</button>
    {/if}
  </div>
</div>

<style>
  .crop-back {
    position: fixed;
    inset: 0;
    background: color-mix(in srgb, var(--bg) 78%, transparent);
    z-index: 400;
  }
  .crop {
    position: fixed;
    top: 50%;
    left: 50%;
    transform: translate(-50%, -50%);
    width: min(24rem, calc(100vw - 2rem));
    max-height: calc(100dvh - 2rem);
    overflow-y: auto;
    padding: 1rem;
    border-radius: 14px;
    border: 1px solid var(--border);
    background: var(--panel);
    color: var(--text);
    z-index: 401;
    display: flex;
    flex-direction: column;
    gap: 0.75rem;
    outline: none;
  }
  h2 { margin: 0; font-size: 1.05rem; }
  .note { margin: 0; color: var(--muted); font-size: 0.85rem; line-height: 1.4; }
  .frame {
    position: relative;
    width: 100%;
    aspect-ratio: 1;
    overflow: hidden;
    border-radius: 8px;
    background: var(--bg);
    touch-action: none; /* the drag and pinch are ours, not the page's */
    user-select: none;
  }
  .frame.ready { cursor: grab; }
  .frame.ready:active { cursor: grabbing; }
  .frame img {
    position: absolute;
    top: 0;
    left: 0;
    max-width: none;
    transform-origin: 0 0;
    pointer-events: none;
  }
  /* Dims what falls outside the round avatar, and marks its edge. */
  .ring {
    position: absolute;
    inset: 0;
    border-radius: 50%;
    box-shadow: 0 0 0 100vmax color-mix(in srgb, var(--bg) 55%, transparent);
    outline: 2px solid color-mix(in srgb, var(--text) 70%, transparent);
    outline-offset: -2px;
    pointer-events: none;
  }
  .zoom { display: flex; align-items: center; gap: 0.75rem; font-size: 0.85rem; color: var(--muted); }
  .zoom input { flex: 1; accent-color: var(--accent); }
  .foot { display: flex; justify-content: flex-end; gap: 0.5rem; }
  .btn {
    padding: 0.45rem 0.95rem;
    border-radius: 8px;
    border: 1px solid var(--border);
    background: none;
    color: var(--text);
    font: inherit;
    font-size: 0.9rem;
    cursor: pointer;
  }
  .btn:hover:not(:disabled) { border-color: var(--accent); }
  .btn:disabled { opacity: 0.5; cursor: default; }
  /* White on the accent, as every primary button in both UIs. */
  .btn.primary { background: var(--accent); border-color: var(--accent); color: white; font-weight: 600; }
</style>
