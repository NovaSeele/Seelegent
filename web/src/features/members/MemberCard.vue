<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Member, MemberTrack } from '@/api/types'
import IconButton from '@/components/ui/IconButton.vue'
import Icon from '@/components/ui/Icon.vue'
import StatusPill from '@/components/ui/StatusPill.vue'
import Switch from '@/components/ui/Switch.vue'
import { useFormat } from '@/composables/useFormat'

import { RANK_TONE, trackFacts } from './memberModel'

/**
 * One member: name and id, rank, what it runs on, its role, an on/off switch and its track record. A member whose
 * provider is gone says why it cannot be used.
 */
const props = defineProps<{
  member: Member
  track?: MemberTrack
  /** The track records could not be read: say so instead of "none yet" (E-B11). */
  trackFailed?: boolean
  busy?: boolean
}>()
const emit = defineEmits<{ edit: []; remove: []; toggle: [enabled: boolean] }>()

const { t } = useI18n()
const fmt = useFormat()

const runsOn = computed(() => {
  const m = props.member
  const parts = [
    m.provider
      ? `${m.provider} (${t(`accounts.backend.${m.backend}`)})`
      : t(`accounts.backend.${m.backend}`),
  ]
  if (m.model) parts.push(m.model)
  else if (m.tier) parts.push(t(`roles.tier.${m.tier}`))
  return parts.join(' · ')
})
const facts = computed(() => trackFacts(props.track))
</script>

<template>
  <li class="member" :class="{ 'member--off': !member.enabled }">
    <div class="member__head">
      <div class="member__names">
        <h3 class="member__name">{{ member.name }}</h3>
        <code class="member__id">{{ member.id }}</code>
      </div>
      <StatusPill
        size="sm"
        :tone="RANK_TONE[member.rank]"
        :label="t(`team.rank.${member.rank}`)"
        :title="t(`team.rankHint.${member.rank}`)"
      />
      <StatusPill
        v-if="!member.enabled"
        size="sm"
        tone="muted"
        :label="t('team.members.turnedOff')"
      />
    </div>
    <dl class="member__facts">
      <div>
        <dt>{{ t('team.members.runsOn') }}</dt>
        <dd>{{ runsOn }}</dd>
      </div>
      <div v-if="member.role">
        <dt>{{ t('team.members.role') }}</dt>
        <dd>
          <code>{{ member.role }}</code>
        </dd>
      </div>
    </dl>
    <p v-if="member.invalid" class="member__invalid tone-danger" role="note">
      <Icon name="warning" />
      {{ t('team.members.invalid', { reason: member.invalid }) }}
    </p>
    <div class="member__track">
      <span class="member__track-title">{{ t('team.members.track.title') }}</span>
      <!-- The page says the records could not be read; the card only shows a dash (the words stay for screen readers). -->
      <span v-if="trackFailed && !facts.length" class="member__muted"
        ><span aria-hidden="true">—</span
        ><span class="sr-only">{{ t('team.members.track.failed') }}</span></span
      >
      <span v-else-if="!facts.length" class="member__muted">{{
        t('team.members.track.none')
      }}</span>
      <span v-for="f in facts" :key="f.key" class="member__fact">
        {{ t(`team.members.track.${f.key}`) }} <strong>{{ fmt.number(f.value) }}</strong>
      </span>
    </div>
    <div class="member__foot">
      <Switch
        :model-value="member.enabled"
        :label="t('team.members.enabled')"
        :aria-label="t('team.members.enabledFor', { name: member.name })"
        :disabled="busy"
        @update:model-value="emit('toggle', $event)"
      />
      <span class="member__spacer" />
      <IconButton
        icon="settings"
        size="sm"
        :label="t('team.members.edit', { name: member.name })"
        @click="emit('edit')"
      />
      <IconButton
        icon="trash"
        size="sm"
        :label="t('team.members.delete', { name: member.name })"
        @click="emit('remove')"
      />
    </div>
  </li>
</template>

<style scoped>
.member {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  min-width: 0;
  padding: var(--space-4);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-lg);
  background: var(--bg-surface);
}

/* Turned off reads from the pill, the name and the switch; the card keeps its surface so the pills on it keep
   their contrast (a muted pill on --bg-surface-2 fell to 4.2:1). */
.member--off {
  border-style: dashed;
}

.member--off .member__name {
  color: var(--text-secondary);
}

.member__head {
  display: flex;
  align-items: flex-start;
  justify-content: space-between;
  gap: var(--space-2);
}

.member__names {
  display: flex;
  flex-direction: column;
  min-width: 0;
}

.member__name {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
  overflow-wrap: anywhere;
}

.member__id {
  color: var(--text-muted);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
}

.member__facts {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
  margin: 0;
  font-size: var(--fs-sm);
}

.member__facts div {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.member__facts dt {
  min-width: 72px;
  color: var(--text-secondary);
}

.member__facts dd {
  margin: 0;
  overflow-wrap: anywhere;
}

.member__invalid {
  display: flex;
  align-items: center;
  gap: var(--space-1);
  padding: var(--space-1) var(--space-2);
  border-radius: var(--radius-sm);
  background: var(--tone-bg);
  color: var(--tone-fg);
  font-size: var(--fs-xs);
}

.member__track {
  display: flex;
  flex-wrap: wrap;
  align-items: baseline;
  gap: var(--space-1) var(--space-3);
  font-size: var(--fs-xs);
}

.member__track-title {
  color: var(--text-secondary);
  font-weight: var(--fw-medium);
}

.member__muted {
  color: var(--text-muted);
}

.member__fact strong {
  font-variant-numeric: tabular-nums;
}

.member__foot {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
}

.member__spacer {
  flex: 1;
}
</style>
