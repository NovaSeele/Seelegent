<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import Tabs, { type TabItem } from '@/components/ui/Tabs.vue'
import { readStored, writeStored, type StoredValue } from '@/lib/storage'
import { useGitHubStore } from '@/stores/github'

import AccessPanel from './AccessPanel.vue'
import ConnectRepoDialog from './ConnectRepoDialog.vue'
import IssueInbox from './IssueInbox.vue'
import RepoBar from './RepoBar.vue'
import TicketDetail from './TicketDetail.vue'
import TicketList from './TicketList.vue'

/**
 * Issues: how Seelegent reaches GitHub, the connected repositories, the chosen one's inbox (decide what to do with
 * each issue) and its tickets (an issue's worktree, tasks and pull request). With a ticket id, that ticket. The
 * route passes its parameter as a prop (props: true), so it follows the page on screen under the settings overlay.
 */
const props = defineProps<{ ticketId?: string }>()

type View = 'inbox' | 'tickets'
const LAST_VIEW: StoredValue<View> = {
  name: 'issues.view',
  fallback: 'inbox',
  validate: (v): v is View => v === 'inbox' || v === 'tickets',
}

const { t } = useI18n()
const gh = useGitHubStore()
const connecting = ref(false)
const stored = ref<View>(readStored(LAST_VIEW))

onMounted(() => gh.ensure())

const view = computed({
  get: () => stored.value,
  set: (v: View) => {
    stored.value = v
    writeStored(LAST_VIEW, v)
  },
})
const openTickets = computed(
  () =>
    gh.tickets.filter(
      (x) => x.repoId === gh.repoId && x.status !== 'done' && x.status !== 'dropped',
    ).length,
)
const undecided = computed(
  () => gh.issues.filter((i) => i.state === 'open' && !i.decision && !i.ticket).length,
)
const tabs = computed<TabItem<View>[]>(() => [
  { value: 'inbox', label: t('issues.tabs.inbox'), icon: 'issues', count: undecided.value },
  { value: 'tickets', label: t('issues.tabs.tickets'), icon: 'git', count: openTickets.value },
])
</script>

<template>
  <PageContainer>
    <TicketDetail v-if="props.ticketId" :key="props.ticketId" :ticket-id="props.ticketId" />
    <template v-else>
      <PageHeader :title="t('nav.issues.label')" :description="t('nav.issues.desc')">
        <template #actions>
          <Button variant="primary" icon="plus" @click="connecting = true">
            {{ t('issues.connect.open') }}
          </Button>
        </template>
      </PageHeader>
      <div class="pad">
        <AccessPanel />
        <Spinner
          v-if="gh.reposStatus === 'loading' && !gh.repos.length"
          :label="t('issues.repo.loading')"
        />
        <div v-else-if="gh.reposStatus === 'error'" class="pad__note" role="alert">
          {{ t('issues.repo.loadFailed', { error: gh.reposError }) }}
          <Button size="sm" variant="ghost" @click="gh.loadRepos()">{{
            t('common.action.retry')
          }}</Button>
        </div>
        <EmptyState
          v-else-if="!gh.repos.length"
          icon="issues"
          :title="t('issues.repo.empty')"
          :description="t('issues.repo.emptyDesc')"
        >
          <Button variant="primary" icon="plus" @click="connecting = true">
            {{ t('issues.connect.open') }}
          </Button>
        </EmptyState>
        <template v-else>
          <RepoBar />
          <Tabs
            v-model="view"
            :items="tabs"
            :label="t('issues.tabs.label')"
            id-prefix="issues-view"
          />
          <div
            :id="`issues-view-panel-${view}`"
            role="tabpanel"
            :aria-labelledby="`issues-view-tab-${view}`"
            tabindex="0"
            class="pad__panel"
          >
            <IssueInbox v-if="view === 'inbox'" />
            <TicketList v-else />
          </div>
        </template>
      </div>
      <ConnectRepoDialog v-model:open="connecting" />
    </template>
  </PageContainer>
</template>

<style scoped>
.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

@media (width < 600px) {
  .pad {
    padding-inline: var(--space-4);
  }
}

.pad__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}

.pad__panel {
  border-radius: var(--radius-sm);
}

.pad__panel:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}
</style>
