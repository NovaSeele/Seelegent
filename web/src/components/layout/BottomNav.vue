<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import Icon from '@/components/ui/Icon.vue'
import Sheet from '@/components/ui/Sheet.vue'
import { NAV_ITEMS, type NavId } from '@/config/nav'

/**
 * Phone bottom bar (56px): the four main pages and "More", which opens a bottom sheet with the other pages and
 * settings. "More" shows as active while one of its pages is open.
 */
const props = defineProps<{ active?: NavId }>()

const { t } = useI18n()
const moreOpen = ref(false)

const primary = NAV_ITEMS.filter((item) => item.mobilePrimary)
const secondary = NAV_ITEMS.filter((item) => !item.mobilePrimary)
const moreActive = computed(() => secondary.some((item) => item.id === props.active))
</script>

<template>
  <nav class="bottom-nav" :aria-label="t('nav.rail.mobile')">
    <RouterLink
      v-for="item in primary"
      :key="item.id"
      :to="item.path"
      class="bottom-nav__item"
      :class="{ 'bottom-nav__item--active': active === item.id }"
      :aria-current="active === item.id ? 'page' : undefined"
    >
      <span class="bottom-nav__icon"><Icon :name="item.icon" /></span>
      <span class="bottom-nav__label">{{ t(`nav.${item.id}.label`) }}</span>
    </RouterLink>
    <button
      type="button"
      class="bottom-nav__item"
      :class="{ 'bottom-nav__item--active': moreActive }"
      aria-haspopup="dialog"
      :aria-expanded="moreOpen"
      @click="moreOpen = true"
    >
      <span class="bottom-nav__icon"><Icon name="more" /></span>
      <span class="bottom-nav__label">{{ t('nav.more.label') }}</span>
    </button>
  </nav>
  <Sheet v-model:open="moreOpen" side="bottom" :title="t('nav.more.label')">
    <ul class="more-list">
      <li v-for="item in secondary" :key="item.id">
        <RouterLink
          :to="item.path"
          class="more-list__item"
          :class="{ 'more-list__item--active': active === item.id }"
          :aria-current="active === item.id ? 'page' : undefined"
          @click="moreOpen = false"
        >
          <Icon :name="item.icon" class="more-list__icon" />
          <span>{{ t(`nav.${item.id}.label`) }}</span>
        </RouterLink>
      </li>
      <li class="more-list__sep">
        <RouterLink :to="{ name: 'settings' }" class="more-list__item" @click="moreOpen = false">
          <Icon name="settings" class="more-list__icon" />
          <span>{{ t('nav.settings.label') }}</span>
        </RouterLink>
      </li>
    </ul>
  </Sheet>
</template>

<style scoped>
.bottom-nav {
  display: grid;
  grid-template-columns: repeat(5, minmax(0, 1fr));
  height: calc(var(--bottom-nav-height) + env(safe-area-inset-bottom));
  padding-bottom: env(safe-area-inset-bottom);
  border-top: var(--border-width) solid var(--border-subtle);
  background: var(--bg-app);
}

.bottom-nav__item {
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: var(--space-0);
  min-width: 0;
  border: 0;
  background: none;
  color: var(--text-secondary);
  font: inherit;
  text-decoration: none;
  cursor: pointer;
}

.bottom-nav__icon {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 48px;
  height: 28px;
  border-radius: var(--radius-pill);
  font-size: var(--icon-lg);
  transition: background-color var(--duration-fast) var(--ease-standard);
}

.bottom-nav__label {
  max-width: 100%;
  padding: 0 var(--space-1);
  overflow: hidden;
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
  line-height: var(--lh-tight);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.bottom-nav__item--active {
  color: var(--text-primary);
}

.bottom-nav__item--active .bottom-nav__icon {
  background: var(--accent-soft);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--accent);
}

.more-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  margin: 0;
  padding: 0;
  list-style: none;
}

.more-list__sep {
  margin-top: var(--space-1);
  padding-top: var(--space-1);
  border-top: var(--border-width) solid var(--border-subtle);
}

.more-list__item {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: max(var(--control-lg), var(--target-min));
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  color: var(--text-primary);
  text-decoration: none;
}

.more-list__item:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.more-list__item--active {
  background: var(--accent-soft);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--accent);
}

.more-list__icon {
  font-size: var(--icon-lg);
  color: var(--text-secondary);
}

.more-list__item--active .more-list__icon {
  color: inherit;
}
</style>
