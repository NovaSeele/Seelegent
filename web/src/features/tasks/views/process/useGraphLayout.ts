/**
 * Layout of the Graph view at the chosen zoom. "Fit" scales time so the graph fills the scroller's width (watched
 * with a ResizeObserver); 2x and 4x multiply that scale.
 */
import { computed, onBeforeUnmount, ref, shallowRef, watch, type Ref } from 'vue'

import { GRAPH_DEFAULTS, layoutGraph } from './graph'
import type { Step, TurnNode } from './process'

export type Zoom = 'fit' | '2' | '4'

export function useGraphLayout(
  turns: Ref<readonly TurnNode[]>,
  durationOf: (step: Step) => number,
) {
  const zoom = ref<Zoom>('fit')
  const scroller = ref<HTMLElement | null>(null)
  const available = shallowRef(0)
  let observer: ResizeObserver | null = null
  // The scroller appears only once the events are loaded (ProcessFrame shows a loading state first), so watch
  // the ref instead of measuring on mount; without a width, Fit would fall back to the default scale and overflow.
  watch(
    scroller,
    (el) => {
      observer?.disconnect()
      if (!el) return
      available.value = el.clientWidth
      if (typeof ResizeObserver === 'undefined') return
      observer ??= new ResizeObserver(
        ([entry]) => (available.value = entry?.contentRect.width ?? 0),
      )
      observer.observe(el)
    },
    { flush: 'post' },
  )
  onBeforeUnmount(() => observer?.disconnect())

  const layout = computed(() => {
    const at = (pxPerSec: number) =>
      layoutGraph(turns.value, { ...GRAPH_DEFAULTS, pxPerSec }, durationOf)
    // Width grows linearly with px/s except for fixed parts (padding, idle breaks, minimum bar widths), so two
    // layouts give the scale that fills the width; zoom multiplies it.
    const one = at(1)
    const slope = at(2).width - one.width
    if (available.value <= 0 || slope <= 0) return at(GRAPH_DEFAULTS.pxPerSec)
    let fit = Math.min(400, Math.max(0.02, (available.value - (one.width - slope)) / slope))
    // Minimum bar widths stop mattering as the scale grows, so the estimate can overshoot: shrink until it fits.
    let fitted = at(fit)
    for (let i = 0; i < 6 && fitted.width > available.value && fit > 0.02; i++) {
      fit = Math.max(0.02, fit * (available.value / fitted.width) * 0.98)
      fitted = at(fit)
    }
    return zoom.value === 'fit' ? fitted : at(fit * Number(zoom.value))
  })

  return { zoom, scroller, layout }
}
