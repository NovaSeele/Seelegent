<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import ChoiceGroup from '@/components/ui/ChoiceGroup.vue'

export type ConnectWay = 'subscription' | 'apiKey'

/**
 * Step 1 of adding an account: how it connects, as two radio cards (a subscription sign-in, or an API key). A
 * pointer click on a card goes on at once (`next`); from the keyboard the arrows move the choice and Enter (the
 * form's submit) goes on, so arrowing through never skips the step.
 */
const way = defineModel<ConnectWay>({ required: true })
defineProps<{ formId: string }>()
const emit = defineEmits<{ next: [] }>()

const { t } = useI18n()
const root = ref<HTMLElement | null>(null)

const options = computed(() => [
  {
    value: 'subscription' as const,
    label: t('accounts.add.connect.subscription'),
    description: t('accounts.add.connect.subscriptionDesc'),
  },
  {
    value: 'apiKey' as const,
    label: t('accounts.add.connect.apiKey'),
    description: t('accounts.add.connect.apiKeyDesc'),
  },
])

let pending = false
/** A click with detail > 0 came from a pointer; the clicks a key press makes have detail 0. */
function onClick(event: MouseEvent) {
  if (event.detail === 0 || pending) return
  if (!(event.target instanceof Element) || !event.target.closest('label')) return
  pending = true
  // The label's click and the click it forwards to its radio arrive together: go on once, after the model updated.
  setTimeout(() => {
    pending = false
    emit('next')
  })
}

onMounted(() => root.value?.querySelector<HTMLInputElement>('input:checked')?.focus())
</script>

<template>
  <form :id="formId" ref="root" class="connect" @submit.prevent="emit('next')" @click="onClick">
    <ChoiceGroup v-model="way" :legend="t('accounts.add.connect.legend')" :options="options" />
  </form>
</template>

<style scoped>
.connect :deep(.choices__list) {
  grid-template-columns: repeat(auto-fit, minmax(200px, 1fr));
}

.connect :deep(.choices__legend) {
  color: var(--text-primary);
  font-size: var(--fs-sm);
}
</style>
