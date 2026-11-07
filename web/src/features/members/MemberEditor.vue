<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Member } from '@/api/types'
import { TIERS } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Select from '@/components/ui/Select.vue'
import Switch from '@/components/ui/Switch.vue'
import TextField from '@/components/ui/TextField.vue'
import { slugify } from '@/features/roles/rolesModel'
import { useProvidersStore } from '@/stores/providers'
import { useRolesStore } from '@/stores/roles'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import { BACKENDS, draftOf, inputOf, MEMBER_ID, RANKS, type Backend } from './memberModel'

/**
 * Creates or edits a member. `target` is null when closed, 'new' for a new member, or the member being edited.
 * The id follows the name until typed by hand and cannot change once saved. A provider member runs on its
 * provider's backend and has no fallback backends.
 */
const props = defineProps<{ target: Member | 'new' | null }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const team = useTeamStore()
const providers = useProvidersStore()
const roles = useRolesStore()
const toast = useToastStore()

const draft = ref(draftOf())
const idTouched = ref(false)
const busy = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')

const editing = computed(() => props.target !== null && props.target !== 'new')

watch(
  () => props.target,
  (target) => {
    if (target === null) return
    providers.ensure()
    roles.ensure()
    draft.value = draftOf(target === 'new' ? undefined : target)
    idTouched.value = target !== 'new'
    errors.value = {}
    failure.value = ''
  },
)

watch(
  () => draft.value.name,
  (name) => {
    if (!idTouched.value) draft.value.id = name.trim() ? slugify(name) : ''
  },
)

const provider = computed(() => providers.items.find((p) => p.id === draft.value.provider))
const rankOptions = computed(() =>
  RANKS.map((value) => ({ value, label: t(`team.rank.${value}`) })),
)
const providerOptions = computed(() => [
  { value: '', label: t('team.members.editor.noProvider') },
  ...providers.items.map((p) => ({
    value: p.id,
    label: `${p.name} (${t(`accounts.backend.${p.backend}`)})`,
  })),
])
const backendOptions = computed(() =>
  BACKENDS.map((value) => ({ value, label: t(`accounts.backend.${value}`) })),
)
const tierOptions = computed(() => [
  { value: '' as const, label: t('team.members.editor.default') },
  ...TIERS.map((value) => ({ value, label: t(`roles.tier.${value}`) })),
])
const roleOptions = computed(() => [
  { value: '', label: t('team.members.editor.default') },
  ...[...new Set(roles.items.map((r) => r.id))].sort().map((id) => ({ value: id, label: id })),
])
const backend = computed<Backend>(() => provider.value?.backend ?? draft.value.backend)

function toggleFallback(b: Backend, on: boolean) {
  const list = draft.value.fallbackBackends.filter((x) => x !== b)
  draft.value.fallbackBackends = on ? [...list, b] : list
}

function check(): boolean {
  const next: Record<string, string> = {}
  if (!draft.value.name.trim()) next.name = t('team.members.editor.errors.name')
  if (!editing.value && draft.value.id && !MEMBER_ID.test(draft.value.id)) {
    next.id = t('team.members.editor.errors.id')
  }
  errors.value = next
  return Object.keys(next).length === 0
}

async function save() {
  if (busy.value || !check()) return
  busy.value = true
  failure.value = ''
  try {
    const body = inputOf(draft.value, provider.value?.backend)
    const target = props.target
    const saved =
      target && target !== 'new'
        ? await team.patchMember(target.id, { ...body, id: target.id })
        : await team.createMember(body)
    toast.show({ message: t('team.members.saved', { name: saved.name }), tone: 'success' })
    emit('close')
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="target !== null"
    size="lg"
    :title="editing ? t('team.members.editor.editTitle') : t('team.members.editor.newTitle')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="emit('close')"
  >
    <form id="member-editor" class="editor" @submit.prevent="save">
      <div class="editor__row">
        <TextField
          v-model="draft.name"
          :label="t('team.members.editor.name')"
          :error="errors.name"
        />
        <TextField
          v-model="draft.id"
          :label="t('team.members.editor.id')"
          :hint="t('team.members.editor.idHint')"
          :error="errors.id"
          :disabled="editing"
          @input="idTouched = true"
        />
      </div>
      <SegmentedControl
        v-model="draft.rank"
        :options="rankOptions"
        :label="t('team.members.editor.rank')"
      />
      <p class="editor__hint">{{ t(`team.rankHint.${draft.rank}`) }}</p>
      <div class="editor__row">
        <Select
          v-model="draft.provider"
          :label="t('team.members.editor.provider')"
          :options="providerOptions"
        />
        <Select
          v-if="!draft.provider"
          v-model="draft.backend"
          :label="t('team.members.editor.backend')"
          :options="backendOptions"
        />
      </div>
      <div class="editor__row">
        <Select
          v-model="draft.tier"
          :label="t('team.members.editor.tier')"
          :options="tierOptions"
        />
        <TextField
          v-model="draft.model"
          :label="t('team.members.editor.model')"
          :hint="t('team.members.editor.modelHint')"
        />
        <Select
          v-model="draft.role"
          :label="t('team.members.editor.role')"
          :options="roleOptions"
        />
      </div>
      <fieldset v-if="!draft.provider" class="editor__set">
        <legend class="editor__legend">{{ t('team.members.editor.fallback') }}</legend>
        <p class="editor__hint">{{ t('team.members.editor.fallbackHint') }}</p>
        <div class="editor__checks">
          <Checkbox
            v-for="b in BACKENDS.filter((x) => x !== backend)"
            :key="b"
            :model-value="draft.fallbackBackends.includes(b)"
            :label="t(`accounts.backend.${b}`)"
            @update:model-value="toggleFallback(b, $event)"
          />
        </div>
      </fieldset>
      <Switch
        v-model="draft.enabled"
        :label="t('team.members.editor.enabled')"
        :description="t('team.members.editor.enabledHint')"
      />
      <p v-if="failure" class="editor__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="emit('close')">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="member-editor" variant="primary" :loading="busy">
        {{ t('team.members.editor.save') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.editor {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}

.editor__row {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 200px), 1fr));
  align-items: start;
  gap: var(--space-3);
}

.editor__set {
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.editor__legend {
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.editor__hint {
  margin-top: calc(-1 * var(--space-2));
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.editor__set .editor__hint {
  margin: 0 0 var(--space-2);
}

.editor__checks {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-3);
}

.editor__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
