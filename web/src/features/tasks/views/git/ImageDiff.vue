<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitImageUrl } from '@/api/git'
import type { GitFile } from '@/api/types'

import { imageSides } from './git'
import ImagePreview from './ImagePreview.vue'

/**
 * The preview of an image the task changed, against HEAD: before and after side by side (stacked on narrow
 * screens) for a change, after only for a new file, before only for a deleted one. Only this file's images load.
 */
const props = defineProps<{ taskId: string; file: GitFile }>()

const { t } = useI18n()
const sides = computed(() =>
  imageSides(props.file).map((s) => ({
    ...s,
    src: gitImageUrl(props.taskId, s.path, s.version, props.file.stamp),
    label: t(`git.image.${s.side}`),
  })),
)
</script>

<template>
  <div v-if="sides.length" class="idiff">
    <ImagePreview
      v-for="s in sides"
      :key="s.side"
      :src="s.src"
      :label="s.label"
      :alt="t('git.image.alt', { path: s.path, side: s.label })"
    />
  </div>
</template>

<style scoped>
.idiff {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
  align-items: flex-start;
}
</style>
