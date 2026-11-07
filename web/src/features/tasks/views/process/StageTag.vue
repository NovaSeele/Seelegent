<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import type { Stage } from './process'

/** A step's stage: a coloured dot and its name. */
defineProps<{ stage: Stage }>()

const { t } = useI18n()
</script>

<template>
  <span class="stage" :class="`stage--${stage}`">
    <span class="stage__dot" aria-hidden="true" />
    {{ t(`process.stage.${stage}`) }}
  </span>
</template>

<style scoped>
.stage {
  display: inline-flex;
  flex: none;
  align-items: center;
  gap: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-2xs);
  white-space: nowrap;
}

.stage__dot {
  width: 8px;
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--stage-color);
}

.stage--explore {
  --stage-color: var(--stage-explore);
}

.stage--edit {
  --stage-color: var(--stage-edit);
}

.stage--verify {
  --stage-color: var(--stage-verify);
}

.stage--run {
  --stage-color: var(--stage-run);
}

.stage--report {
  --stage-color: var(--stage-report);
}
</style>
