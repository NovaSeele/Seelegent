<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import type { Issue, TriageRequest } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import ChoiceGroup, { type Choice } from '@/components/ui/ChoiceGroup.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useGitHubStore } from '@/stores/github'
import { useTeamStore } from '@/stores/team'

/**
 * Decides an issue: assign makes a ticket (its own worktree and branch) with a delegated task, meeting opens a
 * meeting in that worktree, skip records the decision with a reason. The issue text goes to the worker fenced as
 * untrusted data; the brief here is the user's own words.
 */
const props = defineProps<{ issue: Issue | null }>()
const open = defineModel<boolean>('open', { required: true })

type Action = TriageRequest['action']

const { t } = useI18n()
const router = useRouter()
const gh = useGitHubStore()
const team = useTeamStore()

const action = ref<Action>('assign')
const prompt = ref('')
const member = ref('')
const model = ref('')
const question = ref('')
const attendees = ref<string[]>([])
const reason = ref('')
const busy = ref(false)
const error = ref('')

const choices = computed<Choice<Action>[]>(() =>
  (['assign', 'meeting', 'skip'] as const).map((value) => ({
    value,
    label: t(`issues.triage.action.${value}`),
    description: t(`issues.triage.actionDesc.${value}`),
  })),
)
const usable = computed(() => team.members.filter((m) => m.enabled && !m.invalid))
const memberOptions = computed<SelectOption<string>[]>(() => [
  { value: '', label: t('issues.triage.anyWorker') },
  ...usable.value.map((m) => ({ value: m.id, label: m.name })),
])

watch(open, (now) => {
  if (!now) return
  team.ensureMembers()
  error.value = ''
  action.value = props.issue?.suggestion?.action ?? 'assign'
  member.value = props.issue?.suggestion?.member ?? ''
  question.value = props.issue?.title ?? ''
  reason.value = props.issue?.suggestion?.reason ?? ''
})

function body(): TriageRequest | null {
  if (action.value === 'skip' && !reason.value.trim()) {
    error.value = t('issues.triage.reasonError')
    return null
  }
  if (action.value === 'meeting' && attendees.value.length < 1) {
    error.value = t('issues.triage.attendeesError')
    return null
  }
  const req: TriageRequest = { action: action.value, by: 'user' }
  if (reason.value.trim()) req.reason = reason.value.trim()
  if (action.value === 'assign') {
    if (prompt.value.trim()) req.prompt = prompt.value.trim()
    if (member.value) req.member = member.value
    if (model.value.trim()) req.model = model.value.trim()
  }
  if (action.value === 'meeting') {
    req.question = question.value.trim() || undefined
    req.attendees = attendees.value.map((id) => ({ member: id }))
  }
  return req
}

async function submit() {
  const issue = props.issue
  const req = body()
  if (!issue || !req || busy.value) return
  busy.value = true
  error.value = ''
  try {
    const res = await gh.api().triage(gh.repoId, issue.key, req)
    gh.putIssue(res.issue)
    open.value = false
    prompt.value = ''
    attendees.value = []
    if (res.ticket) void router.push({ name: 'issues', params: { ticketId: res.ticket.id } })
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}

function toggle(id: string, on: boolean) {
  attendees.value = on ? [...attendees.value, id] : attendees.value.filter((a) => a !== id)
}
</script>

<template>
  <Dialog
    :open="open"
    size="lg"
    :title="t('issues.triage.title', { key: issue?.key ?? '' })"
    :description="issue?.title"
    :dismissible="!busy"
    @update:open="open = $event"
  >
    <form id="triage-issue" class="form" @submit.prevent="submit">
      <ChoiceGroup v-model="action" :legend="t('issues.triage.decision')" :options="choices" />
      <template v-if="action === 'assign'">
        <TextArea
          v-model="prompt"
          :label="t('issues.triage.prompt')"
          :hint="t('issues.triage.promptHint')"
          :rows="5"
        />
        <div class="form__row">
          <Select v-model="member" :label="t('issues.triage.member')" :options="memberOptions" />
          <TextField
            v-model="model"
            :label="t('issues.triage.model')"
            :hint="t('issues.triage.modelHint')"
          />
        </div>
      </template>
      <template v-else-if="action === 'meeting'">
        <TextField v-model="question" :label="t('issues.triage.question')" />
        <fieldset class="form__set">
          <legend class="form__legend">{{ t('issues.triage.attendees') }}</legend>
          <p v-if="!usable.length" class="form__hint">{{ t('issues.triage.noMembers') }}</p>
          <div class="form__checks">
            <Checkbox
              v-for="m in usable"
              :key="m.id"
              :model-value="attendees.includes(m.id)"
              :label="m.name"
              @update:model-value="toggle(m.id, $event)"
            />
          </div>
        </fieldset>
      </template>
      <TextField
        v-model="reason"
        :label="t('issues.triage.reason')"
        :hint="action === 'skip' ? t('issues.triage.reasonRequired') : undefined"
      />
      <p v-if="error" class="form__error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="triage-issue" variant="primary" :loading="busy">
        {{ t(`issues.triage.submit.${action}`) }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.form__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
  align-items: start;
  gap: var(--space-3);
}

.form__set {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.form__legend {
  padding: 0;
  margin-bottom: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.form__hint {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.form__checks {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 180px), 1fr));
  gap: var(--space-2);
}

.form__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
