<script setup lang="ts">
import { computed, nextTick, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Switch from '@/components/ui/Switch.vue'
import { useFormat } from '@/composables/useFormat'
import { formatMB, freeMB } from '@/lib/memory'
import { useConnectionStore } from '@/stores/connection'

import CommitField from './CommitField.vue'
import { crossFieldClash, type FieldDef } from './fields'
import SettingRow from './SettingRow.vue'
import { useAutoField } from './useAutoField'
import type { SettingsForm } from './useSettingsForm'

/**
 * One daemon setting as a row: its control (by kind), "Saving…" / "Saved" next to it, the daemon's error under
 * it, and a button that resets it to the default (a null in the merge patch). A setting that works on one system
 * only says so, and that it has no effect on any other (Health.os); it stays editable and fully legible, since
 * settings may move to another machine. A memory threshold shows the machine's free memory now; an MB field says
 * what its value is in GB and gives its range in GB or TB. The notes are part of the input's description.
 * A machine-sized setting (useAutoField) shows "Auto" and the value in use instead of the field while it is auto,
 * with "Set" next to it; once set, its reset button is "Use auto" (null) and a note says the user set it.
 */
const props = defineProps<{ def: FieldDef; form: SettingsForm; highlighted?: boolean }>()

const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()

const label = computed(() => t(`settings.items.${props.def.item}.label`))
const value = computed(() => props.form.get(props.def.path))
const error = computed(() => props.form.errors.value[props.def.path])
const busy = computed(() => props.form.saving.value.has(props.def.path))
const done = computed(() => props.form.saved.value.has(props.def.path))
const { autoPath, isAuto, editing, showAuto, autoDetail, startValue, explicitNote } = useAutoField(
  () => props.def,
  props.form,
)
/** The number in a number field: the saved one, or the value in use when the user sets an auto one. */
const numberShown = computed(() => (isAuto.value ? startValue.value : Number(value.value ?? 0)))

const options = computed(() =>
  props.def.kind === 'select'
    ? props.def.options.map((option) => ({
        value: option,
        label: t(
          `settings.options.${props.def.kind === 'select' ? props.def.optionsKey : ''}.${option}`,
        ),
      }))
    : [],
)

const noteId = useId()
const autoId = useId()
const rowEl = ref<HTMLElement | null>(null)

const hint = computed(() => {
  const def = props.def
  if (def.kind !== 'number' && def.kind !== 'percent') return undefined
  const mb = def.kind === 'number' && def.unit === 'mb'
  const size = (n: number) => (mb && n > 0 ? formatMB(fmt.value, n) : n)
  const range = { min: size(def.min), max: size(def.max) }
  const text =
    def.kind === 'number' && def.zero
      ? t(`settings.daemon.rangeZero.${def.zero}`, range)
      : t('settings.daemon.range', range)
  const saved = def.kind === 'number' ? numberShown.value : Number(value.value ?? 0)
  return mb && saved >= 1024
    ? `${text} · ${t('settings.daemon.mbIs', { size: formatMB(fmt.value, saved) })}`
    : text
})

/** The setting works only on `def.os`, and the daemon runs on another system. */
const otherOs = computed(() => {
  const os = connection.health?.os
  return !!props.def.os && !!os && os !== props.def.os
})

const nowText = computed(() => {
  const mb = props.def.now ? freeMB(connection.health?.memory, props.def.now) : null
  return mb === null ? '' : t('settings.daemon.nowFree', { free: formatMB(fmt.value, mb) })
})

/** The row has notes under it (system, free memory now, set by the user): part of the field's description. */
const hasNotes = computed(() => !!props.def.os || !!nowText.value || !!explicitNote.value)

const percent = computed(() => Math.round(Number(value.value ?? 0) * 1000) / 10)
const lines = computed(() => (Array.isArray(value.value) ? value.value.join('\n') : ''))

function save(next: unknown) {
  void props.form.set(props.def.path, next)
}

/**
 * A number as shown (`shown`, a percentage for percent fields) saved as `value`. Outside the field's range it is
 * not sent: the field says which values it takes (the daemon's own message stays for anything else it refuses).
 */
function saveInRange(shown: number, value: number) {
  const def = props.def
  if ((def.kind === 'number' || def.kind === 'percent') && (shown < def.min || shown > def.max)) {
    const key =
      def.kind === 'number' ? 'settings.daemon.rangeWhole' : 'settings.daemon.rangePercent'
    props.form.fail(def.path, t(key, { min: def.min, max: def.max }))
    return
  }
  const clash = crossFieldClash(props.form.settings.value, def.path, value)
  if (clash.length) {
    for (const path of clash) props.form.fail(path, t('settings.daemon.criticalBelowMin'))
    return
  }
  save(value)
}

function focusIn(selector: string) {
  void nextTick(() => rowEl.value?.querySelector<HTMLElement>(selector)?.focus())
}

/** "Set" on an auto setting: the number field, starting from the value in use, with the focus in it. */
function customize() {
  editing.value = true
  focusIn('input')
}

/**
 * The field shows its value again. A field opened from Auto with nothing new in it goes back to Auto (nothing is
 * sent); after Enter or Esc the focus moves to "Set", after a click elsewhere it stays where it went.
 */
function onUnchanged() {
  props.form.clear(props.def.path)
  if (!isAuto.value || !editing.value) return
  const focused = rowEl.value?.contains(document.activeElement) ?? false
  editing.value = false
  if (focused) focusIn('[data-auto-set]')
}

/** "Use auto": a set value goes back to auto (null); a field opened from Auto just closes. */
async function useAuto() {
  const focused = rowEl.value?.contains(document.activeElement) ?? false
  if (isAuto.value) {
    editing.value = false
    props.form.clear(props.def.path)
  } else if (!(await props.form.reset(props.def.path))) return
  if (focused) focusIn('[data-auto-set]')
}

function saveLines(text: string) {
  save(
    text
      .split(/\r?\n/)
      .map((line) => line.trim())
      .filter(Boolean),
  )
}
</script>

<template>
  <SettingRow :item="def.item" :highlighted="highlighted" :stacked="def.kind === 'lines'">
    <div class="sf-wrap">
      <div ref="rowEl" class="sf">
        <div class="sf__control" :class="{ 'sf__control--switch': def.kind === 'bool' }">
          <p v-if="showAuto" :id="autoId" class="sf__auto" data-auto>
            <span>
              <span class="sf__auto-tag">{{ t('settings.daemon.auto') }}</span>
              <template v-if="autoDetail"> · {{ autoDetail }}</template>
            </span>
          </p>
          <NumberField
            v-else-if="def.kind === 'number'"
            :model-value="numberShown"
            :label="label"
            hide-label
            size="sm"
            :unit="def.unit ? t(`settings.units.${def.unit}`) : undefined"
            :hint="hint"
            :error="error"
            :describedby="hasNotes ? noteId : undefined"
            :debounce="500"
            integer
            @commit="saveInRange($event, $event)"
            @unchanged="onUnchanged"
          />
          <NumberField
            v-else-if="def.kind === 'percent'"
            :model-value="percent"
            :label="label"
            hide-label
            size="sm"
            :unit="t('settings.units.pct')"
            :hint="hint"
            :error="error"
            :debounce="500"
            @commit="saveInRange($event, $event / 100)"
            @unchanged="form.clear(def.path)"
          />
          <Switch
            v-else-if="def.kind === 'bool'"
            :model-value="value === true"
            :label="label"
            hide-label
            :disabled="busy"
            @update:model-value="save"
          />
          <Select
            v-else-if="def.kind === 'select'"
            :model-value="String(value ?? '')"
            :label="label"
            hide-label
            size="sm"
            :options="options"
            @update:model-value="save"
          />
          <CommitField
            v-else-if="def.kind === 'lines'"
            :model-value="lines"
            :label="label"
            hide-label
            multiline
            :placeholder="t(`settings.items.${def.item}.placeholder`)"
            :hint="t('settings.daemon.lines')"
            :error="error"
            @commit="saveLines"
            @unchanged="form.clear(def.path)"
          />
          <code v-else class="sf__readonly">{{ value }}</code>
        </div>
        <!-- Keyed: a click that closes a field opened from Auto must not land on the "Set" that replaces it. -->
        <Button
          v-if="showAuto"
          key="set"
          data-auto-set
          size="sm"
          variant="ghost"
          :aria-label="t('settings.daemon.setOf', { name: label })"
          :aria-describedby="hasNotes ? `${autoId} ${noteId}` : autoId"
          :disabled="busy"
          @click="customize"
        >
          {{ t('settings.daemon.set') }}
        </Button>
        <Button
          v-else-if="autoPath"
          key="auto"
          data-auto-reset
          size="sm"
          variant="ghost"
          :aria-label="t('settings.daemon.useAutoOf', { name: label })"
          :disabled="busy"
          @click="useAuto"
        >
          {{ t('settings.daemon.useAuto') }}
        </Button>
        <Button
          v-else-if="def.kind !== 'readonly'"
          key="reset"
          size="sm"
          variant="ghost"
          :aria-label="t('settings.daemon.resetOf', { name: label })"
          :disabled="busy"
          @click="form.reset(def.path)"
        >
          {{ t('settings.daemon.reset') }}
        </Button>
        <span class="sf__state" aria-live="polite">
          <Spinner v-if="busy" size="sm" :label="t('settings.daemon.saving')" />
          <span v-else-if="done" class="sf__saved">
            <Icon name="check" />{{ t('settings.daemon.saved') }}
          </span>
        </span>
      </div>
      <p
        v-if="error && (def.kind === 'bool' || def.kind === 'select')"
        class="sf__error"
        role="alert"
      >
        {{ error }}
      </p>
      <p v-if="def.kind === 'readonly'" class="sf__note">{{ t('settings.daemon.readonly') }}</p>
      <p v-if="hasNotes" :id="noteId" class="sf__notes">
        <span v-if="explicitNote" class="sf__note" data-explicit>{{ explicitNote }}</span>
        <span v-if="def.os" class="sf__note" :data-os-note="otherOs ? 'other' : 'this'">
          {{
            t(otherOs ? 'settings.daemon.osOther' : 'settings.daemon.osOnly', {
              os: t(`settings.os.${def.os}`),
            })
          }}
        </span>
        <span v-if="nowText && !otherOs" class="sf__note" data-now>{{ nowText }}</span>
      </p>
    </div>
  </SettingRow>
</template>

<style scoped src="./settingField.css"></style>
