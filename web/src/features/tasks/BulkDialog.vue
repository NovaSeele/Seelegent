<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { tasksApi } from '@/api'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { useTasksStore } from '@/stores/tasks'
import { useToastStore } from '@/stores/toast'

import { runBulk } from './selection'

export type BulkAction = 'cancel' | 'accept'

/**
 * Confirms a bulk action: lists every task it will touch (each can be unticked), says what happens, runs the
 * action three at a time and reports one summary toast.
 */
const props = defineProps<{ action: BulkAction | null; ids: readonly string[] }>()
const emit = defineEmits<{ close: []; done: [ok: readonly string[]] }>()

const { t } = useI18n()
const tasks = useTasksStore()
const toast = useToastStore()

const checked = ref<Record<string, boolean>>({})
const running = ref(false)

watch(
  () => [props.action, props.ids] as const,
  ([action, ids]) => {
    if (action) checked.value = Object.fromEntries(ids.map((id) => [id, true]))
  },
  { immediate: true },
)

const chosen = computed(() => props.ids.filter((id) => checked.value[id]))
const open = computed(() => props.action !== null)

async function confirm() {
  const action = props.action
  if (!action || !chosen.value.length) return
  running.value = true
  const result = await runBulk(chosen.value, async (id) => {
    const summary =
      action === 'cancel' ? await tasksApi.cancel(id) : await tasksApi.review(id, 'accepted')
    tasks.upsertIfNewer(summary)
  })
  running.value = false
  const total = chosen.value.length
  const message = t(`tasks.bulk.${action}Done`, { ok: result.ok.length, total })
  const first = result.failed[0]
  toast.show({
    message: first
      ? `${message} · ${t('tasks.bulk.failed', { count: result.failed.length, error: first.error })}`
      : message,
    tone: first ? (result.ok.length ? 'attention' : 'error') : 'success',
  })
  emit('done', result.ok)
}
</script>

<template>
  <Dialog
    :open="open"
    size="md"
    :title="action ? t(`tasks.bulk.${action}Title`) : ''"
    :description="action ? t(`tasks.bulk.${action}Desc`) : ''"
    :dismissible="!running"
    @update:open="(value) => !value && emit('close')"
  >
    <ul class="bulk-list">
      <li v-for="id in ids" :key="id">
        <Checkbox v-model="checked[id]" :label="tasks.get(id)?.title ?? id" :disabled="running" />
      </li>
    </ul>
    <template #footer>
      <Button variant="ghost" :disabled="running" @click="emit('close')">
        {{ t('tasks.bulk.keep') }}
      </Button>
      <Button
        :variant="action === 'cancel' ? 'danger' : 'primary'"
        :loading="running"
        :disabled="!chosen.length"
        @click="confirm"
      >
        {{ action ? t(`tasks.bulk.${action}Confirm`, chosen.length) : '' }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.bulk-list {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  max-height: 50vh;
  overflow: auto;
  list-style: none;
}
</style>
