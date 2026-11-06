<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import { AUTO_THEMES, THEME_GROUPS, THEMES, themeInfo, type ThemeChoice } from '@/config/themes'

import ThemePreview from './ThemePreview.vue'

/**
 * Theme grid: "Auto" first, then the themes by family, each card a live miniature of its palette. Native radio
 * inputs give arrow-key selection and screen-reader semantics.
 */
const model = defineModel<ThemeChoice>({ required: true })

const { t } = useI18n()

const groups = computed(() =>
  THEME_GROUPS.map((group) => ({
    group,
    themes: THEMES.filter((theme) => theme.group === group),
  })),
)

const autoNames = computed(() => ({
  light: themeInfo(AUTO_THEMES.light).name,
  dark: themeInfo(AUTO_THEMES.dark).name,
}))
</script>

<template>
  <div class="picker" role="radiogroup" aria-labelledby="setting-theme">
    <div class="picker__grid">
      <label class="card card--wide" :class="{ 'card--checked': model === 'auto' }">
        <input v-model="model" class="sr-only" type="radio" name="theme" value="auto" />
        <span class="card__preview">
          <ThemePreview :theme="AUTO_THEMES.light" />
          <ThemePreview :theme="AUTO_THEMES.dark" />
        </span>
        <span class="card__name">
          <Icon name="monitor" />
          {{ t('settings.appearance.auto.label') }}
        </span>
        <span class="card__hint">{{ t('settings.appearance.auto.desc', autoNames) }}</span>
      </label>
    </div>
    <section v-for="entry in groups" :key="entry.group" class="picker__group">
      <h4 class="picker__group-title">{{ t(`settings.appearance.themeGroup.${entry.group}`) }}</h4>
      <div class="picker__grid">
        <label
          v-for="theme in entry.themes"
          :key="theme.id"
          class="card"
          :class="{ 'card--checked': model === theme.id }"
        >
          <input v-model="model" class="sr-only" type="radio" name="theme" :value="theme.id" />
          <span class="card__preview"><ThemePreview :theme="theme.id" /></span>
          <span class="card__name">
            <Icon :name="theme.scheme === 'dark' ? 'moon' : 'sun'" />
            {{ theme.name }}
          </span>
        </label>
      </div>
    </section>
  </div>
</template>

<style scoped>
.picker {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.picker__group-title {
  margin-bottom: var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-xs);
  font-weight: var(--fw-semibold);
}

.picker__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(148px, 1fr));
  gap: var(--space-3);
}

/* Concentric: the preview sits 4px inside the 12px card, so it takes radius-md (12 - 4). */
.card {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
  padding: var(--space-1) var(--space-1) var(--space-2);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  cursor: pointer;
  transition: border-color var(--duration-fast) var(--ease-standard);
}

.card--wide {
  grid-column: span 2;
}

.card:hover {
  border-color: var(--border-strong);
}

.card--checked,
.card--checked:hover {
  border-color: var(--accent);
  box-shadow: 0 0 0 1px var(--accent);
}

.card:has(:focus-visible) {
  box-shadow: var(--focus-ring);
}

.card__preview {
  display: flex;
  height: 72px;
  overflow: hidden;
  border-radius: var(--radius-md);
}

.card__name {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  padding: 0 var(--space-1);
  color: var(--text-primary);
  font-size: var(--fs-sm);
  font-weight: var(--fw-medium);
}

.card__name :deep(svg) {
  flex: none;
  color: var(--text-muted);
}

.card__hint {
  padding: 0 var(--space-1);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

@media (width < 600px) {
  .picker__grid {
    grid-template-columns: repeat(auto-fill, minmax(132px, 1fr));
  }
}
</style>
