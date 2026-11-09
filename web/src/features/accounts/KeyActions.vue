<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'

import { useKeyTest } from './useKeyTest'

type KeyAction = 'test' | 'key' | 'model' | 'budget'

/**
 * The everyday actions of an API key, shown in an open row and on a card: Test again, Replace key, Change model and
 * Set budget. Rename, Turn off and Remove stay in the ⋯ menu (and the switch). A key that is missing is added through
 * the next-step button, so "Replace key" is only offered once there is a key. The test runs here (its button
 * shows it running); the others open their dialog.
 */
const props = defineProps<{ account: Account }>()
const emit = defineEmits<{ dialog: [kind: Exclude<KeyAction, 'test'>] }>()

const { t } = useI18n()
const keyTest = useKeyTest()

const items = computed(() => {
  const a = props.account
  const list: { id: KeyAction; label: string; icon: 'refresh' | 'key' | 'gauge' | 'quota' }[] = []
  if (a.keyHint) {
    list.push({ id: 'test', label: t('accounts.action.test'), icon: 'refresh' })
    list.push({ id: 'key', label: t('accounts.action.replaceKey'), icon: 'key' })
  }
  if (a.provider) list.push({ id: 'model', label: t('accounts.action.model'), icon: 'gauge' })
  // A local service is free: there is nothing to budget.
  if (a.spendSource !== 'free')
    list.push({ id: 'budget', label: t('accounts.action.budget'), icon: 'quota' })
  return list
})

function act(id: KeyAction) {
  if (id === 'test') void keyTest.test(props.account)
  else emit('dialog', id)
}
</script>

<template>
  <div class="keyactions">
    <Button
      v-for="item in items"
      :key="item.id"
      size="sm"
      variant="secondary"
      :icon="item.icon"
      :loading="item.id === 'test' && keyTest.isTesting(account.id)"
      @click.stop="act(item.id)"
    >
      {{ item.label }}
    </Button>
  </div>
</template>

<style scoped>
.keyactions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}
</style>
