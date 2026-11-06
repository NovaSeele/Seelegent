/**
 * The app-wide commands (palette entries and shortcuts): go to each page, open the palette and each settings
 * section, change theme, change language, toggle the sidebar. Registered by the shell for its lifetime.
 */
import { onBeforeUnmount } from 'vue'
import { useI18n } from 'vue-i18n'
import { useRouter } from 'vue-router'

import {
  NAV_ITEMS,
  PALETTE_ALT_SHORTCUT,
  PALETTE_SHORTCUT,
  SETTINGS_SEQUENCE,
  SETTINGS_SHORTCUT,
  SIDEBAR_SHORTCUT,
} from '@/config/nav'
import { THEMES } from '@/config/themes'
import { SECTIONS, type SectionId } from '@/features/settings/sections'
import { LOCALE_NAMES, SUPPORTED_LOCALES } from '@/i18n'
import { useCommandsStore, type Command } from '@/stores/commands'
import { usePaletteStore } from '@/stores/palette'
import { useUiStore } from '@/stores/ui'

export function useAppCommands() {
  const { t } = useI18n()
  const router = useRouter()
  const commands = useCommandsStore()
  const palette = usePaletteStore()
  const ui = useUiStore()

  function openSettings(section?: SectionId) {
    const target = { name: 'settings', params: section ? { section } : {} } as const
    const inSettings = router.currentRoute.value.name === 'settings'
    return inSettings ? router.replace(target) : router.push(target)
  }

  const list: Command[] = [
    {
      id: 'palette.open',
      title: { key: 'palette.open' },
      group: 'general',
      icon: 'command',
      shortcut: PALETTE_SHORTCUT,
      allowInInput: true,
      run: () => palette.open(),
    },
    {
      id: 'palette.openAlt',
      title: { key: 'palette.open' },
      group: 'general',
      shortcut: PALETTE_ALT_SHORTCUT,
      allowInInput: true,
      listed: false,
      run: () => palette.open(),
    },
    ...NAV_ITEMS.map((item): Command => ({
      id: `nav.${item.id}`,
      title: { key: `nav.${item.id}.label` },
      keywords: item.keywords ? { key: `nav.${item.id}.keywords` } : undefined,
      group: 'navigation',
      icon: item.icon,
      shortcut: item.shortcut,
      run: () => void router.push(item.path),
    })),
    {
      id: 'settings.open',
      title: { key: 'palette.cmd.openSettings' },
      group: 'settings',
      icon: 'settings',
      shortcut: SETTINGS_SHORTCUT,
      allowInInput: true,
      run: () => void openSettings(),
    },
    {
      id: 'settings.openSeq',
      title: { key: 'palette.cmd.openSettings' },
      group: 'settings',
      shortcut: SETTINGS_SEQUENCE,
      listed: false,
      run: () => void openSettings(),
    },
    ...SECTIONS.map((section): Command => ({
      id: `settings.${section.id}`,
      title: { key: `settings.sections.${section.id}.label` },
      keywords: { key: 'palette.keywords.settings' },
      group: 'settings',
      icon: section.icon,
      run: () => void openSettings(section.id),
    })),
    {
      id: 'theme.pick',
      title: { key: 'palette.cmd.pickTheme' },
      keywords: { key: 'palette.keywords.theme' },
      group: 'appearance',
      icon: 'palette',
      run: () => palette.open(`${t('palette.cmd.themePrefix')} `),
    },
    {
      id: 'theme.auto',
      title: {
        key: 'palette.cmd.applyTheme',
        params: { name: { key: 'settings.appearance.auto.label' } },
      },
      group: 'appearance',
      icon: 'monitor',
      hidden: true,
      run: () => ui.setTheme('auto'),
    },
    ...THEMES.map((theme): Command => ({
      id: `theme.${theme.id}`,
      title: { key: 'palette.cmd.applyTheme', params: { name: theme.name } },
      group: 'appearance',
      icon: theme.scheme === 'dark' ? 'moon' : 'sun',
      hidden: true,
      run: () => ui.setTheme(theme.id),
    })),
    {
      id: 'language.pick',
      title: { key: 'locale.command.pick' },
      group: 'language',
      icon: 'language',
      run: () => palette.open(`${t('locale.command.prefix')} `),
    },
    ...SUPPORTED_LOCALES.map((loc): Command => ({
      id: `language.${loc}`,
      title: { key: 'locale.command.apply', params: { name: LOCALE_NAMES[loc] } },
      group: 'language',
      icon: 'language',
      hidden: true,
      run: () => ui.setLocale(loc),
    })),
    {
      id: 'sidebar.toggle',
      title: { key: 'palette.cmd.toggleSidebar' },
      group: 'general',
      icon: 'sidebar',
      shortcut: SIDEBAR_SHORTCUT,
      allowInInput: true,
      run: () => ui.toggleSidebar(),
    },
  ]

  const unregister = commands.register(...list)
  onBeforeUnmount(unregister)
}
