<script setup lang="ts">
import { useI18n } from 'vue-i18n'

import { FIELDS, type FieldSection } from './fields'
import SettingField from './SettingField.vue'
import { useSettingsForm } from './useSettingsForm'

/** A section of plain daemon settings, one row per field of FIELDS[section], saved as you go. */
defineProps<{ section: FieldSection; highlight?: string }>()

const { t } = useI18n()
const form = useSettingsForm()
</script>

<template>
  <p v-if="!form.settings.value" class="daemon__offline">{{ t('settings.daemon.offline') }}</p>
  <div v-else>
    <SettingField
      v-for="def in FIELDS[section]"
      :key="def.path"
      :def="def"
      :form="form"
      :highlighted="highlight === def.item"
    />
  </div>
</template>

<style scoped>
.daemon__offline {
  color: var(--text-secondary);
  font-size: var(--fs-sm);
}
</style>
