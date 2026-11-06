<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import { LOCALE_NAMES, SUPPORTED_LOCALES, type Locale } from '@/i18n'
import { browserLocale, useUiStore } from '@/stores/ui'

import SettingRow from './SettingRow.vue'

/**
 * Language: follow the browser, or one of the seven languages. Each is listed by its own name, with the name in
 * the current language under it. The switch applies at once, without reloading.
 */
defineProps<{ highlight?: string }>()

const { t, locale } = useI18n()
const ui = useUiStore()

const choice = computed({
  get: () => ui.localeChoice ?? 'browser',
  set: (value: Locale | 'browser') => void ui.setLocale(value === 'browser' ? null : value),
})

const displayNames = computed(() => {
  try {
    return new Intl.DisplayNames([locale.value], { type: 'language' })
  } catch {
    return null
  }
})

const options = computed(() =>
  SUPPORTED_LOCALES.map((loc) => ({
    value: loc,
    name: LOCALE_NAMES[loc],
    local: loc === locale.value ? '' : (displayNames.value?.of(loc) ?? ''),
  })),
)
</script>

<template>
  <SettingRow item="locale" stacked :highlighted="highlight === 'locale'">
    <div class="langs" role="radiogroup" aria-labelledby="setting-locale">
      <label class="lang" :class="{ 'lang--checked': choice === 'browser' }">
        <input v-model="choice" class="sr-only" type="radio" name="locale" value="browser" />
        <Icon name="monitor" class="lang__icon" />
        <span class="lang__text">
          <span class="lang__name">{{ t('locale.browser') }}</span>
          <span class="lang__sub">{{ LOCALE_NAMES[browserLocale()] }}</span>
        </span>
        <Icon v-if="choice === 'browser'" name="check" class="lang__check" />
      </label>
      <label
        v-for="option in options"
        :key="option.value"
        class="lang"
        :class="{ 'lang--checked': choice === option.value }"
      >
        <input v-model="choice" class="sr-only" type="radio" name="locale" :value="option.value" />
        <Icon name="language" class="lang__icon" />
        <span class="lang__text">
          <span class="lang__name" :lang="option.value">{{ option.name }}</span>
          <span v-if="option.local" class="lang__sub">{{ option.local }}</span>
        </span>
        <Icon v-if="choice === option.value" name="check" class="lang__check" />
      </label>
    </div>
  </SettingRow>
</template>

<style scoped>
.langs {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: var(--space-2);
}

.lang {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: max(52px, var(--target-min));
  padding: var(--space-2) var(--space-3);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  cursor: pointer;
}

.lang:hover {
  border-color: var(--border-strong);
}

.lang--checked,
.lang--checked:hover {
  border-color: var(--accent);
  background: var(--accent-soft);
}

.lang:has(:focus-visible) {
  box-shadow: var(--focus-ring);
}

.lang__icon {
  flex: none;
  color: var(--text-muted);
  font-size: var(--icon-md);
}

.lang__text {
  display: flex;
  flex: 1;
  flex-direction: column;
  min-width: 0;
}

.lang__name {
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}

.lang__sub {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.lang__check {
  flex: none;
  color: var(--accent);
}
</style>
