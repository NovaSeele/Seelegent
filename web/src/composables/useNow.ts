/**
 * A shared clock for "2 minutes ago" and "waiting 3 h" labels. One timer serves every component that asks for the
 * same interval; it stops while the tab is hidden (no idle wake-ups) and catches up when the tab is shown again.
 */
import { getCurrentScope, onScopeDispose, readonly, shallowRef, type Ref } from 'vue'

interface Clock {
  now: Ref<number>
  users: number
  timer: ReturnType<typeof setInterval> | null
  onVisibility: () => void
}

const clocks = new Map<number, Clock>()

function tick(clock: Clock) {
  clock.now.value = Date.now()
}

function startTimer(clock: Clock, intervalMs: number) {
  if (clock.timer !== null) return
  clock.timer = setInterval(() => tick(clock), intervalMs)
}

function stopTimer(clock: Clock) {
  if (clock.timer === null) return
  clearInterval(clock.timer)
  clock.timer = null
}

/** The current time in ms, updated every `intervalMs` while the page is visible. */
export function useNow(intervalMs = 1000): Readonly<Ref<number>> {
  let clock = clocks.get(intervalMs)
  if (!clock) {
    const created: Clock = {
      now: shallowRef(Date.now()),
      users: 0,
      timer: null,
      onVisibility: () => {
        if (document.hidden) stopTimer(created)
        else {
          tick(created)
          startTimer(created, intervalMs)
        }
      },
    }
    clock = created
    clocks.set(intervalMs, clock)
  }
  const active = clock
  if (active.users++ === 0) {
    tick(active)
    if (typeof document === 'undefined' || !document.hidden) startTimer(active, intervalMs)
    if (typeof document !== 'undefined') {
      document.addEventListener('visibilitychange', active.onVisibility)
    }
  }
  if (getCurrentScope()) {
    onScopeDispose(() => {
      if (--active.users > 0) return
      stopTimer(active)
      if (typeof document !== 'undefined') {
        document.removeEventListener('visibilitychange', active.onVisibility)
      }
      clocks.delete(intervalMs)
    })
  }
  return readonly(active.now)
}
