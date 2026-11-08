<script setup lang="ts">
import { onBeforeUnmount, ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { useFormat } from '@/composables/useFormat'
import { formatBytes } from '@/lib/memory'

/**
 * One image of the Git view (GET /tasks/{id}/git/image) in an `<img>` only: never inline SVG markup, so a
 * script in it cannot run. A checkerboard shows transparency, and the pixel size shows once it loads. When the
 * image fails, the endpoint is asked once more for its reason (too large, a Git LFS pointer, not an image).
 */
const props = defineProps<{ src: string; label: string; alt: string }>()

const { t } = useI18n()
const format = useFormat()
const size = ref<{ width: number; height: number } | null>(null)
const failure = ref<string | null>(null)
let probe: AbortController | null = null

watch(
  () => props.src,
  () => {
    probe?.abort()
    size.value = null
    failure.value = null
  },
)
onBeforeUnmount(() => probe?.abort())

function onLoad(event: Event) {
  const img = event.target as HTMLImageElement
  size.value = { width: img.naturalWidth, height: img.naturalHeight }
}

interface ErrorInfo {
  code?: string
  message?: string
  details?: { size?: number; reason?: string }
}

/** The message for an answer the image could not be shown from. */
function reasonOf(status: number, info: ErrorInfo): string {
  if (status === 413 && typeof info.details?.size === 'number') {
    return t('git.image.tooLarge', { size: formatBytes(format.value, info.details.size) })
  }
  if (info.details?.reason === 'lfs_pointer') return t('git.image.lfs')
  if (status === 415) return t('git.image.unsupported')
  if (status === 404) return t('git.image.missing')
  return t('git.image.failed', { error: info.message ?? String(status) })
}

async function onError() {
  const src = props.src
  failure.value = t('git.image.broken')
  probe?.abort()
  const call = (probe = new AbortController())
  try {
    const res = await fetch(src, { credentials: 'same-origin', signal: call.signal })
    if (res.ok || call !== probe || src !== props.src) return
    const body = (await res.json().catch(() => ({}))) as { error?: ErrorInfo }
    if (call === probe) failure.value = reasonOf(res.status, body.error ?? {})
  } catch {
    // The daemon did not answer: the plain message stays.
  }
}
</script>

<template>
  <figure class="ip">
    <figcaption class="ip__caption">
      <span>{{ label }}</span>
      <span v-if="size" class="ip__size">
        {{ t('git.image.pixels', { width: size.width, height: size.height }) }}
      </span>
    </figcaption>
    <p v-if="failure" class="ip__failure" role="status">{{ failure }}</p>
    <div v-else class="ip__frame">
      <img class="ip__img" :src="src" :alt="alt" @load="onLoad" @error="onError" />
    </div>
  </figure>
</template>

<style scoped>
.ip {
  display: flex;
  flex: 1 1 16rem;
  flex-direction: column;
  gap: var(--space-1);
  min-width: 0;
}

.ip__caption {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  color: var(--text-secondary);
  font-size: var(--fs-xs);
}

.ip__size {
  color: var(--text-muted);
  font-family: var(--font-mono);
}

.ip__frame {
  display: flex;
  align-items: center;
  justify-content: center;
  padding: var(--space-2);
  border: var(--border-width) solid var(--border-subtle);
  border-radius: var(--radius-md);
  background-color: var(--bg-surface);
  background-image: conic-gradient(
    var(--bg-hover) 25%,
    transparent 0 50%,
    var(--bg-hover) 0 75%,
    transparent 0
  );
  background-size: 16px 16px;
}

.ip__img {
  display: block;
  max-width: 100%;
  height: auto;
}

.ip__failure {
  padding: var(--space-3);
  border: var(--border-width) dashed var(--border-subtle);
  border-radius: var(--radius-md);
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
