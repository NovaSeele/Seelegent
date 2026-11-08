<script setup lang="ts">
import { onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Account } from '@/api/types'
import ProviderMark from '@/components/ui/ProviderMark.vue'

import AccountRow from './AccountRow.vue'
import type { AccountDialog } from './useAccountControls'

/**
 * The accounts as a compact list, grouped by backend (in the page's order), one row per account; a row opens in
 * place to show its details, one row at a time. Below NARROW_LIST pixels of its own width (the container query in
 * accountRow.css uses the same number) rows take two lines and their priority moves into the ⋯ menu.
 */
defineProps<{
  groups: readonly { backend: Account['backend']; items: readonly Account[] }[]
  now: number
}>()
const emit = defineEmits<{ dialog: [account: Account, kind: AccountDialog] }>()

const { t } = useI18n()

const NARROW_LIST = 720

const openId = ref<string | null>(null)
const root = ref<HTMLElement | null>(null)
const narrow = ref(false)

function toggle(id: string) {
  openId.value = openId.value === id ? null : id
}

let observer: ResizeObserver | null = null
onMounted(() => {
  if (typeof ResizeObserver === 'undefined' || !root.value) return
  observer = new ResizeObserver(([entry]) => {
    if (entry) narrow.value = entry.contentRect.width < NARROW_LIST
  })
  observer.observe(root.value)
})
onBeforeUnmount(() => observer?.disconnect())
</script>

<template>
  <div ref="root" class="list">
    <div class="list__cols" aria-hidden="true">
      <span />
      <span>{{ t('accounts.list.col.account') }}</span>
      <span>{{ t('accounts.list.col.status') }}</span>
      <span>{{ t('accounts.list.col.slots') }}</span>
      <span>{{ t('accounts.list.col.quota') }}</span>
      <span class="list__cols-end">{{ t('accounts.list.col.controls') }}</span>
    </div>
    <section
      v-for="group in groups"
      :key="group.backend"
      class="list__group"
      :aria-labelledby="`accounts-list-${group.backend}`"
    >
      <h2 :id="`accounts-list-${group.backend}`" class="list__heading">
        <ProviderMark :backend="group.backend" :label="false" />
        {{ t(`accounts.backend.${group.backend}`) }}
        <span class="list__count">{{ group.items.length }}</span>
      </h2>
      <ul class="list__rows">
        <AccountRow
          v-for="account in group.items"
          :key="account.id"
          :account="account"
          :now="now"
          :open="openId === account.id"
          :narrow="narrow"
          @toggle="toggle(account.id)"
          @dialog="emit('dialog', account, $event)"
        />
      </ul>
    </section>
  </div>
</template>

<style scoped>
.list {
  container: account-list / inline-size;
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  /* Column sizes shared by the header and every row (accountRow.css). */
  --account-cols: var(--control-sm) minmax(0, 1.3fr) minmax(0, 1fr) 76px minmax(0, 1.8fr) 176px;
}

.list__cols {
  display: grid;
  grid-template-columns: var(--account-cols);
  gap: var(--space-3);
  margin-bottom: calc(-1 * var(--space-2));
  padding: 0 var(--space-3) 0 var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-medium);
}

.list__cols-end {
  text-align: end;
}

.list__group {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.list__heading {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.list__count {
  min-width: 20px;
  padding: 0 var(--space-1);
  border-radius: var(--radius-pill);
  background: var(--neutral-bg);
  color: var(--neutral-fg);
  font-size: var(--fs-2xs);
  line-height: 20px;
  text-align: center;
}

.list__rows {
  margin: 0;
  padding: 0;
  overflow: hidden;
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  list-style: none;
}

@container account-list (width < 720px) {
  .list__cols {
    display: none;
  }
}
</style>
