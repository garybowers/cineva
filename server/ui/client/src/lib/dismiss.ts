/**
 * Close a popup when the viewer clicks away from it or presses Escape.
 *
 * Every floating panel is expected to behave this way, and a popup that
 * stays open over the page until something else happens to close it feels
 * broken. Written once here rather than per component, because the three
 * places that had it each implemented it slightly differently and the
 * places that did not simply went without.
 *
 * Call it from inside an `$effect` that returns its result, so the listeners
 * are torn down when the popup closes or the component goes away:
 *
 *     $effect(() => {
 *       if (!open) return
 *       return onDismiss('.menuwrap', () => (open = false))
 *     })
 *
 * `inside` is a selector for the popup *and* whatever opens it: a click on
 * the toggle must not be treated as a click away, or the button would close
 * the menu it just opened.
 */
export function onDismiss(inside: string, close: () => void): () => void {
  const away = (e: MouseEvent) => {
    if (!(e.target as HTMLElement | null)?.closest(inside)) close()
  }
  const esc = (e: KeyboardEvent) => {
    if (e.key === 'Escape') close()
  }
  // Deferred a frame: the click that opened the popup is still propagating,
  // and would otherwise reach this listener and close it immediately.
  const raf = requestAnimationFrame(() => {
    document.addEventListener('click', away)
    document.addEventListener('keydown', esc)
  })
  return () => {
    cancelAnimationFrame(raf)
    document.removeEventListener('click', away)
    document.removeEventListener('keydown', esc)
  }
}
