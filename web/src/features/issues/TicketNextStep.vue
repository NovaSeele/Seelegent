<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Ticket } from '@/api/types'
import Button from '@/components/ui/Button.vue'
import Icon from '@/components/ui/Icon.vue'
import { useFormat } from '@/composables/useFormat'

import { failingChecks, safeUrl, ticketPhase } from './issueModel'
import FindingList from './FindingList.vue'
import { findingView, refusalView, type Words } from './ticketText'

/**
 * What the ticket waits for and the one thing to do next: review the task, open the pull request, open it on
 * GitHub by hand, fix a refused push, fix the failing checks, or start a failed ticket again. A branch the earlier
 * version pushed with secrets in it is a separate warning. Shown only while there is something to do.
 */
const props = defineProps<{ ticket: Ticket }>()
const emit = defineEmits<{ act: [action: 'pr' | 'link' | 'retry' | 'fixChecks'] }>()

const { t } = useI18n()
const fmt = useFormat()

const phase = computed(() => ticketPhase(props.ticket))
const say = (w: Words) => t(w.key, w.params, w.n ?? 1)
const megabytes = (bytes: number) =>
  fmt.value.number(bytes / 1_048_576, {
    style: 'unit',
    unit: 'megabyte',
    maximumFractionDigits: 1,
  })

const refusal = computed(() =>
  phase.value === 'push_refused' && props.ticket.refusal
    ? refusalView(props.ticket.refusal, megabytes)
    : null,
)
const exposed = computed(() => (props.ticket.exposed ?? []).map((f) => findingView(f, megabytes)))
const manualUrl = computed(() => safeUrl(props.ticket.pr?.manualUrl))
const failing = computed(() => failingChecks(props.ticket.checks))
const fixing = computed(() => props.ticket.checksFix?.taskId ?? '')
/** The task that waits for a verdict: the newest finished one without one. */
const toReview = computed(
  () => [...props.ticket.tasks].reverse().find((x) => x.status === 'done' && !x.verdict)?.id ?? '',
)
const shown = computed(() =>
  ['push_refused', 'open_pr', 'checks_failed', 'review', 'ready', 'failed'].includes(phase.value),
)
</script>

<template>
  <section v-if="exposed.length" class="banner tone-attention" role="status">
    <Icon name="warning" class="banner__icon" />
    <div class="banner__body">
      <p class="banner__title">
        <strong>{{
          t('issues.next.exposed.title', { count: exposed.length }, exposed.length)
        }}</strong>
      </p>
      <p class="banner__text">{{ t('issues.next.exposed.why') }}</p>
      <FindingList :findings="exposed" />
      <p class="banner__text banner__next">{{ t('issues.next.exposed.next') }}</p>
    </div>
  </section>

  <section
    v-if="shown"
    class="banner"
    :class="phase === 'failed' ? 'tone-danger' : 'tone-attention'"
    role="status"
  >
    <Icon :name="phase === 'failed' ? 'statusFailed' : 'warning'" class="banner__icon" />
    <div class="banner__body">
      <template v-if="refusal">
        <p class="banner__title">
          <strong>{{ say(refusal.title) }}</strong>
        </p>
        <p v-if="refusal.why" class="banner__text">{{ say(refusal.why) }}</p>
        <FindingList v-if="refusal.findings.length" :findings="refusal.findings" />
        <p v-if="refusal.detail" class="banner__text">
          {{ t('issues.refusal.gitSaid') }} <code>{{ refusal.detail }}</code>
        </p>
        <p class="banner__text banner__next">{{ say(refusal.next) }}</p>
        <div class="banner__actions">
          <Button size="sm" variant="primary" icon="refresh" @click="emit('act', 'pr')">
            {{ t('issues.next.tryAgain') }}
          </Button>
        </div>
      </template>

      <template v-else-if="phase === 'open_pr'">
        <p class="banner__title">
          <strong>{{ t('issues.next.openPr.title') }}</strong>
        </p>
        <p class="banner__text">{{ t('issues.next.openPr.why') }}</p>
        <div class="banner__actions">
          <Button v-if="manualUrl" size="sm" variant="primary" icon="external" :href="manualUrl">
            {{ t('issues.next.openPr.action') }}
          </Button>
          <Button size="sm" @click="emit('act', 'link')">{{ t('issues.actions.link') }}</Button>
        </div>
      </template>

      <template v-else-if="phase === 'checks_failed'">
        <p class="banner__title">
          <strong>{{
            t('issues.next.checks.title', { count: failing.length }, failing.length)
          }}</strong>
        </p>
        <p class="banner__text">
          {{ failing.length ? failing.join(', ') : t('issues.next.checks.why') }}
        </p>
        <div class="banner__actions">
          <Button
            v-if="fixing"
            size="sm"
            variant="primary"
            :to="{ name: 'tasks', params: { taskId: fixing } }"
          >
            {{ t('issues.next.checks.fixing') }}
          </Button>
          <Button
            v-else
            size="sm"
            variant="primary"
            icon="warning"
            @click="emit('act', 'fixChecks')"
          >
            {{ t('issues.actions.fixChecks') }}
          </Button>
        </div>
      </template>

      <template v-else-if="phase === 'review'">
        <p class="banner__title">
          <strong>{{ t('issues.next.review.title') }}</strong>
        </p>
        <p class="banner__text">{{ t('issues.next.review.why') }}</p>
        <div v-if="toReview" class="banner__actions">
          <Button size="sm" variant="primary" :to="{ name: 'tasks', params: { taskId: toReview } }">
            {{ t('issues.next.review.action') }}
          </Button>
        </div>
      </template>

      <template v-else-if="phase === 'ready'">
        <p class="banner__title">
          <strong>{{ t('issues.next.ready.title') }}</strong>
        </p>
        <p class="banner__text">{{ t('issues.next.ready.why') }}</p>
        <div class="banner__actions">
          <Button size="sm" variant="primary" icon="git" @click="emit('act', 'pr')">
            {{ t('issues.actions.pr') }}
          </Button>
        </div>
      </template>

      <template v-else-if="phase === 'failed'">
        <p class="banner__title">
          <strong>{{ t('issues.next.failed.title') }}</strong>
        </p>
        <p class="banner__text">{{ ticket.error || t('issues.next.failed.why') }}</p>
        <div class="banner__actions">
          <Button size="sm" variant="primary" icon="refresh" @click="emit('act', 'retry')">
            {{ t('issues.next.failed.action') }}
          </Button>
        </div>
      </template>
    </div>
  </section>
</template>

<style scoped>
.banner {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  border: var(--border-width) solid var(--tone-border);
  border-radius: var(--radius-lg);
  background: var(--tone-bg);
}

.banner__icon {
  flex: none;
  margin-top: var(--space-1);
  color: var(--tone-fg);
  font-size: var(--icon-lg);
}

.banner__body {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
}

.banner__title {
  color: var(--text-primary);
  font-size: var(--fs-sm);
}

.banner__title strong {
  color: var(--tone-fg);
}

.banner__text {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
  overflow-wrap: anywhere;
}

.banner__text code {
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  white-space: pre-wrap;
}

.banner__next {
  color: var(--text-primary);
}

.banner__actions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

@media (width < 600px) {
  .banner {
    padding: var(--space-3);
  }
}
</style>
