<script setup lang="ts">
import { usePageWidth, type PageWidth } from './pageWidth'

/**
 * The body of a non-split page. `readable` centres it at --page-max-readable; `full` uses the whole workspace.
 * Without `width` it follows the route's default (meta.width; see pageWidth.ts and docs/ui-layout.md).
 */
const props = defineProps<{ width?: PageWidth }>()

const width = usePageWidth(() => props.width)
</script>

<template>
  <div class="page" :class="`page--${width}`" :data-page-width="width">
    <slot />
  </div>
</template>

<style scoped>
.page {
  width: 100%;
  min-height: 100%;
}

.page--readable {
  max-width: var(--page-max-readable);
  margin-inline: auto;
}
</style>
