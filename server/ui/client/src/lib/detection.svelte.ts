// Intro and credits detection as the client sees it: what an administrator
// or trusted editor has asked for, so the page they asked from can say how
// it is getting on. The status arrives over the event stream ("segments");
// the app hands each one to setDetection.

import { tv } from './tv.svelte'
import {
  detectionStatus, redetectItem,
  type DetectionStatus, type DetectionTarget, type Item, type SegmentKind, type User,
} from './api'

export const detection = $state<{ status: DetectionStatus | null }>({ status: null })

export function setDetection(s: DetectionStatus | null) {
  detection.status = s
}

/** Fetch the status now, for a page opened while detection is running. */
export function loadDetection() {
  detectionStatus().then(setDetection).catch(() => {})
}

/** Whether a user may correct intro and credits times and start detection. */
// Never on a TV: editing and detection are administration, which belongs
// on a computer or phone (see docs/tv.md).
export const canEditSegments = (u: User | null | undefined) =>
  !tv.on && !!u && (u.role === 'admin' || !!u.segmentEditor)

/** The targeted detection under this item — the season or film itself, a
    season of this series, or an episode's season — if any is waiting or
    being worked on. */
export function detectionFor(item: Item): DetectionTarget | undefined {
  return (detection.status?.targets ?? []).find(
    (t) => t.itemId === item.id || t.seriesId === item.id || (item.kind === 'episode' && t.itemId === item.parentId),
  )
}

const waitReason: Record<string, string> = {
  manual: 'detection is paused',
  playing: 'waiting while something plays',
  scanning: 'waiting while a library scans',
}

/** One line on where detection under this item stands, or '' for nothing. */
export function detectionLine(item: Item): string {
  const t = detectionFor(item)
  if (!t) return ''
  const s = detection.status
  const what = t.kind === 'film' ? 'credits' : `season ${t.season ?? ''}`.trim()
  if (!t.working) return `Detection queued (${what})`
  if (s?.paused && s.pauseReason && waitReason[s.pauseReason]) return `Detecting ${what}: ${waitReason[s.pauseReason]}`
  return `Detecting ${what}…`
}

/**
 * Send an item back to detection, asking first whether times set by hand
 * should be replaced too. Returns a line of feedback for the page.
 */
export async function detect(item: Item, kinds?: SegmentKind[]): Promise<string> {
  let res = await redetectItem(item.id, { kinds })
  let replaced = false
  if (res.locked > 0) {
    const n = res.locked
    const ask = item.kind === 'movie'
      ? 'This film’s credits were set by hand. Replace them with what detection finds?'
      : `Also replace ${n} ${n === 1 ? 'episode' : 'episodes'} you set by hand?`
    if (confirm(ask)) {
      res = await redetectItem(item.id, { kinds, includeLocked: true })
      replaced = true
    }
  }
  loadDetection()
  if (res.items === 0) return 'Nothing here to detect.'
  if (res.reopened === 0 && res.locked > 0 && !replaced) return 'Everything here was set by hand, so it was kept.'
  const kept = res.locked > 0 && !replaced ? ` Times set by hand are kept.` : ''
  if (item.kind === 'movie') return `Reading this film’s chapters again.${kept}`
  return `Detection starts as soon as nothing is playing.${kept}`
}
