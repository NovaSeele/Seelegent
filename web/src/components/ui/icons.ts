/**
 * Inline icon set (no icon font, nothing downloaded). Each icon is a list of SVG elements on a 24x24 grid drawn
 * with `stroke="currentColor"`, rendered by Icon.vue without v-html. Shapes follow the old dashboard's icons.
 */
type Shape =
  | readonly ['path', string]
  | readonly ['circle', number, number, number]
  | readonly ['rect', number, number, number, number, number]
  | readonly ['line', number, number, number, number]
  | readonly ['polyline', string]
  /** A polyline drawn in the surface colour, to cut a mark out of a filled shape. */
  | readonly ['knock', string]
  /** A dashed circle (queued). */
  | readonly ['ring', number, number, number]
  /** A filled circle (status dots, brand core). */
  | readonly ['dot', number, number, number]

export type IconDef = readonly Shape[]

export const ICONS = {
  brand: [
    ['path', 'M12 2 3 7v10l9 5 9-5V7l-9-5z'],
    ['path', 'M12 22V12M3 7l9 5 9-5'],
    ['dot', 12, 12, 2.2],
  ],
  watch: [
    ['rect', 3, 3, 7.5, 7.5, 1.5],
    ['rect', 13.5, 3, 7.5, 7.5, 1.5],
    ['rect', 3, 13.5, 7.5, 7.5, 1.5],
    ['rect', 13.5, 13.5, 7.5, 7.5, 1.5],
  ],
  tasks: [['polyline', '22 12 18 12 15 21 9 3 6 12 2 12']],
  issues: [
    ['circle', 12, 12, 9],
    ['circle', 12, 12, 2],
  ],
  meetings: [
    ['path', 'M4 5h12v8H9l-5 4z'],
    ['path', 'M20 9v9l-4-3h-5'],
  ],
  contests: [
    ['path', 'M8 4h8v5a4 4 0 0 1-8 0z'],
    ['path', 'M8 6H5a3 3 0 0 0 3 4M16 6h3a3 3 0 0 1-3 4'],
    ['path', 'M12 13v4M8 20h8M10 17h4'],
  ],
  automations: [
    ['circle', 12, 13, 8],
    ['path', 'M12 9v4l2.5 2.5'],
    ['path', 'M9 2h6'],
    ['path', 'M19.5 5.5 18 7'],
  ],
  proof: [
    ['path', 'M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6z'],
    ['polyline', '8.5 12 11 14.5 15.5 10'],
  ],
  accounts: [
    ['path', 'M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2'],
    ['circle', 9, 7, 4],
    ['path', 'M23 21v-2a4 4 0 0 0-3-3.87'],
    ['path', 'M16 3.13a4 4 0 0 1 0 7.75'],
  ],
  usage: [
    ['path', 'M3 3v18h18'],
    ['rect', 7, 12, 3, 5, 0.5],
    ['rect', 12, 8, 3, 9, 0.5],
    ['rect', 17, 5, 3, 12, 0.5],
  ],
  roles: [
    ['rect', 2, 7, 20, 14, 2],
    ['path', 'M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16'],
  ],
  members: [
    [
      'path',
      'M21.3 6.4a5.2 5.2 0 0 1-6.3 5.1l-8.5 8.6a2.2 2.2 0 0 1-3.2-3.2l8.6-8.5a5.2 5.2 0 0 1 5.1-6.3l-3 3a1.5 1.5 0 0 0 0 2.1l2.8 2.8a1.5 1.5 0 0 0 2.1 0z',
    ],
  ],
  settings: [
    ['circle', 12, 12, 3],
    [
      'path',
      'M19.4 15a1.65 1.65 0 0 0 .33 1.82l.06.06a2 2 0 1 1-2.83 2.83l-.06-.06a1.65 1.65 0 0 0-1.82-.33 1.65 1.65 0 0 0-1 1.51V21a2 2 0 1 1-4 0v-.09A1.65 1.65 0 0 0 9 19.4a1.65 1.65 0 0 0-1.82.33l-.06.06a2 2 0 1 1-2.83-2.83l.06-.06A1.65 1.65 0 0 0 4.68 15a1.65 1.65 0 0 0-1.51-1H3a2 2 0 1 1 0-4h.09A1.65 1.65 0 0 0 4.6 9a1.65 1.65 0 0 0-.33-1.82l-.06-.06a2 2 0 1 1 2.83-2.83l.06.06A1.65 1.65 0 0 0 9 4.68a1.65 1.65 0 0 0 1-1.51V3a2 2 0 1 1 4 0v.09a1.65 1.65 0 0 0 1 1.51 1.65 1.65 0 0 0 1.82-.33l.06-.06a2 2 0 1 1 2.83 2.83l-.06.06A1.65 1.65 0 0 0 19.4 9a1.65 1.65 0 0 0 1.51 1H21a2 2 0 1 1 0 4h-.09a1.65 1.65 0 0 0-1.51 1z',
    ],
  ],
  search: [
    ['circle', 11, 11, 7.5],
    ['line', 21, 21, 16.65, 16.65],
  ],
  command: [['path', 'M9 6a3 3 0 1 0-3 3h12a3 3 0 1 0-3-3v12a3 3 0 1 0 3-3H6a3 3 0 1 0 3 3z']],
  more: [
    ['dot', 5, 12, 1.6],
    ['dot', 12, 12, 1.6],
    ['dot', 19, 12, 1.6],
  ],
  menu: [
    ['line', 4, 6, 20, 6],
    ['line', 4, 12, 20, 12],
    ['line', 4, 18, 20, 18],
  ],
  x: [
    ['line', 18, 6, 6, 18],
    ['line', 6, 6, 18, 18],
  ],
  minus: [['line', 5, 12, 19, 12]],
  plus: [
    ['line', 12, 5, 12, 19],
    ['line', 5, 12, 19, 12],
  ],
  check: [['polyline', '20 6 9 17 4 12']],
  chevronDown: [['polyline', '6 9 12 15 18 9']],
  chevronUp: [['polyline', '18 15 12 9 6 15']],
  chevronLeft: [['polyline', '15 18 9 12 15 6']],
  chevronRight: [['polyline', '9 18 15 12 9 6']],
  arrowLeft: [
    ['line', 19, 12, 5, 12],
    ['polyline', '12 19 5 12 12 5'],
  ],
  external: [
    ['path', 'M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6'],
    ['polyline', '15 3 21 3 21 9'],
    ['line', 10, 14, 21, 3],
  ],
  refresh: [
    ['polyline', '23 4 23 10 17 10'],
    ['polyline', '1 20 1 14 7 14'],
    ['path', 'M3.51 9a9 9 0 0 1 14.85-3.36L23 10M1 14l4.64 4.36A9 9 0 0 0 20.49 15'],
  ],
  copy: [
    ['rect', 9, 9, 12, 12, 2],
    ['path', 'M5 15H4a1 1 0 0 1-1-1V4a1 1 0 0 1 1-1h10a1 1 0 0 1 1 1v1'],
  ],
  info: [
    ['circle', 12, 12, 10],
    ['line', 12, 16, 12, 12],
    ['line', 12, 8, 12.01, 8],
  ],
  warning: [
    [
      'path',
      'M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z',
    ],
    ['line', 12, 9, 12, 13],
    ['line', 12, 17, 12.01, 17],
  ],
  error: [
    ['circle', 12, 12, 10],
    ['line', 15, 9, 9, 15],
    ['line', 9, 9, 15, 15],
  ],
  success: [
    ['circle', 12, 12, 10],
    ['polyline', '8 12.5 11 15.5 16 9.5'],
  ],
  offline: [
    ['line', 2, 2, 22, 22],
    ['path', 'M8.5 16.5a5 5 0 0 1 7 0'],
    ['path', 'M2 8.82a15 15 0 0 1 4.17-2.65'],
    ['path', 'M10.66 5c4.01-.36 8.14.9 11.34 3.76'],
    ['path', 'M16.85 11.25a10 10 0 0 1 2.22 1.68'],
    ['path', 'M5 13a10 10 0 0 1 5.24-2.76'],
    ['line', 12, 20, 12.01, 20],
  ],
  sun: [
    ['circle', 12, 12, 4.5],
    [
      'path',
      'M12 1.5v2M12 20.5v2M4.2 4.2l1.4 1.4M18.4 18.4l1.4 1.4M1.5 12h2M20.5 12h2M4.2 19.8l1.4-1.4M18.4 5.6l1.4-1.4',
    ],
  ],
  moon: [['path', 'M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z']],
  monitor: [
    ['rect', 2, 3, 20, 14, 2],
    ['path', 'M8 21h8M12 17v4'],
  ],
  palette: [
    ['path', 'M12 22a10 10 0 1 1 10-10c0 3-2.2 4-4 4h-2.5a2 2 0 0 0-1.5 3.3c.6.8.2 2.7-2 2.7z'],
    ['dot', 7.5, 11, 1.4],
    ['dot', 10, 7, 1.4],
    ['dot', 15, 7, 1.4],
  ],
  language: [
    ['circle', 12, 12, 10],
    ['line', 2, 12, 22, 12],
    [
      'path',
      'M12 2a15.3 15.3 0 0 1 4 10 15.3 15.3 0 0 1-4 10 15.3 15.3 0 0 1-4-10 15.3 15.3 0 0 1 4-10z',
    ],
  ],
  workers: [
    ['rect', 4, 4, 16, 16, 2],
    ['rect', 9, 9, 6, 6, 1],
    ['path', 'M9 1v3M15 1v3M9 20v3M15 20v3M20 9h3M20 14h3M1 9h3M1 14h3'],
  ],
  quota: [
    ['path', 'M12 14l3.5-3.5'],
    ['path', 'M3.34 19a10 10 0 1 1 17.32 0'],
  ],
  gates: [['path', 'M12 3 4.5 6v5.5c0 4.6 3.2 8.4 7.5 9.5 4.3-1.1 7.5-4.9 7.5-9.5V6z']],
  bell: [
    ['path', 'M6 9a6 6 0 1 1 12 0c0 6.5 2.5 8 2.5 8h-17S6 15.5 6 9'],
    ['path', 'M10.3 21a2 2 0 0 0 3.4 0'],
  ],
  keyboard: [
    ['rect', 2, 5, 20, 14, 2],
    ['path', 'M6 9h.01M10 9h.01M14 9h.01M18 9h.01M7 15h10'],
  ],
  webhook: [
    [
      'path',
      'M18 16.5a3 3 0 1 1-2.6 4.5M9 19.5H5a3 3 0 1 1 1.2-5.7l3.5-6M12 3a3 3 0 0 1 2.7 4.3l3.3 6',
    ],
  ],
  memory: [['path', 'M4 19.5v-15A2.5 2.5 0 0 1 6.5 2H20v20H6.5a2.5 2.5 0 0 1 0-5H20']],
  flask: [
    ['path', 'M9 3h6M10 3v6l-5.5 9.5A1.7 1.7 0 0 0 6 21h12a1.7 1.7 0 0 0 1.5-2.5L14 9V3'],
    ['path', 'M7.5 15h9'],
  ],
  sidebar: [
    ['rect', 3, 3, 18, 18, 2],
    ['line', 9, 3, 9, 21],
  ],
  compass: [
    ['circle', 12, 12, 10],
    ['path', 'M16.24 7.76 14.12 14.12 7.76 16.24 9.88 9.88z'],
  ],
  // Status shapes (status is icon + word + colour, never colour alone).
  statusQueued: [['ring', 12, 12, 9]],
  statusRunning: [
    ['circle', 12, 12, 9],
    ['path', 'M12 3a9 9 0 0 1 9 9'],
  ],
  statusWaiting: [
    ['circle', 12, 12, 9],
    ['polyline', '12 7 12 12 15.5 14'],
  ],
  statusReview: [
    ['circle', 12, 12, 9],
    ['polyline', '8.5 12 11 14.5 15.5 9.5'],
  ],
  statusAccepted: [
    ['dot', 12, 12, 9.5],
    ['knock', '8.5 12 11 14.5 15.5 9.5'],
  ],
  statusRejected: [
    ['path', 'M3.5 12a8.5 8.5 0 1 0 2.5-6L3 9'],
    ['polyline', '3 3.5 3 9 8.5 9'],
  ],
  statusBlocked: [
    ['path', 'M8.3 2.5h7.4l5.8 5.8v7.4l-5.8 5.8H8.3l-5.8-5.8V8.3z'],
    ['line', 9.5, 9, 9.5, 15],
    ['line', 14.5, 9, 14.5, 15],
  ],
  statusFailed: [
    ['path', 'M12 3.5 21.5 20h-19z'],
    ['line', 12, 10, 12, 14],
    ['line', 12, 17, 12.01, 17],
  ],
  statusCancelled: [
    ['circle', 12, 12, 9],
    ['line', 5.6, 5.6, 18.4, 18.4],
  ],
} as const satisfies Record<string, IconDef>

export type IconName = keyof typeof ICONS
