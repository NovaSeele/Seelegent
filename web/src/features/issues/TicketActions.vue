<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import type { Repo, Ticket } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useGitHubStore } from '@/stores/github'

import { ticketDroppable, ticketFinished, ticketPushable } from './issueModel'

/**
 * What the user can do with a ticket: push it and open its draft pull request, record one opened by hand, start
 * another task or a task for failing checks, comment on the issue, drop the ticket, or remove a finished ticket's
 * worktree. The buttons follow the daemon's own rules; the daemon still refuses what the state does not allow.
 */
const props = defineProps<{ ticket: Ticket; repo: Repo | null }>()
const emit = defineEmits<{ changed: [ticket: Ticket] }>()

type Mode = 'pr' | 'link' | 'task' | 'comment' | 'drop'

const { t } = useI18n()
const router = useRouter()
const gh = useGitHubStore()

const mode = ref<Mode | null>(null)
const text = ref('')
const body = ref('')
const draft = ref(true)
const busy = ref(false)
const error = ref('')

const finished = computed(() => ticketFinished(props.ticket.status))
const pushable = computed(() => ticketPushable(props.ticket.status) && !!props.repo?.remote)
const onGitHub = computed(() => !!props.repo?.github)
const fixable = computed(() => !finished.value && props.ticket.checks?.state === 'failure')
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
  error.value = ''
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
      const n = Number(value.replace(/^#/, ''))
      if (!Number.isInteger(n) || n < 1) throw new Error(t('issues.actions.numberError'))
      return api.linkPR(id, { number: n })
    }
    case 'comment':
      if (!value) throw new Error(t('issues.actions.textError'))
      return api.comment(id, { body: value })
    case 'drop':
      return api.drop(id, { reason: value || undefined })
    case 'task': {
      if (!value) throw new Error(t('issues.actions.textError'))
      const res = await api.addTask(id, { prompt: value })
      mode.value = null
      await goToTask(res.taskId)
      return null
    }
  }
}

async function submit() {
  if (!mode.value || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const next = await run(mode.value)
    if (next) emit('changed', next)
    mode.value = null
  } catch (cause) {
    error.value = errorMessage(cause)
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
</script>

<template>
  <div class="acts">
    <Button v-if="pushable" variant="primary" size="sm" icon="git" @click="start('pr')">
      {{ ticket.pr?.number ? t('issues.actions.push') : t('issues.actions.pr') }}
    </Button>
    <Button
      v-if="fixable"
      variant="primary"
      size="sm"
      icon="warning"
      :loading="busy && mode === null"
      @click="fixChecks"
    >
      {{ t('issues.actions.fixChecks') }}
    </Button>
    <Button v-if="pushable && onGitHub && !ticket.pr?.number" size="sm" @click="start('link')">
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
      <form id="ticket-action" class="form" @submit.prevent="submit">
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
        <TextField v-else-if="mode === 'link'" v-model="text" :label="t('issues.actions.number')" />
        <TextArea
          v-else-if="mode === 'task' || mode === 'comment'"
          v-model="text"
          :label="t(`issues.actions.field.${mode}`)"
          :rows="5"
        />
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
