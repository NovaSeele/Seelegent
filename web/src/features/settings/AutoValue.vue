<script setup lang="ts">
import { I18nT, useI18n } from 'vue-i18n'

/**
 * The Auto state of a machine-sized setting, where its number field would be: "Auto: 2 GB on this machine",
 * "Auto: half of your Free memory to start, 1 GB" for a value derived from the other threshold, or
 * "Auto" alone while the value in use is not known (`line` null, from useAutoField). Its id, data attributes and
 * click handler come from the row.
 */
defineProps<{ line: { key: string; value: string; other: string } | null }>()

const { t } = useI18n()
</script>

<template>
  <p class="auto">
    <I18nT v-if="line" :keypath="line.key" tag="span" scope="global">
      <template #auto>
        <span class="auto__tag">{{ t('settings.daemon.auto') }}</span>
      </template>
      <template #value>{{ line.value }}</template>
      <template #other>{{ line.other }}</template>
    </I18nT>
    <span v-else class="auto__tag">{{ t('settings.daemon.auto') }}</span>
  </p>
</template>

<style scoped>
/* At the number field's height (NumberField sm), so the rows keep their rhythm. */
.auto {
  display: flex;
  align-items: center;
  min-height: max(var(--control-sm), var(--target-min));
  padding: var(--space-0) var(--space-2);
  border: var(--border-width) dashed var(--border-strong);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  font-variant-numeric: tabular-nums;
}

.auto:hover {
  border-color: var(--border-control);
  cursor: pointer;
}

.auto__tag {
  color: var(--text-primary);
  font-weight: var(--fw-medium);
}
</style>
