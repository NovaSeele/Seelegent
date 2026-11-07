<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Switch from '@/components/ui/Switch.vue'

import CommitField from './CommitField.vue'
import type { FieldDef } from './fields'
import SettingRow from './SettingRow.vue'
import type { SettingsForm } from './useSettingsForm'

/**
 * One daemon setting as a row: its control (by kind), "Saving…" / "Saved" next to it, the daemon's error under
 * it, and a button that resets it to the default (a null in the merge patch).
 */
const props = defineProps<{ def: FieldDef; form: SettingsForm; highlighted?: boolean }>()

const { t } = useI18n()

const label = computed(() => t(`settings.items.${props.def.item}.label`))
const value = computed(() => props.form.get(props.def.path))
const error = computed(() => props.form.errors.value[props.def.path])
const busy = computed(() => props.form.saving.value.has(props.def.path))
const done = computed(() => props.form.saved.value.has(props.def.path))

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
  save(value)
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
      <div class="sf">
        <div class="sf__control" :class="{ 'sf__control--switch': def.kind === 'bool' }">
          <NumberField
            v-if="def.kind === 'number'"
            :model-value="Number(value ?? 0)"
            :label="label"
            hide-label
            size="sm"
            :unit="def.unit ? t(`settings.units.${def.unit}`) : undefined"
            :hint="t('settings.daemon.range', { min: def.min, max: def.max })"
            :error="error"
            :debounce="500"
            integer
            @commit="saveInRange($event, $event)"
            @unchanged="form.clear(def.path)"
          />
          <NumberField
            v-else-if="def.kind === 'percent'"
            :model-value="percent"
            :label="label"
            hide-label
            size="sm"
            :unit="t('settings.units.pct')"
            :hint="t('settings.daemon.range', { min: def.min, max: def.max })"
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
          />
          <code v-else class="sf__readonly">{{ value }}</code>
        </div>
        <Button
          v-if="def.kind !== 'readonly'"
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
    </div>
  </SettingRow>
</template>

<style scoped>
.sf-wrap {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 100%;
}

.sf {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  width: 100%;
}

/* The control fills the row's fixed column (SettingRow) next to its reset button and state. */
.sf__control {
  flex: 1 1 0;
  min-width: 0;
}

/* A switch is narrow: its reset button sits right next to it. */
.sf__control--switch {
  flex: 0 0 auto;
  min-width: 0;
}

.sf__state {
  display: inline-flex;
  align-items: center;
  min-width: 64px;
  min-height: var(--control-sm);
}

.sf__saved {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.sf__readonly {
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
}

.sf__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-xs);
}

.sf__note {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
