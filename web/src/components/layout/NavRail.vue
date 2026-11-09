<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink } from 'vue-router'

import Icon from '@/components/ui/Icon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Tooltip from '@/components/ui/Tooltip.vue'
import { NAV_ITEMS, PALETTE_SHORTCUT, SETTINGS_SHORTCUT, type NavId } from '@/config/nav'
import { usePaletteStore } from '@/stores/palette'

/**
 * Side navigation from 600px. At 900px and up each entry shows its icon with its label under it (76px);
 * between 600 and 899px only the icon (56px). Every entry has a tooltip with its name and its keyboard
 * shortcut ("Members  G E"), so the shortcuts can be learnt from the rail. Work pages come first, then a
 * divider and the team pages; the palette and settings sit at the bottom. Metrics: docs/ui-layout.md.
 */
defineProps<{ active?: NavId; compact: boolean }>()

const { t, locale } = useI18n()
const palette = usePaletteStore()

const groups = computed(() => [
  NAV_ITEMS.filter((item) => item.group === 'work'),
  NAV_ITEMS.filter((item) => item.group === 'team'),
])

// The page list scrolls when it does not fit (short windows, long labels). Its top and bottom edges fade out
// only while there is more to scroll that way, so a cut-off item reads as "scroll for more".
const list = ref<HTMLElement | null>(null)
const fadeTop = ref(false)
const fadeBottom = ref(false)

function updateFade() {
  const el = list.value
  if (!el) return
  fadeTop.value = el.scrollTop > 1
  fadeBottom.value = el.scrollTop + el.clientHeight < el.scrollHeight - 1
}

let observer: ResizeObserver | undefined
onMounted(() => {
  updateFade()
  if (typeof ResizeObserver === 'undefined' || !list.value) return
  observer = new ResizeObserver(updateFade)
  observer.observe(list.value)
  for (const child of list.value.children) observer.observe(child)
})
onBeforeUnmount(() => observer?.disconnect())
// Labels change length with the language.
watch(locale, () => void nextTick(updateFade))
</script>

<template>
  <nav class="rail" :class="{ 'rail--compact': compact }" :aria-label="t('nav.rail.main')">
    <RouterLink to="/" class="rail__brand" :aria-label="t('nav.brand.title')">
      <Icon name="brand" />
    </RouterLink>
    <div
      ref="list"
      class="rail__groups"
      :class="{ 'rail__groups--fade-top': fadeTop, 'rail__groups--fade-bottom': fadeBottom }"
      @scroll.passive="updateFade"
    >
      <ul v-for="(items, index) in groups" :key="index" class="rail__list">
        <li v-for="item in items" :key="item.id">
          <Tooltip :text="t(`nav.${item.id}.label`)" :shortcut="item.shortcut" placement="right">
            <RouterLink
              :to="item.path"
              class="rail__item"
              :class="{ 'rail__item--active': active === item.id }"
              :aria-current="active === item.id ? 'page' : undefined"
              :aria-label="compact ? t(`nav.${item.id}.label`) : undefined"
            >
              <span class="rail__icon"><Icon :name="item.icon" /></span>
              <span v-if="!compact" class="rail__label">{{ t(`nav.${item.id}.label`) }}</span>
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
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.rail__groups {
  position: relative;
  display: flex;
  flex-direction: column;
  flex: 1;
  gap: var(--space-2);
  width: 100%;
  min-height: 0;
  overflow: hidden auto;
  scrollbar-width: none;
  /* Edge fades, each on only while the list can scroll that way (mask alpha only; no theme colour). */
  --fade-top: 0px;
  --fade-bottom: 0px;
  mask-image: linear-gradient(
    to bottom,
    transparent 0,
    black var(--fade-top),
    black calc(100% - var(--fade-bottom)),
    transparent 100%
  );
}

.rail__groups--fade-top {
  --fade-top: var(--space-6);
}

.rail__groups--fade-bottom {
  --fade-bottom: var(--space-6);
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
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
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
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--accent);
}

.rail__item--active:hover {
  background: transparent;
}

/* Up to two lines, broken between words ("Bằng chứng"). Page names are chosen so no single word is wider than
   the 68px label box (catalogs.spec.ts checks a length limit); the tooltip repeats the name. */
.rail__label {
  display: -webkit-box;
  max-width: 100%;
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

/* Touch laptops show the rail too: the footer buttons get a 44px target (IconButton also follows
   --target-min; this keeps the rail's own footer explicit). */
@media (pointer: coarse) {
  .rail__footer :deep(.icon-btn) {
    width: 44px;
    height: 44px;
  }
}

/* Short windows (1366×768 laptops leave about 680px): 44px items, a 24px icon pill, no gap between items and
   a tighter divider, so all eleven pages fit (9 × 44 + 2 two-line items × 58 = 512px in 532px). */
@media (height < 760px) {
  .rail {
    gap: var(--space-1);
  }

  .rail__groups {
    gap: var(--space-1);
  }

  .rail__list {
    gap: 0;
  }

  .rail__list + .rail__list {
    padding-top: var(--space-1);
  }

  .rail__item {
    min-height: 44px;
  }

  .rail__icon {
    height: 24px;
  }
}
</style>
