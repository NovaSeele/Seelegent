<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import TextField from '@/components/ui/TextField.vue'

import { checkKey, MAX_NOTICE_NAME } from './apiKey'

/**
 * The API key input of the add form and of "Replace key": hidden by default with a Show/Hide toggle, checked as
 * it is typed with the daemon's own rules (apiKey.ts), so a URL or a key with spaces inside is caught before
 * anything is sent. What the cleaning did ("Removed LLM_API_KEY= from the start") and a detected service are said
 * under it. `error` is the server's word on the key; `showEmpty` turns an empty field into an error (after a
 * submit). Errors are also announced (aria-live).
 */
const raw = defineModel<string>({ required: true })
const props = defineProps<{
  error?: string
  /** "Detected Anthropic key" is shown with this service name. */
  detected?: string
  showEmpty?: boolean
  label?: string
}>()

const { t } = useI18n()
const shown = ref(false)
const field = ref<InstanceType<typeof TextField> | null>(null)

const check = computed(() => checkKey(raw.value))
const error = computed(() => {
  if (props.error) return props.error
  const reason = check.value.error
  if (!reason || (reason === 'empty' && !props.showEmpty)) return ''
  return t(`accounts.keyError.${reason}`)
})
const notes = computed(() => {
  const c = check.value
  if (c.error) return []
  return [
    props.detected ? t('accounts.service.detected', { service: props.detected }) : '',
    // Like the daemon, a name longer than 40 characters is not quoted (it may be part of a key).
    c.removedName
      ? c.removedName.length <= MAX_NOTICE_NAME
        ? t('accounts.keyField.nameRemoved', { name: c.removedName })
        : t('accounts.notice.key_name_removed')
      : '',
    c.quotesRemoved ? t('accounts.notice.key_quotes_removed') : '',
    c.assignment ? t('accounts.notice.key_assignment') : '',
  ].filter(Boolean)
})

defineExpose({ focus: () => field.value?.focus(), check })
</script>

<template>
  <div class="keyfield">
    <TextField
      ref="field"
      v-model="raw"
      :type="shown ? 'text' : 'password'"
      autocomplete="new-password"
      :label="label ?? t('accounts.keyField.label')"
      :hint="t('accounts.keyField.hint')"
      :error="error || undefined"
    >
      <template #end>
        <button
          type="button"
          class="keyfield__toggle"
          :aria-pressed="shown"
          :aria-label="t('accounts.keyField.showLabel')"
          @click="shown = !shown"
        >
          {{ shown ? t('accounts.keyField.hide') : t('accounts.keyField.show') }}
        </button>
      </template>
    </TextField>
    <ul v-if="notes.length" class="keyfield__notes">
      <li v-for="note in notes" :key="note">{{ note }}</li>
    </ul>
    <!-- Errors and notes are read out as they appear; the field's own error text is not live. -->
    <p class="sr-only" aria-live="polite">{{ [error, ...notes].filter(Boolean).join('. ') }}</p>
  </div>
</template>

<style scoped>
.keyfield {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.keyfield__toggle {
  flex: none;
  padding: var(--space-0) var(--space-1);
  border: 0;
  border-radius: var(--radius-sm);
  background: none;
  color: var(--link);
  font: inherit;
  font-size: var(--fs-xs);
  cursor: pointer;
}

.keyfield__toggle:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 1px;
}

.keyfield__notes {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  margin: 0;
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  list-style: none;
}
</style>
