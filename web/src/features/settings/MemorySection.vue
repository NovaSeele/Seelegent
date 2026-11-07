<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import Select from '@/components/ui/Select.vue'
import Spinner from '@/components/ui/Spinner.vue'
import TextArea from '@/components/ui/TextArea.vue'
import { useFormat } from '@/composables/useFormat'
import { useConnectionStore } from '@/stores/connection'
import { useSettingsStore } from '@/stores/settings'
import { useToastStore } from '@/stores/toast'

import SettingRow from './SettingRow.vue'

/**
 * Project memory: conventions, commands and pitfalls the daemon adds to every brief for that folder. Pick a
 * project, edit, save. When someone else saves the same project's memory meanwhile, a notice offers to load theirs
 * (your text stays until you choose).
 */
defineProps<{ highlight?: string }>()

const MAX = 65536

const { t } = useI18n()
const fmt = useFormat()
const connection = useConnectionStore()
const settings = useSettingsStore()
const toast = useToastStore()

const cwd = ref(connection.projects[0]?.cwd ?? '')
const text = ref('')
const status = ref<'idle' | 'loading' | 'ready' | 'error'>('idle')
const error = ref('')
const saving = ref(false)

const options = computed(() =>
  connection.projects.map((p) => ({ value: p.cwd, label: `${p.name} — ${p.cwd}` })),
)
const dirty = computed(() => status.value === 'ready' && text.value !== settings.memory?.text)

async function load(folder: string) {
  if (!folder) return
  status.value = 'loading'
  try {
    text.value = (await settings.loadMemory(folder)).text
    status.value = 'ready'
  } catch (cause) {
    error.value = errorMessage(cause)
    status.value = 'error'
  }
}

watch(cwd, (folder) => void load(folder), { immediate: true })

async function save() {
  saving.value = true
  try {
    await settings.saveMemory(cwd.value, text.value)
    toast.show({ message: t('settings.memory.saved'), tone: 'success' })
  } catch (cause) {
    toast.show({
      message: t('settings.memory.saveFailed', { error: errorMessage(cause) }),
      tone: 'error',
    })
  } finally {
    saving.value = false
  }
}
</script>

<template>
  <SettingRow item="projectMemory" stacked :highlighted="highlight === 'projectMemory'">
    <div class="memory">
      <p v-if="!options.length" class="memory__note">{{ t('settings.memory.noProjects') }}</p>
      <template v-else>
        <Select v-model="cwd" :label="t('settings.memory.project')" size="sm" :options="options" />
        <Spinner v-if="status === 'loading'" :label="t('settings.memory.loading')" />
        <p v-else-if="status === 'error'" class="memory__note" role="alert">
          {{ t('settings.memory.loadFailed', { error }) }}
        </p>
        <template v-else-if="status === 'ready'">
          <div v-if="settings.memoryChanged" class="memory__changed tone-attention" role="status">
            {{ t('settings.memory.changed') }}
            <Button size="sm" variant="ghost" @click="load(cwd)">
              {{ t('settings.memory.reload') }}
            </Button>
          </div>
          <TextArea
            v-model="text"
            :label="t('settings.memory.text')"
            :rows="12"
            :maxlength="MAX"
            mono
            :placeholder="t('settings.memory.placeholder')"
          />
          <div class="memory__foot">
            <span class="memory__when">
              {{
                settings.memory?.updatedAt
                  ? t('settings.memory.updated', { time: fmt.dateTime(settings.memory.updatedAt) })
                  : t('settings.memory.none')
              }}
            </span>
            <Button
              v-if="dirty"
              size="sm"
              variant="ghost"
              @click="text = settings.memory?.text ?? ''"
            >
              {{ t('settings.memory.discard') }}
            </Button>
            <Button size="sm" variant="primary" :disabled="!dirty" :loading="saving" @click="save">
              {{ t('settings.memory.save') }}
            </Button>
          </div>
        </template>
      </template>
    </div>
  </SettingRow>
</template>

<style scoped>
.memory {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  margin-top: var(--space-2);
}

.memory__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.memory__changed {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-sm);
}

.memory__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  justify-content: flex-end;
  gap: var(--space-2);
}

.memory__when {
  flex: 1;
  color: var(--text-muted);
  font-size: var(--fs-xs);
}
</style>
