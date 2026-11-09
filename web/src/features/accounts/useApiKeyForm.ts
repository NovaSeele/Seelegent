import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type {
  AccountCreated,
  KeyTestRequest,
  KeyTestResult,
  NewAccount,
  Notice,
  ProviderInput,
  TierModels,
} from '@/api/types'
import { useAccountsStore } from '@/stores/accounts'
import { useProvidersStore } from '@/stores/providers'

import {
  checkKey,
  detectPreset,
  isCustomPreset,
  parseService,
  serviceForPreset,
  serviceValue,
} from './apiKey'
import { testAllowsSave } from './keyTest'
import { listedModels, allowList, parseModels, tierModels } from './providerForm'
import { useServiceNames } from './useServiceNames'

const NO_TIERS: TierModels = { fast: '', standard: '', strong: '', max: '' }

/**
 * The state and the one call of the API-key form ("Test and add"): the service (an own provider, or a preset a
 * provider is made or reused from), the key (cleaned like the daemon does; a known prefix picks the service while
 * the user has not picked one), the model, name, priority, budget and the advanced fields. `submit(true)` tests the
 * key first and adds the account only when the key works (or the service cannot be checked); `submit(false)` adds
 * it untested. The add is one atomic POST /accounts. Errors the daemon ties to a field show at that field.
 */
export function useApiKeyForm(initialService: string, done: (created: AccountCreated) => void) {
  const { t } = useI18n()
  const accounts = useAccountsStore()
  const providers = useProvidersStore()
  const names = useServiceNames()

  const service = ref(initialService)
  const serviceTouched = ref(initialService !== '')
  const rawKey = ref('')
  const detected = ref('')
  const tiers = ref<TierModels>({ ...NO_TIERS })
  const label = ref('')
  const labelTouched = ref(false)
  const priority = ref(0)
  const budget = ref(0)
  const budgetStop = ref(true)
  const baseUrl = ref('')
  const wireApi = ref('')
  const extraModels = ref('')

  const busy = ref(false)
  const testing = ref(false)
  const attempted = ref(false)
  const result = ref<KeyTestResult | null>(null)
  const notices = ref<Notice[]>([])
  const errors = ref<Record<string, string>>({})
  const failure = ref('')

  const ref_ = computed(() => parseService(service.value))
  const info = computed(() => (ref_.value ? names.of(ref_.value) : null))
  const existing = computed(() => info.value?.provider)
  const preset = computed(() => info.value?.preset)
  const custom = computed(() => isCustomPreset(preset.value?.id))
  const check = computed(() => checkKey(rawKey.value))

  /** Models the service is known to have: its tiers and list, the preset's suggestions, a key test's answer. */
  const knownModels = computed(() => {
    const p = existing.value
    const fromService = p
      ? [...tierModels(p), ...listedModels(allowList(p))]
      : [
          ...(preset.value?.tiers ? tierModels({ tiers: preset.value.tiers }) : []),
          ...(preset.value?.models ?? []),
        ]
    return [...new Set([...fromService, ...(result.value?.models ?? [])])]
  })

  function defaultLabel(): string {
    const name = info.value?.name ?? ''
    const p = existing.value
    const presetId = preset.value?.id
    const used = accounts.accounts.filter((a) => {
      if (!a.provider) return false
      if (p) return a.provider === p.id
      return providers.items.find((x) => x.id === a.provider)?.preset === presetId
    }).length
    return t('accounts.add.defaultLabel', { backend: name, n: used + 1 })
  }

  // A new service: its preset's address and suggested tiers; any old test result no longer applies.
  watch(
    service,
    () => {
      result.value = null
      notices.value = []
      errors.value = {}
      const p = preset.value
      tiers.value = existing.value ? { ...existing.value.tiers } : { ...(p?.tiers ?? NO_TIERS) }
      baseUrl.value = p?.baseUrl ?? ''
      wireApi.value = p?.wireApi ?? ''
      extraModels.value = (p?.models ?? []).join(', ')
      if (!labelTouched.value) label.value = info.value ? defaultLabel() : ''
    },
    { immediate: true },
  )

  // A pasted key names its service (sk-ant-, sk-or-, gsk_) while the user has not chosen one.
  watch(rawKey, () => {
    result.value = null
    if (serviceTouched.value) return
    const presetId = detectPreset(check.value.key)
    const found = presetId ? providers.presets.find((p) => p.id === presetId) : undefined
    if (!presetId || !found) {
      detected.value = ''
      return
    }
    service.value = serviceValue(serviceForPreset(presetId, providers.items))
    detected.value = names.preset(found)
  })

  const serviceError = computed(
    () =>
      errors.value.provider ??
      errors.value.newProvider ??
      (attempted.value && !ref_.value ? t('accounts.service.required') : ''),
  )
  const baseUrlError = computed(
    () =>
      errors.value['newProvider.baseUrl'] ??
      (attempted.value && custom.value && !baseUrl.value.trim()
        ? t('accounts.form.baseUrlRequired')
        : ''),
  )
  const keyError = computed(() => errors.value.apiKey ?? '')
  const valid = computed(
    () => !!ref_.value && !check.value.error && (!custom.value || baseUrl.value.trim() !== ''),
  )

  function target(): Pick<KeyTestRequest, 'provider' | 'newProvider'> {
    const p = existing.value
    if (p) return { provider: p.id }
    const pre = preset.value
    if (!pre) return {}
    const input: ProviderInput = { preset: pre.id, backend: pre.backend }
    if (!pre.official && baseUrl.value.trim() !== pre.baseUrl) input.baseUrl = baseUrl.value.trim()
    if (pre.backend === 'codex' && wireApi.value && wireApi.value !== pre.wireApi)
      input.wireApi = wireApi.value
    const models = parseModels(extraModels.value)
    if (models.join('\n') !== pre.models.join('\n')) input.models = models
    if (Object.values(tiers.value).some(Boolean)) input.tiers = { ...tiers.value }
    return { newProvider: input }
  }

  function showError(error: unknown) {
    const problems = fieldProblems(error)
    errors.value = Object.fromEntries(problems.map((p) => [p.field, p.message]))
    if (!problems.length) failure.value = errorMessage(error)
  }

  async function test(): Promise<KeyTestResult | null> {
    testing.value = true
    try {
      const found = await providers.test({ ...target(), apiKey: check.value.key })
      result.value = found
      notices.value = found.notices ?? []
      return found
    } finally {
      testing.value = false
    }
  }

  /** Checks the key without adding anything: lists the service's models for the model dropdown. */
  async function listModels() {
    attempted.value = true
    if (!valid.value || busy.value) return
    busy.value = true
    errors.value = {}
    failure.value = ''
    try {
      await test()
    } catch (error) {
      showError(error)
    } finally {
      busy.value = false
    }
  }

  async function submit(withTest: boolean) {
    attempted.value = true
    if (!valid.value || busy.value) return
    busy.value = true
    errors.value = {}
    failure.value = ''
    try {
      const tested = withTest ? await test() : null
      if (tested && !testAllowsSave(tested)) return
      const body: NewAccount = {
        ...target(),
        kind: 'api-key',
        apiKey: check.value.key,
        label: label.value.trim() || defaultLabel(),
        priority: priority.value,
        ...(budget.value > 0 ? { budgetUsd: budget.value, budgetStop: budgetStop.value } : {}),
      }
      const created = await accounts.create(body)
      // The test above ran before the account existed: run it once more on the stored key, so the account
      // records it ("Works") instead of starting as "Not tested".
      if (tested)
        void providers
          .test({ accountId: created.id })
          .then((r) => accounts.noteTest(created.id, r))
          .catch(() => undefined)
      done(created)
    } catch (error) {
      showError(error)
    } finally {
      busy.value = false
    }
  }

  return {
    service,
    serviceTouched,
    rawKey,
    detected,
    tiers,
    label,
    labelTouched,
    priority,
    budget,
    budgetStop,
    baseUrl,
    wireApi,
    extraModels,
    busy,
    testing,
    attempted,
    result,
    notices,
    errors,
    failure,
    info,
    existing,
    preset,
    custom,
    knownModels,
    serviceError,
    baseUrlError,
    keyError,
    valid,
    failed: computed(() => !!result.value && !testAllowsSave(result.value)),
    listModels,
    submit,
  }
}
