<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import Menu from '@/components/ui/Menu.vue'
import NumberField from '@/components/ui/NumberField.vue'
import { providerEdge } from '@/components/ui/provider'
import StatusPill from '@/components/ui/StatusPill.vue'
import Switch from '@/components/ui/Switch.vue'

import AccountFacts from './AccountFacts.vue'
import { showsSlots } from './accountView'
import LoginPanel from './LoginPanel.vue'
import QuotaBars from './QuotaBars.vue'
import QuotaGroups from './QuotaGroups.vue'
import { type AccountDialog, useAccountControls } from './useAccountControls'
import { useAccountState } from './useAccountState'

/**
 * One account as a list row: name and type, status, slots, the routing quota at a glance, and the controls (use
 * for work, priority, ⋯). Clicking the row or its chevron opens the details in place: the chips, every quota
 * window and the next step, as the card shows them. The controls never open the row. In a narrow list
 * (`narrow`, set by AccountList) the row takes two lines and the priority moves into the ⋯ menu.
 */
const props = defineProps<{ account: Account; now: number; open: boolean; narrow: boolean }>()
const emit = defineEmits<{ toggle: []; dialog: [kind: AccountDialog] }>()

const { t } = useI18n()
const id = useId()

const { pill } = useAccountState(props)
const { actions, session, kindLabel, next, menu, onMenu, setEnabled, setPriority } =
  useAccountControls(
    props,
    (kind) => emit('dialog', kind),
    () => props.narrow,
  )

const slots = computed(() =>
  showsSlots(props.account)
    ? t('accounts.list.slots', { busy: props.account.busy, max: props.account.maxConcurrent })
    : '',
)
const details = computed(() =>
  [
    props.account.keyHint ? t('accounts.keyHint', { hint: props.account.keyHint }) : '',
    props.account.email ?? '',
    props.account.quota.plan ?? '',
  ].filter(Boolean),
)
</script>

<template>
  <li
    class="row provider-edge"
    :class="{ 'row--open': open, 'row--off': !account.enabled }"
    :style="providerEdge(account.backend)"
  >
    <div class="row__main" @click="emit('toggle')">
      <IconButton
        class="row__chev"
        :icon="open ? 'chevronDown' : 'chevronRight'"
        size="sm"
        :label="
          t(open ? 'accounts.list.collapse' : 'accounts.list.expand', { name: account.label })
        "
        :tooltip="false"
        :aria-expanded="open"
        :aria-controls="`${id}-details`"
      />
      <div class="row__name">
        <h3 :id="`${id}-name`" class="row__label">{{ account.label }}</h3>
        <p class="row__kind">
          {{ kindLabel }}<span v-if="slots" class="row__slots-inline"> · {{ slots }}</span>
        </p>
      </div>
      <StatusPill
        class="row__status"
        :tone="pill.tone"
        :label="pill.label"
        :title="pill.title"
        size="sm"
      />
      <span class="row__slots" :class="{ 'row__slots--busy': account.busy > 0 }">{{ slots }}</span>
      <QuotaBars class="row__quota" :account="account" :now="now" />
      <div class="row__controls" @click.stop>
        <Switch
          :model-value="account.enabled"
          :label="t('accounts.enabled')"
          :aria-label="t('accounts.list.enabledFor', { name: account.label })"
          hide-label
          :disabled="actions.isBusy('enabled')"
          @update:model-value="setEnabled"
        />
        <NumberField
          v-if="!narrow"
          class="row__priority"
          :model-value="account.priority"
          :label="t('accounts.priority')"
          hide-label
          size="sm"
          integer
          :disabled="actions.isBusy('priority')"
          @commit="setPriority"
        />
        <Menu
          :items="menu"
          :label="t('accounts.action.more', { name: account.label })"
          @select="onMenu"
        >
          <template #trigger="{ attrs }">
            <IconButton
              v-bind="attrs"
              icon="more"
              size="sm"
              :label="t('accounts.action.more', { name: account.label })"
              :tooltip="false"
            />
          </template>
        </Menu>
      </div>
    </div>

    <LoginPanel v-if="session" class="row__login" :account="account" :session="session" />

    <div
      v-if="open"
      :id="`${id}-details`"
      class="row__details"
      role="region"
      :aria-labelledby="`${id}-name`"
    >
      <p v-if="details.length" class="row__meta">
        <span v-for="item in details" :key="item">{{ item }}</span>
      </p>
      <AccountFacts :account="account" :now="now" :has-session="!!session" />
      <QuotaGroups :account="account" :now="now" />
      <div v-if="next" class="row__next">
        <Button
          v-if="next === 'login'"
          size="sm"
          variant="primary"
          icon="external"
          :loading="actions.isBusy('login')"
          @click="onMenu('login')"
        >
          {{ t('accounts.action.login') }}
        </Button>
        <Button v-else size="sm" variant="primary" @click="emit('dialog', 'key')">
          {{ t('accounts.action.key') }}
        </Button>
      </div>
    </div>
  </li>
</template>

<style scoped src="./accountRow.css"></style>
