<script setup lang="ts">
import { computed, onMounted, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Role } from '@/api/types'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import Chip from '@/components/ui/Chip.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { pathKey } from '@/lib/paths'
import { useRolesStore } from '@/stores/roles'
import { useToastStore } from '@/stores/toast'

import RoleCard from './RoleCard.vue'
import RoleEditor, { type EditorTarget } from './RoleEditor.vue'
import { filterRoles, sortRoles, type ScopeFilter } from './rolesModel'
import TemplatePicker from './TemplatePicker.vue'

/**
 * Worker roles: the rules, report style, skills and defaults a worker gets when a task names the role. Global
 * roles apply everywhere; a project role applies in its folder and hides a global role with the same id. Filter by
 * scope, start new roles from templates, edit, move between scopes and delete. Live over roles.changed.
 */
const { t } = useI18n()
const roles = useRolesStore()
const toast = useToastStore()

const filter = ref<ScopeFilter>('')
const picking = ref(false)
const target = ref<EditorTarget | null>(null)
const deleting = ref<Role | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')

onMounted(() => roles.ensure())

/** Installed skill names per place ('' = global), read for the places whose roles name skills. */
const installed = shallowRef<Readonly<Record<string, ReadonlySet<string>>>>({})
const placeOf = (role: Role) => (role.scope === 'global' ? '' : (role.cwd ?? ''))

watch(
  () => roles.items,
  async (items) => {
    const places = new Set(items.filter((r) => r.skills.length).map(placeOf))
    for (const place of places) {
      if (place in installed.value) continue
      try {
        const list = await roles.loadSkills(place)
        installed.value = { ...installed.value, [place]: new Set(list.map((s) => s.name)) }
      } catch {
        // Unknown: the card marks no skill as missing.
      }
    }
  },
  { immediate: true },
)

const projectName = (cwd: string | undefined) =>
  roles.projects.find((p) => cwd !== undefined && pathKey(p.cwd) === pathKey(cwd))?.name ??
  cwd ??
  ''

const chips = computed(() => [
  { value: '', label: t('roles.filter.all'), count: roles.items.length },
  {
    value: 'global',
    label: t('roles.scope.global'),
    count: roles.items.filter((r) => r.scope === 'global').length,
  },
  ...roles.projects
    .filter((p) => p.count > 0)
    .map((p) => ({ value: p.cwd, label: p.name, count: p.count })),
])

const shown = computed(() => sortRoles(filterRoles(roles.items, filter.value)))

/** Global role id → the projects whose own role with that id overrides it. */
const overrides = computed(() => {
  const out = new Map<string, string[]>()
  for (const role of roles.items) {
    if (role.scope !== 'project' || !role.overridesGlobal) continue
    out.set(role.id, [...(out.get(role.id) ?? []), projectName(role.cwd)])
  }
  return out
})

function startNew(template: Role | undefined) {
  const project = filter.value && filter.value !== 'global' ? filter.value : undefined
  target.value = {
    from: template,
    scope: project ? 'project' : 'global',
    cwd: project,
  }
}

function askDelete(role: Role) {
  deleteError.value = ''
  deleting.value = role
}

async function remove() {
  const role = deleting.value
  if (!role) return
  deleteBusy.value = true
  try {
    await roles.remove(role)
    toast.show({ message: t('roles.deleted', { name: role.name }), tone: 'success' })
    deleting.value = null
  } catch (error) {
    deleteError.value = errorMessage(error)
  } finally {
    deleteBusy.value = false
  }
}
</script>

<template>
  <PageContainer>
    <PageHeader :title="t('nav.roles.label')" :description="t('nav.roles.desc')">
      <template #actions>
        <Button variant="primary" icon="plus" @click="picking = true">{{ t('roles.new') }}</Button>
      </template>
    </PageHeader>

    <div class="roles">
      <div
        v-if="roles.items.length"
        class="roles__filter"
        role="group"
        :aria-label="t('roles.filter.label')"
      >
        <Chip
          v-for="chip in chips"
          :key="chip.value"
          :selected="filter === chip.value"
          @toggle="filter = chip.value"
        >
          {{ chip.label }} · {{ chip.count }}
        </Chip>
      </div>

      <div v-if="roles.status === 'loading'" class="roles__loading">
        <Spinner :label="t('roles.loading')" />
      </div>
      <EmptyState
        v-else-if="roles.status === 'error'"
        icon="warning"
        :title="t('roles.loadFailed')"
        :description="roles.error ?? undefined"
      >
        <Button variant="secondary" @click="roles.load()">{{ t('common.action.retry') }}</Button>
      </EmptyState>
      <EmptyState
        v-else-if="!roles.items.length"
        icon="roles"
        :title="t('roles.empty.title')"
        :description="t('roles.empty.desc')"
      >
        <Button variant="primary" icon="plus" @click="picking = true">{{ t('roles.new') }}</Button>
      </EmptyState>
      <template v-else>
        <p v-if="roles.error" class="roles__stale" role="status">
          {{ t('roles.stale', { error: roles.error }) }}
        </p>
        <div class="roles__grid">
          <RoleCard
            v-for="role in shown"
            :key="`${role.scope}:${role.cwd ?? ''}:${role.id}`"
            :role="role"
            :project-name="projectName(role.cwd)"
            :installed="installed[placeOf(role)] ?? null"
            :overridden-in="role.scope === 'global' ? overrides.get(role.id) : undefined"
            @edit="target = { role }"
            @remove="askDelete(role)"
          />
        </div>
      </template>
    </div>

    <TemplatePicker v-if="picking" v-model:open="picking" @pick="startNew" />
    <RoleEditor :target="target" @close="target = null" />
    <ConfirmDialog
      :open="deleting !== null"
      :title="t('roles.deleteTitle', { name: deleting?.name ?? '' })"
      :description="
        deleting?.scope === 'project'
          ? t('roles.deleteProjectDesc', { project: projectName(deleting.cwd) })
          : t('roles.deleteGlobalDesc')
      "
      :confirm-label="t('roles.deleteConfirm')"
      danger
      :busy="deleteBusy"
      :error="deleteError"
      @update:open="deleting = null"
      @confirm="remove"
    />
  </PageContainer>
</template>

<style scoped>
.roles {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

.roles__filter {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.roles__loading {
  display: flex;
  justify-content: center;
  padding: var(--space-8) 0;
}

.roles__stale {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.roles__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  gap: var(--space-3);
}

@media (width < 600px) {
  .roles {
    padding: 0 var(--space-4) var(--space-6);
  }
}
</style>
