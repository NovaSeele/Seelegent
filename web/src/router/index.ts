/**
 * Routes. Every page is lazy except Watch, the home page: it is also what renders behind the settings dialog on
 * a cold open of /settings/<section>, and RouterView can only render a background route whose components are
 * already loaded.
 *
 * Settings is an overlay route: /settings/<section> opens the settings dialog over the page you came from
 * (the shell keeps rendering that page; see AppShell). Section changes replace the history entry, so Back
 * closes the dialog.
 */
import { createRouter, createWebHistory, type RouteRecordRaw } from 'vue-router'

import WatchPage from '@/features/watch/WatchPage.vue'
import type { NavId } from '@/config/nav'

declare module 'vue-router' {
  interface RouteMeta {
    /** i18n key of the page title (document title, headings). */
    titleKey?: string
    /** The nav entry highlighted for this route. */
    nav?: NavId
    /** Rendered as an overlay over the previous page. */
    overlay?: boolean
  }
}

export const routes: RouteRecordRaw[] = [
  {
    path: '/',
    name: 'watch',
    component: WatchPage,
    meta: { nav: 'watch', titleKey: 'nav.watch.label' },
  },
  { path: '/watch', redirect: '/' },
  {
    path: '/tasks/:taskId?',
    name: 'tasks',
    component: () => import('@/features/tasks/TasksPage.vue'),
    props: true,
    meta: { nav: 'tasks', titleKey: 'nav.tasks.label' },
  },
  {
    path: '/issues/:ticketId?',
    name: 'issues',
    component: () => import('@/features/issues/IssuesPage.vue'),
    props: true,
    meta: { nav: 'issues', titleKey: 'nav.issues.label' },
  },
  {
    path: '/meetings/:meetingId?',
    name: 'meetings',
    component: () => import('@/features/meetings/MeetingsPage.vue'),
    props: true,
    meta: { nav: 'meetings', titleKey: 'nav.meetings.label' },
  },
  {
    path: '/contests/:contestId?',
    name: 'contests',
    component: () => import('@/features/contests/ContestsPage.vue'),
    props: true,
    meta: { nav: 'contests', titleKey: 'nav.contests.label' },
  },
  {
    path: '/automations',
    name: 'automations',
    component: () => import('@/features/automations/AutomationsPage.vue'),
    meta: { nav: 'automations', titleKey: 'nav.automations.label' },
  },
  {
    path: '/proof',
    name: 'proof',
    component: () => import('@/features/proof/ProofPage.vue'),
    meta: { nav: 'proof', titleKey: 'nav.proof.label' },
  },
  {
    path: '/accounts',
    name: 'accounts',
    component: () => import('@/features/accounts/AccountsPage.vue'),
    meta: { nav: 'accounts', titleKey: 'nav.accounts.label' },
  },
  {
    path: '/usage',
    name: 'usage',
    component: () => import('@/features/usage/UsagePage.vue'),
    meta: { nav: 'usage', titleKey: 'nav.usage.label' },
  },
  {
    path: '/roles',
    name: 'roles',
    component: () => import('@/features/roles/RolesPage.vue'),
    meta: { nav: 'roles', titleKey: 'nav.roles.label' },
  },
  {
    path: '/members',
    name: 'members',
    component: () => import('@/features/members/MembersPage.vue'),
    meta: { nav: 'members', titleKey: 'nav.members.label' },
  },
  {
    path: '/settings/:section?',
    name: 'settings',
    // The dialog itself lives in the shell; the route only carries the state.
    component: WatchPage,
    meta: { overlay: true, titleKey: 'nav.settings.label' },
  },
  {
    path: '/:pathMatch(.*)*',
    name: 'not-found',
    component: () => import('@/features/not-found/NotFoundPage.vue'),
    meta: { titleKey: 'pages.notFound.title' },
  },
]

export function createAppRouter(history = createWebHistory(import.meta.env.BASE_URL)) {
  // No scrollBehavior: the document never scrolls; the shell's workspace does (AppShell resets it per page).
  return createRouter({ history, routes })
}

export const router = createAppRouter()
