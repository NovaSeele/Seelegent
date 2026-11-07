<script setup lang="ts">
import { computed, ref, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account, LoginSession } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Spinner from '@/components/ui/Spinner.vue'
import TextField from '@/components/ui/TextField.vue'
import { safeHref } from '@/lib/richText'
import { useAccountsStore } from '@/stores/accounts'
import { useToastStore } from '@/stores/toast'

import { useActions } from './accountView'

/**
 * A running sign-in, driven by account.login frames: starting → open_url (open or copy the link; a device code
 * when the backend shows one) → waiting_code (paste the code the page shows) → done or failed. in_terminal means
 * the sign-in goes on in the terminal window the daemon opened: there is nothing to post, so no code box (only
 * waiting_code shows one). The typed code is kept while the panel is on screen, so a frame that re-renders the
 * card does not wipe it.
 */
const props = defineProps<{ account: Account; session: LoginSession }>()

const { t } = useI18n()
const accounts = useAccountsStore()
const toast = useToastStore()
const actions = useActions()
const id = useId()
const code = ref('')

const finished = computed(() => props.session.state === 'done' || props.session.state === 'failed')

/** The sign-in link, only when it is http(s): anything else is neither opened nor copied. */
const link = computed(() => (props.session.url ? safeHref(props.session.url) : null))

function openLink() {
  if (link.value) window.open(link.value, '_blank', 'noopener,noreferrer')
}

async function copy(text: string, message: string) {
  try {
    await navigator.clipboard.writeText(text)
    toast.show({ message, tone: 'success' })
  } catch {
    toast.show({ message: t('accounts.login.copyFailed'), tone: 'error' })
  }
}

async function submit() {
  const value = code.value.trim()
  if (!value) return
  const ok = await actions.run('code', () => accounts.sendCode(props.account.id, value))
  if (ok) code.value = ''
}
</script>

<template>
  <section
    class="login"
    :class="
      session.state === 'failed'
        ? 'tone-danger'
        : session.state === 'done'
          ? 'tone-success'
          : 'tone-neutral'
    "
    :aria-labelledby="`${id}-title`"
    aria-live="polite"
  >
    <h4 :id="`${id}-title`" class="login__title">
      <Spinner v-if="!finished" size="sm" decorative />
      {{ t(`accounts.login.state.${session.state}`) }}
    </h4>

    <template v-if="session.state === 'open_url' || session.state === 'waiting_code'">
      <p class="login__text">
        {{
          t(session.state === 'open_url' ? 'accounts.login.openHint' : 'accounts.login.codeHint')
        }}
      </p>
      <p v-if="session.userCode" class="login__code">
        <span>{{ t('accounts.login.deviceCode') }}</span>
        <code class="login__device">{{ session.userCode }}</code>
        <Button
          size="sm"
          variant="ghost"
          icon="copy"
          @click="copy(session.userCode, t('accounts.login.codeCopied'))"
        >
          {{ t('accounts.login.copy') }}
        </Button>
      </p>
      <div v-if="link" class="login__row">
        <Button size="sm" variant="primary" icon="external" @click="openLink">
          {{ t('accounts.login.open') }}
        </Button>
        <Button
          size="sm"
          variant="secondary"
          icon="copy"
          @click="copy(link, t('accounts.login.linkCopied'))"
        >
          {{ t('accounts.login.copyLink') }}
        </Button>
      </div>
      <form v-if="session.state === 'waiting_code'" class="login__form" @submit.prevent="submit">
        <TextField
          v-model="code"
          :label="t('accounts.login.codeLabel')"
          size="sm"
          autocomplete="one-time-code"
        />
        <Button type="submit" size="sm" :loading="actions.isBusy('code')" :disabled="!code.trim()">
          {{ t('accounts.login.send') }}
        </Button>
      </form>
    </template>

    <p v-else-if="session.state === 'in_terminal'" class="login__text">
      {{ t('accounts.login.terminalHint') }}
    </p>

    <!-- In the terminal the daemon's message repeats the title in English; the title and hint say it already. -->
    <p v-if="session.message && session.state !== 'in_terminal'" class="login__text">
      {{ session.message }}
    </p>

    <div v-if="finished" class="login__row">
      <Button
        v-if="session.state === 'failed'"
        size="sm"
        variant="secondary"
        icon="refresh"
        :loading="actions.isBusy('retry')"
        @click="actions.run('retry', () => accounts.login(account.id))"
      >
        {{ t('accounts.login.retry') }}
      </Button>
      <Button size="sm" variant="ghost" @click="accounts.clearLogin(account.id)">
        {{ t('common.action.dismiss') }}
      </Button>
    </div>
  </section>
</template>

<style scoped>
.login {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-md);
  background: var(--bg-surface-2);
}

.login__title {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.login__text {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.login__row,
.login__code {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.login__code {
  font-size: var(--fs-xs);
}

.login__device {
  padding: var(--space-0) var(--space-2);
  border-radius: var(--radius-xs);
  background: var(--bg-code);
  font-family: var(--font-mono);
  font-size: var(--fs-md);
  letter-spacing: 0.08em;
}

.login__form {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-2);
}

.login__form > :first-child {
  flex: 1 1 180px;
}
</style>
