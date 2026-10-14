// Home Assistant as the client sees it: the rooms, controls and cameras
// this user may see, and the modes they may run. One copy for the whole
// app, shared by the Rooms page, the player's room panel, title pages and
// Settings. App loads it once signed in and hands it the "homeassistant"
// and "mode-run" events from the event stream.

import {
  haEntityAction, haRooms, listHAModes, runHAMode,
  type HAAction, type HAArea, type HAEntity, type HAMode, type HAModeRun, type HARooms,
} from './api'

export const ha = $state<{
  rooms: HARooms | null
  modes: HAMode[]
  /** The latest progress of each mode run from here, by mode id. */
  runs: Record<string, HAModeRun>
}>({ rooms: null, modes: [], runs: {} })

let loading: Promise<void> | null = null
let again = false

/** Fetch rooms and modes. Overlapping calls fold into one more fetch. */
export function loadHA(): Promise<void> {
  if (loading) {
    again = true
    return loading
  }
  loading = (async () => {
    try {
      const rooms = await haRooms()
      ha.rooms = rooms
      ha.modes = rooms.configured ? await listHAModes().catch(() => []) : []
    } catch {
      // A server without Home Assistant, or one that cannot answer: the
      // pages that use it simply do not appear.
      ha.rooms = null
      ha.modes = []
    } finally {
      loading = null
      if (again) {
        again = false
        void loadHA()
      }
    }
  })()
  return loading
}

/** Forget everything, on signing out. */
export function clearHA() {
  ha.rooms = null
  ha.modes = []
  ha.runs = {}
}

/** Whether there is anything to show: Home Assistant is set up and this
    user may see an area or run a mode. */
export const haVisible = (): boolean =>
  !!ha.rooms?.configured && (ha.rooms.areas.some((a) => a.entities.length > 0) || ha.modes.length > 0)

/** Controls, as opposed to cameras: what a room's panel lists. */
export const controlsOf = (a: HAArea): HAEntity[] => a.entities.filter((e) => e.domain !== 'camera')
export const camerasOf = (a: HAArea): HAEntity[] => a.entities.filter((e) => e.domain === 'camera')

/** This device's room, when it has one this user can see anything in. */
export function currentRoom(): HAArea | undefined {
  const id = ha.rooms?.currentAreaId
  if (!id) return undefined
  return ha.rooms?.areas.find((a) => a.id === id)
}

/** The name of an area, from the areas listed or from every area. */
export function areaName(id: string): string {
  return ha.rooms?.areas.find((a) => a.id === id)?.name
    || ha.rooms?.allAreas?.find((a) => a.id === id)?.name
    || ''
}

/** Modes that play in the given room. */
export const modesIn = (areaId: string): HAMode[] => (areaId ? ha.modes.filter((m) => m.areaId === areaId) : [])

/** Modes that play the title they are started from. */
export const titleModes = (): HAMode[] => (ha.rooms?.configured ? ha.modes.filter((m) => m.needsTitle) : [])

function patchEntity(entityId: string, change: Partial<HAEntity>) {
  for (const a of ha.rooms?.areas ?? []) {
    const i = a.entities.findIndex((e) => e.entityId === entityId)
    if (i >= 0) a.entities[i] = { ...a.entities[i], ...change }
  }
}

function findEntity(entityId: string): HAEntity | undefined {
  for (const a of ha.rooms?.areas ?? []) {
    const e = a.entities.find((x) => x.entityId === entityId)
    if (e) return e
  }
  return undefined
}

/**
 * A "homeassistant" event: changed states replace the ones shown; an
 * event with no data means something larger changed (the connection,
 * exposure, access, rooms or modes), so everything is fetched again.
 */
export function onHAEvent(data: unknown) {
  const d = data as { entities?: HAEntity[] } | null
  if (!d?.entities) {
    void loadHA()
    return
  }
  for (const e of d.entities) {
    if (findEntity(e.entityId)) patchEntity(e.entityId, e)
  }
}

/**
 * Work a control. What it should look like afterwards is shown at once and
 * corrected by the event that follows; if the call fails the old state is
 * put back, the real one fetched, and the server's reason thrown.
 */
export async function act(entity: HAEntity, action: HAAction, expect: Partial<HAEntity> = {}): Promise<void> {
  const before = findEntity(entity.entityId)
  const undo: Partial<HAEntity> = {}
  if (before) {
    for (const k of Object.keys(expect) as (keyof HAEntity)[]) (undo as Record<string, unknown>)[k] = before[k]
  }
  patchEntity(entity.entityId, expect)
  try {
    await haEntityAction(entity.entityId, action)
  } catch (err) {
    patchEntity(entity.entityId, undo)
    void loadHA()
    throw err
  }
}

/** Start a mode, returning its run's id. Its progress arrives as mode-run
    events. */
export async function startMode(mode: HAMode, itemId?: string): Promise<string> {
  const { runId } = await runHAMode(mode.id, itemId)
  // Its first event may have beaten the answer here.
  if (ha.runs[mode.id]?.runId !== runId) {
    ha.runs[mode.id] = {
      runId, modeId: mode.id, modeName: mode.name, status: 'running', step: 0, steps: mode.steps.length,
    }
  }
  return runId
}

/** Whether a finished run sent its title to another device than this one. */
export const playedElsewhere = (r: HAModeRun): boolean =>
  !!r.playedOn && r.playedOn !== ha.rooms?.currentDeviceId

/** Modes for the player's panel: those that play in this device's room
    first, then the rest. */
export function modesForHere(): HAMode[] {
  const areaId = ha.rooms?.configured ? ha.rooms.currentAreaId : ''
  const here = modesIn(areaId)
  return [...here, ...ha.modes.filter((m) => !here.includes(m))]
}

/**
 * A "mode-run" event: remembered against its mode, so a mode's button can
 * show how it is getting on, and returned as a line for the app's notice.
 */
export function onModeRun(data: unknown): { kind: 'info' | 'error'; text: string; ms: number } | null {
  const r = data as HAModeRun | null
  if (!r?.modeId) return null
  ha.runs[r.modeId] = r
  const name = r.modeName || ha.modes.find((m) => m.id === r.modeId)?.name || 'Mode'
  if (r.status === 'done') {
    setTimeout(() => {
      if (ha.runs[r.modeId]?.runId === r.runId) delete ha.runs[r.modeId]
    }, 4000)
    if (playedElsewhere(r)) return { kind: 'info', text: `${name}: playing on ${r.playedOnName || 'the other device'}`, ms: 6000 }
    return { kind: 'info', text: `${name} done`, ms: 4000 }
  }
  if (r.status === 'failed') {
    setTimeout(() => {
      if (ha.runs[r.modeId]?.runId === r.runId) delete ha.runs[r.modeId]
    }, 15000)
    const at = r.step > 0 ? ` at step ${r.step} of ${r.steps}` : ''
    return { kind: 'error', text: `${name} stopped${at}: ${r.error || 'something went wrong'}`, ms: 15000 }
  }
  return { kind: 'info', text: runLine(r, name), ms: 60000 }
}

/** "Movie mode: running step 2 of 4". */
export function runLine(r: HAModeRun, name = r.modeName): string {
  if (r.status === 'done') return `${name} done`
  if (r.status === 'failed') return `${name} stopped: ${r.error || 'something went wrong'}`
  if (r.step > 0 && r.steps > 0) return `${name}: running step ${Math.min(r.step, r.steps)} of ${r.steps}`
  return `${name}: starting`
}
