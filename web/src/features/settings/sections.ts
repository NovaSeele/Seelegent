/**
 * Settings sections (left list of the settings dialog, deep links /settings/<section>) and the search index.
 * Each entry names one setting; its label, description and search keywords are i18n keys
 * (settings.items.<id>.{label,desc,keywords}), so search works in the active language and in English.
 * Sections without a daemon API yet list the settings they will hold, so search already leads there.
 */
import type { IconName } from '@/components/ui/icons'

export const SECTION_IDS = [
  'appearance',
  'workers',
  'quota',
  'gates',
  'notifications',
  'shortcuts',
  'hooks',
  'memory',
  'experiments',
  'language',
] as const

export type SectionId = (typeof SECTION_IDS)[number]

export const DEFAULT_SECTION: SectionId = 'appearance'

export interface SectionInfo {
  id: SectionId
  icon: IconName
  /** Has a working view in this milestone (others show "not available yet"). */
  ready: boolean
  /** Setting ids in this section (settings.items.<id>). */
  items: readonly string[]
}

export const SECTIONS: readonly SectionInfo[] = [
  {
    id: 'appearance',
    icon: 'palette',
    ready: true,
    items: [
      'theme',
      'fontUi',
      'fontMono',
      'sizeUi',
      'sizeTerm',
      'lineHeightTerm',
      'motion',
      'reset',
    ],
  },
  {
    id: 'workers',
    icon: 'workers',
    ready: false,
    items: ['concurrency', 'defaultTier', 'modelCap'],
  },
  { id: 'quota', icon: 'quota', ready: false, items: ['quotaReserve', 'rotation'] },
  { id: 'gates', icon: 'gates', ready: false, items: ['gateCommands', 'gateBeforeReview'] },
  { id: 'notifications', icon: 'bell', ready: false, items: ['desktopNotify', 'sound'] },
  { id: 'shortcuts', icon: 'keyboard', ready: false, items: ['keymap'] },
  { id: 'hooks', icon: 'webhook', ready: false, items: ['webhooks'] },
  { id: 'memory', icon: 'memory', ready: false, items: ['projectMemory'] },
  { id: 'experiments', icon: 'flask', ready: false, items: ['experimental'] },
  { id: 'language', icon: 'language', ready: true, items: ['locale'] },
]

export function isSectionId(value: unknown): value is SectionId {
  return typeof value === 'string' && (SECTION_IDS as readonly string[]).includes(value)
}

export function sectionInfo(id: SectionId): SectionInfo {
  return SECTIONS.find((section) => section.id === id) as SectionInfo
}
