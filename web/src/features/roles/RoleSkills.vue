<script setup lang="ts">
import { computed, ref, shallowRef, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Skill } from '@/api/types'
import Checkbox from '@/components/ui/Checkbox.vue'
import Chip from '@/components/ui/Chip.vue'
import Spinner from '@/components/ui/Spinner.vue'
import TextField from '@/components/ui/TextField.vue'
import { useRolesStore } from '@/stores/roles'

import { LIMITS } from './rolesModel'

/**
 * The skills a role loads, picked from those installed where the role works (the project's own skills plus the
 * user's). A chosen skill that is not installed there is listed as missing, with a warning, so it can be removed.
 */
const props = defineProps<{ cwd: string }>()
const selected = defineModel<string[]>({ required: true })

const { t } = useI18n()
const roles = useRolesStore()
const available = shallowRef<readonly Skill[]>([])
const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')
const query = ref('')

watch(
  () => props.cwd,
  async (cwd) => {
    status.value = 'loading'
    try {
      available.value = await roles.loadSkills(cwd)
      status.value = 'ready'
    } catch (cause) {
      error.value = errorMessage(cause)
      status.value = 'error'
    }
  },
  { immediate: true },
)

const missing = computed(() =>
  status.value === 'ready'
    ? selected.value.filter((name) => !available.value.some((s) => s.name === name))
    : [],
)

const shown = computed(() => {
  const q = query.value.trim().toLowerCase()
  return q
    ? available.value.filter((s) => `${s.name} ${s.description}`.toLowerCase().includes(q))
    : available.value
})

const full = computed(() => selected.value.length >= LIMITS.skills)

function toggle(name: string, on: boolean) {
  selected.value = on ? [...selected.value, name] : selected.value.filter((s) => s !== name)
}
</script>

<template>
  <fieldset class="skills">
    <legend class="skills__legend">
      {{ t('roles.skills.title') }}
      <span class="skills__count">{{ selected.length }} / {{ LIMITS.skills }}</span>
    </legend>
    <div v-if="missing.length" class="skills__missing tone-attention" role="status">
      <p>{{ t('roles.skills.missing', { n: missing.length }, missing.length) }}</p>
      <div class="skills__chips">
        <Chip
          v-for="name in missing"
          :key="name"
          tone="attention"
          removable
          @remove="toggle(name, false)"
        >
          {{ name }}
        </Chip>
      </div>
    </div>
    <Spinner v-if="status === 'loading'" size="sm" :label="t('roles.skills.loading')" />
    <p v-else-if="status === 'error'" class="skills__note">
      {{ t('roles.skills.loadFailed', { error }) }}
    </p>
    <p v-else-if="!available.length" class="skills__note">{{ t('roles.skills.none') }}</p>
    <template v-else>
      <TextField
        v-if="available.length > 8"
        v-model="query"
        type="search"
        icon="search"
        size="sm"
        hide-label
        :label="t('roles.skills.search')"
        :placeholder="t('roles.skills.search')"
      />
      <div class="skills__list">
        <Checkbox
          v-for="skill in shown"
          :key="skill.name"
          :model-value="selected.includes(skill.name)"
          :label="skill.name"
          :description="`${t(`roles.skills.root.${skill.root}`)} · ${skill.description}`"
          :disabled="full && !selected.includes(skill.name)"
          @update:model-value="toggle(skill.name, $event)"
        />
      </div>
    </template>
  </fieldset>
</template>

<style scoped>
.skills {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  margin: 0;
  padding: 0;
  border: 0;
}

.skills__legend {
  display: flex;
  gap: var(--space-2);
  margin-bottom: var(--space-2);
  padding: 0;
  color: var(--text-secondary);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
}

.skills__count {
  color: var(--text-muted);
  font-variant-numeric: tabular-nums;
}

.skills__missing {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-md);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-xs);
}

.skills__chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.skills__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.skills__list {
  position: relative;
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 240px), 1fr));
  gap: var(--space-2);
  max-height: 260px;
  overflow: auto;
}
</style>
