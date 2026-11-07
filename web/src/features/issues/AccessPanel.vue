<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import TextField from '@/components/ui/TextField.vue'
import { useGitHubStore } from '@/stores/github'
import { useToastStore } from '@/stores/toast'

import { TIER_TONE } from './issueModel'

/**
 * How Seelegent reaches GitHub: the tier (gh, a stored token, or manual), the hosts that count as GitHub, and the
 * token form. The token is sent once and never shown again: the daemon keeps it in a private file and answers
 * only its last four characters. These calls are dashboard-only (no MCP tool or command can make them).
 */
const { t } = useI18n()
const gh = useGitHubStore()
const toast = useToastStore()

const token = ref('')
const busy = ref(false)
const failure = ref('')
const removing = ref(false)

const access = computed(() => gh.access)
const tier = computed(() => access.value?.tier ?? 'manual')

async function save() {
  if (!token.value.trim() || busy.value) return
  busy.value = true
  failure.value = ''
  try {
    await gh.api().saveToken(token.value.trim())
    token.value = ''
    await gh.refreshAccess()
    toast.show({ message: t('issues.access.saved'), tone: 'success' })
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function remove() {
  busy.value = true
  failure.value = ''
  try {
    await gh.api().removeToken()
    removing.value = false
    await gh.refreshAccess()
  } catch (error) {
    failure.value = errorMessage(error)
  } finally {
    busy.value = false
  }
}

async function refresh() {
  try {
    await gh.refreshAccess()
  } catch (error) {
    failure.value = errorMessage(error)
  }
}
</script>

<template>
  <section class="acc" :aria-label="t('issues.access.title')">
    <div class="acc__head">
      <h2 class="acc__title">{{ t('issues.access.title') }}</h2>
      <StatusPill
        size="sm"
        :tone="TIER_TONE[tier] ?? 'neutral'"
        :icon="tier === 'manual' ? 'warning' : 'success'"
        :label="t(`issues.access.tier.${tier}`)"
      />
      <Button size="sm" variant="ghost" icon="refresh" @click="refresh">
        {{ t('issues.access.check') }}
      </Button>
    </div>
    <p class="acc__desc">{{ t(`issues.access.tierDesc.${tier}`) }}</p>
    <p v-if="access?.ghUser" class="acc__fact">
      {{ t('issues.access.ghUser', { user: access.ghUser, version: access.ghVersion ?? '' }) }}
    </p>
    <p class="acc__fact">
      {{ t('issues.access.hosts', { hosts: (access?.hosts ?? ['github.com']).join(', ') }) }}
      <RouterLink class="acc__link" :to="{ name: 'settings', params: { section: 'system' } }">
        {{ t('issues.access.hostsEdit') }}
      </RouterLink>
    </p>
    <form class="acc__form" autocomplete="off" @submit.prevent="save">
      <TextField
        v-model="token"
        type="password"
        autocomplete="new-password"
        :label="access?.tokenHint ? t('issues.access.replace') : t('issues.access.token')"
        :hint="
          access?.tokenHint
            ? t('issues.access.stored', { hint: access.tokenHint })
            : t('issues.access.tokenHint')
        "
        :error="failure || undefined"
      />
      <div class="acc__actions">
        <Button type="submit" variant="primary" size="sm" :loading="busy" :disabled="!token.trim()">
          {{ t('issues.access.save') }}
        </Button>
        <Button
          v-if="access?.tokenHint"
          variant="ghost"
          size="sm"
          :disabled="busy"
          @click="removing = true"
        >
          {{ t('issues.access.remove') }}
        </Button>
      </div>
    </form>
    <ConfirmDialog
      v-model:open="removing"
      :title="t('issues.access.removeTitle')"
      :description="t('issues.access.removeDesc')"
      :confirm-label="t('issues.access.remove')"
      danger
      :busy="busy"
      @confirm="remove"
    />
  </section>
</template>

<style scoped>
.acc {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

.acc__head {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.acc__title {
  margin-right: auto;
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}

.acc__desc,
.acc__fact {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.acc__link {
  margin-left: var(--space-2);
  color: var(--link);
}

.acc__form {
  display: flex;
  flex-wrap: wrap;
  align-items: flex-end;
  gap: var(--space-2) var(--space-3);
  margin-top: var(--space-2);
}

.acc__form > :first-child {
  flex: 1 1 260px;
  min-width: 0;
}

.acc__actions {
  display: flex;
  gap: var(--space-2);
  padding-bottom: var(--space-1);
}
</style>
