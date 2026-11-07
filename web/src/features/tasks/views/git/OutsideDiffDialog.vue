<script setup lang="ts">
import { computed, onBeforeUnmount, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitApi, isAbortError, type GitApi } from '@/api'
import type { FileDiff } from '@/api/types'
import DiffView from '@/components/diff/DiffView.vue'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'

/**
 * What a turn changed outside the worker's edit tools (shell commands, formatters, other programs), read from
 * the turn's snapshots: one diff per file. `turns` are the turns to choose from, newest first.
 */
const props = withDefaults(
  defineProps<{ taskId: string; turns: readonly number[]; turn?: number; api?: GitApi }>(),
  { turn: undefined, api: () => gitApi },
)
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()

type State =
  | { status: 'loading' }
  | { status: 'error'; error: string }
  | { status: 'ready'; diffs: FileDiff[] }

const selected = ref(props.turn ?? props.turns[0] ?? 1)
const state = shallowRef<State>({ status: 'loading' })
const options = computed(() =>
  props.turns.map((n) => ({ value: n, label: t('task.header.turn', { n }) })),
)

let call: AbortController | null = null
async function load() {
  call?.abort()
  const mine = (call = new AbortController())
  state.value = { status: 'loading' }
  try {
    const diffs = await props.api.outsideDiff(props.taskId, selected.value, mine.signal)
    if (mine === call) state.value = { status: 'ready', diffs }
  } catch (err) {
    if (mine !== call || isAbortError(err)) return
    state.value = { status: 'error', error: err instanceof Error ? err.message : String(err) }
  }
}

watch(
  () => props.turn,
  (value) => {
    if (value !== undefined) selected.value = value
  },
)
watch(
  [open, selected],
  ([isOpen]) => {
    if (isOpen) void load()
    else call?.abort()
  },
  { immediate: true },
)
onBeforeUnmount(() => call?.abort())
</script>

<template>
  <Dialog
    v-model:open="open"
    size="xl"
    :title="t('git.outside.title')"
    :description="t('git.outside.desc')"
  >
    <div class="outside">
      <Select
        v-if="turns.length > 1"
        v-model="selected"
        size="sm"
        :label="t('git.outside.turn')"
        :options="options"
      />
      <p v-if="state.status === 'loading'" class="outside__state">
        <Spinner size="sm" decorative />
        {{ t('git.diff.loading') }}
      </p>
      <div v-else-if="state.status === 'error'" class="outside__state" role="alert">
        <span>{{ t('git.outside.error', { error: state.error }) }}</span>
        <Button size="sm" @click="load">{{ t('common.action.retry') }}</Button>
      </div>
      <p v-else-if="!state.diffs.length" class="outside__state">{{ t('git.outside.empty') }}</p>
      <section
        v-for="diff in state.status === 'ready' ? state.diffs : []"
        :key="diff.path"
        class="outside__file"
        :aria-label="diff.path"
      >
        <code class="outside__path">{{ diff.path }}</code>
        <DiffView :diff="diff" :limit="400" />
      </section>
    </div>
  </Dialog>
</template>

<style scoped>
.outside {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  min-width: 0;
}

.outside__state {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--space-4);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.outside__file {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.outside__path {
  font-family: var(--font-mono);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}
</style>
