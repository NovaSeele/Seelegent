<script setup lang="ts">
import { useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import { formatUSD } from '@/lib/usd'
import { useAccountsStore } from '@/stores/accounts'

import { useActions } from './accountView'
import type { Need } from './needsYou'
import type { AccountDialog } from './useAccountControls'
import { useKeyTest } from './useKeyTest'

/**
 * "Needs you": one row per account that waits for the user, with the problem and the step that fixes it ("Claude
 * Code 2: no key. [Add key]"). Shown only when there is something in it (the page leaves it out otherwise).
 */
defineProps<{ needs: readonly Need[] }>()
const emit = defineEmits<{ dialog: [account: Account, kind: AccountDialog] }>()

const { t } = useI18n()
const accounts = useAccountsStore()
const actions = useActions()
const keyTest = useKeyTest()
const id = useId()

const DIALOG: Readonly<Partial<Record<NonNullable<Need['action']>, AccountDialog>>> = {
  key: 'key',
  replaceKey: 'key',
  budget: 'budget',
}

function problem(need: Need): string {
  const a = need.account
  if (need.problem === 'refused')
    return t(`accounts.needs.problem.${a.testResult === 'refused' ? 'refused' : a.testResult}`)
  if (need.problem === 'budget_reached')
    return t('accounts.needs.problem.budget_reached', {
      spent: formatUSD(a.spentUsd ?? 0),
      budget: formatUSD(a.budgetUsd ?? 0),
    })
  return t(`accounts.needs.problem.${need.problem}`)
}

/** What else to say: a sign-in account without credit is switched off and on after a top-up. */
const hint = (need: Need) =>
  need.problem === 'credits' && !need.action ? t('accounts.restReason.creditsHint') : ''

function act(need: Need) {
  const a = need.account
  if (need.action === 'login') void actions.run(`login:${a.id}`, () => accounts.login(a.id))
  else if (need.action === 'test') void keyTest.test(a)
  else if (need.action) {
    const kind = DIALOG[need.action]
    if (kind) emit('dialog', a, kind)
  }
}

const busy = (need: Need) =>
  actions.isBusy(`login:${need.account.id}`) || keyTest.isTesting(need.account.id)
</script>

<template>
  <section class="needs tone-attention" :aria-labelledby="`${id}-title`">
    <h2 :id="`${id}-title`" class="needs__title">{{ t('accounts.needs.title') }}</h2>
    <ul class="needs__list">
      <li v-for="need in needs" :key="need.account.id" class="needs__row">
        <p class="needs__text">
          {{ t('accounts.needs.row', { name: need.account.label, problem: problem(need) }) }}
          <span v-if="hint(need)" class="needs__hint">{{ hint(need) }}</span>
        </p>
        <Button
          v-if="need.action"
          size="sm"
          variant="secondary"
          :loading="busy(need)"
          @click="act(need)"
        >
          {{ t(`accounts.needs.action.${need.action}`) }}
        </Button>
      </li>
    </ul>
  </section>
</template>

<style scoped>
.needs {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
}

.needs__title {
  color: var(--tone-fg);
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.needs__list {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  padding: 0;
  list-style: none;
}

.needs__row {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-1) var(--space-3);
  min-height: var(--control-md);
}

.needs__text {
  min-width: 0;
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.needs__hint {
  display: block;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}
</style>
