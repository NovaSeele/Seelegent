<script setup lang="ts">
import { computed, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import { useFormat } from '@/composables/useFormat'
import { useGitHubStore } from '@/stores/github'
import { useToastStore } from '@/stores/toast'

/**
 * The chosen repository: which one (when several are connected), where it is, whether its origin is on GitHub,
 * the last inbox sync, and the Sync and Disconnect actions. Disconnecting forgets the repository and its inbox;
 * its folder, branches and worktrees stay as they are.
 */
const { t } = useI18n()
const fmt = useFormat()
const gh = useGitHubStore()
const toast = useToastStore()

const syncing = ref(false)
const removing = ref(false)
const removeBusy = ref(false)
const removeError = ref('')

const options = computed<SelectOption<string>[]>(() =>
  gh.repos.map((r) => ({ value: r.id, label: r.owner ? `${r.owner}/${r.repo}` : r.name })),
)
const chosen = computed({
  get: () => gh.repoId,
  set: (id: string) => gh.select(id),
})
const repo = computed(() => gh.repo)

async function sync() {
  const r = repo.value
  if (!r || syncing.value) return
  syncing.value = true
  try {
    const res = await gh.api().syncRepo(r.id)
    toast.show({
      message: res.skipped
        ? t('issues.repo.syncSkipped', { reason: res.skipped })
        : t('issues.repo.synced', { added: res.added, updated: res.updated, closed: res.closed }),
      tone: res.skipped ? 'info' : 'success',
    })
    await Promise.all([gh.loadRepos(), gh.loadIssues()])
  } catch (cause) {
    toast.show({ message: errorMessage(cause), tone: 'error' })
  } finally {
    syncing.value = false
  }
}

async function disconnect() {
  const r = repo.value
  if (!r) return
  removeBusy.value = true
  removeError.value = ''
  try {
    await gh.disconnect(r.id)
    removing.value = false
  } catch (cause) {
    removeError.value = errorMessage(cause)
  } finally {
    removeBusy.value = false
  }
}
</script>

<template>
  <section v-if="repo" class="repo" :aria-label="t('issues.repo.label')">
    <div class="repo__top">
      <Select
        v-if="options.length > 1"
        v-model="chosen"
        class="repo__select"
        :label="t('issues.repo.choose')"
        :options="options"
        size="sm"
      />
      <h2 v-else class="repo__name">{{ options[0]?.label }}</h2>
      <div class="repo__actions">
        <Button size="sm" icon="refresh" :loading="syncing" @click="sync">
          {{ t('issues.repo.sync') }}
        </Button>
        <Button size="sm" variant="ghost" @click="removing = true">
          {{ t('issues.repo.disconnect') }}
        </Button>
      </div>
    </div>
    <div class="repo__meta">
      <code class="repo__path">{{ repo.path }}</code>
      <StatusPill
        size="sm"
        :tone="repo.github ? 'success' : 'muted'"
        :icon="repo.github ? 'git' : 'question'"
        :label="
          repo.github
            ? t('issues.repo.github', { host: repo.host ?? '' })
            : t('issues.repo.notGithub')
        "
      />
      <span v-if="repo.lastSyncAt">{{
        t('issues.repo.lastSync', { when: fmt.relative(repo.lastSyncAt) })
      }}</span>
      <span>{{ t('issues.repo.counts', { ...repo.counts }) }}</span>
    </div>
    <p v-if="!repo.github && repo.notGithub" class="repo__note">{{ repo.notGithub }}</p>
    <p v-if="repo.syncError" class="repo__error" role="alert">{{ repo.syncError }}</p>
    <ConfirmDialog
      :open="removing"
      :title="
        t('issues.repo.disconnectTitle', {
          name: options.find((o) => o.value === repo?.id)?.label ?? '',
        })
      "
      :description="t('issues.repo.disconnectDesc')"
      :confirm-label="t('issues.repo.disconnect')"
      danger
      :busy="removeBusy"
      :error="removeError"
      @update:open="removing = false"
      @confirm="disconnect"
    />
  </section>
</template>

<style scoped>
.repo {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
}

.repo__top {
  display: flex;
  flex-wrap: wrap;
  align-items: end;
  justify-content: space-between;
  gap: var(--space-2);
}

.repo__select {
  flex: 1 1 240px;
  max-width: 420px;
}

.repo__name {
  font-size: var(--fs-lg);
  font-weight: var(--fw-semibold);
  overflow-wrap: anywhere;
}

.repo__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.repo__meta {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-1) var(--space-3);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.repo__path {
  overflow-wrap: anywhere;
}

.repo__note {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.repo__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}
</style>
