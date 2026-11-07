<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Tier, TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import Select from '@/components/ui/Select.vue'
import { canFollowup, isFinished } from '@/lib/taskStatus'
import { useAccountsStore } from '@/stores/accounts'
import { useConnectionStore } from '@/stores/connection'
import { useDraftsStore } from '@/stores/drafts'

import type { TaskActions } from './useTaskActions'

/**
 * Sends the worker another turn (by: user) on the same conversation, optionally on another tier or model.
 * Mod+Enter sends; the draft survives switching tasks. One line while idle and empty; it opens up (tier, model,
 * hint) on focus or once it holds text. While the worker cannot take a follow-up it is one line saying why.
 */
const props = defineProps<{ task: TaskSummary; actions: TaskActions }>()

const { t } = useI18n()
const id = useId()
const drafts = useDraftsStore()
const connection = useConnectionStore()
const accounts = useAccountsStore()

const form = ref<HTMLFormElement | null>(null)
const input = ref<HTMLTextAreaElement | null>(null)
const message = computed({
  get: () => drafts.get('followup', props.task.id),
  set: (value: string) => drafts.set('followup', props.task.id, value),
})
const tier = ref<Tier | ''>('')
const model = ref('')
const focused = ref(false)

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
const expanded = computed(
  () => focused.value || !!message.value || !!tier.value || !!model.value || sending.value,
)

/** Why a follow-up cannot be sent now. */
const blockedReason = computed(() => {
  const task = props.task
  if (task.manual) return t('task.followup.driving')
  if (task.attention?.state === 'question' && !isFinished(task)) {
    return t('task.followup.waitingAnswer')
  }
  return t('task.followup.busy')
})

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

function onFocusOut(event: FocusEvent) {
  const next = event.relatedTarget
  if (!(next instanceof Node) || !form.value?.contains(next)) focused.value = false
}

defineExpose({ focus: () => input.value?.focus() })
</script>

<template>
  <p v-if="!allowed" class="fc fc--blocked" role="status">
    <Icon name="info" class="fc__icon" />
    <span>{{ blockedReason }}</span>
  </p>
  <form
    v-else
    ref="form"
    class="fc"
    :class="{ 'fc--open': expanded }"
    :aria-labelledby="`${id}-label`"
    @submit.prevent="send"
    @focusin="focused = true"
    @focusout="onFocusOut"
  >
    <label
      :id="`${id}-label`"
      :for="`${id}-msg`"
      class="fc__label"
      :class="{ 'sr-only': !expanded }"
    >
      {{ t('task.followup.label') }}
    </label>
    <div class="fc__line">
      <textarea
        :id="`${id}-msg`"
        ref="input"
        v-model="message"
        class="fc__input"
        :rows="expanded ? 3 : 1"
        :placeholder="t('task.followup.placeholder')"
        :disabled="sending"
        :aria-describedby="`${id}-hint`"
        @keydown="onKeydown"
      />
      <Button
        v-if="!expanded"
        type="submit"
        size="sm"
        variant="primary"
        icon="send"
        :disabled="!message.trim()"
      >
        {{ t('task.followup.send') }}
      </Button>
    </div>
    <div v-if="expanded" class="fc__row">
      <Select
        v-model="tier"
        :label="t('task.followup.tierLabel')"
        hide-label
        size="sm"
        :options="tierOptions"
      />
      <Select
        v-if="modelOptions.length > 1"
        v-model="model"
        :label="t('task.followup.modelLabel')"
        hide-label
        size="sm"
        :options="modelOptions"
      />
      <span :id="`${id}-hint`" class="fc__hint">{{ t('task.followup.hint') }}</span>
      <Button
        type="submit"
        size="sm"
        variant="primary"
        icon="send"
        :loading="sending"
        :disabled="!message.trim()"
      >
        {{ t('task.followup.send') }}
      </Button>
    </div>
    <span v-else :id="`${id}-hint`" class="sr-only">{{ t('task.followup.hint') }}</span>
  </form>
</template>

<style scoped>
.fc {
  display: flex;
  flex: none;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-5);
  border-top: var(--border-width) solid var(--border-subtle);
  background: var(--bg-surface);
}

.fc--open {
  padding-block: var(--space-3);
}

.fc--blocked {
  flex-direction: row;
  align-items: center;
  min-height: calc(var(--control-sm) + 2 * var(--space-2));
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.fc__icon {
  flex: none;
  color: var(--text-muted);
}

.fc__label {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.fc__line {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.fc__input {
  flex: 1;
  min-width: 0;
  max-height: 40vh;
  padding: var(--space-1) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-app);
  color: var(--text-primary);
  font: inherit;
  font-size: var(--fs-sm);
  line-height: var(--lh-ui);
  resize: none;
}

.fc--open .fc__input {
  min-height: 64px;
  padding-block: var(--space-2);
  resize: vertical;
}

.fc__input:focus-visible {
  outline: none;
  border-color: var(--border-focus);
  box-shadow: var(--focus-ring);
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
    padding-inline: var(--space-4);
  }

  .fc__hint {
    display: none;
  }
}
</style>
