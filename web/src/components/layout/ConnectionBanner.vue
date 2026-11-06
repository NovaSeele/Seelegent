<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useConnectionStore } from '@/stores/connection'
import { useToastStore } from '@/stores/toast'

/**
 * Shown while the event stream is down: says so, counts down to the next attempt and offers to retry now.
 * When the connection comes back after an outage, a short toast confirms it.
 */
const { t } = useI18n()
const connection = useConnectionStore()
const toasts = useToastStore()

const now = ref(Date.now())
const timer = setInterval(() => (now.value = Date.now()), 1000)
onBeforeUnmount(() => clearInterval(timer))

const offline = computed(() => connection.status === 'offline')
const secondsLeft = computed(() =>
  connection.retryAt === null ? 0 : Math.max(0, Math.ceil((connection.retryAt - now.value) / 1000)),
)

watch(
  () => connection.status,
  (next, previous) => {
    if (next === 'online' && previous === 'offline') {
      toasts.show({ message: t('nav.conn.restored'), tone: 'success' })
    }
  },
)
</script>

<template>
  <div v-if="offline" class="banner tone-attention" role="status">
    <Icon name="offline" class="banner__icon" />
    <p class="banner__text">
      <span>{{ t('nav.conn.lost') }}</span>
      <span v-if="secondsLeft > 0" class="banner__count">
        {{ t('nav.conn.retryIn', { n: secondsLeft }) }}
      </span>
    </p>
    <Button size="sm" variant="ghost" icon="refresh" @click="connection.retryNow()">
      {{ t('common.action.retry') }}
    </Button>
  </div>
</template>

<style scoped>
.banner {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  min-height: var(--control-lg);
  padding: var(--space-1) var(--space-1) var(--space-1) var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.banner__icon {
  flex: none;
  color: var(--tone-fg);
  font-size: var(--icon-md);
}

.banner__text {
  display: flex;
  flex: 1;
  flex-wrap: wrap;
  gap: 0 var(--space-2);
  min-width: 0;
}

.banner__count {
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}

@media (width < 600px) {
  .banner {
    border-width: 0 0 var(--border-width);
    border-radius: 0;
  }
}
</style>
