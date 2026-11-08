<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { ApiError, errorMessage, fieldProblems } from '@/api/client'
import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Dialog from '@/components/ui/Dialog.vue'
import NumberField from '@/components/ui/NumberField.vue'
import TextField from '@/components/ui/TextField.vue'
import { useNow } from '@/composables/useNow'
import { useAccountsStore } from '@/stores/accounts'
import { useToastStore } from '@/stores/toast'

import { offConsequence } from './accountView'
import BudgetDialog from './BudgetDialog.vue'
import KeyDialog from './KeyDialog.vue'
import ModelDialog from './ModelDialog.vue'
import type { AccountDialog } from './useAccountControls'

/**
 * The account dialogs the cards, list rows and the "Needs you" strip ask for: rename, the priority (a narrow list
 * row has no room for its field), an API key's key, budget and model (KeyDialog, BudgetDialog, ModelDialog), delete
 * and sign out (409 while the account runs a task, said in the dialog), and turning work off, which
 * says what changes now (running turns finish; the last account of its kind leaves new work waiting).
 */
const props = defineProps<{ account: Account | undefined }>()
const dialog = defineModel<AccountDialog | null>({ required: true })

const { t } = useI18n()
const accounts = useAccountsStore()
const toast = useToastStore()

const label = ref('')
const priority = ref(0)
const busy = ref(false)
const error = ref('')

watch(dialog, (value) => {
  error.value = ''
  label.value = value === 'rename' ? (props.account?.label ?? '') : ''
  priority.value = props.account?.priority ?? 0
})

// The account went away (deleted elsewhere): nothing left to edit.
watch(
  () => props.account,
  (account) => {
    if (!account && !busy.value) dialog.value = null
  },
)

const name = computed(() => props.account?.label ?? '')

// Read while the dialog is open, so a turn that ends meanwhile updates the wording.
const now = useNow(30_000)
const off = computed(() =>
  props.account ? offConsequence(props.account, accounts.accounts, now.value) : null,
)
const backendName = computed(() => {
  const a = props.account
  if (!a) return ''
  const backend = t(`accounts.backend.${a.backend}`)
  return a.provider ? t('accounts.off.withProvider', { backend, provider: a.provider }) : backend
})

async function run(action: (account: Account) => Promise<unknown>, done: string) {
  const account = props.account
  if (!account || busy.value) return
  busy.value = true
  error.value = ''
  try {
    await action(account)
    dialog.value = null
    toast.show({ message: done, tone: 'success' })
  } catch (cause) {
    const field = fieldProblems(cause)[0]
    error.value =
      cause instanceof ApiError && cause.status === 409 && dialog.value === 'delete'
        ? t('accounts.delete.running', { error: cause.message })
        : cause instanceof ApiError && cause.status === 409 && dialog.value === 'logout'
          ? t('accounts.logout.running', { error: cause.message })
          : (field?.message ?? errorMessage(cause))
  } finally {
    busy.value = false
  }
}

const rename = () =>
  run(
    (a) => accounts.update(a.id, { label: label.value.trim() }),
    t('accounts.done.renamed', { name: label.value.trim() }),
  )
const savePriority = () =>
  run(
    (a) => accounts.update(a.id, { priority: priority.value }),
    t('accounts.done.priority', { name: name.value, n: priority.value }),
  )
const remove = () =>
  run((a) => accounts.removeAccount(a.id), t('accounts.done.deleted', { name: name.value }))
const logout = () => run((a) => accounts.logout(a.id), t('accounts.done.logout'))
const turnOff = () =>
  run(
    (a) => accounts.update(a.id, { enabled: false }),
    t('accounts.done.off', { name: name.value }),
  )
</script>

<template>
  <Dialog
    :open="dialog === 'rename'"
    size="sm"
    :title="t('accounts.rename.title')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="dialog = null"
  >
    <form id="account-rename" @submit.prevent="rename">
      <TextField v-model="label" :label="t('accounts.add.label')" :error="error || undefined" />
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="dialog = null">
        {{ t('common.action.cancel') }}
      </Button>
      <Button
        type="submit"
        form="account-rename"
        variant="primary"
        :loading="busy"
        :disabled="!label.trim()"
      >
        {{ t('accounts.rename.submit') }}
      </Button>
    </template>
  </Dialog>

  <Dialog
    :open="dialog === 'priority'"
    size="sm"
    :title="t('accounts.priorityDialog.title', { name })"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="dialog = null"
  >
    <form id="account-priority" @submit.prevent="savePriority">
      <NumberField
        :model-value="priority"
        :label="t('accounts.priority')"
        :hint="t('accounts.add.priorityHint')"
        :error="error || undefined"
        integer
        @commit="priority = $event"
      />
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="dialog = null">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="account-priority" variant="primary" :loading="busy">
        {{ t('accounts.priorityDialog.submit') }}
      </Button>
    </template>
  </Dialog>

  <KeyDialog :open="dialog === 'key'" :account="account" @update:open="dialog = null" />
  <BudgetDialog :open="dialog === 'budget'" :account="account" @update:open="dialog = null" />
  <ModelDialog :open="dialog === 'model'" :account="account" @update:open="dialog = null" />

  <ConfirmDialog
    :open="dialog === 'delete'"
    :title="t('accounts.delete.title', { name })"
    :description="t('accounts.delete.desc')"
    :confirm-label="t('accounts.delete.confirm')"
    danger
    :busy="busy"
    :error="error"
    @update:open="dialog = null"
    @confirm="remove"
  />

  <ConfirmDialog
    :open="dialog === 'logout'"
    :title="t('accounts.logout.title', { name })"
    :description="t('accounts.logout.desc')"
    :confirm-label="t('accounts.logout.confirm')"
    danger
    :busy="busy"
    :error="error"
    @update:open="dialog = null"
    @confirm="logout"
  />

  <ConfirmDialog
    :open="dialog === 'off'"
    :title="t('accounts.off.title', { name })"
    :description="t('accounts.off.desc')"
    :confirm-label="t('accounts.off.confirm')"
    danger
    :busy="busy"
    :error="error"
    @update:open="dialog = null"
    @confirm="turnOff"
  >
    <ul class="off">
      <li v-if="off && off.running > 0">
        {{ t('accounts.off.running', { n: off.running }, off.running) }}
      </li>
      <li>
        {{
          off?.only
            ? t('accounts.off.only', { backend: backendName })
            : t('accounts.off.others', { backend: backendName })
        }}
      </li>
    </ul>
  </ConfirmDialog>
</template>

<style scoped>
.off {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  padding-inline-start: var(--space-5);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  list-style: disc;
}
</style>
