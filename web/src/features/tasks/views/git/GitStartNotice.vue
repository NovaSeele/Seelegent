<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'

import type { StartNotice } from './useGitHistory'

/**
 * Says plainly when the Git view cannot follow the task's history: the start was never recorded, it is no longer
 * in the branch's history, the repository is on another branch than the task started on, or the commits since the
 * start could not be read this time.
 */
defineProps<{ notices: readonly StartNotice[] }>()

const { t } = useI18n()
</script>

<template>
  <ul v-if="notices.length" class="notices">
    <li
      v-for="notice in notices"
      :key="notice.kind"
      class="notice"
      :class="notice.kind === 'unknown' ? 'tone-neutral' : 'tone-attention'"
      :data-kind="notice.kind"
    >
      <Icon :name="notice.kind === 'unknown' ? 'info' : 'warning'" />
      <p v-if="notice.kind === 'unknown'">{{ t('git.notice.unknown') }}</p>
      <p v-else-if="notice.kind === 'historyUnavailable'">
        {{ t('git.notice.historyUnavailable') }}
      </p>
      <p v-else-if="notice.kind === 'unreachable'">
        {{ t('git.notice.unreachable', { sha: notice.start.slice(0, 10) }) }}
      </p>
      <p v-else>
        {{
          t('git.notice.branchChanged', {
            from: notice.from,
            to: notice.to ?? t('git.notice.detached'),
          })
        }}
      </p>
    </li>
  </ul>
</template>

<style scoped>
.notices {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.notice {
  display: flex;
  align-items: flex-start;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.notice :deep(svg) {
  flex: none;
  margin-top: 0.15em;
  color: var(--tone-fg);
}

.notice p {
  min-width: 0;
  max-width: var(--measure-prose);
  overflow-wrap: anywhere;
}
</style>
