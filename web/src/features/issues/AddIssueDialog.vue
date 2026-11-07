<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import Dialog from '@/components/ui/Dialog.vue'
import TextArea from '@/components/ui/TextArea.vue'
import TextField from '@/components/ui/TextField.vue'
import { useGitHubStore } from '@/stores/github'

/** Adds a local issue to the chosen repo's inbox (it never reaches GitHub). */
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const gh = useGitHubStore()

const title = ref('')
const body = ref('')
const busy = ref(false)
const error = ref('')

watch(open, (now) => {
  if (now) error.value = ''
})

async function submit() {
  if (busy.value || !gh.repoId) return
  if (!title.value.trim()) {
    error.value = t('issues.add.titleError')
    return
  }
  busy.value = true
  error.value = ''
  try {
    gh.putIssue(await gh.api().addIssue(gh.repoId, { title: title.value.trim(), body: body.value }))
    title.value = ''
    body.value = ''
    open.value = false
  } catch (cause) {
    error.value = errorMessage(cause)
  } finally {
    busy.value = false
  }
}
</script>

<template>
  <Dialog
    :open="open"
    :title="t('issues.add.title')"
    :description="t('issues.add.desc')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="open = $event"
  >
    <form id="add-issue" class="form" @submit.prevent="submit">
      <TextField v-model="title" :label="t('issues.add.titleField')" :error="error || undefined" />
      <TextArea v-model="body" :label="t('issues.add.body')" :rows="6" />
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="add-issue" variant="primary" :loading="busy">
        {{ t('issues.add.submit') }}
      </Button>
    </template>
  </Dialog>
</template>

<style scoped>
.form {
  display: flex;
  flex-direction: column;
  gap: var(--space-4);
}
</style>
