<script setup lang="ts">
import { computed, nextTick, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Account } from '@/api/types'
import ChoiceGroup from '@/components/ui/ChoiceGroup.vue'
import ProviderMark from '@/components/ui/ProviderMark.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { useToastStore } from '@/stores/toast'

import { BACKENDS } from './accountView'
import LoginPanel from './LoginPanel.vue'

/**
 * Step 2 of adding a subscription account: the product (Gemini through Antigravity, Claude Pro/Max through Claude
 * Code, ChatGPT through Codex) and a name, then "Sign in" (the dialog's footer calls `submit`): the account is made
 * with its own profile and its sign-in starts, shown here with the LoginPanel until it is done. "Use the sign-in
 * already on this computer" adds the account on this computer's sign-in instead; Antigravity only, and only while
 * no account uses it yet.
 */
defineProps<{ formId: string }>()
const emit = defineEmits<{ added: [account: Account]; system: [account: Account] }>()

const { t } = useI18n()
const accounts = useAccountsStore()
const toast = useToastStore()

const backend = ref<Account['backend']>('agy')
const label = ref('')
const labelTouched = ref(false)
const busy = ref(false)
const error = ref('')
const failure = ref('')
const created = ref<Account | null>(null)
const root = ref<HTMLFormElement | null>(null)

const options = computed(() =>
  BACKENDS.map((value) => ({
    value,
    label: t(`accounts.backend.${value}`),
    description: t(`accounts.add.product.${value}`),
  })),
)

/** The account on this computer's sign-in can exist once, and only Antigravity has one. */
const systemFree = computed(
  () =>
    backend.value === 'agy' &&
    !accounts.accounts.some((a) => a.kind === 'system' && a.backend === 'agy'),
)

const session = computed(() => (created.value ? accounts.logins[created.value.id] : undefined))
const account = computed(() => (created.value ? accounts.get(created.value.id) : undefined))

function defaultLabel() {
  const n = accounts.accounts.filter((a) => a.backend === backend.value).length + 1
  return t('accounts.add.defaultLabel', { backend: t(`accounts.backend.${backend.value}`), n })
}

watch(
  backend,
  () => {
    if (!labelTouched.value) label.value = defaultLabel()
  },
  { immediate: true },
)

async function add(kind: 'isolated' | 'system') {
  if (busy.value) return null
  busy.value = true
  error.value = ''
  failure.value = ''
  try {
    return await accounts.create({
      backend: backend.value,
      kind,
      label: label.value.trim() || defaultLabel(),
    })
  } catch (cause) {
    const field = fieldProblems(cause).find((p) => p.field === 'label')
    if (field) error.value = field.message
    else failure.value = errorMessage(cause)
    return null
  } finally {
    busy.value = false
  }
}

/** Makes the account and starts its sign-in; a sign-in that cannot start is said, the account stays. */
async function submit() {
  if (created.value) return
  const made = await add('isolated')
  if (!made) return
  created.value = made
  emit('added', made)
  try {
    await accounts.login(made.id)
  } catch (cause) {
    toast.show({
      message: t('accounts.add.loginFailed', { name: made.label, error: errorMessage(cause) }),
      tone: 'error',
    })
  }
}

async function useSystem() {
  const made = await add('system')
  if (made) emit('system', made)
}

onMounted(async () => {
  await nextTick()
  root.value?.querySelector<HTMLInputElement>('input:checked')?.focus()
})

defineExpose({
  submit,
  busy,
  started: computed(() => created.value !== null),
  signedIn: computed(() => session.value?.state === 'done'),
})
</script>

<template>
  <form :id="formId" ref="root" class="signin" @submit.prevent="submit">
    <template v-if="!created">
      <ChoiceGroup v-model="backend" :legend="t('accounts.add.product.legend')" :options="options">
        <template #mark="{ option }">
          <ProviderMark :backend="option.value" :label="false" class="signin__mark" />
        </template>
      </ChoiceGroup>
      <button
        v-if="systemFree"
        type="button"
        class="signin__system"
        :disabled="busy"
        @click="useSystem"
      >
        {{ t('accounts.add.useSystem') }}
      </button>
      <TextField
        v-model="label"
        :label="t('accounts.add.label')"
        :error="error || undefined"
        @update:model-value="labelTouched = true"
      />
    </template>
    <template v-else-if="account">
      <p class="signin__made">{{ t('accounts.add.made', { name: account.label }) }}</p>
      <LoginPanel v-if="session" :account="account" :session="session" />
    </template>
    <p v-if="failure" class="signin__error" role="alert">{{ failure }}</p>
  </form>
</template>

<style scoped>
.signin {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.signin__mark {
  margin-right: var(--space-2);
}

.signin__system {
  align-self: flex-start;
  margin-top: calc(-1 * var(--space-2));
  padding: 0;
  border: 0;
  background: none;
  color: var(--link);
  font: inherit;
  font-size: var(--fs-xs);
  text-decoration: underline;
  cursor: pointer;
}

.signin__system:disabled {
  cursor: default;
  opacity: 0.6;
}

.signin__made {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.signin__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
