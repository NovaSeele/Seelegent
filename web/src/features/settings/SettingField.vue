<script setup lang="ts">
import { computed, ref, useId } from 'vue'
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

import AutoValue from './AutoValue.vue'
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
const rowEl = ref<HTMLElement | null>(null)
const auto = useAutoField(() => props.def, props.form, rowEl)
const { autoPath, isAuto, showAuto, autoLine, startValue, explicitNote } = auto
const { customize, onUnchanged, onEscape, useAuto } = auto
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

/** The row's action: "Set" on an auto setting, "Use auto" once set, "Default" on any other editable setting. */
const action = computed(() => {
  const kind = showAuto.value ? 'set' : autoPath.value ? 'useAuto' : 'reset'
  if (kind === 'reset' && props.def.kind === 'readonly') return null
  const run = { set: customize, useAuto, reset: () => void props.form.reset(props.def.path) }[kind]
  const aria = t(`settings.daemon.${kind}Of`, { name: label.value })
  return { kind, text: t(`settings.daemon.${kind}`), aria, run }
})
const setDescribedBy = computed(() => (hasNotes.value ? `${autoId} ${noteId}` : autoId))

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
        <div
          class="sf__control"
          :class="{ 'sf__control--switch': def.kind === 'bool' }"
          @keydown.esc="onEscape"
        >
          <!-- A click on the box acts like "Set"; the keyboard path is the Set button (one tab stop). -->
          <AutoValue v-if="showAuto" :id="autoId" :line="autoLine" data-auto @click="customize" />
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
          v-if="action"
          :key="action.kind"
          class="sf__action"
          :data-auto-set="action.kind === 'set' ? '' : undefined"
          :data-auto-reset="action.kind === 'useAuto' ? '' : undefined"
          size="sm"
          variant="ghost"
          :aria-label="action.aria"
          :aria-describedby="action.kind === 'set' ? setDescribedBy : undefined"
          :disabled="busy"
          @click="action.run"
        >
          {{ action.text }}
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
