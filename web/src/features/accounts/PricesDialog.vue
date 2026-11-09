<script setup lang="ts">
import { computed, nextTick, ref, useId, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Provider } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import IconButton from '@/components/ui/IconButton.vue'
import TextField from '@/components/ui/TextField.vue'
import { useProvidersStore } from '@/stores/providers'
import { useToastStore } from '@/stores/toast'

import { defaultCacheWrite, type PriceRow, priceErrorKey, priceRow, pricesInput } from './prices'
import { allowList, listedModels, tierModels } from './providerForm'

/**
 * A service's prices, in US dollars per 1M tokens: one row per model (input, cached input, cache write, output;
 * "*" for any model without its own row), rows added and removed, saved as one table. An empty optional price shows
 * what it stands for as its placeholder. An empty table clears the prices: the spend is then counted in tokens only. Each field shows its own error, from the form's checks or from the
 * daemon's 400. The service's own price page is linked when it has one. New prices apply from now on.
 */
const props = defineProps<{ provider: Provider | null | undefined }>()
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const providers = useProvidersStore()
const toast = useToastStore()
const id = useId()

const formEl = ref<HTMLFormElement | null>(null)
const rows = ref<PriceRow[]>([])
const errors = ref<Record<string, string>>({})
const busy = ref(false)
let keys = 0

const name = computed(() => props.provider?.name ?? '')
const suggestions = computed(() => {
  const p = props.provider
  return p ? [...new Set(['*', ...tierModels(p), ...listedModels(allowList(p))])] : []
})

watch(open, (value) => {
  if (!value) return
  errors.value = {}
  rows.value = (props.provider?.prices ?? []).map((p) => priceRow(++keys, p))
})

const COLUMNS = ['model', 'input', 'cached', 'write', 'output'] as const
const LABELS = {
  model: 'model',
  input: 'input',
  cached: 'cachedInput',
  write: 'cacheWrite',
  output: 'output',
} as const
const columnLabel = (c: (typeof COLUMNS)[number]) => t(`accounts.prices.${LABELS[c]}`)

/** An empty optional price shows what it stands for: cached input the input price, cache write 1.25 × it. */
function placeholder(row: PriceRow, column: (typeof COLUMNS)[number]): string | undefined {
  if (column === 'cached') return row.input || undefined
  if (column === 'write') return defaultCacheWrite(row.input) || undefined
  return undefined
}

/** Focus the model field of row `index` (the nearest one left), or "Add a row" when there is none. */
async function focusRow(index: number) {
  await nextTick()
  const form = formEl.value
  const lines = form?.querySelectorAll<HTMLElement>('.prices__row') ?? []
  const line = lines[Math.min(index, lines.length - 1)]
  const target =
    line?.querySelector('input') ?? form?.querySelector<HTMLElement>('.prices__foot button')
  target?.focus()
}

function add() {
  rows.value = [...rows.value, priceRow(++keys)]
  void focusRow(rows.value.length - 1)
}

function remove(key: number) {
  const index = rows.value.findIndex((r) => r.key === key)
  rows.value = rows.value.filter((r) => r.key !== key)
  errors.value = {}
  void focusRow(index)
}

async function save() {
  const p = props.provider
  if (!p || busy.value) return
  const checked = pricesInput(rows.value)
  errors.value = Object.fromEntries(
    Object.entries(checked.errors).map(([k, problem]) => [
      k,
      t(`accounts.prices.error.${problem}`),
    ]),
  )
  if (Object.keys(checked.errors).length) return
  busy.value = true
  try {
    await providers.setPrices(p.id, checked.prices)
    open.value = false
    toast.show({
      message: checked.prices.length
        ? t('accounts.done.pricesSet', { service: p.name })
        : t('accounts.done.pricesCleared', { service: p.name }),
      tone: 'success',
    })
  } catch (error) {
    const problems = fieldProblems(error)
    const found: Record<string, string> = {}
    for (const problem of problems) {
      const key = priceErrorKey(problem.field)
      if (key) found[key] = problem.message
    }
    if (!Object.keys(found).length) found.table = errorMessage(error)
    errors.value = found
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    v-model:open="open"
    size="lg"
    :title="t('accounts.prices.title', { service: name })"
    :description="t('accounts.prices.perMillion')"
    :dismissible="!busy"
    initial-focus=".prices input"
  >
    <form :id="`${id}-form`" ref="formEl" class="prices" novalidate @submit.prevent="save">
      <datalist :id="`${id}-models`">
        <option v-for="m in suggestions" :key="m" :value="m" />
      </datalist>
      <p v-if="!rows.length" class="prices__empty">{{ t('accounts.prices.empty') }}</p>
      <ul v-else class="prices__rows">
        <li v-for="(row, i) in rows" :key="row.key" class="prices__row">
          <TextField
            v-for="column in COLUMNS"
            :key="column"
            v-model="row[column]"
            :class="`prices__${column}`"
            size="sm"
            :label="columnLabel(column)"
            :inputmode="column === 'model' ? 'text' : 'decimal'"
            :list="column === 'model' ? `${id}-models` : undefined"
            :placeholder="placeholder(row, column)"
            :error="errors[`${i}.${column}`]"
          />
          <span class="prices__remove">
            <IconButton
              icon="trash"
              size="sm"
              :label="t('accounts.prices.remove', { model: row.model || '—' })"
              @click="remove(row.key)"
            />
          </span>
        </li>
      </ul>
      <p v-if="errors.table" class="prices__error" role="alert">{{ errors.table }}</p>
      <div class="prices__foot">
        <Button size="sm" variant="secondary" icon="plus" @click="add">
          {{ t('accounts.prices.add') }}
        </Button>
        <a
          v-if="provider?.pricingUrl"
          class="prices__link"
          :href="provider.pricingUrl"
          target="_blank"
          rel="noopener noreferrer"
        >
          {{ t('accounts.prices.pricingLink', { service: name }) }}
        </a>
      </div>
      <p class="prices__hint">
        {{ t('accounts.prices.anyModel') }} {{ t('accounts.prices.cacheWriteHint') }}
        {{ t('accounts.prices.appliesFromNow') }}
      </p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" :form="`${id}-form`" variant="primary" :loading="busy">
        {{ t('accounts.prices.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.prices {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.prices__rows {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.prices__row {
  display: grid;
  grid-template-columns: minmax(8rem, 2fr) repeat(4, minmax(4.5rem, 1fr)) auto;
  align-items: start;
  gap: var(--space-2);
}

@media (width < 600px) {
  .prices__row {
    /* The model and the remove button on the first line, the four prices under them, two by two. */
    grid-template-areas:
      'model model remove'
      'input cached .'
      'write output .';
    grid-template-columns: repeat(2, minmax(0, 1fr)) auto;
    padding-bottom: var(--space-2);
    border-bottom: var(--border-width) solid var(--border-subtle);
  }

  .prices__model {
    grid-area: model;
  }

  .prices__input {
    grid-area: input;
  }

  .prices__cached {
    grid-area: cached;
  }

  .prices__write {
    grid-area: write;
  }

  .prices__output {
    grid-area: output;
  }

  .prices__remove {
    grid-area: remove;
  }
}

/* Level with the fields, under a label's height (an error under a field leaves the row's top as it is). */
.prices__remove {
  display: flex;
  align-items: center;
  min-height: max(var(--control-sm), var(--target-min));
  margin-top: calc(1lh + var(--space-1));
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.prices__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.prices__link {
  color: var(--link);
  font-size: var(--fs-xs);
}

.prices__empty,
.prices__hint {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.prices__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
