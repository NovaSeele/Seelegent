<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { FileDiff, Note } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import { useClientWidth } from '@/composables/useClientWidth'
import { useFormat } from '@/composables/useFormat'

import DiffExtraRow from './DiffExtraRow.vue'
import { diffLineCount, diffRows } from './diffRows'
import { useLinePicker } from './useLinePicker'

/**
 * A parsed diff as a unified table: old and new line numbers, a sign column (named for screen readers) and the
 * text, which keeps its spaces and tabs. Long lines scroll sideways unless `wrap` is on. Past `limit` lines the
 * rest waits behind "Show all"; binary and truncated diffs say so instead of guessing.
 *
 * With `annotatable`, the new-side line numbers are one keyboard group (useLinePicker): choosing one shows a
 * large "Add a note on line N" button under it (`line`), and the `composer` slot takes its place for
 * `composerLine`. `notes` show under the line they follow now; a note whose line is gone (currentLine 0) is not
 * anchored here. Notes, the button and the composer keep to the visible width of the scroll box.
 */
const props = withDefaults(
  defineProps<{
    diff: FileDiff
    limit?: number
    wrap?: boolean
    annotatable?: boolean
    notes?: readonly Note[]
    composerLine?: number | null
  }>(),
  { limit: 2000, notes: () => [], composerLine: null },
)
const emit = defineEmits<{ line: [line: number] }>()

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

/** Notes by the new-side line they sit under now. */
const notesAt = computed(() => {
  const out = new Map<number, Note[]>()
  for (const n of props.notes) {
    if (n.side !== 'new' || !n.currentLine) continue
    out.set(n.currentLine, [...(out.get(n.currentLine) ?? []), n])
  }
  return out
})

const table = ref<HTMLElement | null>(null)
const picker = useLinePicker(table, () => props.composerLine)
const lines = computed(() =>
  model.value.rows.flatMap((r) => (r.kind !== 'hunk' && r.newNo ? [r.newNo] : [])),
)
const stop = computed(() => picker.stopOf(lines.value))

// The visible width of the scroll box, for the rows under a line (DiffExtraRow).
const scroller = ref<HTMLElement | null>(null)
const boxWidth = useClientWidth(scroller)
</script>

<template>
  <div class="diff" :class="{ 'diff--wrap': wrap }">
    <p v-if="diff.binary" class="diff__note">{{ t('git.diff.binary') }}</p>
    <p v-else-if="!diff.hunks.length" class="diff__note">{{ t('git.diff.empty') }}</p>
    <template v-else>
      <div
        ref="scroller"
        class="diff__scroll"
        :style="boxWidth ? { '--diff-box': `${boxWidth}px` } : undefined"
      >
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
          <tbody ref="table" @click="picker.onClick" @keydown="picker.onKeydown">
            <template v-for="row in model.rows" :key="row.key">
              <tr
                :class="[
                  `diff__row--${row.kind}`,
                  {
                    'is-selected':
                      row.kind !== 'hunk' && row.newNo && row.newNo === picker.selected.value,
                  },
                ]"
              >
                <template v-if="row.kind === 'hunk'">
                  <td colspan="4" class="diff__hunk">{{ row.text }}</td>
                </template>
                <template v-else>
                  <td class="diff__no" aria-hidden="true">{{ row.oldNo ?? '' }}</td>
                  <td v-if="annotatable && row.newNo" class="diff__no">
                    <button
                      type="button"
                      class="diff__pick"
                      :data-line="row.newNo"
                      :tabindex="row.newNo === stop ? 0 : -1"
                      :aria-pressed="row.newNo === picker.selected.value"
                      :aria-label="t('team.notes.pickLine', { line: row.newNo })"
                    >
                      {{ row.newNo }}
                    </button>
                  </td>
                  <td v-else class="diff__no" aria-hidden="true">{{ row.newNo ?? '' }}</td>
                  <td class="diff__sign">
                    <span aria-hidden="true">{{ SIGN[row.kind] }}</span>
                    <span v-if="row.kind !== 'context'" class="sr-only">
                      {{ t(`git.diff.${row.kind}`) }}
                    </span>
                  </td>
                  <td class="diff__text">{{ row.text }}</td>
                </template>
              </tr>
              <template v-if="row.kind !== 'hunk' && row.newNo">
                <template v-if="row.kind !== 'del'">
                  <DiffExtraRow v-for="n in notesAt.get(row.newNo) ?? []" :key="n.id">
                    <slot name="note" :note="n">
                      <p class="diff__notebody">{{ n.body }}</p>
                    </slot>
                  </DiffExtraRow>
                </template>
                <DiffExtraRow v-if="annotatable && row.newNo === composerLine">
                  <slot name="composer" :line="row.newNo" />
                </DiffExtraRow>
                <DiffExtraRow
                  v-else-if="annotatable && row.newNo === picker.selected.value"
                  tone="action"
                >
                  <Button size="md" icon="plus" class="diff__add" @click="emit('line', row.newNo)">
                    {{ t('team.notes.addOnLine', { line: row.newNo }) }}
                  </Button>
                </DiffExtraRow>
              </template>
            </template>
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

/* A line number keeps the row's normal height; the large target is the add button it reveals. */
.diff__pick {
  min-width: 4ch;
  padding: 0;
  border: 0;
  border-radius: var(--radius-xs);
  background: transparent;
  color: inherit;
  font: inherit;
  text-align: right;
  cursor: pointer;
}

.diff__pick:hover,
.diff__pick[aria-pressed='true'] {
  background: var(--term-prompt-bg);
  color: var(--term-fg);
}

.diff__pick:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}

.diff__row--context.is-selected,
.diff__row--add.is-selected {
  box-shadow: inset 3px 0 0 var(--accent);
}

.diff__add {
  min-height: var(--target-min);
}

.diff__notebody {
  white-space: pre-wrap;
  overflow-wrap: anywhere;
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
