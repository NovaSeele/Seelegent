<script setup lang="ts">
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import Icon from '@/components/ui/Icon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import type { NavId } from '@/config/nav'
import { usePaletteStore } from '@/stores/palette'

/** Phone top bar (48px): brand, the current page's name, search and settings. */
defineProps<{ active?: NavId }>()

const { t } = useI18n()
const palette = usePaletteStore()
</script>

<template>
  <header class="top-bar">
    <RouterLink to="/" class="top-bar__brand" :aria-label="t('nav.brand.title')">
      <Icon name="brand" />
    </RouterLink>
    <p class="top-bar__title">{{ active ? t(`nav.${active}.label`) : 'Seelegent' }}</p>
    <IconButton
      icon="search"
      :label="t('palette.open')"
      :tooltip="false"
      size="lg"
      @click="palette.open()"
    />
    <IconButton
      icon="settings"
      :label="t('nav.settings.label')"
      :tooltip="false"
      size="lg"
      :to="{ name: 'settings' }"
    />
  </header>
</template>

<style scoped>
.top-bar {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  height: calc(var(--top-bar-height) + env(safe-area-inset-top));
  padding: env(safe-area-inset-top) var(--space-2) 0;
  border-bottom: var(--border-width) solid var(--border-subtle);
  background: var(--bg-app);
}

.top-bar__brand {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: var(--control-lg);
  height: var(--control-lg);
  border-radius: var(--radius-md);
  color: var(--accent);
  font-size: 22px;
}

.top-bar__title {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
