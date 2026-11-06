<script setup lang="ts">
import { computed, nextTick, ref, watch, type Component } from 'vue'
import { useI18n } from 'vue-i18n'
import { RouterLink, useRoute, useRouter } from 'vue-router'

import Dialog from '@/components/ui/Dialog.vue'
import Icon from '@/components/ui/Icon.vue'
import IconButton from '@/components/ui/IconButton.vue'
import TextField from '@/components/ui/TextField.vue'
import { useBreakpoint } from '@/composables/useMediaQuery'

import AppearanceSection from './AppearanceSection.vue'
import LanguageSection from './LanguageSection.vue'
import PlaceholderSection from './PlaceholderSection.vue'
import { searchSettings, type SettingHit } from './search'
import { DEFAULT_SECTION, isSectionId, SECTIONS, type SectionId } from './sections'

/**
 * Settings, a dialog over the current page driven by the route /settings/<section>. Left: search and the section
 * list; right: the section. On phones it is two steps: the list, then the section with a back button.
 * Moving between sections replaces the history entry, so Back (or close) returns to the page underneath.
 */
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const { isMobile } = useBreakpoint()

const open = computed(() => route.name === 'settings')
const requested = computed(() => {
  const value = route.params.section
  return isSectionId(value) ? value : null
})
/** On phones the bare /settings shows only the list. */
const section = computed<SectionId | null>(
  () => requested.value ?? (isMobile.value ? null : DEFAULT_SECTION),
)
const showList = computed(() => !isMobile.value || section.value === null)
const showContent = computed(() => section.value !== null)

const VIEWS: Partial<Record<SectionId, Component>> = {
  appearance: AppearanceSection,
  language: LanguageSection,
}

const query = ref('')
const hits = computed<SettingHit[]>(() => searchSettings(query.value))
const highlight = ref<string | undefined>()
const body = ref<HTMLElement | null>(null)
let highlightTimer: ReturnType<typeof setTimeout> | undefined

watch(open, (isOpen) => {
  if (!isOpen) query.value = ''
})

function sectionLink(id: SectionId) {
  return { name: 'settings', params: { section: id }, replace: true } as const
}

async function goTo(hit: SettingHit) {
  await router.replace(sectionLink(hit.section))
  await nextTick()
  const row = body.value?.querySelector<HTMLElement>(`[data-setting="${hit.item}"]`)
  row?.scrollIntoView({ block: 'start', behavior: 'smooth' })
  clearTimeout(highlightTimer)
  highlight.value = hit.item
  highlightTimer = setTimeout(() => (highlight.value = undefined), 1600)
}

function close() {
  const state = window.history.state as { back?: string | null } | null
  if (state?.back) router.back()
  else void router.replace('/')
}
</script>

<template>
  <Dialog
    :open="open"
    size="xl"
    bare
    :aria-label="t('settings.title')"
    :initial-focus="isMobile ? undefined : 'input'"
    @close="close"
  >
    <div class="settings" :class="{ 'settings--mobile': isMobile }">
      <aside v-if="showList" class="settings__nav">
        <header class="settings__nav-head">
          <h2 class="settings__title">{{ t('settings.title') }}</h2>
          <IconButton
            v-if="isMobile"
            icon="x"
            :label="t('common.action.close')"
            :tooltip="false"
            @click="close"
          />
        </header>
        <div class="settings__search">
          <TextField
            v-model="query"
            type="search"
            icon="search"
            size="sm"
            hide-label
            :label="t('settings.search.label')"
            :placeholder="t('settings.search.placeholder')"
          />
        </div>
        <nav v-if="!query.trim()" :aria-label="t('settings.sectionsLabel')">
          <ul class="settings__list">
            <li v-for="entry in SECTIONS" :key="entry.id">
              <RouterLink
                :to="sectionLink(entry.id)"
                class="settings__link"
                :class="{ 'settings__link--active': section === entry.id }"
                :aria-current="section === entry.id ? 'page' : undefined"
              >
                <Icon :name="entry.icon" class="settings__link-icon" />
                <span class="settings__link-label">{{
                  t(`settings.sections.${entry.id}.label`)
                }}</span>
                <Icon v-if="isMobile" name="chevronRight" class="settings__link-chevron" />
              </RouterLink>
            </li>
          </ul>
        </nav>
        <ul v-else class="settings__list" :aria-label="t('settings.search.results')">
          <li v-for="hit in hits" :key="`${hit.section}.${hit.item}`">
            <button type="button" class="settings__link settings__hit" @click="goTo(hit)">
              <span class="settings__link-label">{{ hit.label }}</span>
              <span class="settings__hit-section">{{
                t(`settings.sections.${hit.section}.label`)
              }}</span>
            </button>
          </li>
          <li v-if="hits.length === 0" class="settings__empty" role="status">
            {{ t('settings.search.empty', { query: query.trim() }) }}
          </li>
        </ul>
      </aside>
      <section
        v-if="showContent && section"
        class="settings__content"
        aria-labelledby="settings-section-title"
      >
        <header class="settings__head">
          <IconButton
            v-if="isMobile"
            icon="arrowLeft"
            :label="t('settings.back')"
            :tooltip="false"
            :to="{ name: 'settings', replace: true }"
          />
          <h2 id="settings-section-title" class="settings__section-title">
            {{ t(`settings.sections.${section}.label`) }}
          </h2>
          <IconButton icon="x" :label="t('common.action.close')" :tooltip="false" @click="close" />
        </header>
        <div ref="body" class="settings__body">
          <p class="settings__section-desc">{{ t(`settings.sections.${section}.desc`) }}</p>
          <component :is="VIEWS[section]" v-if="VIEWS[section]" :highlight="highlight" />
          <PlaceholderSection v-else :key="section" :section="section" :highlight="highlight" />
        </div>
      </section>
    </div>
  </Dialog>
</template>

<style scoped>
.settings {
  display: flex;
  flex: 1;
  min-height: 0;
}

.settings__nav {
  display: flex;
  flex-direction: column;
  flex: none;
  width: 248px;
  min-height: 0;
  border-right: var(--border-width) solid var(--border-subtle);
  background: var(--bg-surface);
}

.settings--mobile .settings__nav {
  flex: 1;
  width: auto;
  border-right: 0;
}

.settings__nav-head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-4) var(--space-3) var(--space-2) var(--space-4);
}

.settings__title {
  flex: 1;
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
}

.settings__search {
  padding: 0 var(--space-3) var(--space-2);
}

.settings__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  margin: 0;
  padding: var(--space-1) var(--space-2) var(--space-3);
  overflow-y: auto;
  list-style: none;
}

.settings__link {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  width: 100%;
  min-height: max(var(--control-md), var(--target-min));
  padding: var(--space-1) var(--space-2);
  border: 0;
  border-radius: var(--radius-md);
  background: none;
  color: var(--text-secondary);
  font: inherit;
  text-align: start;
  text-decoration: none;
  cursor: pointer;
}

.settings__link:hover {
  background: var(--bg-hover);
  color: var(--text-primary);
}

.settings__link--active,
.settings__link--active:hover {
  background: var(--accent-soft);
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}

.settings__link-icon {
  flex: none;
  font-size: var(--icon-md);
}

.settings__link--active .settings__link-icon {
  color: var(--accent);
}

.settings__link-label {
  flex: 1;
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.settings__link-chevron {
  color: var(--text-muted);
}

.settings__hit {
  flex-direction: column;
  align-items: flex-start;
  gap: 0;
  padding: var(--space-1) var(--space-2);
}

.settings__hit .settings__link-label {
  width: 100%;
  color: var(--text-primary);
}

.settings__hit-section {
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.settings__empty {
  padding: var(--space-3) var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.settings__content {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
  min-height: 0;
}

.settings__head {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-3) var(--space-3) var(--space-6);
  border-bottom: var(--border-width) solid var(--border-subtle);
}

.settings--mobile .settings__head {
  padding-left: var(--space-2);
}

.settings__section-title {
  flex: 1;
  min-width: 0;
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
}

.settings__body {
  flex: 1;
  min-height: 0;
  padding: var(--space-4) var(--space-6) var(--space-6);
  overflow-y: auto;
  overscroll-behavior: contain;
}

.settings--mobile .settings__body {
  padding: var(--space-3) var(--space-4) var(--space-6);
}

.settings__section-desc {
  max-width: var(--measure-prose);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
}
</style>
