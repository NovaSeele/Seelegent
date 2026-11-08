<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Upstream } from './useRepoStatus'

/**
 * The branch against its upstream, for the Git view's bar: "origin/main ↑2 ↓1", "✓" or "(gone)", all in the muted tone (amber is for what needs the user). Nothing
 * when the branch has no upstream. The arrows are spelled out for screen readers.
 */
const props = defineProps<{ upstream: Upstream | null }>()

const { t } = useI18n()

const text = computed(() => {
  const u = props.upstream
  if (!u) return ''
  if (u.gone) return t('git.upstream.gone', { name: u.name })
  if (!u.ahead && !u.behind) return t('git.upstream.synced', { name: u.name })
  const parts = []
  if (u.ahead) parts.push(t('git.upstream.ahead', { n: u.ahead }, u.ahead))
  if (u.behind) parts.push(t('git.upstream.behind', { n: u.behind }, u.behind))
  return `${t('git.upstream.label', { name: u.name })}: ${parts.join(', ')}`
})
</script>

<template>
  <span v-if="upstream" class="up" :title="text">
    <span aria-hidden="true" class="up__short">
      <span class="up__name">{{ upstream.name }}</span>
      <template v-if="!upstream.gone">
        <span v-if="upstream.ahead">↑{{ upstream.ahead }}</span>
        <span v-if="upstream.behind">↓{{ upstream.behind }}</span>
        <span v-if="!upstream.ahead && !upstream.behind">✓</span>
      </template>
      <span v-else class="up__gone">{{ t('git.upstream.goneShort') }}</span>
    </span>
    <span class="sr-only">{{ text }}</span>
  </span>
</template>

<style scoped>
.up {
  display: inline-flex;
  align-items: center;
  max-width: 100%;
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.up__short {
  display: inline-flex;
  flex-wrap: wrap;
  gap: var(--space-0) var(--space-1);
  min-width: 0;
}

.up__name {
  overflow-wrap: anywhere;
}

.up__gone {
  font-family: var(--font-sans);
}
</style>
