<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Contest } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Select from '@/components/ui/Select.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import { defaultWinner, pickableOut, qualifiedLabels } from './contestModel'

/**
 * The pick of a ready contest. It applies the proposed winner unless the person chooses another qualified variant;
 * a choice that stops qualifying (a new score) falls back to the proposal. When no variant qualifies, a separate
 * "apply anyway" asks to name one of the disqualified variants that still have work to apply, with the reason
 * each is out.
 */
const props = defineProps<{ contest: Contest }>()
const emit = defineEmits<{ picked: [contest: Contest] }>()

const { t } = useI18n()
const team = useTeamStore()
const toast = useToastStore()

const ready = computed(() => props.contest.status === 'ready')
const qualified = computed(() => qualifiedLabels(props.contest))
const outs = computed(() => pickableOut(props.contest))
/** Picking anyway: no variant qualifies, the person must name one. */
const anyway = computed(() => !qualified.value.length && outs.value.length > 0)

const chosen = ref('')
const winner = computed(() => {
  if (anyway.value) return outs.value.some((r) => r.label === chosen.value) ? chosen.value : ''
  return qualified.value.includes(chosen.value) ? chosen.value : defaultWinner(props.contest)
})
const options = computed(() =>
  anyway.value
    ? [
        { value: '', label: t('team.contests.choose'), disabled: true },
        ...outs.value.map((r) => ({
          value: r.label,
          label: t('team.contests.outOption', {
            label: r.label,
            reason: t(`team.contests.disqualified.${r.disqualified}`),
          }),
        })),
      ]
    : qualified.value.map((l) => ({ value: l, label: l })),
)

const open = ref(false)
const busy = ref(false)
const error = ref('')

function start() {
  chosen.value = anyway.value ? '' : winner.value
  error.value = ''
  open.value = true
}

async function pick() {
  const label = winner.value
  if (!label) return
  busy.value = true
  error.value = ''
  try {
    emit('picked', await team.api().pickContest(props.contest.id, { winner: label, by: 'user' }))
    toast.show({ message: t('team.contests.picked', { winner: label }), tone: 'success' })
    open.value = false
  } catch (e) {
    error.value = errorMessage(e)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <template v-if="ready && (qualified.length || anyway)">
    <Button v-if="anyway" variant="secondary" @click="start">
      {{ t('team.contests.pickAnyway') }}
    </Button>
    <Button v-else variant="primary" @click="start">
      {{ t('team.contests.pick', { winner }) }}
    </Button>
    <ConfirmDialog
      :open="open"
      :title="
        anyway
          ? t('team.contests.pickAnywayTitle')
          : t('team.contests.pickTitle', { winner: winner })
      "
      :description="t('team.contests.pickDesc', { cwd: contest.cwd })"
      :confirm-label="winner ? t('team.contests.pick', { winner }) : t('team.contests.pickNone')"
      :danger="anyway"
      :busy="busy"
      :error="error"
      :confirm-disabled="!winner"
      @update:open="open = false"
      @confirm="pick"
    >
      <p v-if="anyway" class="pick__warn">{{ t('team.contests.pickAnywayDesc') }}</p>
      <Select
        v-if="options.length > 1"
        v-model="chosen"
        :label="t('team.contests.winner')"
        :options="options"
      />
    </ConfirmDialog>
  </template>
</template>

<style scoped>
.pick__warn {
  margin-bottom: var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
