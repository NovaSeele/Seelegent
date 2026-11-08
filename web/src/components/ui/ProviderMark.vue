<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import { isProviderId, providerToken } from './provider'

/**
 * A worker backend's identity: an 8px dot in its hue and its name ("Antigravity", "Claude Code", "Codex"), plus a
 * neutral chip with the provider's name for an API-key account. The hue never colours the text and never stands
 * for a status (see provider.ts). An unknown backend gets the neutral hue and its id as the name.
 *
 * `label: false` hides the name for a spot that already writes it right beside the mark (a group heading); the
 * dot alone is decorative.
 */
const props = withDefaults(
  defineProps<{
    backend: string
    size?: 'sm' | 'md'
    label?: boolean
    /** The API provider of an API-key account (DeepSeek, OpenRouter, …), shown as a neutral chip. */
    provider?: string
  }>(),
  { size: 'md', label: true, provider: undefined },
)

const { t } = useI18n()

const name = computed(() =>
  isProviderId(props.backend) ? t(`accounts.backend.${props.backend}`) : props.backend,
)
const hue = computed(() => ({ '--pm-hue': providerToken(props.backend) }))
</script>

<template>
  <span class="pm" :class="`pm--${size}`" :style="hue" :data-backend="backend">
    <span class="pm__dot" aria-hidden="true" />
    <span v-if="label" class="pm__name">{{ name }}</span>
    <span v-if="provider" class="pm__chip">{{ provider }}</span>
  </span>
</template>

<style scoped>
.pm {
  display: inline-flex;
  align-items: center;
  gap: var(--space-1);
  min-width: 0;
  max-width: 100%;
  vertical-align: middle;
  white-space: nowrap;
}

.pm--md {
  gap: var(--space-2);
  font-size: var(--fs-sm);
}

.pm__dot {
  flex: none;
  width: 8px;
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--pm-hue);
}

.pm__name {
  overflow: hidden;
  text-overflow: ellipsis;
}

.pm__chip {
  overflow: hidden;
  padding: 0 var(--space-1);
  border-radius: var(--radius-xs);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  line-height: 16px;
  text-overflow: ellipsis;
}
</style>
