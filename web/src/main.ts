import { createPinia } from 'pinia'
import { createApp } from 'vue'

import App from './App.vue'
import { migrateLegacyPrefs } from './config/migrate'
import { activateLocale, i18n } from './i18n'
import { router } from './router'
import { useConnectionStore } from './stores/connection'
import { useUiStore } from './stores/ui'
import './styles/index.css'

async function start() {
  // Old dashboard preferences are copied once, before any store reads its key.
  migrateLegacyPrefs()

  const app = createApp(App).use(createPinia()).use(i18n)
  const ui = useUiStore()
  // The first paint already uses the right language: its catalogs load before mounting.
  await activateLocale(ui.locale)
  app.use(router)
  await router.isReady()
  app.mount('#app')
  useConnectionStore().start()
}

void start()
