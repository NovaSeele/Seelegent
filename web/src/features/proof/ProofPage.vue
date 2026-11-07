<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { readStored, writeStored, type StoredValue } from '@/lib/storage'
import { pathKey } from '@/lib/paths'
import { useConnectionStore } from '@/stores/connection'
import { useToastStore } from '@/stores/toast'

import ProofCounts from './ProofCounts.vue'
import ProofFeatures from './ProofFeatures.vue'
import ProofMatrixView from './ProofMatrixView.vue'
import { repoOptions } from './proofModel'
import ProofRuns from './ProofRuns.vue'
import ProofSide from './ProofSide.vue'
import { useProof } from './useProof'

/**
 * Proof (docs/gates.md): for one repository, which Spec Kit requirements a green gate proves, the regression runs
 * that run every gate command again, the lane claims held and what the gates did. The repository is picked from
 * the projects the daemon knows and remembered per viewer.
 */
const { t } = useI18n()
const connection = useConnectionStore()
const toast = useToastStore()

const LAST_REPO: StoredValue<string> = {
  name: 'proof.repo',
  fallback: '',
  validate: (v): v is string => typeof v === 'string',
}

const options = computed(() => repoOptions(connection.projects))
const cwd = ref(readStored(LAST_REPO))
watch(
  options,
  (list) => {
    if (!list.length) return
    if (!list.some((o) => pathKey(o.cwd) === pathKey(cwd.value))) cwd.value = list[0]?.cwd ?? ''
  },
  { immediate: true },
)
watch(cwd, (value) => writeStored(LAST_REPO, value))

const feature = ref('')
const proof = useProof(cwd, feature)

// The first feature is shown until the viewer picks another; a feature gone from the index is dropped.
watch(
  () => proof.index.value?.features,
  (features) => {
    if (!features) return
    if (!features.some((f) => f.dir === feature.value)) feature.value = features[0]?.dir ?? ''
  },
)
watch(cwd, () => (feature.value = ''))

const force = ref(false)
const refused = ref<string[] | null>(null)
async function run() {
  refused.value = null
  try {
    const conflict = await proof.startRun(force.value)
    if (conflict) refused.value = conflict.active ?? []
  } catch (e) {
    toast.show({ message: t('proof.runs.failed', { error: errorMessage(e) }), tone: 'error' })
  }
}
async function cancel(id: string) {
  try {
    await proof.cancelRun(id)
  } catch (e) {
    toast.show({ message: t('proof.runs.failed', { error: errorMessage(e) }), tone: 'error' })
  }
}

const running = computed(() => proof.index.value?.runs.some((r) => r.state === 'running') ?? false)
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.proof.label')" :description="t('nav.proof.desc')">
      <template v-if="options.length" #actions>
        <Select
          v-model="cwd"
          :label="t('proof.repo')"
          hide-label
          size="sm"
          :options="options.map((o) => ({ value: o.cwd, label: o.name }))"
        />
      </template>
    </PageHeader>

    <div class="proof">
      <EmptyState
        v-if="!options.length"
        icon="proof"
        :title="t('proof.empty.title')"
        :description="t('proof.empty.desc')"
      />
      <div v-else-if="proof.state.value === 'loading'" class="proof__loading">
        <Spinner :label="t('proof.loading')" />
      </div>
      <EmptyState
        v-else-if="proof.state.value === 'error'"
        icon="warning"
        :title="t('proof.loadFailed')"
        :description="proof.error.value"
      >
        <Button variant="secondary" @click="proof.load()">{{ t('common.action.retry') }}</Button>
      </EmptyState>
      <template v-else-if="proof.index.value">
        <section class="proof__overview" :aria-label="t('proof.overview')">
          <div class="proof__totals">
            <p class="proof__root">
              <code>{{ proof.index.value.repoRoot }}</code>
            </p>
            <ProofCounts :counts="proof.index.value.totals" />
          </div>
          <div class="proof__run">
            <Checkbox v-model="force" :label="t('proof.runs.force')" />
            <Button
              variant="primary"
              icon="refresh"
              :loading="proof.busy.value"
              :disabled="running"
              @click="run"
            >
              {{ t('proof.runs.start') }}
            </Button>
          </div>
          <p v-if="refused" class="proof__refused" role="status">
            {{ t('proof.runs.refused', { tasks: refused.join(', ') }) }}
          </p>
        </section>

        <div class="proof__grid">
          <ProofFeatures
            :features="proof.index.value.features"
            :selected="feature"
            @select="feature = $event"
          />
          <ProofMatrixView
            v-if="proof.index.value.features.length"
            :matrix="proof.matrix.value"
            :loading="proof.matrixLoading.value"
            :error="proof.matrixError.value"
          />
        </div>

        <div class="proof__grid">
          <div class="proof__side">
            <ProofSide :lanes="proof.lanes.value" :stats="proof.stats.value" />
          </div>
          <ProofRuns :runs="proof.index.value.runs" :busy="proof.busy.value" @cancel="cancel" />
        </div>
      </template>
    </div>
  </PageContainer>
</template>

<style scoped>
.proof {
  display: flex;
  flex-direction: column;
  gap: var(--space-6);
  padding: 0 var(--space-6) var(--space-8);
}

.proof__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8);
}

.proof__overview {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-3) var(--space-5);
}

.proof__totals {
  display: flex;
  flex: 1 1 320px;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.proof__root {
  color: var(--text-secondary);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.proof__run {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-3);
}

.proof__refused {
  flex-basis: 100%;
  color: var(--attention-fg);
  font-size: var(--fs-sm);
}

.proof__grid {
  display: grid;
  grid-template-columns: minmax(220px, 1fr) minmax(0, 2fr);
  gap: var(--space-5);
  align-items: start;
}

.proof__side {
  display: flex;
  flex-direction: column;
  gap: var(--space-5);
  min-width: 0;
}

@media (width < 900px) {
  .proof__grid {
    grid-template-columns: 1fr;
  }
}

@media (width < 600px) {
  .proof {
    padding: 0 var(--space-4) var(--space-6);
  }
}
</style>
