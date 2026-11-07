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

import type { TicketAsk, TicketFailure } from '@/api/types'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import { useTeamStore } from '@/stores/team'

/**
 * The member and backend pickers of "Try again": the same task can go to someone else (reassign). "As before" is
 * whoever the last task was asked for; when that member is gone or turned off, it says so and cannot be picked.
 */
const props = defineProps<{ asked?: TicketAsk; failure?: TicketFailure }>()
const choice = defineModel<RetryChoice>({ required: true })

const { t } = useI18n()
const team = useTeamStore()
onMounted(() => team.ensureMembers())

const usable = computed(() => team.members.filter((m) => m.enabled && !m.invalid))
/** "As before" spelled out: who it is, and why it cannot be picked when the member is gone. */
const same = computed<SelectOption<string>>(() => {
  const f = props.failure
  if (f?.member) {
    const key = f.reason === 'member_disabled' ? 'retrySameDisabled' : 'retrySameMissing'
    return { value: '', label: t(`issues.actions.${key}`, { member: f.member }), disabled: true }
  }
  const who = props.asked?.member || props.asked?.backend
  return {
    value: '',
    label: who ? t('issues.actions.retrySameWho', { who }) : t('issues.actions.retrySame'),
  }
})
const members = computed<SelectOption<string>[]>(() => [
  same.value,
  ...usable.value.map((m) => ({ value: m.id, label: m.name })),
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
    <Select
      v-if="usable.length"
      v-model="member"
      :label="t('issues.triage.member')"
      :hint="failure ? t('issues.actions.retryPickOther') : undefined"
      :options="members"
    />
    <p v-else class="pick__none">
      {{ t(failure ? 'issues.actions.retryNoMembers' : 'issues.actions.retryNoMembersSame') }}
    </p>
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

.pick__none {
  align-self: center;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

@media (width < 600px) {
  .pick {
    grid-template-columns: 1fr;
  }
}
</style>
