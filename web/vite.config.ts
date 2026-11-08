/// <reference types="vitest/config" />
import { readdirSync } from 'node:fs'
import { fileURLToPath, URL } from 'node:url'

import vue from '@vitejs/plugin-vue'
import { defineConfig, type Plugin } from 'vite'

// Development daemon (see docs/development.md); the installed daemon on 47821 is never touched.
// SEELEGENT_DEV_DAEMON points the proxy elsewhere, e.g. at a mock daemon on another port.
const devDaemon = process.env.SEELEGENT_DEV_DAEMON || 'http://127.0.0.1:47831'

const themesDir = fileURLToPath(new URL('./src/styles/themes', import.meta.url))

/** Theme ids, one per palette file in src/styles/themes/ (the single source of the list). */
function themeIds(): string[] {
  return readdirSync(themesDir)
    .filter((name) => name.endsWith('.css'))
    .map((name) => name.slice(0, -'.css'.length))
    .sort()
}

/** The marker in index.html's pre-paint script that receives the theme id list. */
const THEMES_MARKER = '/*seelegent:themes*/ []'

/**
 * Fills the theme id list into the pre-paint script, so it can only ever set a theme that has a palette
 * (an unknown id would leave the page without colours).
 */
function prepaintThemes(): Plugin {
  return {
    name: 'seelegent:prepaint-themes',
    transformIndexHtml(html) {
      if (!html.includes(THEMES_MARKER)) throw new Error('index.html: theme marker missing')
      return html.replace(THEMES_MARKER, JSON.stringify(themeIds()))
    },
  }
}

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
  plugins: [vue(), prepaintThemes(), keepDistPlaceholder()],
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
    // Each test file gets its own VM context in a reused worker instead of a new process with a new jsdom
    // (measured with --maxWorkers=2: 50-58 s wall and 123-140 CPU-s before, 7-10 s and 18-26 CPU-s after).
    pool: 'vmThreads',
    // A light default on a shared machine (the default is one worker per CPU but one); --maxWorkers overrides it.
    maxWorkers: 4,
    // dev/*.spec.mjs test the mock daemon's pure helpers, so dev mode keeps matching the daemon.
    include: ['src/**/*.spec.ts', 'dev/**/*.spec.mjs'],
  },
})
