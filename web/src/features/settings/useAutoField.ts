/**
 * The Auto state of a machine-sized setting (fields.ts AUTO_PATHS) for its row. The setting is auto while it is
 * null or absent in the settings, and set by the user once it holds a number (0 = off or no limit). While auto,
 * the row shows the value the daemon uses on this machine (Health.resources, read again while Resources is open)
 * instead of a number field; "Set" switches to the field, which starts from that value, and nothing is sent until
 * the user commits another number. Any other field gets an inert result (no auto path).
 */
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useFormat } from '@/composables/useFormat'
import { formatMB } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'

import { isAutoPath, type AutoPath, type FieldDef } from './fields'
import type { SettingsForm } from './useSettingsForm'

export function useAutoField(def: () => FieldDef, form: SettingsForm) {
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
   * The auto value in use, or null while Health has none or still has the value the user had set (it is read
   * again right after the settings change).
   */
  const effective = computed(() => {
    const path = autoPath.value
    const resource = path ? connection.health?.resources?.[path] : undefined
    return resource?.source === 'auto' ? resource.value : null
  })

  /** An amount as people read it: "off" or "no limit" for 0, GB from 1 GB up, a plain count otherwise. */
  function amount(value: number): string {
    const field = def()
    if (field.kind !== 'number') return String(value)
    if (value === 0 && field.zero) return t(`settings.daemon.zeroIs.${field.zero}`)
    return field.unit === 'mb' ? formatMB(fmt.value, value) : fmt.value.number(value)
  }

  /** "2 GB on this machine", or '' while the value in use is not known. */
  const autoDetail = computed(() =>
    effective.value === null
      ? ''
      : t('settings.daemon.autoValue', { value: amount(effective.value) }),
  )

  /** The number the field starts from when the user sets a value: the one in use (0 while unknown). */
  const startValue = computed(() => effective.value ?? 0)

  /** "Set by you." for a value the user set ("Set by you: off." for 0), '' while auto or for other fields. */
  const explicitNote = computed(() => {
    const field = def()
    if (!autoPath.value || isAuto.value || field.kind !== 'number') return ''
    return Number(form.get(autoPath.value)) === 0 && field.zero
      ? t(`settings.daemon.explicitZero.${field.zero}`)
      : t('settings.daemon.explicit')
  })

  return { autoPath, isAuto, editing, showAuto, autoDetail, startValue, explicitNote }
}
