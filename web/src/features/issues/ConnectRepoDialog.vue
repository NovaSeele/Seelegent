<script setup lang="ts">
import { ref, watch } from 'vue'
import { useI18n } from 'vue-i18n'

import { errorMessage } from '@/api/client'
import Button from '@/components/ui/Button.vue'
import Checkbox from '@/components/ui/Checkbox.vue'
import Dialog from '@/components/ui/Dialog.vue'
import TextField from '@/components/ui/TextField.vue'
import { useConnectionStore } from '@/stores/connection'
import { useGitHubStore } from '@/stores/github'

import { isAbsolutePath } from './issueModel'

/** Connects a local repository by its folder, or makes a new one there first (git init). */
const open = defineModel<boolean>('open', { required: true })

const { t } = useI18n()
const gh = useGitHubStore()
const connection = useConnectionStore()

const path = ref('')
const init = ref(false)
const busy = ref(false)
const error = ref('')

watch(open, (now) => {
  if (!now) return
  error.value = ''
  if (!path.value) path.value = connection.projects[0]?.cwd ?? ''
})

async function submit() {
  if (busy.value) return
  if (!isAbsolutePath(path.value)) {
    error.value = t('issues.connect.pathError')
    return
  }
  busy.value = true
  error.value = ''
  try {
    await gh.connect(path.value.trim(), init.value)
    open.value = false
    init.value = false
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
    :title="t('issues.connect.title')"
    :description="t('issues.connect.desc')"
    :dismissible="!busy"
    initial-focus="input"
    @update:open="open = $event"
  >
    <form id="connect-repo" class="form" @submit.prevent="submit">
      <TextField
        v-model="path"
        :label="t('issues.connect.path')"
        :hint="t('issues.connect.pathHint')"
        :error="error || undefined"
      />
      <Checkbox
        v-model="init"
        :label="t('issues.connect.init')"
        :description="t('issues.connect.initDesc')"
      />
    </form>
    <template #footer>
      <Button variant="ghost" :disabled="busy" @click="open = false">
        {{ t('common.action.cancel') }}
      </Button>
      <Button type="submit" form="connect-repo" variant="primary" :loading="busy">
        {{ t('issues.connect.submit') }}
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
