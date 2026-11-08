<script setup lang="ts">
import { computed, onMounted, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRoute, useRouter } from 'vue-router'

import { errorMessage } from '@/api/client'
import type { Automation } from '@/api/types'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useAutomationsStore } from '@/stores/automations'
import { useConnectionStore } from '@/stores/connection'
import { useToastStore } from '@/stores/toast'

import AutomationCard from './AutomationCard.vue'
import AutomationEditor from './AutomationEditor.vue'
import AutomationRuns from './AutomationRuns.vue'
import ConfirmEnableDialog from './ConfirmEnableDialog.vue'

/**
 * Automations: saved delegations on a schedule. The lead's tools can create and change them and ask for one to
 * be turned on; only this page turns one on, after showing exactly what it will run. A confirmation link
 * (/automations?confirm=<id>, what the lead hands the user) opens that dialog.
 */
const { t } = useI18n()
const route = useRoute()
const router = useRouter()
const store = useAutomationsStore()
const connection = useConnectionStore()
const toast = useToastStore()

const editing = ref(false)
const edited = ref<Automation | null>(null)
const confirming = ref(false)
const confirmId = ref<string | null>(null)
const history = ref(false)
const historyOf = ref<Automation | null>(null)
const removing = ref<Automation | null>(null)
const removeBusy = ref(false)
const removeError = ref('')
const running = ref<Automation | null>(null)
const busyId = ref('')

onMounted(() => store.ensure())

const masterOff = computed(() => connection.settings?.automationsEnabled === false)
const shown = computed(() => [...store.list].sort((a, b) => a.name.localeCompare(b.name)))

watch(
  () => route.query.confirm,
  (id) => {
    if (typeof id !== 'string' || !id) return
    confirmId.value = id
    confirming.value = true
  },
  { immediate: true },
)
watch(confirming, (now) => {
  if (!now && route.query.confirm) void router.replace({ name: 'automations' })
})

function edit(a: Automation | null) {
  edited.value = a
  editing.value = true
}

function askConfirm(a: Automation) {
  confirmId.value = a.id
  confirming.value = true
}

function showRuns(a: Automation) {
  historyOf.value = a
  history.value = true
}

async function act(a: Automation, action: () => Promise<string>) {
  busyId.value = a.id
  try {
    toast.show({ message: await action(), tone: 'success' })
  } catch (cause) {
    toast.show({ message: errorMessage(cause), tone: 'error' })
  } finally {
    busyId.value = ''
  }
}

const disable = (a: Automation) =>
  act(a, async () => {
    await store.patch(a.id, { enabled: false })
    return t('automations.card.disabled', { name: a.name })
  })

// Asked first: a run hands work to a worker at once (precheck, quota and overlap still decide).
function runNow() {
  const a = running.value
  if (!a) return
  running.value = null
  void runOne(a)
}

const runOne = (a: Automation) =>
  act(a, async () => {
    const run = await store.api().run(a.id)
    return t('automations.card.ran', { outcome: t(`automations.outcome.${run.outcome}`) })
  })

async function remove() {
  const a = removing.value
  if (!a) return
  removeBusy.value = true
  removeError.value = ''
  try {
    await store.remove(a.id)
    removing.value = null
  } catch (cause) {
    removeError.value = errorMessage(cause)
  } finally {
    removeBusy.value = false
  }
}
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.automations.label')" :description="t('nav.automations.desc')">
      <template #actions>
        <Button variant="primary" icon="plus" @click="edit(null)">{{
          t('automations.new')
        }}</Button>
      </template>
    </PageHeader>
    <div class="pad">
      <p v-if="masterOff" class="pad__master" role="status">
        {{ t('automations.masterOff') }}
        <RouterLink :to="{ name: 'settings', params: { section: 'system' } }">
          {{ t('automations.masterLink') }}
        </RouterLink>
      </p>
      <Spinner
        v-if="store.status === 'loading' && !store.list.length"
        :label="t('automations.loading')"
      />
      <div v-else-if="store.status === 'error'" class="pad__note" role="alert">
        {{ t('automations.loadFailed', { error: store.error }) }}
        <Button size="sm" variant="ghost" @click="store.load()">{{
          t('common.action.retry')
        }}</Button>
      </div>
      <EmptyState
        v-else-if="!shown.length"
        icon="automations"
        :title="t('automations.empty')"
        :description="t('automations.emptyDesc')"
      >
        <Button variant="primary" icon="plus" @click="edit(null)">{{
          t('automations.new')
        }}</Button>
      </EmptyState>
      <div v-else class="pad__list">
        <AutomationCard
          v-for="a in shown"
          :key="a.id"
          :automation="a"
          :busy="busyId === a.id"
          @confirm="askConfirm(a)"
          @disable="disable(a)"
          @run="running = a"
          @edit="edit(a)"
          @runs="showRuns(a)"
          @remove="removing = a"
        />
      </div>
    </div>
    <AutomationEditor v-model:open="editing" :automation="edited" />
    <ConfirmEnableDialog v-model:open="confirming" :automation-id="confirmId" />
    <AutomationRuns v-model:open="history" :automation="historyOf" />
    <ConfirmDialog
      :open="removing !== null"
      :title="t('automations.deleteTitle', { name: removing?.name ?? '' })"
      :description="t('automations.deleteDesc')"
      :confirm-label="t('automations.card.delete')"
      danger
      :busy="removeBusy"
      :error="removeError"
      @update:open="removing = null"
      @confirm="remove"
    />
    <ConfirmDialog
      :open="running !== null"
      :title="t('automations.runTitle', { name: running?.name ?? '' })"
      :description="t('automations.runDesc')"
      :confirm-label="t('automations.card.runNow')"
      @update:open="running = null"
      @confirm="runNow"
    />
  </PageContainer>
</template>

<style scoped>
.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

@media (width < 600px) {
  .pad {
    padding-inline: var(--space-4);
  }
}

.pad__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.pad__master {
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.pad__master a {
  margin-inline-start: var(--space-1);
  color: var(--link);
}

.pad__list {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 420px), 1fr));
  gap: var(--space-3);
}
</style>
