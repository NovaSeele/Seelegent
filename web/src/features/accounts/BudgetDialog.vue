<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import NumberField from '@/components/ui/NumberField.vue'
import { formatUSD } from '@/lib/usd'
import { useAccountsStore } from '@/stores/accounts'
import { useToastStore } from '@/stores/toast'

import AccountSpend from './AccountSpend.vue'

/**
 * An API key's budget: an amount in US dollars and "Stop using this key when the budget is used up" (ticked by
 * default). The spend so far is shown above it. A service that reports no cost cannot be held to a budget, which
 * is said. "Remove budget" sends 0.
 */
const props = defineProps<{ account: Account | undefined }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const accounts = useAccountsStore()
const toast = useToastStore()

const amount = ref(0)
const stop = ref(true)
const busy = ref(false)
const error = ref('')

const name = computed(() => props.account?.label ?? '')
const noCost = computed(() => props.account?.spendSource !== 'cli')

watch(open, (value) => {
  if (!value) return
  amount.value = props.account?.budgetUsd ?? 0
  stop.value = props.account?.budgetStop ?? true
  error.value = ''
})

async function save(budgetUsd: number) {
  const a = props.account
  if (!a || busy.value) return
  if (budgetUsd < 0 || (budgetUsd === 0 && !a.budgetUsd)) {
    error.value = t('accounts.budget.positive')
    return
  }
  busy.value = true
  error.value = ''
  try {
    await accounts.update(a.id, { budgetUsd, budgetStop: stop.value })
    open.value = false
    toast.show({
      message:
        budgetUsd > 0
          ? t('accounts.done.budgetSet', { name: a.label, budget: formatUSD(budgetUsd) })
          : t('accounts.done.budgetRemoved', { name: a.label }),
      tone: 'success',
    })
  } catch (cause) {
    error.value = fieldProblems(cause)[0]?.message ?? errorMessage(cause)
  } finally {
    busy.value = false
  }
}

function onEnter(event: KeyboardEvent) {
  if (event.target instanceof HTMLInputElement && event.target.inputMode === 'decimal')
    void save(amount.value)
}
</script>

<template>
  <Dialog
    v-model:open="open"
    size="sm"
    :title="t('accounts.budget.title', { name })"
    :dismissible="!busy"
    initial-focus="input"
  >
    <!-- The number field keeps Enter to commit its value; here Enter saves too (the amount is committed first). -->
    <form
      id="account-budget"
      class="budget"
      @submit.prevent="save(amount)"
      @keydown.enter="onEnter"
    >
      <AccountSpend v-if="account" :account="account" plain />
      <NumberField
        :model-value="amount"
        :label="t('accounts.form.budget')"
        unit="USD"
        :error="error || undefined"
        @commit="amount = $event"
      />
      <Checkbox v-model="stop" :label="t('accounts.form.budgetStop')" />
      <p v-if="noCost" class="budget__note">{{ t('accounts.budget.noCost') }}</p>
    </form>
    <template #footer>
      <Button v-if="account?.budgetUsd" variant="ghost" :disabled="busy" @click="save(0)">
        {{ t('accounts.budget.remove') }}
      </Button>
      <Button type="submit" form="account-budget" variant="primary" :loading="busy">
        {{ t('accounts.budget.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.budget {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.budget__note {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
