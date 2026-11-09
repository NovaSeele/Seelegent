<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { KeyTestResult, Notice } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'

import { keyTestMessage, type Translate } from './keyTest'

/**
 * What a key test found ("Key works. 14 models available." or the reason with its fix) and what the daemon
 * changed or advises (notices: base URL shortened, preset switched, key looks like another service's, …). Both are
 * announced as they appear.
 */
const props = defineProps<{
  result: KeyTestResult | null
  notices?: readonly Notice[]
  testing?: boolean
}>()

const { t } = useI18n()

const tone = computed(() => {
  const r = props.result
  if (!r) return 'neutral'
  if (r.ok) return 'success'
  return r.reason === 'no_models_endpoint' ? 'neutral' : 'danger'
})
const message = computed(() => (props.result ? keyTestMessage(props.result, t as Translate) : ''))
const noticeTexts = computed(() => [
  ...new Set((props.notices ?? []).map((n) => t(`accounts.notice.${n.code}`))),
])
</script>

<template>
  <div class="note" role="status" aria-live="polite">
    <p v-if="testing" class="note__line">{{ t('accounts.form.testing') }}</p>
    <p v-else-if="result" class="note__line note__box" :class="`tone-${tone}`">
      <Icon :name="result.ok ? 'success' : tone === 'danger' ? 'error' : 'info'" />
      <span>{{ message }}</span>
    </p>
    <ul v-if="noticeTexts.length" class="note__notices">
      <li v-for="text in noticeTexts" :key="text">{{ text }}</li>
    </ul>
  </div>
</template>

<style scoped>
.note:empty {
  display: none;
}

.note {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.note__line {
  font-size: var(--fs-sm);
}

.note__box {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  color: var(--tone-fg);
}

.note__box :deep(svg) {
  flex: none;
  margin-top: 2px;
}

.note__notices {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  margin: 0;
  padding-inline-start: var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
