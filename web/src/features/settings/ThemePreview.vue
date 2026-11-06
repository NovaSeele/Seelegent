<script setup lang="ts">
import type { ThemeId } from '@/config/themes'

/**
 * A miniature of the app in one theme: rail with the accent dot, a panel with title and text lines, status
 * chips (running, attention, success) and a primary button. data-theme-preview scopes that theme's colour tokens
 * to this element, so it shows the real palette whatever the page's theme is.
 */
defineProps<{ theme: ThemeId }>()
</script>

<template>
  <span :data-theme-preview="theme" class="mini" aria-hidden="true">
    <span class="mini__rail"><span class="mini__dot" /></span>
    <span class="mini__panel">
      <span class="mini__line mini__line--title" />
      <span class="mini__line" />
      <span class="mini__chips">
        <span class="mini__chip mini__chip--running" />
        <span class="mini__chip mini__chip--attention" />
        <span class="mini__chip mini__chip--accepted" />
      </span>
      <span class="mini__button" />
    </span>
  </span>
</template>

<style scoped>
.mini {
  display: flex;
  flex: 1;
  gap: 4px;
  min-width: 0;
  padding: 4px;
  background: var(--bg-app);
}

.mini__rail {
  display: flex;
  flex-direction: column;
  align-items: center;
  width: 12px;
  padding-top: 2px;
}

.mini__dot {
  width: 8px;
  height: 8px;
  border-radius: var(--radius-pill);
  background: var(--accent);
}

.mini__panel {
  display: flex;
  flex: 1;
  flex-direction: column;
  gap: 5px;
  min-width: 0;
  padding: 6px;
  border: 1px solid var(--border-subtle);
  border-radius: var(--radius-xs);
  background: var(--bg-surface);
}

.mini__line {
  width: 70%;
  height: 4px;
  border-radius: var(--radius-pill);
  background: var(--text-muted);
}

.mini__line--title {
  width: 50%;
  background: var(--text-primary);
}

.mini__chips {
  display: flex;
  gap: 3px;
}

.mini__chip {
  width: 14px;
  height: 6px;
  border-radius: var(--radius-pill);
}

.mini__chip--running {
  background: var(--status-running-fg);
}

.mini__chip--attention {
  background: var(--attention-fg);
}

.mini__chip--accepted {
  background: var(--status-accepted-fg);
}

.mini__button {
  width: 26px;
  height: 8px;
  margin-top: auto;
  border-radius: var(--radius-xs);
  background: var(--accent);
}
</style>
