<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Icon from '@/components/ui/Icon.vue'
import { FALLBACK_LOCALE, LOCALE_NAMES, SUPPORTED_LOCALES, type Locale } from '@/i18n'
import { useUiStore } from '@/stores/ui'

import SettingRow from './SettingRow.vue'

/**
 * Language: one of the seven languages, English until the user picks another (never guessed from the browser).
 * Each is listed by its own name, with the name in the current language under it. The switch applies at once,
 * without reloading.
 */
defineProps<{ highlight?: string }>()

const { locale } = useI18n()
const ui = useUiStore()

const choice = computed({
  get: () => ui.localeChoice ?? FALLBACK_LOCALE,
  set: (value: Locale) => void ui.setLocale(value),
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
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
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
