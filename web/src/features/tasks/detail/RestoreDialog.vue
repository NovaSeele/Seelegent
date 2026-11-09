<script setup lang="ts">
import { computed, nextTick, ref, shallowRef } from 'vue'
import { useI18n } from 'vue-i18n'

import { gitApi, type GitApi } from '@/api'
import type { RestoreFailed, RestoreResult } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'

/**
 * Restore asks first: it lists the files, says their current content is replaced with what they held before the
 * turn and that a backup is kept, then shows what happened and the backup's name. Files someone changed after the
 * turn ended are left alone by the daemon; "Restore anyway" overwrites them after a second question. `changed`
 * asks the parent to read the task again.
 */
const props = withDefaults(
  defineProps<{ taskId: string; turn: number; files: readonly string[]; api?: GitApi }>(),
  { api: () => gitApi },
)
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ changed: [] }>()

const { t } = useI18n()

type Step = 'confirm' | 'force' | 'done'
const step = ref<Step>('confirm')
const busy = ref(false)
const error = ref<string | null>(null)
/** The results so far: a forced restore adds to the first one. */
const results = shallowRef<RestoreResult[]>([])

const restored = computed(() => results.value.flatMap((r) => r.restored))
const backups = computed(() => results.value.flatMap((r) => (r.backup ? [r.backup] : [])))
/** Skipped by the latest try (a forced retry replaces the earlier list). */
const skipped = computed<RestoreFailed[]>(() => results.value.at(-1)?.skipped ?? [])
const changed = computed(() =>
  skipped.value.filter((s) => s.code === 'changed_since_turn').map((s) => s.path),
)
const listed = computed(() => (step.value === 'force' ? changed.value : props.files))

const title = computed(() => {
  if (step.value === 'done') return t('task.safety.restore.doneTitle')
  if (step.value === 'force') return t('task.safety.restore.forceTitle', { n: props.turn })
  return t('task.safety.restore.title', { n: props.turn })
})

/** Moves focus to the step's main button, since the one that had it is gone. */
async function focusMain() {
  await nextTick()
  document.querySelector<HTMLElement>('[data-restore-main]')?.focus()
}

async function run(force: boolean) {
  busy.value = true
  error.value = null
  try {
    const files = force ? changed.value : props.files
    const result = await props.api.restore(props.taskId, props.turn, { files, force })
    results.value = [...results.value, result]
    step.value = 'done'
    if (result.restored.length) emit('changed')
  } catch (err) {
    error.value = t('task.actions.failed', {
      error: err instanceof Error ? err.message : String(err),
    })
  } finally {
    busy.value = false
    void focusMain()
  }
}

function anyway() {
  step.value = 'force'
  void focusMain()
}

const why = (s: RestoreFailed) =>
  s.code === 'changed_since_turn' ? t('task.safety.restore.changed') : s.error
</script>

<template>
  <Dialog v-model:open="open" size="md" :title="title" :dismissible="!busy">
    <div class="restore">
      <template v-if="step !== 'done'">
        <p>
          {{
            step === 'force'
              ? t('task.safety.restore.forceDesc', { n: turn })
              : t('task.safety.restore.desc', { n: turn })
          }}
        </p>
        <ul class="restore__files">
          <li v-for="path in listed" :key="path">
            <code class="restore__path">{{ path }}</code>
          </li>
        </ul>
      </template>
      <template v-else>
        <p v-if="restored.length">
          {{ t('task.safety.restore.done', { count: restored.length }, restored.length) }}
        </p>
        <p v-else>{{ t('task.safety.restore.nothing') }}</p>
        <p v-for="name in backups" :key="name" class="restore__muted">
          {{ t('task.safety.restore.backup') }}
          <code class="restore__path">{{ name }}</code>
        </p>
        <section v-if="skipped.length" class="restore__skipped">
          <h3 class="restore__title">{{ t('task.safety.restore.skippedTitle') }}</h3>
          <ul class="restore__files">
            <li v-for="s in skipped" :key="s.path">
              <code class="restore__path">{{ s.path }}</code>
              <span class="restore__muted"> · {{ why(s) }}</span>
            </li>
          </ul>
          <p v-if="changed.length" class="restore__muted">
            {{ t('task.safety.restore.changedNote') }}
          </p>
        </section>
      </template>
      <p v-if="error" class="restore__error" role="alert">{{ error }}</p>
    </div>
    <template #footer>
      <template v-if="step === 'done'">
        <Button v-if="changed.length" variant="danger" @click="anyway">
          {{ t('task.safety.restore.anyway') }}
        </Button>
        <Button data-restore-main variant="primary" @click="open = false">
          {{ t('common.action.close') }}
        </Button>
      </template>
      <template v-else>
        <Button variant="ghost" :disabled="busy" @click="open = false">
          {{ t('common.action.cancel') }}
        </Button>
        <Button
          data-restore-main
          :variant="step === 'force' ? 'danger' : 'primary'"
          :loading="busy"
          @click="run(step === 'force')"
        >
          {{
            step === 'force'
              ? t('task.safety.restore.forceConfirm', { n: listed.length })
              : t('task.safety.risk.restore', { n: listed.length })
          }}
        </Button>
      </template>
    </template>
  </Dialog>
</template>

<style scoped>
.restore {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  font-size: var(--fs-sm);
}

.restore__files {
  position: relative;
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  max-height: 30vh;
  margin: 0;
  padding: 0;
  overflow-y: auto;
  list-style: none;
}

.restore__path {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.restore__title {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.restore__skipped {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.restore__muted {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  overflow-wrap: anywhere;
}

.restore__error {
  color: var(--status-danger-fg);
}
</style>
