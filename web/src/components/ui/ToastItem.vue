<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted } from 'vue'
import { useI18n } from 'vue-i18n'

import type { Toast, ToastAction } from '@/stores/toast'

import Icon from './Icon.vue'
import type { IconName } from './icons'

/** Minimum time left after a pause, so a toast never vanishes right as the pointer leaves it. */
const MIN_RESUME_MS = 1500

const props = defineProps<{ toast: Toast }>()
const emit = defineEmits<{ dismiss: [id: number] }>()

const { t } = useI18n()

const ICON: Record<Toast['tone'], IconName> = {
  info: 'info',
  success: 'success',
  attention: 'warning',
  error: 'error',
}
const TONE = {
  info: 'neutral',
  success: 'success',
  attention: 'attention',
  error: 'danger',
} as const

let timer: ReturnType<typeof setTimeout> | undefined
let remaining = props.toast.duration
let startedAt = 0
let hovered = false
let focused = false

const icon = computed(() => ICON[props.toast.tone])

function start() {
  if (props.toast.duration === 0) return
  clearTimeout(timer)
  startedAt = Date.now()
  timer = setTimeout(() => emit('dismiss', props.toast.id), remaining)
}

function pause() {
  if (props.toast.duration === 0 || timer === undefined) return
  clearTimeout(timer)
  timer = undefined
  remaining = Math.max(MIN_RESUME_MS, remaining - (Date.now() - startedAt))
}

function resume() {
  if (!hovered && !focused) start()
}

/** Pointer over or focus inside the toast holds its timer. */
function hold(source: 'pointer' | 'focus', on: boolean) {
  if (source === 'pointer') hovered = on
  else focused = on
  if (on) pause()
  else resume()
}

function run(action: ToastAction) {
  action.run()
  emit('dismiss', props.toast.id)
}

function onKeydown(event: KeyboardEvent) {
  if (event.key === 'Escape') {
    event.stopPropagation()
    emit('dismiss', props.toast.id)
  }
}

onMounted(start)
onBeforeUnmount(() => clearTimeout(timer))
</script>

<template>
  <div
    class="toast"
    :class="`tone-${TONE[toast.tone]}`"
    :role="toast.tone === 'error' ? 'alert' : 'status'"
    @pointerenter="hold('pointer', true)"
    @pointerleave="hold('pointer', false)"
    @focusin="hold('focus', true)"
    @focusout="hold('focus', false)"
    @keydown="onKeydown"
  >
    <Icon :name="icon" class="toast__icon" />
    <p class="toast__message">{{ toast.message }}</p>
    <div v-if="toast.actions.length" class="toast__actions">
      <button
        v-for="action in toast.actions"
        :key="action.label"
        type="button"
        class="toast__action"
        @click="run(action)"
      >
        {{ action.label }}
      </button>
    </div>
    <button
      type="button"
      class="toast__close"
      :aria-label="t('common.toast.close')"
      @click="emit('dismiss', toast.id)"
    >
      <Icon name="x" />
    </button>
  </div>
</template>

<style scoped>
.toast {
  display: grid;
  grid-template-columns: auto 1fr auto;
  align-items: start;
  gap: var(--space-1) var(--space-2);
  width: 100%;
  padding: var(--space-3) var(--space-2) var(--space-3) var(--space-3);
  border: var(--border-width) solid var(--border-strong);
  border-left: var(--status-stripe) solid var(--tone-fg);
  border-radius: var(--radius-lg);
  background: var(--bg-elevated);
  box-shadow: var(--shadow-md);
  color: var(--text-primary);
  pointer-events: auto;
}

.toast.tone-neutral {
  border-left-color: var(--border-strong);
}

.toast__icon {
  margin-top: 1px;
  color: var(--tone-fg);
  font-size: var(--icon-md);
}

.toast.tone-neutral .toast__icon {
  color: var(--text-secondary);
}

.toast__message {
  min-width: 0;
  font-size: var(--fs-sm);
  line-height: var(--lh-ui);
  overflow-wrap: anywhere;
}

.toast__actions {
  grid-column: 2 / 4;
  grid-row: 2;
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
}

.toast__action {
  min-height: max(var(--control-sm), var(--target-min));
  padding: 0 var(--space-2);
  border: var(--border-width) solid var(--border-strong);
  border-radius: var(--radius-md);
  background: var(--bg-surface);
  color: var(--accent);
  font-size: var(--fs-xs);
  font-weight: var(--fw-medium);
  cursor: pointer;
}

.toast__action:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
}

.toast__close {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: max(24px, var(--target-min));
  height: max(24px, var(--target-min));
  margin: calc(-1 * var(--space-1)) 0;
  padding: 0;
  border: 0;
  border-radius: var(--radius-sm);
  background: transparent;
  color: var(--text-muted);
  font-size: var(--icon-sm);
  cursor: pointer;
}

.toast__close:hover {
  background: var(--bg-hover);
  --text-muted: var(--text-primary);
  --text-secondary: var(--text-primary);
  color: var(--text-primary);
}

.toast__action:focus-visible,
.toast__close:focus-visible {
  outline: none;
  box-shadow: var(--focus-ring);
}
</style>
