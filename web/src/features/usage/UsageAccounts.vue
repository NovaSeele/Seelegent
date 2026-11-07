<script setup lang="ts">
import { ref } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Summary } from '@/api/types'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'

/** The accounts that used the most tokens, each opening to the models it ran. Labels never carry an email. */
const props = defineProps<{ summary: Summary }>()

const { t } = useI18n()
const fmt = useFormat()
const open = ref(new Set<string>())

function toggle(id: string) {
  const next = new Set(open.value)
  if (next.has(id)) next.delete(id)
  else next.add(id)
  open.value = next
}

const cost = (n: number) =>
  props.summary.totals.hasCost ? fmt.value.number(n, { style: 'currency', currency: 'USD' }) : '—'
</script>

<template>
  <section class="usage-table" aria-labelledby="usage-accounts">
    <h2 id="usage-accounts" class="usage-table__title">{{ t('usage.accounts.title') }}</h2>
    <p v-if="!summary.accounts.length" class="usage-table__empty">
      {{ t('usage.accounts.empty') }}
    </p>
    <div v-else class="usage-table__scroll">
      <table>
        <thead>
          <tr>
            <th scope="col">{{ t('usage.col.account') }}</th>
            <th scope="col">{{ t('usage.col.tokens') }}</th>
            <th scope="col">{{ t('usage.col.cost') }}</th>
            <th scope="col">{{ t('usage.col.attempts') }}</th>
            <th scope="col">{{ t('usage.col.time') }}</th>
          </tr>
        </thead>
        <tbody v-for="account in summary.accounts" :key="account.id">
          <tr class="lvl-0">
            <th scope="row">
              <button
                type="button"
                class="disclose"
                :aria-expanded="open.has(account.id)"
                @click="toggle(account.id)"
              >
                <Icon :name="open.has(account.id) ? 'chevronDown' : 'chevronRight'" />
                {{ account.label }}
                <span class="backend">{{ t(`accounts.backend.${account.backend}`) }}</span>
              </button>
            </th>
            <td>{{ fmt.compact(account.tokens) }}</td>
            <td>{{ cost(account.costUsd) }}</td>
            <td>{{ fmt.number(account.attempts) }}</td>
            <td>{{ fmt.duration(account.durationMs) }}</td>
          </tr>
          <template v-if="open.has(account.id)">
            <tr v-for="model in account.models" :key="model.model" class="lvl-1">
              <th scope="row">{{ model.model }}</th>
              <td>{{ fmt.compact(model.tokens) }}</td>
              <td>{{ cost(model.costUsd) }}</td>
              <td>{{ fmt.number(model.attempts) }}</td>
              <td>{{ fmt.duration(model.durationMs) }}</td>
            </tr>
            <tr v-if="account.more" class="lvl-1 more">
              <td colspan="5">{{ t('usage.moreModels', { n: account.more }, account.more) }}</td>
            </tr>
          </template>
        </tbody>
      </table>
    </div>
    <p v-if="summary.moreAccounts" class="usage-table__more">
      {{ t('usage.moreAccounts', { n: summary.moreAccounts }, summary.moreAccounts) }}
    </p>
  </section>
</template>

<style scoped src="./usageTable.css"></style>
<style scoped>
.backend {
  color: var(--text-muted);
  font-size: var(--fs-2xs);
  font-weight: var(--fw-regular);
}
</style>
