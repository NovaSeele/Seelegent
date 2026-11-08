<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Account, KeyTestResult, Notice } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import ApiKeyField from './ApiKeyField.vue'
import { checkKey } from './apiKey'
import { testAllowsSave } from './keyTest'
import KeyTestNote from './KeyTestNote.vue'

/**
 * Adds or replaces an account's API key with the add form's checks: the key is cleaned and checked as it is
 * typed, then tested against the account's own service before it is stored ("Test and save"); a failed test offers
 * "Save without testing". The stored key is tested once more so the account records the result. Only the key's
 * hint is ever shown, and the typed key goes when the dialog closes.
 */
const props = defineProps<{ account: Account | undefined }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const accounts = useAccountsStore()
const providers = useProvidersStore()
const toast = useToastStore()

const raw = ref('')
const busy = ref(false)
const testing = ref(false)
const attempted = ref(false)
const result = ref<KeyTestResult | null>(null)
const notices = ref<Notice[]>([])
const error = ref('')
const failure = ref('')

const name = computed(() => props.account?.label ?? '')
const check = computed(() => checkKey(raw.value))
const failed = computed(() => !!result.value && !testAllowsSave(result.value))

watch(open, () => {
  raw.value = ''
  result.value = null
  notices.value = []
  error.value = ''
  failure.value = ''
  attempted.value = false
})
watch(raw, () => {
  result.value = null
  error.value = ''
})

async function save(withTest: boolean) {
  const a = props.account
  attempted.value = true
  if (!a || busy.value || check.value.error) return
  busy.value = true
  failure.value = ''
  error.value = ''
  try {
    if (withTest) {
      testing.value = true
      const found = await providers
        .test({ provider: a.provider, apiKey: check.value.key })
        .finally(() => (testing.value = false))
      result.value = found
      notices.value = found.notices ?? []
      if (!testAllowsSave(found)) return
    }
    const saved = await accounts.setKey(a.id, check.value.key)
    if (withTest)
      void providers
        .test({ accountId: a.id })
        .then((r) => accounts.noteTest(a.id, r))
        .catch(() => undefined)
    open.value = false
    const notes = [...new Set((saved.notices ?? []).map((n) => t(`accounts.notice.${n.code}`)))]
    toast.show({
      message: [t('accounts.done.keySet', { name: saved.label }), ...notes].join('. '),
      tone: 'success',
    })
  } catch (cause) {
    const field = fieldProblems(cause).find((p) => p.field === 'key' || p.field === 'apiKey')
    if (field) error.value = field.message
    else failure.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}

async function clear() {
  const a = props.account
  if (!a || busy.value) return
  busy.value = true
  try {
    await accounts.clearKey(a.id)
    open.value = false
    toast.show({ message: t('accounts.done.keyCleared', { name: a.label }), tone: 'success' })
  } catch (cause) {
    failure.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="t(account?.keyHint ? 'accounts.key.titleReplace' : 'accounts.key.titleAdd', { name })"
    :description="
      account?.keyHint
        ? t('accounts.key.current', { hint: account.keyHint })
        : t('accounts.key.none')
    "
    :dismissible="!busy"
    initial-focus=".keyfield input"
  >
    <form id="account-key" class="keydlg" novalidate @submit.prevent="save(true)">
      <ApiKeyField v-model="raw" :error="error || undefined" :show-empty="attempted" />
      <KeyTestNote :result="result" :notices="notices" :testing="testing" />
      <p v-if="failure" class="keydlg__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button v-if="account?.keyHint" variant="danger" :disabled="busy" @click="clear">
        {{ t('accounts.key.clear') }}
      </Button>
      <Button v-if="failed" variant="secondary" :disabled="busy" @click="save(false)">
        {{ t('accounts.key.saveUntested') }}
      </Button>
      <Button
        type="submit"
        form="account-key"
        variant="primary"
        :loading="busy"
        :disabled="!!check.error"
      >
        {{ t('accounts.key.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.keydlg {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.keydlg__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
