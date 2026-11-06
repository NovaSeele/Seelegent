/**
 * Runs `callback` before the next paint (requestAnimationFrame), so bursts of updates render once per frame.
 * Falls back to a 16 ms timer where there is no animation frame (tests, non-browser environments).
 */
export function nextFrame(callback: () => void): void {
  if (typeof requestAnimationFrame === 'function') requestAnimationFrame(() => callback())
  else setTimeout(callback, 16)
}
