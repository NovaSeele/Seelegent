<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { GitFile } from '@/api/types'
import DiffView from '@/components/diff/DiffView.vue'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Switch from '@/components/ui/Switch.vue'

import type { DiffState } from './useGit'

/** The diff of the file selected in the Git view, with its path and the line-wrap switch. */
defineProps<{ file: GitFile | null | undefined; diff: DiffState }>()
const emit = defineEmits<{ retry: [] }>()
const wrap = defineModel<boolean>('wrap', { required: true })

const { t } = useI18n()
</script>

<template>
  <section class="pane" :aria-label="t('git.diffLabel')">
    <div v-if="file" class="pane__bar">
      <code class="pane__path">
        {{ file.oldPath ? `${file.oldPath} → ` : '' }}{{ file.path }}
      </code>
      <Switch v-model="wrap" :label="t('git.wrap')" />
    </div>
    <p v-if="diff.status === 'loading'" class="pane__state">
      <Spinner size="sm" decorative />
      {{ t('git.diff.loading') }}
    </p>
    <div v-else-if="diff.status === 'error'" class="pane__state" role="alert">
      <span>{{ t('git.diff.error', { error: diff.error }) }}</span>
      <Button size="sm" @click="emit('retry')">{{ t('common.action.retry') }}</Button>
    </div>
    <DiffView v-else-if="diff.status === 'ready'" :diff="diff.diff" :wrap="wrap" />
  </section>
</template>

<style scoped>
.pane {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.pane__bar {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.pane__path {
  min-width: 0;
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.pane__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
