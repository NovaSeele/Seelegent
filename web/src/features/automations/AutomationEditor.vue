<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Automation } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAutomationsStore } from '@/stores/automations'
import { useToastStore } from '@/stores/toast'

import AutomationAdvanced from './AutomationAdvanced.vue'
import {
  changesRun,
  problems as findProblems,
  toForm,
  toInput,
  toPatch,
  type AutomationForm,
  type FormProblem,
} from './automationModel'
import ScheduleFields from './ScheduleFields.vue'
import { useFormFields } from './useFormFields'

/**
 * Creates an automation or edits one. A new automation is always saved off: turning it on is the separate,
 * confirmed step on its card. Editing what a run does turns an enabled automation off until it is confirmed again
 * (the editor says so before saving).
 */
const props = defineProps<{ automation: Automation | null }>()
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ saved: [automation: Automation] }>()

const { t } = useI18n()
const store = useAutomationsStore()
const toast = useToastStore()

const form = ref<AutomationForm>(toForm(null))
const shown = ref<readonly FormProblem[]>([])
const busy = ref(false)
const error = ref('')

watch(open, (now) => {
  if (!now) return
  form.value = toForm(props.automation)
  shown.value = []
  error.value = ''
})

const editing = computed(() => props.automation !== null)
const turnsOff = computed(
  () => !!props.automation?.enabled && changesRun(form.value, props.automation),
)
const field = useFormFields(form)
const name = field('name')
const prompt = field('prompt')
const cwd = field('cwd')

async function submit() {
  if (busy.value) return
  shown.value = findProblems(form.value)
  if (shown.value.length) {
    error.value = t('automations.editor.fix')
    return
  }
  busy.value = true
  error.value = ''
  try {
    const saved = props.automation
      ? await store.patch(props.automation.id, toPatch(form.value))
      : await store.create(toInput(form.value))
    toast.show({
      message: editing.value ? t('automations.editor.savedEdit') : t('automations.editor.savedNew'),
      tone: 'success',
    })
    open.value = false
    emit('saved', saved)
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="open"
    size="lg"
    :title="editing ? t('automations.editor.editTitle') : t('automations.editor.newTitle')"
    :description="t('automations.editor.desc')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="open = $event"
  >
    <form id="automation-editor" class="form" novalidate @submit.prevent="submit">
      <TextField
        v-model="name"
        :label="t('automations.editor.name')"
        :hint="t('automations.editor.nameHint')"
      />
      <TextArea
        v-model="prompt"
        :label="t('automations.editor.prompt')"
        :hint="t('automations.editor.promptHint')"
        :error="shown.includes('prompt') ? t('automations.editor.promptError') : undefined"
        :rows="6"
      />
      <TextField
        v-model="cwd"
        :label="t('automations.editor.cwd')"
        :hint="t('automations.editor.cwdHint')"
        :error="shown.includes('cwd') ? t('automations.editor.cwdError') : undefined"
      />
      <ScheduleFields v-model="form" :problems="shown" />
      <details
        class="form__more"
        :open="shown.includes('minRemaining') || shown.includes('graceMinutes')"
      >
        <summary>{{ t('automations.editor.more') }}</summary>
        <AutomationAdvanced
          v-model="form"
          class="form__adv"
          :problems="shown"
          :gate-can-reset="automation?.gate == null"
        />
      </details>
      <p v-if="turnsOff" class="form__warn" role="status">{{ t('automations.editor.turnsOff') }}</p>
      <p v-if="error" class="form__error" role="alert">{{ error }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="automation-editor" variant="primary" :loading="busy">
        {{ editing ? t('automations.editor.save') : t('automations.editor.create') }}
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

.form__more summary {
  cursor: pointer;
  border-radius: var(--radius-sm);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.form__more summary:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.form__adv {
  margin-top: var(--space-3);
}

.form__warn {
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.form__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}
</style>
