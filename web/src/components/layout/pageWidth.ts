/**
 * Page width contract (docs/ui-layout.md):
 *   readable  document and table pages (Watch, Usage, Accounts, Roles, Members, Proof, Automations, the Issues,
 *             Meetings and Contests lists, Settings): content centred, at most --page-max-readable wide.
 *   full      split-pane views (Tasks and task detail, Git, Tree/Graph, terminal): the whole workspace width,
 *             always. SplitLayout is always full width.
 * Each route picks its default in meta.width (router/index.ts; unset = readable). AppShell provides the width of
 * the page on screen (the background route, also while settings is open over it); PageContainer can override it.
 */
import { computed, inject, type ComputedRef, type InjectionKey } from 'vue'

export type PageWidth = 'readable' | 'full'

export const DEFAULT_PAGE_WIDTH: PageWidth = 'readable'

export const PAGE_WIDTH_KEY: InjectionKey<ComputedRef<PageWidth>> = Symbol('pageWidth')

/** The width a page should use: its own override, else its route's default. */
export function usePageWidth(override: () => PageWidth | undefined): ComputedRef<PageWidth> {
  const fromRoute = inject(PAGE_WIDTH_KEY, undefined)
  return computed(() => override() ?? fromRoute?.value ?? DEFAULT_PAGE_WIDTH)
}
