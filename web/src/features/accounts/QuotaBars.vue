<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import ProgressBar from '@/components/ui/ProgressBar.vue'
import type { Tone } from '@/components/ui/tone'
import Tooltip from '@/components/ui/Tooltip.vue'
import { useFormat } from '@/composables/useFormat'
import { useConnectionStore } from '@/stores/connection'

import { quotaSummary, windowMinutes, type SummaryWindow } from './quotaSummary'

/**
 * A list row's quota at a glance: up to two thin bars of the group the pool routes this backend's work on (see
 * quotaSummary.ts), each with what is left, its reset time in a tooltip, and "+N" for the windows not shown. When
 * no group matches the tier models, one bar shows the lowest window of all in a neutral colour (it may belong to
 * models the workers never use, so it never turns amber or red) and its tooltip says so.
 */
const props = defineProps<{ account: Account; now: number }>()

const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()

const summary = computed(() =>
  quotaSummary(props.account, connection.settings?.backends?.[props.account.backend]?.tiers),
)

const DAY = 24 * 60

/** "5h", "Weekly", … for a window, else its own label or key. */
function spanName(item: SummaryWindow): string {
  const w = item.window
  const m = windowMinutes(w)
  if (m === 7 * DAY) return t('accounts.list.span.week')
  if (m === DAY) return t('accounts.list.span.day')
  if (m === 30 * DAY) return t('accounts.list.span.month')
  if (m > 0 && m < DAY && m % 60 === 0) return t('accounts.list.span.hours', { n: m / 60 })
  return w.label || w.window
}

function tone(remaining: number, routing: boolean): Tone {
  if (!routing) return 'muted'
  const used = 1 - remaining
  return used >= 1 ? 'danger' : used >= 0.85 ? 'attention' : 'neutral'
}

const bars = computed(() => {
  const { windows, routing } = summary.value
  return windows.map((item) => {
    const span = spanName(item)
    const where = t(routing ? 'accounts.list.quota.tip' : 'accounts.list.quota.lowestTip', {
      group: item.group,
      window: span,
    })
    const reset = item.window.resetAt
      ? t('accounts.quota.resets', { time: fmt.value.relative(item.window.resetAt, props.now) })
      : ''
    const left = t('common.ui.quotaLeft', { value: fmt.value.percent(item.window.remaining) })
    return {
      key: `${item.group}/${item.window.window}`,
      name: routing ? span : `${item.group} · ${span}`,
      left,
      tip: reset ? `${where} · ${reset}` : where,
      tone: tone(item.window.remaining, routing),
      remaining: item.window.remaining,
    }
  })
})

const moreText = computed(() =>
  t('accounts.list.quota.more', { n: summary.value.more }, summary.value.more),
)
</script>

<template>
  <div class="qb" :class="{ 'qb--fallback': bars.length && !summary.routing }">
    <p v-if="account.quota.noPlan" class="qb__note">{{ t('accounts.status.no_plan') }}</p>
    <p v-else-if="account.quota.source === 'passive'" class="qb__note">
      {{ t('accounts.list.quota.passive') }}
    </p>
    <p v-else-if="!bars.length" class="qb__note">
      {{ account.status === 'new' ? t('accounts.list.quota.checking') : t('accounts.quota.none') }}
    </p>
    <template v-else>
      <Tooltip v-for="bar in bars" :key="bar.key" :text="bar.tip" class="qb__item">
        <div class="qb__bar">
          <span class="qb__head">
            <span class="qb__name">{{ bar.name }}</span>
            <span class="qb__value" :class="`tone-${bar.tone}`">{{ bar.left }}</span>
          </span>
          <ProgressBar
            :value="bar.remaining"
            :tone="bar.tone"
            :label="bar.name"
            :value-text="`${bar.left}. ${bar.tip}`"
            size="sm"
          />
        </div>
      </Tooltip>
      <Tooltip v-if="summary.more" :text="moreText" class="qb__more-anchor">
        <span class="qb__more">
          <span aria-hidden="true">{{
            t('accounts.list.quota.moreShort', { n: summary.more })
          }}</span>
          <span class="sr-only">{{ moreText }}</span>
        </span>
      </Tooltip>
    </template>
  </div>
</template>

<style scoped>
.qb {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-width: 0;
}

.qb__item {
  flex: 1 1 0;
  min-width: 0;
}

.qb__bar {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.qb__head {
  display: flex;
  justify-content: space-between;
  gap: var(--space-2);
  font-size: var(--fs-2xs);
  line-height: var(--lh-tight);
}

.qb__name {
  overflow: hidden;
  color: var(--text-secondary);
  text-overflow: ellipsis;
  white-space: nowrap;
}

.qb__value {
  flex: none;
  color: var(--text-secondary);
  font-variant-numeric: tabular-nums;
}

.qb__value.tone-attention,
.qb__value.tone-danger {
  color: var(--tone-fg);
  font-weight: var(--fw-medium);
}

.qb__more-anchor {
  flex: none;
}

.qb__more {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-variant-numeric: tabular-nums;
}

.qb__note {
  overflow: hidden;
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
