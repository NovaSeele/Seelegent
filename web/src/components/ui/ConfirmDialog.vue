<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import Button from './Button.vue'
import Dialog from './Dialog.vue'

/**
 * A yes/no question before a change that cannot be undone: the title says what happens, the description what it
 * affects, and the confirm button repeats the verb. While `busy` it cannot be dismissed. An `error` from the last
 * try shows above the buttons, so the dialog stays open on failure.
 */
withDefaults(
  defineProps<{
    open: boolean
    title: string
    description?: string
    confirmLabel: string
    /** The safe button; say what it keeps when the confirm verb is itself "Cancel …". */
    cancelLabel?: string
    danger?: boolean
    busy?: boolean
    error?: string | null
    /** The confirm button waits for a choice inside the dialog. */
    confirmDisabled?: boolean
  }>(),
  { description: undefined, cancelLabel: undefined, error: null },
)

const emit = defineEmits<{ 'update:open': [value: boolean]; confirm: [] }>()
const { t } = useI18n()
</script>

<template>
  <Dialog
    :open="open"
    size="sm"
    :title="title"
    :description="description"
    :dismissible="!busy"
    @update:open="emit('update:open', $event)"
  >
    <slot />
    <p v-if="error" class="confirm__error" role="alert">{{ error }}</p>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="emit('update:open', false)">
        {{ cancelLabel ?? t('common.action.cancel') }}
      </Button>
      <Button
        :variant="danger ? 'danger' : 'primary'"
        :loading="busy"
        :disabled="confirmDisabled"
        @click="emit('confirm')"
      >
        {{ confirmLabel }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.confirm__error {
  color: var(--status-danger-fg);
  font-size: var(--fs-sm);
}
</style>
