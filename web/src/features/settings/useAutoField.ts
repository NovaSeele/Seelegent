/**
 * The Auto state of a machine-sized setting (fields.ts AUTO_PATHS) for its row. The setting is auto while it is
 * null or absent in the settings, and set by the user once it holds a number (0 = off or no limit). While auto,
 * the row shows the value the daemon uses on this machine (Health.resources, read again while Resources is open)
 * instead of a number field; "Set" switches to the field, which starts from that value, and nothing is sent until
 * the user commits another number. Any other field gets an inert result (no auto path). `row` is the row's
 * element: focus moves within it (into the field on "Set", back to "Set", marked data-auto-set, on leaving it).
 */
import { computed, nextTick, ref, watch, type Ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { EffectiveValue } from '@/api/types'
import { useFormat } from '@/composables/useFormat'
import { formatMB } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'

import { isAutoPath, type AutoPath, type FieldDef } from './fields'
import type { SettingsForm } from './useSettingsForm'

/** Where an effective value comes from ('derived': a memory threshold worked out from the other one the user set). */
type ResourceSource = EffectiveValue['source']

/** The setting a derived value is worked out from, and how (settings.daemon.derived.<how>). */
const DERIVED_FROM: Partial<Record<AutoPath, { item: string; how: 'half' | 'twice' }>> = {
  criticalFreeMemoryMB: { item: 'minFreeMemory', how: 'half' },
  minFreeMemoryMB: { item: 'criticalFreeMemory', how: 'twice' },
}

export function useAutoField(
  def: () => FieldDef,
  form: SettingsForm,
  row: Ref<HTMLElement | null>,
) {
  const { t } = useI18n()
  const fmt = useFormat()
  const connection = useConnectionStore()

  const autoPath = computed<AutoPath | null>(() => {
    const field = def()
    return field.kind === 'number' && isAutoPath(field.path) ? field.path : null
  })
  const isAuto = computed(() => autoPath.value !== null && form.get(autoPath.value) == null)
  /** The user chose to set a value while the setting is auto: the number field shows until it saves or is left. */
  const editing = ref(false)
  watch(isAuto, (auto) => {
    if (!auto) editing.value = false
  })
  /** Auto, and the user has not chosen to set a value: the row shows the Auto state instead of a number field. */
  const showAuto = computed(() => isAuto.value && !editing.value)

  /**
   * The value in use while the setting is not set, and how the daemon got it: from the machine ('auto'), or from
   * the other memory threshold the user set ('derived': a floor half the minimum, a minimum twice the floor).
   * Null while Health has none or still has the value the user had set (it is read again after every change).
   */
  const effective = computed(() => {
    const path = autoPath.value
    const resource = path ? connection.health?.resources?.[path] : undefined
    const source = resource?.source as ResourceSource | undefined
    return resource && (source === 'auto' || source === 'derived')
      ? { value: resource.value, derived: source === 'derived' }
      : null
  })

  /**
   * The Auto line: its message ({auto} is the "Auto" tag, {other} the setting it is derived from) and the amount:
   * "Auto: 2 GB on this machine", "Auto: off on this machine" for 0, "Auto: half of your “Free memory to start”,
   * 1 GB"; null while the value in use is not known.
   */
  const autoLine = computed(() => {
    const field = def()
    const path = autoPath.value
    if (!effective.value || !path || field.kind !== 'number') return null
    const { value, derived } = effective.value
    const amount = field.unit === 'mb' ? formatMB(fmt.value, value) : fmt.value.number(value)
    const from = derived ? DERIVED_FROM[path] : undefined
    if (from) {
      const other = t(`settings.items.${from.item}.label`)
      return { key: `settings.daemon.derived.${from.how}` as const, value: amount, other }
    }
    if (value === 0 && field.zero) {
      return { key: `settings.daemon.autoZero.${field.zero}` as const, value: '', other: '' }
    }
    return { key: 'settings.daemon.autoValue' as const, value: amount, other: '' }
  })

  /** The number the field starts from when the user sets a value: the one in use (0 while unknown). */
  const startValue = computed(() => effective.value?.value ?? 0)

  /** "Set by you." for a value the user set ("Set by you: off." for 0), '' while auto or for other fields. */
  const explicitNote = computed(() => {
    const field = def()
    if (!autoPath.value || isAuto.value || field.kind !== 'number') return ''
    return Number(form.get(autoPath.value)) === 0 && field.zero
      ? t(`settings.daemon.explicitZero.${field.zero}`)
      : t('settings.daemon.explicit')
  })

  function focusIn(selector: string) {
    void nextTick(() => row.value?.querySelector<HTMLElement>(selector)?.focus())
  }
  const hasFocus = () => row.value?.contains(document.activeElement) ?? false

  /** "Set" on an auto setting: the number field, starting from the value in use, with the focus in it. */
  function customize() {
    editing.value = true
    focusIn('input')
  }

  /**
   * The field shows its value again. A field opened from Auto with nothing new in it goes back to Auto (nothing
   * is sent). After Enter or Esc the focus moves to "Set"; after a click elsewhere it stays where it went, unless
   * it would be lost (Tab moved it to "Use auto", which "Set" replaces): then it goes to "Set" too.
   */
  function onUnchanged() {
    form.clear(def().path)
    if (!isAuto.value || !editing.value) return
    const focused = hasFocus()
    editing.value = false
    void nextTick(() => {
      const active = document.activeElement
      if (focused || !active || active === document.body) {
        row.value?.querySelector<HTMLElement>('[data-auto-set]')?.focus()
      }
    })
  }

  /**
   * Esc in a field opened from Auto closes it back to Auto and goes no further: the field starts at the value in
   * use, so NumberField sees nothing to undo and would let Esc close the whole settings dialog.
   */
  function onEscape(event: KeyboardEvent) {
    if (!isAuto.value || !editing.value) return
    event.stopPropagation()
    onUnchanged()
  }

  /** "Use auto": a set value goes back to auto (null); a field opened from Auto just closes. */
  async function useAuto() {
    const focused = hasFocus()
    if (isAuto.value) {
      editing.value = false
      form.clear(def().path)
    } else if (!(await form.reset(def().path))) return
    if (focused) focusIn('[data-auto-set]')
  }

  return {
    autoPath,
    isAuto,
    showAuto,
    autoLine,
    startValue,
    explicitNote,
    customize,
    onUnchanged,
    onEscape,
    useAuto,
  }
}
