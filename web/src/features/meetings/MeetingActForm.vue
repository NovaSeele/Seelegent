<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Meeting } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import TextArea from '@/components/ui/TextArea.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

/** Turns a closed meeting's decision into delegated tasks, one per item (POST /meetings/{id}/act). */
const props = defineProps<{ meeting: Meeting }>()
const emit = defineEmits<{ updated: [meeting: Meeting] }>()

const { t } = useI18n()
const team = useTeamStore()
const toast = useToastStore()

const items = ref<string[]>([''])
const busy = ref(false)
const failure = ref('')
const filled = computed(() => items.value.map((i) => i.trim()).filter(Boolean))

async function act() {
  const n = filled.value.length
  busy.value = true
  failure.value = ''
  try {
    const res = await team.api().actMeeting(props.meeting.id, {
      items: filled.value.map((text) => ({ text })),
    })
    emit('updated', res.meeting)
    toast.show({ message: t('team.meetings.act.done', { n }, n), tone: 'success' })
    items.value = ['']
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <form class="act" @submit.prevent="act">
    <div v-for="(_, i) in items" :key="i" class="act__item">
      <TextArea v-model="items[i]" :label="t('team.meetings.act.item', { n: i + 1 })" :rows="2" />
      <IconButton
        v-if="items.length > 1"
        icon="trash"
        size="sm"
        :label="t('team.meetings.act.remove', { n: i + 1 })"
        @click="items.splice(i, 1)"
      />
    </div>
    <div class="act__actions">
      <Button size="sm" variant="secondary" icon="plus" @click="items.push('')">
        {{ t('team.meetings.act.another') }}
      </Button>
      <Button type="submit" variant="primary" :loading="busy" :disabled="!filled.length">
        {{ t('team.meetings.act.submit', { n: filled.length }, filled.length) }}
      </Button>
    </div>
    <p v-if="failure" class="act__error" role="alert">{{ failure }}</p>
  </form>
</template>

<style scoped>
.act {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.act__item {
  display: flex;
  align-items: flex-end;
  gap: var(--space-2);
}

.act__item > :first-child {
  flex: 1;
  min-width: 0;
}

.act__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.act__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
