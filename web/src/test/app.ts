/**
 * Test helpers: a fresh Pinia and the app's i18n instance (English loaded) as mount plugins, the global key
 * dispatcher on window, and a controllable fake EventSource.
 */
import { createPinia, setActivePinia, type Pinia } from 'pinia'
import { nextTick } from 'vue'

import type { EventSourceLike } from '@/api/sse'
import { createKeyDispatcher } from '@/composables/useShortcuts'
import { activateLocale, i18n, type Locale } from '@/i18n'
import { useCommandsStore } from '@/stores/commands'
import { useLayersStore } from '@/stores/layers'

export async function setupApp(
  locale: Locale = 'en',
): Promise<{ pinia: Pinia; plugins: [Pinia, typeof i18n] }> {
  const pinia = createPinia()
  setActivePinia(pinia)
  await activateLocale(locale)
  return { pinia, plugins: [pinia, i18n] }
}

/** Installs the app's keydown dispatcher (Esc to the top layer, shortcuts); returns the uninstall function. */
export function installKeys(): () => void {
  const handler = createKeyDispatcher(useCommandsStore(), useLayersStore())
  window.addEventListener('keydown', handler)
  return () => window.removeEventListener('keydown', handler)
}

export function press(
  key: string,
  init: KeyboardEventInit = {},
  target: EventTarget | null = null,
) {
  const el = target ?? document.activeElement ?? document.body
  el.dispatchEvent(new KeyboardEvent('keydown', { key, bubbles: true, cancelable: true, ...init }))
}

export async function flush(times = 3) {
  for (let i = 0; i < times; i++) await nextTick()
}

export class FakeEventSource implements EventSourceLike {
  static instances: FakeEventSource[] = []
  readyState = 0
  onopen: EventSourceLike['onopen'] = null
  onerror: EventSourceLike['onerror'] = null
  closed = false
  private listeners = new Map<string, ((event: MessageEvent<string>) => void)[]>()

  constructor(readonly url: string) {
    FakeEventSource.instances.push(this)
  }

  static get last(): FakeEventSource {
    const source = FakeEventSource.instances.at(-1)
    if (!source) throw new Error('no EventSource created')
    return source
  }

  addEventListener(type: string, listener: (event: MessageEvent<string>) => void) {
    this.listeners.set(type, [...(this.listeners.get(type) ?? []), listener])
  }

  close() {
    this.closed = true
    this.readyState = 2
  }

  open() {
    this.readyState = 1
    this.onopen?.call(this, new Event('open'))
  }

  fail() {
    this.onerror?.call(this, new Event('error'))
  }

  emit(type: string, data: string, id = '') {
    const event = new MessageEvent<string>(type, { data, lastEventId: id })
    for (const listener of this.listeners.get(type) ?? []) listener(event)
  }
}
