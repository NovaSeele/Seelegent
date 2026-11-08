<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import Checkbox from '@/components/ui/Checkbox.vue'
import NumberField from '@/components/ui/NumberField.vue'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useTeamStore } from '@/stores/team'

import { BACKENDS, type AutomationForm, type FormProblem, type GateChoice } from './automationModel'
import { useFormFields } from './useFormFields'

/**
 * Who runs an automation and what guards it: the worker (a team member, or a backend and model), the gate after
 * the turn, the lane, the precheck command that can skip a run, the quota rules and how late a missed run may
 * still start.
 */
const form = defineModel<AutomationForm>({ required: true })
const props = defineProps<{ problems: readonly FormProblem[]; gateCanReset: boolean }>()

const { t } = useI18n()
const team = useTeamStore()
const field = useFormFields(form)
const member = field('member')
const backend = field('backend')
const model = field('model')
const gate = field('gate')
const gateCommands = field('gateCommands')
const lane = field('lane')
const noLane = field('noLane')
const precheck = field('precheck')
const useBeforeReset = field('useBeforeReset')
const minRemaining = field('minRemaining')
const graceMinutes = field('graceMinutes')

onMounted(() => team.ensureMembers())

const members = computed<SelectOption<string>[]>(() => [
  { value: '', label: t('automations.editor.anyWorker') },
  ...team.members.filter((m) => m.enabled).map((m) => ({ value: m.id, label: m.name })),
])
const backends = computed<SelectOption<string>[]>(() => [
  { value: '', label: t('automations.editor.defaultBackend') },
  ...BACKENDS.map((value) => ({ value, label: t(`accounts.backend.${value}`) })),
])
const gates = computed<SelectOption<GateChoice>[]>(() =>
  (['default', 'on', 'off'] as const)
    .filter((value) => value !== 'default' || props.gateCanReset)
    .map((value) => ({ value, label: t(`automations.editor.gate.${value}`) })),
)
</script>

<template>
  <div class="adv">
    <div class="adv__row">
      <Select v-model="member" :label="t('automations.editor.member')" :options="members" />
      <Select
        v-model="backend"
        :label="t('automations.editor.backend')"
        :options="backends"
        :disabled="!!member"
      />
      <TextField
        v-model="model"
        :label="t('automations.editor.model')"
        :hint="t('automations.editor.modelHint')"
      />
    </div>
    <div class="adv__row">
      <Select v-model="gate" :label="t('automations.editor.gateLabel')" :options="gates" />
      <TextArea
        v-model="gateCommands"
        :label="t('automations.editor.gateCommands')"
        :hint="t('automations.editor.linesHint')"
        :rows="2"
        mono
      />
    </div>
    <div class="adv__row">
      <TextArea
        v-model="lane"
        :label="t('automations.editor.lane')"
        :hint="t('automations.editor.laneHint')"
        :rows="2"
        mono
        :disabled="noLane"
      />
      <Checkbox
        v-model="noLane"
        :label="t('automations.editor.noLane')"
        :description="t('automations.editor.noLaneDesc')"
      />
    </div>
    <TextField
      v-model="precheck"
      :label="t('automations.editor.precheck')"
      :hint="t('automations.editor.precheckHint')"
    />
    <div class="adv__row">
      <NumberField
        :model-value="minRemaining"
        :label="t('automations.editor.minRemaining')"
        :hint="t('automations.editor.minRemainingHint')"
        unit="%"
        :error="
          problems.includes('minRemaining')
            ? t('automations.editor.rangeError', { min: 0, max: 100 })
            : undefined
        "
        @commit="minRemaining = $event"
      />
      <NumberField
        :model-value="graceMinutes"
        :label="t('automations.editor.grace')"
        :hint="t('automations.editor.graceHint')"
        :unit="t('automations.editor.minutes')"
        integer
        :error="
          problems.includes('graceMinutes')
            ? t('automations.editor.rangeError', { min: 0, max: 1440 })
            : undefined
        "
        @commit="graceMinutes = $event"
      />
    </div>
    <Checkbox
      v-model="useBeforeReset"
      :label="t('automations.editor.beforeReset')"
      :description="t('automations.editor.beforeResetDesc')"
    />
  </div>
</template>

<style scoped>
.adv {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.adv__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  align-items: start;
  gap: var(--space-3);
}
</style>
