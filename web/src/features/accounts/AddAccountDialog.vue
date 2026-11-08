<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account, AccountCreated } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import ApiKeyForm from './ApiKeyForm.vue'
import ConnectChoice, { type ConnectWay } from './ConnectChoice.vue'
import SignInForm from './SignInForm.vue'

/**
 * "Add account", one dialog for every kind: first how the account connects (ConnectChoice), then either the
 * subscription sign-in (SignInForm, which shows the sign-in until it is done) or the API-key form (ApiKeyForm:
 * service, key, model; "Test and add"). `service` ("provider:<id>" or "preset:<id>") opens the API-key form at
 * once with that service chosen, for "Add key" in the Services section. Every step starts fresh when the dialog
 * opens, and the typed key goes with it when the dialog closes.
 */
const open = defineModel<boolean>('open', { required: true })
const props = withDefaults(defineProps<{ service?: string }>(), { service: '' })

const { t } = useI18n()
const providers = useProvidersStore()
const toast = useToastStore()

type Step = 'connect' | 'signin' | 'key'
const step = ref<Step>('connect')
const way = ref<ConnectWay>('subscription')
const keyForm = ref<InstanceType<typeof ApiKeyForm> | null>(null)
const signIn = ref<InstanceType<typeof SignInForm> | null>(null)

watch(open, (value) => {
  if (!value) return
  providers.ensure()
  step.value = props.service ? 'key' : 'connect'
})

const description = computed(() =>
  step.value === 'signin'
    ? t('accounts.add.connect.subscription')
    : step.value === 'key'
      ? t('accounts.add.connect.apiKey')
      : undefined,
)
/** Where focus goes when the dialog opens: the chosen card, or the key field. */
const initialFocus = computed(() =>
  step.value === 'key' ? '.keyfield input' : 'input[type="radio"]:checked',
)

const busy = computed(() => !!keyForm.value?.busy || !!signIn.value?.busy)

function next() {
  step.value = way.value === 'apiKey' ? 'key' : 'signin'
}

function onAdded(created: AccountCreated) {
  open.value = false
  const notes = [...new Set((created.notices ?? []).map((n) => t(`accounts.notice.${n.code}`)))]
  toast.show({
    message: [t('accounts.done.added', { name: created.label }), ...notes].join('. '),
    tone: 'success',
  })
}

function onSystem(account: Account) {
  open.value = false
  toast.show({ message: t('accounts.done.added', { name: account.label }), tone: 'success' })
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="t('accounts.add.title')"
    :description="description"
    :dismissible="!busy"
    :initial-focus="initialFocus"
  >
    <ConnectChoice v-if="step === 'connect'" v-model="way" form-id="add-connect" @next="next" />
    <SignInForm
      v-else-if="step === 'signin'"
      ref="signIn"
      form-id="add-signin"
      @system="onSystem"
    />
    <ApiKeyForm v-else ref="keyForm" form-id="add-key" :service="service" @done="onAdded" />

    <template #footer>
      <Button
        v-if="step !== 'connect' && !(step === 'key' && service) && !signIn?.started"
        variant="ghost"
        :disabled="busy"
        @click="step = 'connect'"
      >
        {{ t('common.action.back') }}
      </Button>
      <template v-if="step === 'connect'">
        <Button type="submit" form="add-connect" variant="primary">
          {{ t('accounts.add.next') }}
        </Button>
      </template>
      <template v-else-if="step === 'signin'">
        <Button v-if="signIn?.started" variant="primary" @click="open = false">
          {{ t('accounts.add.done') }}
        </Button>
        <Button
          v-else
          type="submit"
          form="add-signin"
          variant="primary"
          icon="external"
          :loading="busy"
        >
          {{ t('accounts.add.signIn') }}
        </Button>
      </template>
      <template v-else>
        <Button
          v-if="keyForm?.failed"
          variant="secondary"
          :disabled="busy || !keyForm.valid"
          @click="keyForm?.submit(false)"
        >
          {{ t('accounts.form.addUntested') }}
        </Button>
        <!-- Disabled while the form cannot be sent (no service, no key or a key with a URL or spaces in it):
             the field says why, and nothing reaches the daemon. -->
        <Button
          type="submit"
          form="add-key"
          variant="primary"
          :loading="busy"
          :disabled="!keyForm?.valid"
        >
          {{ t('accounts.form.testAndAdd') }}
        </Button>
      </template>
    </template>
  </Dialog>
</template>
