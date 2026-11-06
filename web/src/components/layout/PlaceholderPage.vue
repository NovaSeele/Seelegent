<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import EmptyState from '@/components/ui/EmptyState.vue'
import { navItem, type NavId } from '@/config/nav'

import PageContainer from './PageContainer.vue'
import PageHeader from './PageHeader.vue'
import SplitLayout from './SplitLayout.vue'

/**
 * Stand-in for a page whose data views arrive in later milestones: the page title and description, and an
 * empty state saying the view is coming. Split views (`split`, Tasks) show the list + detail layout at full
 * width; every other page is a PageContainer at its route's width (docs/ui-layout.md).
 */
const props = defineProps<{
  id: NavId
  split?: boolean
  /** The detail route parameter, when one is open (shows the detail pane on narrow screens). */
  detailId?: string
}>()

const { t } = useI18n()
const item = navItem(props.id)
</script>

<template>
  <SplitLayout v-if="split" :sidebar-label="t(`nav.${id}.label`)" :detail-open="!!detailId">
    <template #sidebar>
      <div class="list-head">
        <h2 class="list-head__title">{{ t(`nav.${id}.label`) }}</h2>
      </div>
      <p class="list-empty">{{ t('pages.placeholder.listEmpty') }}</p>
    </template>
    <PageHeader :title="t(`nav.${id}.label`)" :description="t(`nav.${id}.desc`)" />
    <EmptyState
      :icon="item.icon"
      :title="t('pages.placeholder.title')"
      :description="t('pages.placeholder.desc')"
    />
  </SplitLayout>
  <PageContainer v-else>
    <PageHeader :title="t(`nav.${id}.label`)" :description="t(`nav.${id}.desc`)" />
    <EmptyState
      :icon="item.icon"
      :title="t('pages.placeholder.title')"
      :description="t('pages.placeholder.desc')"
    />
  </PageContainer>
</template>

<style scoped>
.list-head {
  display: flex;
  align-items: center;
  min-height: var(--control-lg);
  padding: var(--space-4) var(--space-4) var(--space-2);
}

.list-empty {
  padding: var(--space-6) var(--space-4);
  color: var(--text-muted);
  font-size: var(--fs-sm);
  text-align: center;
}

.list-head__title {
  font-size: var(--fs-md);
  font-weight: var(--fw-semibold);
}
</style>
