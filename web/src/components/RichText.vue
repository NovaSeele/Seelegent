<script setup lang="ts">
import { computed, h, type VNode } from 'vue'

import { parseRichText, type Block, type Inline } from '@/lib/richText'

/**
 * Worker and lead text rendered safely: the Markdown subset of lib/richText.ts becomes VNodes, so text is always
 * escaped by Vue and only http(s) links are clickable (they open in a new tab without referrer).
 */
const props = defineProps<{ text: string }>()

const blocks = computed(() => parseRichText(props.text))

function inline(nodes: readonly Inline[]): (VNode | string)[] {
  return nodes.map((node) => {
    switch (node.t) {
      case 'text':
        return node.v
      case 'br':
        return h('br')
      case 'code':
        return h('code', { class: 'rt-code' }, node.v)
      case 'strong':
        return h('strong', inline(node.c))
      case 'link':
        return h(
          'a',
          { href: node.href, target: '_blank', rel: 'noopener noreferrer', class: 'rt-link' },
          inline(node.c),
        )
    }
  })
}

function block(node: Block): VNode {
  switch (node.t) {
    case 'p':
      return h('p', inline(node.c))
    case 'h':
      return h('p', { class: `rt-h rt-h${node.level}` }, inline(node.c))
    case 'code':
      return h('pre', { class: 'rt-pre', 'data-lang': node.lang || undefined }, [h('code', node.v)])
    case 'ul':
      return h(
        'ul',
        node.items.map((item) => h('li', inline(item))),
      )
    case 'ol':
      return h(
        'ol',
        { start: node.start },
        node.items.map((item) => h('li', inline(item))),
      )
  }
}

const Render = () => blocks.value.map(block)
</script>

<template>
  <div class="rt"><Render /></div>
</template>

<style scoped>
.rt {
  display: flex;
  flex-direction: column;
  gap: var(--space-2);
  min-width: 0;
  line-height: var(--lh-prose);
  overflow-wrap: anywhere;
}

.rt :deep(ul),
.rt :deep(ol) {
  display: flex;
  flex-direction: column;
  gap: var(--space-0);
  padding-left: var(--space-5);
}

.rt :deep(.rt-h) {
  font-weight: var(--fw-semibold);
}

.rt :deep(.rt-h1) {
  font-size: var(--fs-lg);
}

.rt :deep(.rt-code) {
  padding: 0 var(--space-1);
  border-radius: var(--radius-xs);
  background: var(--bg-code);
  font-family: var(--font-mono);
  font-size: 0.92em;
}

.rt :deep(.rt-pre) {
  position: relative;
  max-width: 100%;
  overflow-x: auto;
  padding: var(--space-2) var(--space-3);
  border-radius: var(--radius-sm);
  background: var(--bg-code);
  font-family: var(--font-mono);
  font-size: var(--fs-xs);
  line-height: var(--lh-ui);
  white-space: pre;
}

.rt :deep(.rt-link) {
  color: var(--accent);
  text-decoration: underline;
  text-underline-offset: 2px;
}
</style>
