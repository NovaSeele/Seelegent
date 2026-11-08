/**
 * Settings sections (left list of the settings dialog, deep links /settings/<section>) and the search index.
 * Each entry names one setting; its label, description and search keywords are i18n keys
 * (settings.items.<id>.{label,desc,keywords}), so search works in the active language and in English.
 *
 * Appearance, shortcuts, notifications and language are this browser's own; every other section edits the
 * daemon's settings (internal/config.Settings), providers, hooks or project memory. Notifications, shortcuts and
 * experiments have no setting behind them yet: they list what they will hold, so search already leads there.
 */
import type { IconName } from '@/components/ui/icons'

export const SECTION_IDS = [
  'appearance',
  'workers',
  'backends',
  'quota',
  'providers',
  'gates',
  'workspace',
  'hooks',
  'memory',
  'notifications',
  'shortcuts',
  'system',
  'experiments',
  'language',
] as const

export type SectionId = (typeof SECTION_IDS)[number]

export const DEFAULT_SECTION: SectionId = 'appearance'

export interface SectionInfo {
  id: SectionId
  icon: IconName
  /** Has a working view (others show "not available yet"). */
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
      'fileIcons',
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
    ready: true,
    items: [
      'concurrency',
      'defaultTier',
      'taskTimeout',
      'stall',
      'stallTool',
      'loopRepeat',
      'attentionDecay',
      'linkIntoProfiles',
    ],
  },
  {
    id: 'backends',
    icon: 'terminal',
    ready: true,
    items: ['backendAgy', 'backendClaude', 'backendCodex'],
  },
  {
    id: 'quota',
    icon: 'quota',
    ready: true,
    items: ['quotaReserve', 'quotaRefresh', 'passiveCooldown', 'usageHistory'],
  },
  { id: 'providers', icon: 'accounts', ready: true, items: ['providers'] },
  {
    id: 'gates',
    icon: 'gates',
    ready: true,
    items: [
      'gates',
      'gateAutoChecks',
      'gateTimeout',
      'gateMaxAutoFixes',
      'gateSettle',
      'gateSettlePoll',
      'gateOutputTail',
      'verifier',
      'verifierTier',
    ],
  },
  {
    id: 'workspace',
    icon: 'git',
    ready: true,
    items: [
      'hygiene',
      'gitGuard',
      'lanes',
      'laneHold',
      'contentionWindow',
      'snapshotRetention',
      'codeGraph',
    ],
  },
  { id: 'hooks', icon: 'webhook', ready: true, items: ['webhooks'] },
  { id: 'memory', icon: 'memory', ready: true, items: ['projectMemory'] },
  { id: 'notifications', icon: 'bell', ready: false, items: ['desktopNotify', 'sound'] },
  { id: 'shortcuts', icon: 'keyboard', ready: false, items: ['keymap'] },
  {
    id: 'system',
    icon: 'monitor',
    ready: true,
    items: ['port', 'githubPoll', 'githubHosts', 'automations'],
  },
  { id: 'experiments', icon: 'flask', ready: false, items: ['experimental'] },
  { id: 'language', icon: 'language', ready: true, items: ['locale'] },
]

export function isSectionId(value: unknown): value is SectionId {
  return typeof value === 'string' && (SECTION_IDS as readonly string[]).includes(value)
}

export function sectionInfo(id: SectionId): SectionInfo {
  return SECTIONS.find((section) => section.id === id) as SectionInfo
}
