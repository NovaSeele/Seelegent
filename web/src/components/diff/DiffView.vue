<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { FileDiff } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import { useFormat } from '@/composables/useFormat'

import { diffLineCount, diffRows } from './diffRows'

/**
 * A parsed diff as a unified table: old and new line numbers, a sign column (named for screen readers) and the
 * text, which keeps its spaces and tabs. Long lines scroll sideways unless `wrap` is on. Past `limit` lines the
 * rest waits behind "Show all"; binary and truncated diffs say so instead of guessing.
 */
const props = withDefaults(
  defineProps<{
    diff: FileDiff
    limit?: number
    wrap?: boolean
  }>(),
  { limit: 2000 },
)

const { t } = useI18n()
const format = useFormat()
const showAll = ref(false)
watch(
  () => props.diff,
  () => (showAll.value = false),
)

const model = computed(() => diffRows(props.diff, showAll.value ? Infinity : props.limit))
const total = computed(() => diffLineCount(props.diff))
const SIGN = { add: '+', del: '-', context: ' ' } as const
</script>

<template>
  <div class="diff" :class="{ 'diff--wrap': wrap }">
    <p v-if="diff.binary" class="diff__note">{{ t('git.diff.binary') }}</p>
    <p v-else-if="!diff.hunks.length" class="diff__note">{{ t('git.diff.empty') }}</p>
    <template v-else>
      <div class="diff__scroll">
        <table class="diff__table">
          <caption class="sr-only">
            {{
              t('git.diff.caption', {
                path: diff.path,
                additions: model.additions,
                deletions: model.deletions,
              })
            }}
          </caption>
          <tbody>
            <tr v-for="row in model.rows" :key="row.key" :class="`diff__row--${row.kind}`">
              <template v-if="row.kind === 'hunk'">
                <td colspan="4" class="diff__hunk">{{ row.text }}</td>
              </template>
              <template v-else>
                <td class="diff__no" aria-hidden="true">{{ row.oldNo ?? '' }}</td>
                <td class="diff__no" aria-hidden="true">{{ row.newNo ?? '' }}</td>
                <td class="diff__sign">
                  <span aria-hidden="true">{{ SIGN[row.kind] }}</span>
                  <span v-if="row.kind !== 'context'" class="sr-only">
                    {{ t(`git.diff.${row.kind}`) }}
                  </span>
                </td>
                <td class="diff__text">{{ row.text }}</td>
              </template>
            </tr>
          </tbody>
        </table>
      </div>
      <p v-if="model.hidden" class="diff__note diff__note--more">
        <span>{{ t('git.diff.hidden', { n: format.number(model.hidden) }, model.hidden) }}</span>
        <Button size="sm" @click="showAll = true">
          {{ t('git.diff.showAll', { n: format.number(total) }) }}
        </Button>
      </p>
    </template>
    <p v-if="diff.truncated" class="diff__note diff__note--warn" role="note">
      {{ t('git.diff.truncated') }}
    </p>
  </div>
</template>

<style scoped>
.diff {
  display: flex;
  flex-direction: column;
  min-width: 0;
  border: var(--border-width) solid var(--term-border);
  border-radius: var(--radius-md);
  background: var(--term-bg);
  color: var(--term-fg);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  font-variant-ligatures: none;
  line-height: var(--lh-ui);
}

.diff__scroll {
  overflow-x: auto;
}

.diff__table {
  min-width: 100%;
  border-collapse: collapse;
}

.diff__table td:not(.diff__hunk) {
  padding: 0 var(--space-2);
  vertical-align: top;
}

.diff__text {
  width: 100%;
  white-space: pre;
  tab-size: 4;
}

.diff--wrap .diff__text {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
}

.diff__no {
  min-width: 4ch;
  color: var(--term-dim);
  text-align: right;
  user-select: none;
}

.diff__sign {
  width: 1ch;
  padding-inline: 0;
  color: var(--term-dim);
  user-select: none;
}

.diff__row--add {
  background: var(--term-diff-add-bg);
}

.diff__row--add .diff__sign,
.diff__row--add .diff__text {
  color: var(--term-diff-add-fg);
}

.diff__row--del {
  background: var(--term-diff-del-bg);
}

.diff__row--del .diff__sign,
.diff__row--del .diff__text {
  color: var(--term-diff-del-fg);
}

.diff__hunk {
  padding-inline: var(--space-2);
  padding-block: var(--space-1);
  background: var(--term-prompt-bg);
  color: var(--term-dim);
  white-space: pre;
}

.diff__row--hunk:not(:first-child) .diff__hunk {
  border-top: var(--border-width) solid var(--term-border);
}

.diff__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  padding: var(--space-3);
  color: var(--term-dim);
  font-family: var(--font-sans);
}

.diff__note--more {
  border-top: var(--border-width) solid var(--term-border);
}

.diff__note--warn {
  border-top: var(--border-width) solid var(--term-border);
  color: var(--term-warn);
}
</style>
