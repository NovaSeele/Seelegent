<script setup lang="ts">
import { computed } from 'vue'
import { useI18n } from 'vue-i18n'

import Button from '@/components/ui/Button.vue'
import SegmentedControl, { type SegmentOption } from '@/components/ui/SegmentedControl.vue'
import Select, { type SelectOption } from '@/components/ui/Select.vue'
import { RANGES } from '@/config/prefs'
import {
  FONT_NAMES,
  MONO_FONTS,
  MOTION_PREFS,
  UI_FONTS,
  type MonoFont,
  type MotionPref,
  type UiFont,
} from '@/config/themes'
import { useFormat } from '@/composables/useFormat'
import { useToastStore } from '@/stores/toast'
import { useUiStore } from '@/stores/ui'

import RangeField from './RangeField.vue'
import SettingRow from './SettingRow.vue'
import ThemePicker from './ThemePicker.vue'

/** Appearance: theme, fonts, text sizes and motion. Changes apply at once and are saved in this browser. */
defineProps<{ highlight?: string }>()

const { t } = useI18n()
const ui = useUiStore()
const toasts = useToastStore()
const format = useFormat()

const theme = computed({ get: () => ui.theme, set: ui.setTheme })
const fontUi = computed({ get: () => ui.fontUi, set: ui.setFontUi })
const fontMono = computed({ get: () => ui.fontMono, set: ui.setFontMono })
const motion = computed({ get: () => ui.motion, set: ui.setMotion })

const uiFonts = computed<SelectOption<UiFont>[]>(() =>
  UI_FONTS.map((id) => ({
    value: id,
    label: id === 'system' ? t('settings.appearance.fontSystem') : FONT_NAMES[id],
  })),
)
const monoFonts = computed<SelectOption<MonoFont>[]>(() =>
  MONO_FONTS.map((id) => ({
    value: id,
    label: id === 'theme' ? t('settings.appearance.fontTheme') : FONT_NAMES[id],
  })),
)
const motions = computed<SegmentOption<MotionPref>[]>(() =>
  MOTION_PREFS.map((id) => ({ value: id, label: t(`settings.appearance.motion.${id}`) })),
)

const px = (value: number) => t('settings.appearance.px', { value: format.value.number(value) })

function reset() {
  const before = {
    theme: ui.theme,
    fontUi: ui.fontUi,
    fontMono: ui.fontMono,
    sizeUi: ui.sizeUi,
    sizeTerm: ui.sizeTerm,
    lineHeightTerm: ui.lineHeightTerm,
    motion: ui.motion,
  }
  ui.resetAppearance()
  toasts.show({
    message: t('settings.appearance.resetDone'),
    tone: 'success',
    actions: [{ label: t('common.action.undo'), run: () => Object.assign(ui, before) }],
  })
}
</script>

<template>
  <div>
    <SettingRow item="theme" stacked :highlighted="highlight === 'theme'">
      <ThemePicker v-model="theme" />
    </SettingRow>
    <SettingRow item="fontUi" :highlighted="highlight === 'fontUi'">
      <Select
        v-model="fontUi"
        :options="uiFonts"
        :label="t('settings.items.fontUi.label')"
        hide-label
      />
    </SettingRow>
    <SettingRow item="fontMono" :highlighted="highlight === 'fontMono'">
      <Select
        v-model="fontMono"
        :options="monoFonts"
        :label="t('settings.items.fontMono.label')"
        hide-label
      />
    </SettingRow>
    <SettingRow item="sizeUi" :highlighted="highlight === 'sizeUi'">
      <RangeField
        v-model="ui.sizeUi"
        :range="RANGES.sizeUi"
        :label="t('settings.items.sizeUi.label')"
        :format="px"
      />
    </SettingRow>
    <SettingRow item="sizeTerm" :highlighted="highlight === 'sizeTerm'">
      <RangeField
        v-model="ui.sizeTerm"
        :range="RANGES.sizeTerm"
        :label="t('settings.items.sizeTerm.label')"
        :format="px"
      />
    </SettingRow>
    <SettingRow item="lineHeightTerm" :highlighted="highlight === 'lineHeightTerm'">
      <RangeField
        v-model="ui.lineHeightTerm"
        :range="RANGES.lineHeightTerm"
        :label="t('settings.items.lineHeightTerm.label')"
        :format="(value) => format.number(value, { minimumFractionDigits: 2 })"
      />
    </SettingRow>
    <SettingRow item="motion" :highlighted="highlight === 'motion'">
      <SegmentedControl
        v-model="motion"
        :options="motions"
        :label="t('settings.items.motion.label')"
      />
    </SettingRow>
    <SettingRow item="reset" :highlighted="highlight === 'reset'">
      <Button icon="refresh" @click="reset">{{ t('settings.appearance.reset') }}</Button>
    </SettingRow>
  </div>
</template>
