<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import Icon from '@/components/ui/Icon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import { NAV_ITEMS, PALETTE_SHORTCUT, SETTINGS_SHORTCUT, type NavId } from '@/config/nav'
import { usePaletteStore } from '@/stores/palette'

/**
 * Side navigation from 600px. At 900px and up each entry shows its icon with a short label under it (68px);
 * between 600 and 899px only the icon, with the name in a tooltip (56px). Work pages come first, then a
 * divider and the team pages; the palette and settings sit at the bottom.
 */
const props = defineProps<{ active?: NavId; compact: boolean }>()

const { t } = useI18n()
const palette = usePaletteStore()

const groups = computed(() => [
  NAV_ITEMS.filter((item) => item.group === 'work'),
  NAV_ITEMS.filter((item) => item.group === 'team'),
])

/** The tooltip shows the full name when the rail shows nothing or only the short label. */
function needsTooltip(id: NavId): boolean {
  return props.compact || t(`nav.${id}.short`) !== t(`nav.${id}.label`)
}
</script>

<template>
  <nav class="rail" :class="{ 'rail--compact': compact }" :aria-label="t('nav.rail.main')">
    <RouterLink to="/" class="rail__brand" :aria-label="t('nav.brand.title')">
      <Icon name="brand" />
    </RouterLink>
    <div class="rail__groups">
      <ul v-for="(items, index) in groups" :key="index" class="rail__list">
        <li v-for="item in items" :key="item.id">
          <Tooltip
            :text="t(`nav.${item.id}.label`)"
            :shortcut="item.shortcut"
            placement="right"
            :disabled="!needsTooltip(item.id)"
          >
            <RouterLink
              :to="item.path"
              class="rail__item"
              :class="{ 'rail__item--active': active === item.id }"
              :aria-current="active === item.id ? 'page' : undefined"
              :aria-label="compact ? t(`nav.${item.id}.label`) : undefined"
            >
              <span class="rail__icon"><Icon :name="item.icon" /></span>
              <span v-if="!compact" class="rail__label">{{ t(`nav.${item.id}.short`) }}</span>
            </RouterLink>
          </Tooltip>
        </li>
      </ul>
    </div>
    <div class="rail__footer">
      <IconButton
        icon="search"
        size="lg"
        :label="t('palette.open')"
        :shortcut="PALETTE_SHORTCUT"
        tooltip-placement="right"
        @click="palette.open()"
      />
      <IconButton
        icon="settings"
        size="lg"
        :label="t('nav.settings.label')"
        :shortcut="SETTINGS_SHORTCUT"
        tooltip-placement="right"
        :to="{ name: 'settings' }"
      />
    </div>
  </nav>
</template>

<style scoped>
.rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: var(--rail-width);
  height: 100%;
  padding: var(--space-2) 0;
  gap: var(--space-2);
  overflow: hidden;
}

.rail--compact {
  width: var(--rail-width-compact);
}

.rail__brand {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  flex: none;
  width: 40px;
  height: 40px;
  border-radius: var(--radius-md);
  color: var(--accent);
  font-size: 24px;
}

.rail__brand:hover {
  background: var(--bg-hover);
}

.rail__groups {
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: var(--space-2);
  width: 100%;
  min-height: 0;
  overflow: hidden auto;
  scrollbar-width: none;
}

.rail__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  margin: 0;
  padding: 0 var(--space-1);
  list-style: none;
}

.rail__list + .rail__list {
  padding-top: var(--space-2);
  border-top: var(--border-width) solid var(--border-subtle);
  margin: 0 var(--space-2);
  padding-left: 0;
  padding-right: 0;
}

.rail__list :deep(.tooltip-anchor) {
  display: block;
}

.rail__item {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-0);
  min-height: 44px;
  padding: var(--space-0) 0;
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  text-decoration: none;
  transition:
    background-color var(--duration-fast) var(--ease-standard),
    color var(--duration-fast) var(--ease-standard);
}

.rail--compact .rail__item {
  min-height: 40px;
}

.rail__item:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

/* The active page is marked by a pill behind its icon, not by the whole item, so labels stay readable. */
.rail__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 36px;
  height: 28px;
  border-radius: var(--radius-pill);
  font-size: var(--icon-lg);
}

.rail__item--active {
  color: var(--text-primary);
}

.rail__item--active .rail__icon {
  background: var(--accent-soft);
  color: var(--accent);
}

.rail__item--active:hover {
  background: transparent;
}

/* Up to two lines: short labels in some languages are two words ("Bằng chứng"); the tooltip has the full name. */
.rail__label {
  display: -webkit-box;
  max-width: 100%;
  padding: 0 var(--space-0);
  overflow: hidden;
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
  line-height: var(--lh-tight);
  text-align: center;
  overflow-wrap: break-word;
  hyphens: auto;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 2;
  line-clamp: 2;
}

.rail__item--active .rail__label {
  font-weight: var(--fw-semibold);
}

.rail__footer {
  display: flex;
  flex-direction: column;
  align-items: center;
  gap: var(--space-1);
}
</style>
