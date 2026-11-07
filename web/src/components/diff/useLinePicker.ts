/**
 * The line numbers of an annotatable diff as one roving-tabindex group with one delegated handler (no listener
 * per line, however long the diff): Tab reaches the current line number once, ↑/↓ (and Home/End) move between
 * lines, Enter, Space or a tap selects one. The selected line shows one large "add a note" button; after the
 * composer of a line closes, focus goes back to that line number.
 */
import { nextTick, ref, type Ref, watch } from 'vue'

const LINE = '[data-line]'

export function useLinePicker(table: Ref<HTMLElement | null>, composerLine: () => number | null) {
  /** The line chosen by a tap or Enter: it shows the add button. */
  const selected = ref<number | null>(null)
  /** The line number that takes Tab (the roving stop). */
  const current = ref<number | null>(null)

  function buttons(): HTMLElement[] {
    return [...(table.value?.querySelectorAll<HTMLElement>(LINE) ?? [])]
  }

  function focusLine(line: number) {
    current.value = line
    void nextTick(() => table.value?.querySelector<HTMLElement>(`[data-line="${line}"]`)?.focus())
  }

  function lineOf(event: Event): number | null {
    const el = (event.target as HTMLElement | null)?.closest<HTMLElement>(LINE)
    return el ? Number(el.dataset.line) : null
  }

  function onClick(event: MouseEvent) {
    const line = lineOf(event)
    if (line === null) return
    current.value = line
    selected.value = selected.value === line ? null : line
  }

  function onKeydown(event: KeyboardEvent) {
    const line = lineOf(event)
    if (line === null) return
    const list = buttons()
    const at = list.findIndex((b) => Number(b.dataset.line) === line)
    let next: HTMLElement | undefined
    if (event.key === 'ArrowDown') next = list[at + 1]
    else if (event.key === 'ArrowUp') next = list[at - 1]
    else if (event.key === 'Home') next = list[0]
    else if (event.key === 'End') next = list.at(-1)
    else if (event.key === 'Escape' && selected.value !== null) {
      selected.value = null
      event.preventDefault()
      return
    } else return
    event.preventDefault()
    if (next) focusLine(Number(next.dataset.line))
  }

  // The composer replaced the add button; when it closes, the line number gets focus back.
  watch(composerLine, (now, before) => {
    if (now !== null) selected.value = now
    else if (before !== null) {
      selected.value = null
      focusLine(before)
    }
  })

  /** The roving stop among `lines` (in order): the current line while it is shown, else the first. */
  function stopOf(lines: readonly number[]): number | undefined {
    return current.value !== null && lines.includes(current.value) ? current.value : lines[0]
  }

  return { selected, current, onClick, onKeydown, stopOf }
}
