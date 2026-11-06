/// <reference types="vitest/config" />
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig, type Plugin } from 'vite'

// Development daemon (see docs/development.md); the installed daemon on 47821 is never touched.
const devDaemon = 'http://127.0.0.1:47831'

/**
 * Re-creates dist/.gitkeep after the build empties dist/. The Go package in web/ embeds dist/ and needs the
 * directory, and the placeholder is committed so `go build` works before the first web build.
 */
function keepDistPlaceholder(): Plugin {
  return {
    name: 'seelegent:keep-dist-placeholder',
    apply: 'build',
    generateBundle() {
      this.emitFile({ type: 'asset', fileName: '.gitkeep', source: '' })
    },
  }
}

export default defineConfig({
  plugins: [vue(), keepDistPlaceholder()],
  resolve: {
    alias: {
      '@': fileURLToPath(new URL('./src', import.meta.url)),
    },
  },
  define: {
    // vue-i18n compile-time flags: Composition API only, no devtools in production.
    __VUE_I18N_FULL_INSTALL__: true,
    __VUE_I18N_LEGACY_API__: false,
    __INTLIFY_PROD_DEVTOOLS__: false,
  },
  server: {
    proxy: {
      '/api': devDaemon,
    },
  },
  build: {
    outDir: 'dist',
    emptyOutDir: true,
  },
  test: {
    environment: 'jsdom',
    include: ['src/**/*.spec.ts'],
  },
})
