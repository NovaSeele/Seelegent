<script setup lang="ts">
import { computed, useId } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Role } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import IconButton from '@/components/ui/IconButton.vue'
import { useAccountsStore } from '@/stores/accounts'

/**
 * One role: name and id, where it lives (global or a project; a project role can hide a global one with the same
 * id), its description, defaults and skills, and the accounts it prefers.
 */
const props = defineProps<{ role: Role; projectName: string }>()
const emit = defineEmits<{ edit: []; remove: [] }>()

const { t } = useI18n()
const accounts = useAccountsStore()
const id = useId()

const preferred = computed(() =>
  props.role.preferredAccounts.map((a) => accounts.get(a)?.label ?? a).join(', '),
)
</script>

<template>
  <article class="role" :aria-labelledby="`${id}-name`">
    <header class="role__head">
      <div class="role__title">
        <h3 :id="`${id}-name`" class="role__name">{{ role.name }}</h3>
        <code class="role__id">{{ role.id }}</code>
      </div>
      <div class="role__badges">
        <span class="badge tone-neutral">
          {{ role.scope === 'global' ? t('roles.scope.global') : projectName }}
        </span>
        <span v-if="role.overridesGlobal" class="badge tone-attention">
          {{ t('roles.overrides') }}
        </span>
      </div>
    </header>

    <p class="role__desc">{{ role.description || t('roles.noDescription') }}</p>

    <dl class="role__facts">
      <div v-if="role.defaultTier || role.defaultEffort">
        <dt>{{ t('roles.defaults') }}</dt>
        <dd>
          {{
            [
              role.defaultTier ? t(`roles.tier.${role.defaultTier}`) : '',
              role.defaultEffort ? t(`roles.effort.${role.defaultEffort}`) : '',
            ]
              .filter(Boolean)
              .join(' · ')
          }}
        </dd>
      </div>
      <div v-if="role.skills.length">
        <dt>{{ t('roles.skills.title') }}</dt>
        <dd class="role__skills">
          <span v-for="skill in role.skills" :key="skill" class="skill">{{ skill }}</span>
        </dd>
      </div>
      <div v-if="role.preferredAccounts.length">
        <dt>{{ t('roles.accounts.title') }}</dt>
        <dd>{{ preferred }}</dd>
      </div>
    </dl>

    <footer class="role__foot">
      <Button size="sm" variant="secondary" @click="emit('edit')">{{ t('roles.edit') }}</Button>
      <IconButton
        icon="x"
        size="sm"
        :label="t('roles.deleteOf', { name: role.name })"
        @click="emit('remove')"
      />
    </footer>
  </article>
</template>

<style scoped>
.role {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.role__head {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
}

.role__title {
  min-width: 0;
}

.role__name {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.role__id {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-2xs);
}

.role__badges {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
}

.badge,
.skill {
  padding: 0 var(--space-2);
  border-radius: var(--radius-pill);
  font-size: var(--fs-2xs);
  line-height: 20px;
}

.badge {
  background: var(--tone-bg);
  color: var(--tone-fg);
}

.skill {
  border: var(--border-width) solid var(--border-subtle);
  color: var(--text-secondary);
  font-family: var(--font-mono);
}

.role__desc {
  display: -webkit-box;
  overflow: hidden;
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  line-clamp: 3;
}

.role__facts {
  display: flex;
  margin: 0;
  flex-direction: column;
  gap: var(--space-2);
  font-size: var(--fs-xs);
}

.role__facts dd {
  margin-inline-start: 0;
}

.role__facts dt {
  color: var(--text-muted);
}

.role__skills {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-1);
  margin-top: var(--space-1);
}

.role__foot {
  display: flex;
  align-items: center;
  justify-content: space-between;
  margin-top: auto;
  padding-top: var(--space-3);
  border-top: var(--border-width) solid var(--border-subtle);
}
</style>
