<script setup lang="ts">
import { shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, isAbortError } from '@/api/client'
import type { ContestDiff } from '@/api/types'
import DiffView from '@/components/diff/DiffView.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useTeamStore } from '@/stores/team'

/**
 * The changes of one variant against the snapshot every variant started from, file by file (parsed by the daemon,
 * contract §3.3). Read when the variant is picked.
 */
const props = defineProps<{ contestId: string; label: string }>()

const { t } = useI18n()
const team = useTeamStore()
const diff = shallowRef<ContestDiff | null>(null)
const error = shallowRef('')
let controller: AbortController | null = null

watch(
  () => [props.contestId, props.label] as const,
  async ([id, label]) => {
    controller?.abort()
    const mine = new AbortController()
    controller = mine
    diff.value = null
    error.value = ''
    try {
      const d = await team.api().contestDiff(id, label, mine.signal)
      if (controller === mine) diff.value = d
    } catch (e) {
      if (controller === mine && !isAbortError(e)) error.value = errorMessage(e)
    }
  },
  { immediate: true },
)
</script>

<template>
  <section class="cdiff" :aria-label="t('team.contests.diff', { label })">
    <h3 class="cdiff__title">{{ t('team.contests.diff', { label }) }}</h3>
    <p v-if="error" class="cdiff__note" role="alert">
      {{ t('team.contests.diffFailed', { error }) }}
    </p>
    <p v-else-if="!diff" class="cdiff__note">
      <Spinner size="sm" decorative />
      {{ t('team.contests.diffLoading') }}
    </p>
    <p v-else-if="!diff.files.length" class="cdiff__note">{{ t('team.contests.diffEmpty') }}</p>
    <template v-else>
      <div v-for="f in diff.files" :key="f.path" class="cdiff__file">
        <code class="cdiff__path">{{ f.oldPath ? `${f.oldPath} → ` : '' }}{{ f.path }}</code>
        <DiffView :diff="f" :limit="600" />
      </div>
    </template>
  </section>
</template>

<style scoped>
.cdiff {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.cdiff__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.cdiff__note {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.cdiff__file {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.cdiff__path {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}
</style>
