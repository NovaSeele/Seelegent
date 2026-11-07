<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { TaskSummary } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Menu, { type MenuItem } from '@/components/ui/Menu.vue'
import { useBreakpoint } from '@/composables/useMediaQuery'
import { awaitsReview, canCancel, canFollowup, canTakeOver } from '@/lib/taskStatus'

import { TASK_KEYS } from './taskKeys'
import type { TaskActions } from './useTaskActions'

/**
 * The task header's actions: accept / reject while it awaits review, then follow up, take over, cancel and copy
 * the id. Each shows only when it applies; on phones the secondary ones fold into a menu.
 */
const props = defineProps<{ task: TaskSummary; actions: TaskActions }>()

const emit = defineEmits<{
  followup: []
  review: [verdict: 'accepted' | 'rejected']
  confirm: [action: 'cancel' | 'takeover']
}>()

const { t } = useI18n()
const { isMobile } = useBreakpoint()

const reviewable = computed(() => awaitsReview(props.task))

const menuItems = computed<MenuItem[]>(() => {
  const items: MenuItem[] = []
  if (canFollowup(props.task)) {
    items.push({ id: 'followup', label: t('task.actions.followup'), icon: 'send' })
  }
  if (canTakeOver(props.task)) {
    items.push({ id: 'takeover', label: t('task.actions.takeover'), icon: 'hand' })
  }
  items.push({ id: 'copy', label: t('task.actions.copyId'), icon: 'copy' })
  if (canCancel(props.task)) {
    items.push({ id: 'cancel', label: t('task.actions.cancel'), icon: 'x', danger: true })
  }
  return items
})

function onMenu(id: string) {
  if (id === 'followup') emit('followup')
  else if (id === 'takeover') emit('confirm', 'takeover')
  else if (id === 'cancel') emit('confirm', 'cancel')
  else if (id === 'copy') void props.actions.copyId()
}
</script>

<template>
  <div class="th__actions">
    <template v-if="reviewable">
      <Button
        size="sm"
        variant="primary"
        icon="check"
        :loading="actions.busy.value === 'review'"
        :aria-keyshortcuts="TASK_KEYS.accept"
        @click="emit('review', 'accepted')"
      >
        {{ t('task.actions.accept') }}
      </Button>
      <Button
        size="sm"
        icon="statusRejected"
        :aria-keyshortcuts="TASK_KEYS.reject"
        @click="emit('review', 'rejected')"
      >
        {{ t('task.actions.reject') }}
      </Button>
    </template>
    <template v-if="!isMobile">
      <IconButton
        v-if="canFollowup(task)"
        icon="send"
        size="sm"
        variant="secondary"
        :label="t('task.actions.followup')"
        :shortcut="TASK_KEYS.followup"
        @click="emit('followup')"
      />
      <IconButton
        v-if="canTakeOver(task)"
        icon="hand"
        size="sm"
        variant="secondary"
        :label="t('task.actions.takeover')"
        :shortcut="TASK_KEYS.takeover"
        @click="emit('confirm', 'takeover')"
      />
      <IconButton
        v-if="canCancel(task)"
        icon="x"
        size="sm"
        variant="secondary"
        :label="t('task.actions.cancel')"
        :shortcut="TASK_KEYS.cancel"
        @click="emit('confirm', 'cancel')"
      />
      <IconButton
        icon="copy"
        size="sm"
        variant="secondary"
        :label="t('task.actions.copyId')"
        @click="actions.copyId()"
      />
    </template>
    <Menu v-else :items="menuItems" :label="t('task.actions.more')" @select="onMenu">
      <template #trigger="{ attrs }">
        <IconButton
          v-bind="attrs"
          icon="more"
          size="sm"
          variant="secondary"
          :label="t('task.actions.more')"
          :tooltip="false"
        />
      </template>
    </Menu>
  </div>
</template>

<style scoped>
.th__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-left: auto;
}
</style>
