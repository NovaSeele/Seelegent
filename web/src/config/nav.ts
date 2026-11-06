/**
 * The app's main pages, in navigation order: the rail, the bottom bar, the palette and the router all read this
 * list. "work" pages are the day-to-day flow; "team" pages configure who does the work. Labels live in
 * nav.json under nav.<id>.{label,desc}: one label everywhere (rail, bottom bar, More sheet, palette, page
 * title, document title), so a page is never called two different things.
 */
import type { IconName } from '@/components/ui/icons'

export const NAV_IDS = [
  'watch',
  'tasks',
  'issues',
  'meetings',
  'contests',
  'automations',
  'proof',
  'accounts',
  'usage',
  'roles',
  'members',
] as const

export type NavId = (typeof NAV_IDS)[number]

export interface NavItem {
  id: NavId
  path: string
  icon: IconName
  group: 'work' | 'team'
  /** One of the four tabs of the phone bottom bar; the rest go under "More". */
  mobilePrimary: boolean
  /** nav.<id>.keywords holds extra search words for the palette ("Issue → PR", "Worker roles"). */
  keywords?: boolean
  /** Default "go to" key sequence. */
  shortcut: string
}

export const NAV_ITEMS: readonly NavItem[] = [
  { id: 'watch', path: '/', icon: 'watch', group: 'work', mobilePrimary: true, shortcut: 'g w' },
  {
    id: 'tasks',
    path: '/tasks',
    icon: 'tasks',
    group: 'work',
    mobilePrimary: true,
    shortcut: 'g t',
  },
  {
    id: 'issues',
    path: '/issues',
    icon: 'issues',
    group: 'work',
    mobilePrimary: true,
    shortcut: 'g i',
    keywords: true,
  },
  {
    id: 'meetings',
    path: '/meetings',
    icon: 'meetings',
    group: 'work',
    mobilePrimary: true,
    shortcut: 'g m',
  },
  {
    id: 'contests',
    path: '/contests',
    icon: 'contests',
    group: 'work',
    mobilePrimary: false,
    shortcut: 'g c',
  },
  {
    id: 'automations',
    path: '/automations',
    icon: 'automations',
    group: 'work',
    mobilePrimary: false,
    shortcut: 'g o',
  },
  {
    id: 'proof',
    path: '/proof',
    icon: 'proof',
    group: 'work',
    mobilePrimary: false,
    shortcut: 'g p',
  },
  {
    id: 'accounts',
    path: '/accounts',
    icon: 'accounts',
    group: 'team',
    mobilePrimary: false,
    shortcut: 'g a',
  },
  {
    id: 'usage',
    path: '/usage',
    icon: 'usage',
    group: 'team',
    mobilePrimary: false,
    shortcut: 'g u',
  },
  {
    id: 'roles',
    path: '/roles',
    icon: 'roles',
    group: 'team',
    mobilePrimary: false,
    shortcut: 'g r',
    keywords: true,
  },
  {
    id: 'members',
    path: '/members',
    icon: 'members',
    group: 'team',
    mobilePrimary: false,
    shortcut: 'g e',
  },
]

export const SETTINGS_SHORTCUT = 'Mod+,'
export const SETTINGS_SEQUENCE = 'g s'
export const PALETTE_SHORTCUT = 'Mod+K'
export const PALETTE_ALT_SHORTCUT = 'Mod+Shift+P'
export const SIDEBAR_SHORTCUT = 'Mod+B'

export function navItem(id: NavId): NavItem {
  const item = NAV_ITEMS.find((entry) => entry.id === id)
  if (!item) throw new Error(`unknown nav item ${id}`)
  return item
}
