<script setup lang="ts">
/**
 * A full-width row under a diff line (a note, the "add a note" button, the note composer). Its content stays in
 * view: it sticks to the left edge of the scroll box and is as wide as the visible box (100cqi of DiffView's
 * .diff__scroll, a size container), so a long line that widens the table never pushes a note off screen.
 */
defineProps<{ tone?: 'note' | 'action' }>()
</script>

<template>
  <tr class="extra-row">
    <td colspan="4" class="extra" :class="tone === 'action' ? 'extra--action' : 'extra--note'">
      <div class="extra__box"><slot /></div>
    </td>
  </tr>
</template>

<style scoped>
.extra {
  padding: 0;
  background: var(--bg-surface);
  color: var(--text-primary);
  font-family: var(--font-sans);
  white-space: normal;
}

.extra--action {
  background: transparent;
}

.extra__box {
  position: sticky;
  left: 0;
  box-sizing: border-box;
  width: 100cqi;
  padding: var(--space-2) var(--space-3);
}
</style>
