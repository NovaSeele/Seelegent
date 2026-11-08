<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Role } from '@/api/types'
import Dialog from '@/components/ui/Dialog.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useRolesStore } from '@/stores/roles'

/** Starting point of a new role: one of the daemon's templates, or a blank role. */
const open = defineModel<boolean>('open', { required: true })
const emit = defineEmits<{ pick: [template: Role | undefined] }>()

const { t } = useI18n()
const roles = useRolesStore()
const status = ref<'loading' | 'ready' | 'error'>('loading')
const error = ref('')

onMounted(async () => {
  try {
    await roles.loadTemplates()
    status.value = 'ready'
  } catch (cause) {
    error.value = errorMessage(cause)
    status.value = 'error'
  }
})

function pick(template: Role | undefined) {
  open.value = false
  emit('pick', template)
}
</script>

<template>
  <Dialog
    v-model:open="open"
    :title="t('roles.templates.title')"
    :description="t('roles.templates.desc')"
  >
    <ul class="templates">
      <li>
        <button type="button" class="template" @click="pick(undefined)">
          <span class="template__name">{{ t('roles.templates.blank') }}</span>
          <span class="template__desc">{{ t('roles.templates.blankDesc') }}</span>
        </button>
      </li>
      <li v-for="template in roles.templates" :key="template.id">
        <button type="button" class="template" @click="pick(template)">
          <span class="template__name">{{ template.name }}</span>
          <span class="template__desc">{{ template.description }}</span>
        </button>
      </li>
    </ul>
    <Spinner v-if="status === 'loading'" size="sm" :label="t('roles.templates.loading')" />
    <p v-else-if="status === 'error'" class="templates__error">
      {{ t('roles.templates.loadFailed', { error }) }}
    </p>
  </Dialog>
</template>

<style scoped>
.templates {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 220px), 1fr));
  gap: var(--space-2);
  margin: 0;
  padding: 0;
  list-style: none;
}

.template {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  width: 100%;
  height: 100%;
  padding: var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--text-primary);
  font: inherit;
  text-align: left;
  cursor: pointer;
}

.template:hover {
  border-color: var(--text-muted);
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.template:focus-visible {
  outline: 2px solid var(--border-focus);
  outline-offset: 2px;
}

.template__name {
  font-size: var(--fs-sm);
  font-weight: var(--fw-semibold);
}

.template__desc {
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.templates__error {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
