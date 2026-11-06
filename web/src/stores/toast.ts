/**
 * Toasts: short, non-blocking messages. Plain toasts stay 4.5 s; toasts with actions or errors stay 8 s
 * (time to read and act); duration 0 keeps one until it is closed. At most MAX_TOASTS are shown; the oldest goes
 * first. Timers live in the ToastItem component, which pauses them on hover and focus.
 */
import { defineStore } from 'pinia'
import { ref } from 'vue'

export type ToastTone = 'info' | 'success' | 'attention' | 'error'

export interface ToastAction {
  label: string
  run: () => void
}

export interface ToastOptions {
  message: string
  tone?: ToastTone
  /** At most two actions are shown. */
  actions?: readonly ToastAction[]
  /** Milliseconds; 0 = until closed. */
  duration?: number
}

export interface Toast {
  readonly id: number
  readonly message: string
  readonly tone: ToastTone
  readonly actions: readonly ToastAction[]
  readonly duration: number
}

export const MAX_TOASTS = 4
export const DURATION_PLAIN = 4500
export const DURATION_LONG = 8000

export function defaultDuration(options: ToastOptions): number {
  if (options.duration !== undefined) return Math.max(0, options.duration)
  return options.actions?.length || options.tone === 'error' ? DURATION_LONG : DURATION_PLAIN
}

export const useToastStore = defineStore('toast', () => {
  const toasts = ref<Toast[]>([])
  let nextId = 1

  function show(options: ToastOptions): number {
    const toast: Toast = {
      id: nextId++,
      message: options.message,
      tone: options.tone ?? 'info',
      actions: (options.actions ?? []).slice(0, 2),
      duration: defaultDuration(options),
    }
    toasts.value = [...toasts.value, toast].slice(-MAX_TOASTS)
    return toast.id
  }

  function dismiss(id: number): void {
    toasts.value = toasts.value.filter((toast) => toast.id !== id)
  }

  function clear(): void {
    toasts.value = []
  }

  return { toasts, show, dismiss, clear }
})
