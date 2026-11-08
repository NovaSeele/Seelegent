<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitApi, type GitApi } from '@/api'
import type { TaskDetail } from '@/api/types'
import Button from '@/components/ui/Button.vue'

import { outsideTurns, restoredByTurn } from '../../detail/safety'
import OutsideDiffDialog from './OutsideDiffDialog.vue'

/**
 * The Git view's "Shell edits" (what turns changed outside the edit tools): shown only when a turn of the task has snapshots to compare, and opening on
 * the newest of those turns. The turns come from the task detail, the same as the safety panel's.
 */
const props = withDefaults(
  defineProps<{ taskId: string; detail: TaskDetail | null | undefined; api?: GitApi }>(),
  { api: () => gitApi },
)

const { t } = useI18n()

const open = ref(false)
const hintId = useId()
const turns = computed(() => outsideTurns(props.detail))
const restored = computed(() => restoredByTurn(props.detail))
</script>

<template>
  <Button
    v-if="turns.length"
    size="sm"
    variant="ghost"
    :title="t('git.outside.hint')"
    :aria-describedby="hintId"
    @click="open = true"
  >
    {{ t('git.outside.open') }}
  </Button>
  <span v-if="turns.length" :id="hintId" class="sr-only">{{ t('git.outside.hint') }}</span>
  <OutsideDiffDialog
    v-if="open"
    v-model:open="open"
    :task-id="taskId"
    :turns="turns"
    :restored="restored"
    :api="api"
  />
</template>
