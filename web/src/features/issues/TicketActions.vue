<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { ApiError, errorMessage } from '@/api/client'
import type { Repo, Ticket } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useGitHubStore } from '@/stores/github'

import {
  isPushRefusal,
  prNumber,
  ticketDroppable,
  ticketFinished,
  ticketPhase,
  ticketPushable,
} from './issueModel'
import RetryFields, { type RetryChoice } from './RetryFields.vue'

/**
 * What the user can do with a ticket: push it and open its draft pull request, record one opened by hand, start
 * another task or a task for failing checks, start a failed ticket again (with another member or backend),
 * comment on the issue, drop the ticket, or remove a finished ticket's worktree. The next step the ticket waits for
 * is offered by its banner (TicketNextStep), which calls act(); the buttons here leave it out. The daemon still
 * refuses what the state does not allow.
 */
const props = defineProps<{ ticket: Ticket; repo: Repo | null }>()
const emit = defineEmits<{ changed: [ticket: Ticket]; refused: [] }>()

type Mode = 'pr' | 'link' | 'task' | 'comment' | 'drop' | 'retry'

const { t } = useI18n()
const router = useRouter()
const gh = useGitHubStore()

const mode = ref<Mode | null>(null)
const text = ref('')
const body = ref('')
const draft = ref(true)
const retry = ref<RetryChoice>({ member: '', backend: '' })
const busy = ref(false)
const error = ref('')
const fieldError = ref('')

const phase = computed(() => ticketPhase(props.ticket))
const finished = computed(() => ticketFinished(props.ticket.status))
const pushable = computed(() => ticketPushable(props.ticket.status) && !!props.repo?.remote)
const onGitHub = computed(() => !!props.repo?.github)
/** A ticket whose member is gone needs another member or a backend: "as before" would fail again. */
const retryPicked = computed(
  () => !props.ticket.failure || !!retry.value.member || !!retry.value.backend,
)
const fixable = computed(() => !finished.value && props.ticket.checks?.state === 'failure')
/** The banner offers these as the next step; a second button here would compete with it. */
const inBanner = computed(() => ({
  pr: ['ready', 'push_refused', 'open_pr'].includes(phase.value),
  link: phase.value === 'open_pr',
  fixChecks: phase.value === 'checks_failed',
}))
const open = computed({
  get: () => mode.value !== null,
  set: (v: boolean) => {
    if (!v && !busy.value) mode.value = null
  },
})

function start(next: Mode) {
  mode.value = next
  text.value = ''
  body.value = ''
  draft.value = true
  retry.value = { member: '', backend: '' }
  error.value = ''
  fieldError.value = ''
}

async function goToTask(taskId: string) {
  await router.push({ name: 'tasks', params: { taskId } })
}

async function run(next: Mode): Promise<Ticket | null> {
  const id = props.ticket.id
  const api = gh.api()
  const value = text.value.trim()
  switch (next) {
    case 'pr':
      return api.openPR(id, {
        title: value || undefined,
        body: body.value.trim() || undefined,
        draft: draft.value,
      })
    case 'link': {
      const n = prNumber(value)
      if (n === null) {
        fieldError.value = t('issues.actions.numberError')
        throw new Error('')
      }
      return api.linkPR(id, { number: n })
    }
    case 'comment':
      if (!value) throw new Error(t('issues.actions.textError'))
      return api.comment(id, { body: value })
    case 'drop':
      return api.drop(id, { reason: value || undefined })
    case 'task':
    case 'retry': {
      if (next === 'task' && !value) throw new Error(t('issues.actions.textError'))
      if (next === 'retry' && !retryPicked.value)
        throw new Error(t('issues.actions.retryPickOther'))
      const req = { prompt: value, ...pick(retry.value) }
      const res = next === 'task' ? await api.addTask(id, req) : await api.retry(id, req)
      mode.value = null
      await goToTask(res.taskId)
      return null
    }
  }
}

/** The member and backend of a retry, only when chosen (the ticket's own otherwise). */
function pick(c: RetryChoice): { member?: string; backend?: string } {
  return { member: c.member || undefined, backend: c.backend || undefined }
}

async function submit() {
  if (!mode.value || busy.value) return
  busy.value = true
  error.value = ''
  fieldError.value = ''
  try {
    const next = await run(mode.value)
    if (next) emit('changed', next)
    mode.value = null
  } catch (cause) {
    if (cause instanceof ApiError && isPushRefusal(cause.details)) {
      // The refusal is kept on the ticket: its banner explains it, with each file and the next step.
      mode.value = null
      emit('refused')
    } else if (!fieldError.value) {
      error.value = errorMessage(cause)
    }
  } finally {
    busy.value = false
  }
}

async function direct(action: () => Promise<void>) {
  if (busy.value) return
  busy.value = true
  error.value = ''
  try {
    await action()
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}

const fixChecks = () =>
  direct(async () => goToTask((await gh.api().fixChecks(props.ticket.id, {})).taskId))
const cleanup = () => direct(async () => emit('changed', await gh.api().cleanup(props.ticket.id)))

/** The next step the banner offers. */
function act(action: 'pr' | 'link' | 'retry' | 'fixChecks') {
  if (action === 'fixChecks') void fixChecks()
  else start(action)
}

defineExpose({ act })
</script>

<template>
  <div class="acts">
    <Button v-if="pushable && !inBanner.pr" size="sm" icon="git" @click="start('pr')">
      {{ ticket.pr?.number ? t('issues.actions.push') : t('issues.actions.pr') }}
    </Button>
    <Button
      v-if="fixable && !inBanner.fixChecks"
      size="sm"
      icon="warning"
      :loading="busy && mode === null"
      @click="fixChecks"
    >
      {{ t('issues.actions.fixChecks') }}
    </Button>
    <Button
      v-if="pushable && onGitHub && !ticket.pr?.number && !inBanner.link"
      size="sm"
      @click="start('link')"
    >
      {{ t('issues.actions.link') }}
    </Button>
    <Button v-if="!finished" size="sm" icon="plus" @click="start('task')">
      {{ t('issues.actions.task') }}
    </Button>
    <Button v-if="onGitHub && ticket.issue.number" size="sm" @click="start('comment')">
      {{ t('issues.actions.comment') }}
    </Button>
    <Button v-if="ticketDroppable(ticket.status)" size="sm" variant="ghost" @click="start('drop')">
      {{ t('issues.actions.drop') }}
    </Button>
    <Button
      v-if="finished && !ticket.worktreeRemoved"
      size="sm"
      variant="ghost"
      icon="trash"
      :loading="busy && mode === null"
      @click="cleanup"
    >
      {{ t('issues.actions.cleanup') }}
    </Button>
    <p v-if="error && mode === null" class="acts__error" role="alert">{{ error }}</p>
    <Dialog
      v-model:open="open"
      :title="mode ? t(`issues.actions.${mode}`) : ''"
      :description="mode ? t(`issues.actions.desc.${mode}`) : undefined"
      :dismissible="!busy"
      initial-focus="input, textarea"
    >
      <form id="ticket-action" class="form" novalidate @submit.prevent="submit">
        <template v-if="mode === 'pr'">
          <TextField
            v-model="text"
            :label="t('issues.actions.prTitle')"
            :hint="t('issues.actions.prTitleHint')"
          />
          <TextArea
            v-model="body"
            :label="t('issues.actions.prBody')"
            :hint="t('issues.actions.prBodyHint')"
            :rows="4"
          />
          <Checkbox v-model="draft" :label="t('issues.actions.draft')" />
        </template>
        <TextField
          v-else-if="mode === 'link'"
          v-model="text"
          inputmode="numeric"
          :label="t('issues.actions.number')"
          :hint="t('issues.actions.numberHint')"
          :error="fieldError || undefined"
          @update:model-value="fieldError = ''"
        />
        <template v-else-if="mode === 'task' || mode === 'comment' || mode === 'retry'">
          <TextArea
            v-model="text"
            :label="t(`issues.actions.field.${mode}`)"
            :hint="mode === 'retry' ? t('issues.actions.retryPromptHint') : undefined"
            :rows="mode === 'retry' ? 3 : 5"
          />
          <RetryFields
            v-if="mode === 'retry'"
            v-model="retry"
            :asked="ticket.asked"
            :failure="ticket.failure"
          />
        </template>
        <TextField
          v-else-if="mode === 'drop'"
          v-model="text"
          :label="t('issues.actions.field.drop')"
        />
        <p v-if="error" class="acts__error" role="alert">{{ error }}</p>
      </form>
      <template #footer>
        <Button variant="ghost" :disabled="busy" @click="open = false">
          {{ t('common.action.cancel') }}
        </Button>
        <Button
          type="submit"
          form="ticket-action"
          :variant="mode === 'drop' ? 'danger' : 'primary'"
          :loading="busy"
          :disabled="mode === 'retry' && !retryPicked"
        >
          {{ mode ? t(`issues.actions.submit.${mode}`) : '' }}
        </Button>
      </template>
    </Dialog>
  </div>
</template>

<style scoped>
.acts {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.acts__error {
  flex-basis: 100%;
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
</style>
