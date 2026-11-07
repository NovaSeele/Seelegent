<script lang="ts">
/** Who runs a ticket's task when it starts again: a member, a backend, or "" for the ticket's own choice. */
export interface RetryChoice {
  member: string
  backend: string
}
</script>

<script setup lang="ts">
import { computed, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import Select, { type SelectOption } from '@/components/ui/Select.vue'
import { useTeamStore } from '@/stores/team'

/** The member and backend pickers of "Try again": the same task can go to someone else (reassign). */
const choice = defineModel<RetryChoice>({ required: true })

const { t } = useI18n()
const team = useTeamStore()
onMounted(() => team.ensureMembers())

const members = computed<SelectOption<string>[]>(() => [
  { value: '', label: t('issues.actions.retrySame') },
  ...team.members
    .filter((m) => m.enabled && !m.invalid)
    .map((m) => ({ value: m.id, label: m.name })),
])
const backends = computed<SelectOption<string>[]>(() => [
  { value: '', label: t('issues.actions.retrySame') },
  ...(['agy', 'claude', 'codex'] as const).map((b) => ({ value: b, label: b })),
])

const member = computed({
  get: () => choice.value.member,
  set: (v: string) => (choice.value = { ...choice.value, member: v }),
})
const backend = computed({
  get: () => choice.value.backend,
  set: (v: string) => (choice.value = { ...choice.value, backend: v }),
})
</script>

<template>
  <div class="pick">
    <Select v-model="member" :label="t('issues.triage.member')" :options="members" />
    <Select
      v-model="backend"
      :label="t('issues.actions.backend')"
      :hint="t('issues.actions.backendHint')"
      :options="backends"
    />
  </div>
</template>

<style scoped>
.pick {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: var(--space-3);
}

@media (width < 600px) {
  .pick {
    grid-template-columns: 1fr;
  }
}
</style>
