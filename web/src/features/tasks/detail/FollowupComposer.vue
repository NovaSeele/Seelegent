<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Tier, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import { canFollowup } from '@/lib/taskStatus'
import { useAccountsStore } from '@/stores/accounts'
import { useConnectionStore } from '@/stores/connection'
import { useDraftsStore } from '@/stores/drafts'

import type { TaskActions } from './useTaskActions'

/**
 * Sends the worker another turn (by: user) on the same conversation, optionally on another tier or model.
 * Mod+Enter sends. The draft survives switching tasks. Disabled, with the reason, while the worker is busy.
 */
const props = defineProps<{ task: TaskSummary; actions: TaskActions }>()

const { t } = useI18n()
const id = useId()
const drafts = useDraftsStore()
const connection = useConnectionStore()
const accounts = useAccountsStore()

const input = ref<HTMLTextAreaElement | null>(null)
const message = computed({
  get: () => drafts.get('followup', props.task.id),
  set: (value: string) => drafts.set('followup', props.task.id, value),
})
const tier = ref<Tier | ''>('')
const model = ref('')

const TIERS: readonly Tier[] = ['fast', 'standard', 'strong', 'max']
const tierOptions = computed(() => [
  { value: '' as const, label: t('task.followup.sameTier') },
  ...TIERS.map((value) => ({ value, label: t(`task.followup.tier.${value}`) })),
])

/** Models of the task's backend: its tier map and what its accounts report. */
const modelOptions = computed(() => {
  const backend = props.task.backend
  const set = new Set<string>()
  const tiers = connection.settings?.backends?.[backend as 'agy' | 'claude' | 'codex']?.tiers
  for (const value of Object.values(tiers ?? {})) if (value) set.add(value)
  for (const account of accounts.accounts) {
    if (account.backend === backend) for (const m of account.models) set.add(m)
  }
  return [
    { value: '', label: t('task.followup.sameModel') },
    ...[...set].sort().map((value) => ({ value, label: value })),
  ]
})

const allowed = computed(() => canFollowup(props.task))
const sending = computed(() => props.actions.busy.value === 'followup')

async function send() {
  const text = message.value.trim()
  if (!text || !allowed.value || sending.value) return
  const ok = await props.actions.followup({
    message: text,
    by: 'user',
    ...(tier.value ? { tier: tier.value } : {}),
    ...(model.value ? { model: model.value } : {}),
  })
  if (ok) {
    message.value = ''
    tier.value = ''
    model.value = ''
  }
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Enter' && (event.ctrlKey || event.metaKey)) {
    event.preventDefault()
    void send()
  }
}

defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <form class="fc" :aria-labelledby="`${id}-label`" @submit.prevent="send">
    <label :id="`${id}-label`" :for="`${id}-msg`" class="fc__label">
      {{ t('task.followup.label') }}
    </label>
    <textarea
      :id="`${id}-msg`"
      ref="input"
      v-model="message"
      class="fc__input"
      rows="3"
      :placeholder="allowed ? t('task.followup.placeholder') : t('task.followup.busy')"
      :disabled="!allowed || sending"
      :aria-describedby="`${id}-hint`"
      @keydown="onKeydown"
    />
    <div class="fc__row">
      <Select
        v-model="tier"
        :label="t('task.followup.tierLabel')"
        hide-label
        size="sm"
        :options="tierOptions"
        :disabled="!allowed"
      />
      <Select
        v-if="modelOptions.length > 1"
        v-model="model"
        :label="t('task.followup.modelLabel')"
        hide-label
        size="sm"
        :options="modelOptions"
        :disabled="!allowed"
      />
      <span :id="`${id}-hint`" class="fc__hint">{{ t('task.followup.hint') }}</span>
      <Button
        type="submit"
        size="sm"
        variant="primary"
        icon="send"
        :loading="sending"
        :disabled="!allowed || !message.trim()"
      >
        {{ t('task.followup.send') }}
      </Button>
    </div>
  </form>
</template>

<style scoped>
.fc {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-5) var(--space-4);
  border-top: var(--border-width) solid var(--border-subtle);
  background: var(--bg-surface);
}

.fc__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.fc__input {
  width: 100%;
  min-height: 64px;
  max-height: 40vh;
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-app);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  resize: vertical;
}

.fc__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: var(--focus-ring);
}

.fc__input:disabled {
  opacity: 0.7;
}

.fc__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.fc__hint {
  flex: 1;
  min-width: 120px;
  color: var(--text-muted);
  font-size: var(--fs-2xs);
}

@media (width < 600px) {
  .fc {
    padding: var(--space-3) var(--space-4);
  }

  .fc__hint {
    display: none;
  }
}
</style>
