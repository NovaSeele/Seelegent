<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import type { Member } from '@/api/types'
import PageContainer from '@/components/layout/PageContainer.vue'
import PageHeader from '@/components/layout/PageHeader.vue'
import Button from '@/components/ui/Button.vue'
import ConfirmDialog from '@/components/ui/ConfirmDialog.vue'
import EmptyState from '@/components/ui/EmptyState.vue'
import Spinner from '@/components/ui/Spinner.vue'
import { useTeamStore } from '@/stores/team'
import { useToastStore } from '@/stores/toast'

import MemberCard from './MemberCard.vue'
import MemberEditor from './MemberEditor.vue'
import { sortMembers } from './memberModel'

/**
 * Department members: named workers with a backend, a model or tier, a rank and a role. The lead gives them tasks
 * (delegate with member), invites them to meetings and consults them on finished tasks. Live over
 * members.changed; the track records follow the meetings and consults.
 */
const { t } = useI18n()
const team = useTeamStore()
const toast = useToastStore()

const target = ref<Member | 'new' | null>(null)
const deleting = ref<Member | null>(null)
const deleteBusy = ref(false)
const deleteError = ref('')
const toggling = ref<string | null>(null)

onMounted(() => team.ensureMembers())

const shown = computed(() => sortMembers(team.members))

async function toggle(member: Member, enabled: boolean) {
  toggling.value = member.id
  try {
    await team.patchMember(member.id, { enabled })
  } catch (error) {
    toast.show({
      message: t('team.members.changeFailed', { name: member.name, error: errorMessage(error) }),
      tone: 'error',
    })
  } finally {
    toggling.value = null
  }
}

function askDelete(member: Member) {
  deleteError.value = ''
  deleting.value = member
}

async function remove() {
  const member = deleting.value
  if (!member) return
  deleteBusy.value = true
  try {
    await team.removeMember(member.id)
    toast.show({ message: t('team.members.deleted', { name: member.name }), tone: 'success' })
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
    <PageHeader :title="t('nav.members.label')" :description="t('nav.members.desc')">
      <template #actions>
        <Button variant="primary" icon="plus" @click="target = 'new'">
          {{ t('team.members.new') }}
        </Button>
      </template>
    </PageHeader>

    <div class="pad">
      <Spinner v-if="team.membersStatus === 'loading'" :label="t('team.members.loading')" />
      <div v-else-if="team.membersStatus === 'error'" class="members__note" role="alert">
        {{ t('team.members.loadFailed', { error: team.membersError }) }}
        <Button size="sm" variant="ghost" @click="team.loadMembers()">
          {{ t('common.action.retry') }}
        </Button>
      </div>
      <EmptyState
        v-else-if="!shown.length"
        icon="members"
        :title="t('team.members.empty')"
        :description="t('team.members.emptyDesc')"
      />
      <template v-else>
        <!-- The members are there but their track records are not: say so, never "none yet" (E-B11). -->
        <div v-if="team.trackError" class="members__note" role="alert">
          {{ t('team.members.trackFailed', { error: team.trackError }) }}
          <Button size="sm" variant="ghost" @click="team.loadMembers()">
            {{ t('common.action.retry') }}
          </Button>
        </div>
        <ul class="members__grid">
          <MemberCard
            v-for="member in shown"
            :key="member.id"
            :member="member"
            :track="team.track[member.id]"
            :track-failed="team.trackError !== null"
            :busy="toggling === member.id"
            @edit="target = member"
            @remove="askDelete(member)"
            @toggle="toggle(member, $event)"
          />
        </ul>
      </template>
    </div>

    <MemberEditor :target="target" @close="target = null" />
    <ConfirmDialog
      :open="deleting !== null"
      :title="t('team.members.deleteTitle', { name: deleting?.name ?? '' })"
      :description="t('team.members.deleteDesc')"
      :confirm-label="t('common.action.remove')"
      danger
      :busy="deleteBusy"
      :error="deleteError"
      @update:open="deleting = null"
      @confirm="remove"
    />
  </PageContainer>
</template>

<style scoped>
.pad {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
  padding: 0 var(--space-6) var(--space-7);
}

.pad--top {
  padding-top: var(--space-4);
  padding-bottom: 0;
}

@media (width < 600px) {
  .pad {
    padding-inline: var(--space-4);
  }
}

.members__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(min(100%, 300px), 1fr));
  gap: var(--space-3);
  margin: 0;
  padding: 0;
  list-style: none;
}

.members__note {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
