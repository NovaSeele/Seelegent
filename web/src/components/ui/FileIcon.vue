<script setup lang="ts">
import { computed } from 'vue'

import { themeInfo } from '@/config/themes'
import { fileIcon, type FileIconSetId } from '@/lib/fileicons'
import { useUiStore } from '@/stores/ui'

/**
 * The icon for a file path (a trailing '/' means a folder) in the icon set chosen in Settings › Appearance, or
 * in `set` when given (settings previews). Decorative: the file name next to it carries the meaning. Draws
 * nothing, and leaves no gap, when icons are off. Glyph colours: styles/fileicons.css.
 */
const props = withDefaults(
  defineProps<{
    path: string
    set?: FileIconSetId
    /** Deleted file, read-only folder. */
    dim?: boolean
    /** 14px icon placed inline before a name, with its own 6px gap. */
    lead?: boolean
  }>(),
  { set: undefined },
)

const ui = useUiStore()
const scheme = computed(() => themeInfo(ui.resolvedTheme).scheme)
const icon = computed(() => fileIcon(props.path, props.set ?? ui.fileIcons, scheme.value))
const classes = computed(() => {
  const value = icon.value
  if (!value) return []
  return [
    'fi',
    `fi-${value.kind}`,
    `fi--${scheme.value}`,
    { 'fi--mono': value.mono, 'fi--lead': props.lead, 'fi--dim': props.dim },
  ]
})
</script>

<template>
  <img
    v-if="icon?.src"
    :class="classes"
    :src="icon.src"
    alt=""
    aria-hidden="true"
    width="16"
    height="16"
    draggable="false"
  />
  <span v-else-if="icon" :class="classes" aria-hidden="true">
    <svg v-if="'text' in icon.glyph" width="16" height="16" viewBox="0 0 16 16">
      <text
        x="8"
        y="8.5"
        text-anchor="middle"
        dominant-baseline="central"
        :font-size="icon.glyph.size"
        v-text="icon.glyph.text"
      />
    </svg>
    <svg
      v-else
      width="14"
      height="14"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      stroke-width="2"
      stroke-linecap="round"
      stroke-linejoin="round"
    >
      <path :d="icon.glyph.path" />
    </svg>
  </span>
</template>
