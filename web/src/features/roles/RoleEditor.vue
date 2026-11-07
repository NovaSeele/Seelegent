<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage, fieldProblems } from '@/api/client'
import type { Role, RoleScope } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import SegmentedControl from '@/components/ui/SegmentedControl.vue'
import Select from '@/components/ui/Select.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useAccountsStore } from '@/stores/accounts'
import { samePlace, useRolesStore, type RolePlace } from '@/stores/roles'
import { useToastStore } from '@/stores/toast'

import RoleSkills from './RoleSkills.vue'
import { draftOf, EFFORTS, fieldsOf, LIMITS, ROLE_ID, slugify, TIERS } from './rolesModel'

export interface EditorTarget {
  /** The role being edited; absent for a new one. */
  role?: Role
  /** A template (or role) to start a new one from. */
  from?: Role
  scope?: RoleScope
  cwd?: string
}

/**
 * Creates or edits a role. The id follows the name until it is typed by hand. Changing the scope, the project or
 * the id of an existing role moves it (write, then delete the old one; see the roles store). Saving onto an id
 * that another role already has in that place is refused here, since the daemon would silently replace it.
 */
const props = defineProps<{ target: EditorTarget | null }>()
const emit = defineEmits<{ close: [] }>()

const { t } = useI18n()
const roles = useRolesStore()
const accounts = useAccountsStore()
const toast = useToastStore()

const draft = ref(draftOf(undefined))
const idTouched = ref(false)
const busy = ref(false)
const errors = ref<Record<string, string>>({})
const failure = ref('')

const editing = computed(() => !!props.target?.role)
const original = computed<RolePlace | undefined>(() => props.target?.role)

watch(
  () => props.target,
  (target) => {
    if (!target) return
    const base = target.role ?? target.from
    draft.value = draftOf(base, target.scope, target.cwd)
    if (!target.role) {
      draft.value.scope = target.scope ?? 'global'
      draft.value.cwd = target.cwd ?? ''
    }
    idTouched.value = !!target.role || !!base?.id
    errors.value = {}
    failure.value = ''
  },
)

watch(
  () => draft.value.name,
  (name) => {
    if (!idTouched.value) draft.value.id = slugify(name)
  },
)

const scopes = computed(() =>
  (['global', 'project'] as const).map((value) => ({ value, label: t(`roles.scope.${value}`) })),
)
const projectOptions = computed(() =>
  roles.projects.map((p) => ({ value: p.cwd, label: `${p.name} — ${p.cwd}` })),
)
const tierOptions = computed(() => [
  { value: '' as const, label: t('roles.none') },
  ...TIERS.map((value) => ({ value, label: t(`roles.tier.${value}`) })),
])
const effortOptions = computed(() => [
  { value: '' as const, label: t('roles.none') },
  ...EFFORTS.map((value) => ({ value, label: t(`roles.effort.${value}`) })),
])

watch(
  () => draft.value.scope,
  (scope) => {
    if (scope === 'project' && !draft.value.cwd) draft.value.cwd = roles.projects[0]?.cwd ?? ''
  },
)

function toggleAccount(id: string, on: boolean) {
  const list = draft.value.preferredAccounts
  draft.value.preferredAccounts = on ? [...list, id] : list.filter((a) => a !== id)
}

function check(): boolean {
  const d = draft.value
  const next: Record<string, string> = {}
  if (!d.name.trim()) next.name = t('roles.errors.name')
  if (!ROLE_ID.test(d.id)) next.id = t('roles.errors.id')
  if (d.scope === 'project' && !d.cwd) next.cwd = t('roles.errors.project')
  const to: RolePlace = { id: d.id, scope: d.scope, cwd: d.cwd }
  const taken = roles.find(to)
  if (!next.id && taken && !(original.value && samePlace(original.value, to))) {
    next.id = t('roles.errors.taken')
  }
  errors.value = next
  return Object.keys(next).length === 0
}

async function save() {
  if (busy.value || !check()) return
  busy.value = true
  failure.value = ''
  const d = draft.value
  try {
    await roles.save(
      { role: fieldsOf(d), scope: d.scope, ...(d.scope === 'project' ? { cwd: d.cwd } : {}) },
      original.value,
    )
    toast.show({ message: t('roles.saved', { name: d.name.trim() }), tone: 'success' })
    emit('close')
  } catch (error) {
    const problems = fieldProblems(error)
    errors.value = Object.fromEntries(
      problems.map((p) => [p.field.replace(/^role\./, ''), p.message]),
    )
    if (!problems.length) failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="target !== null"
    size="lg"
    :title="editing ? t('roles.editTitle') : t('roles.newTitle')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="emit('close')"
  >
    <form id="role-editor" class="editor" @submit.prevent="save">
      <div class="editor__row">
        <TextField v-model="draft.name" :label="t('roles.field.name')" :error="errors.name" />
        <TextField
          v-model="draft.id"
          :label="t('roles.field.id')"
          :hint="t('roles.field.idHint')"
          :error="errors.id"
          @input="idTouched = true"
        />
      </div>
      <div class="editor__row">
        <SegmentedControl v-model="draft.scope" :options="scopes" :label="t('roles.field.scope')" />
        <Select
          v-if="draft.scope === 'project'"
          v-model="draft.cwd"
          :label="t('roles.field.project')"
          :options="projectOptions"
        />
      </div>
      <p v-if="errors.cwd" class="editor__error">{{ errors.cwd }}</p>
      <TextField
        v-model="draft.description"
        :label="t('roles.field.description')"
        :error="errors.description"
      />
      <TextArea
        v-model="draft.rules"
        :label="t('roles.field.rules')"
        :hint="t('roles.field.rulesHint')"
        :maxlength="LIMITS.rules"
        :rows="7"
        :error="errors.rules"
      />
      <TextArea
        v-model="draft.outputStyle"
        :label="t('roles.field.outputStyle')"
        :hint="t('roles.field.outputStyleHint')"
        :maxlength="LIMITS.outputStyle"
        :rows="3"
        :error="errors.outputStyle"
      />
      <div class="editor__row">
        <Select v-model="draft.defaultTier" :label="t('roles.field.tier')" :options="tierOptions" />
        <Select
          v-model="draft.defaultEffort"
          :label="t('roles.field.effort')"
          :options="effortOptions"
        />
      </div>
      <RoleSkills v-model="draft.skills" :cwd="draft.scope === 'project' ? draft.cwd : ''" />
      <fieldset class="editor__accounts">
        <legend class="editor__legend">{{ t('roles.accounts.title') }}</legend>
        <p class="editor__hint">{{ t('roles.accounts.hint') }}</p>
        <div class="editor__grid">
          <Checkbox
            v-for="account in accounts.accounts"
            :key="account.id"
            :model-value="draft.preferredAccounts.includes(account.id)"
            :label="account.label"
            :description="t(`accounts.backend.${account.backend}`)"
            @update:model-value="toggleAccount(account.id, $event)"
          />
        </div>
      </fieldset>
      <p v-if="failure" class="editor__error" role="alert">{{ failure }}</p>
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="emit('close')">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="role-editor" variant="primary" :loading="busy">
        {{ t('roles.save') }}
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
  grid-template-columns: repeat(auto-fit, minmax(min(100%, 220px), 1fr));
  align-items: end;
  gap: var(--space-3);
}

.editor__accounts {
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
  margin-bottom: var(--space-2);
  color: var(--text-muted);
  font-size: var(--fs-xs);
}

.editor__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 200px), 1fr));
  gap: var(--space-2);
}

.editor__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
