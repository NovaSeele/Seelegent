<script setup lang="ts">
import { useI18n } from 'vue-i18n'

/**
 * One setting: its name and description (settings.items.<item>) and the control. `stacked` puts the control under
 * the text (wide controls such as the theme grid); otherwise it sits on the right and wraps under on narrow
 * screens. `data-setting` lets search results scroll to and highlight the row.
 */
withDefaults(defineProps<{ item: string; stacked?: boolean; highlighted?: boolean }>(), {
  stacked: false,
  highlighted: false,
})

const { t } = useI18n()
</script>

<template>
  <div
    class="setting"
    :class="{ 'setting--stacked': stacked, 'setting--highlighted': highlighted }"
    :data-setting="item"
  >
    <div class="setting__text">
      <h3 :id="`setting-${item}`" class="setting__label">
        {{ t(`settings.items.${item}.label`) }}
      </h3>
      <p class="setting__desc">{{ t(`settings.items.${item}.desc`) }}</p>
    </div>
    <div class="setting__control"><slot /></div>
  </div>
</template>

<style scoped>
.setting {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2) var(--space-6);
  padding: var(--space-4) 0;
  border-bottom: var(--border-width) solid var(--border-subtle);
  scroll-margin: var(--space-4);
}

.setting:last-child {
  border-bottom: 0;
}

.setting__text {
  flex: 1 1 260px;
  min-width: 0;
}

.setting__label {
  font-size: var(--fs-md);
  font-weight: var(--fw-medium);
  color: var(--text-primary);
}

.setting__desc {
  margin-top: var(--space-0);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  line-height: var(--lh-prose);
  text-wrap: pretty;
}

/* One fixed-width control column, so the controls of a section line up and each fills it. */
.setting__control {
  display: flex;
  flex: 0 0 var(--setting-control-width);
  align-items: center;
  gap: var(--space-2);
  width: var(--setting-control-width);
  min-width: 0;
  --setting-control-width: 300px;
}

@media (width < 600px) {
  .setting__control {
    flex-basis: 100%;
    width: 100%;
  }
}

.setting--stacked {
  flex-direction: column;
  flex-wrap: nowrap;
  align-items: stretch;
}

.setting--stacked .setting__text,
.setting--stacked .setting__control {
  flex: none;
  width: auto;
}

/* Wide controls (grids) take the full row; a flex parent would shrink them to their content. */
.setting--stacked .setting__control {
  display: block;
}

.setting--highlighted {
  animation: setting-flash 1.6s var(--ease-out);
}

@keyframes setting-flash {
  from {
    box-shadow: inset 3px 0 0 var(--accent);
    background: var(--accent-soft);
  }
}
</style>
