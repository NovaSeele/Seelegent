# Web UI layout

The rules the dashboard's layout follows (`web/`). The numbers live in `web/src/styles/tokens.css` and
`web/src/config/breakpoints.ts`; this page explains them.

## Breakpoints

| Name | Media query | Navigation |
| --- | --- | --- |
| mobile | `(width < 600px)` | 48px top bar + bottom bar (4 pages + More sheet) |
| compact | `600px <= width < 900px` | 56px icon rail, names in tooltips |
| rail | `(width >= 900px)` | 76px rail, icon with its name under it |
| short | `(height < 760px)` | tighter rail items (see below) |

CSS uses range syntax with exactly these numbers. `src/styles/styles.spec.ts` rejects any other number, and
any `min-width`/`max-width` query, in a media query.

## Page width

Every page is one of two kinds:

- **Readable**: documents and tables. These are Watch, Usage, Accounts, Roles, Members, Proof and Automations,
  the Issues, Meetings and Contests lists, and Settings. The content is centred and at most
  `--page-max-readable` (1200px) wide.
- **Full**: split-pane views. These are Tasks and task detail, Git, Tree/Graph and the terminal. They always use
  the whole workspace width, however wide the window is.

How the code does it:

- Each route sets its default in `meta.width` (`web/src/router/index.ts`). If it is not set, the page is
  readable. Only Tasks is `full` today.
- `AppShell` provides the width of the page on screen. While settings is open, that is the page behind the
  dialog, so the width does not change. The workspace shows it as `data-page-width`.
- `PageContainer` wraps a non-split page body. It follows the route's width and takes an override:
  `<PageContainer width="full">`.
- `SplitLayout` is always full width. A split view never sits inside a readable container.
- Settings is a dialog of at most 1080px (`Dialog` size `xl`), so it is readable by construction.
- Running text (page descriptions, settings help) wraps at `--measure-prose` (72ch) on both kinds of page.

## Rail metrics (from 900px)

| Part | Normal | Short window (height < 760px) |
| --- | --- | --- |
| Rail width | 76px (56px compact) | same |
| Item | min 44px; 48px with a one-line label | 44px |
| Icon pill | 36 × 28px | 36 × 24px |
| Gap between items | 2px | 0 |
| Divider between work and team pages | 8px above and below | 4px |
| Label | 11px, centred, at most 2 lines, breaks only between words | same |
| Footer buttons (palette, settings) | 36px; 44px on `pointer: coarse` | same |

- On a 1366×768 laptop the browser leaves about 680px. The short metrics fit all eleven pages there in every
  language: 11 items × 44–58px in about 540px of list.
- When the list still does not fit (longer labels, smaller windows), it scrolls. Its top and bottom edges fade
  over 24px, each only while there is more to scroll that way.
- Every rail item has a tooltip with the page name and its shortcut, for example "Members G › E".
- Page names are one label everywhere: rail, bottom bar, More sheet, palette, page heading and tab title.
  No single word may be wider than the 68px label box. `catalogs.spec.ts` allows at most 11 letters, with a CJK
  character counting as 2. Pick a shorter word rather than letting a word break.

## Workspace

- From 600px the workspace is an inset panel: 8px margin, `--radius-lg` corners and a subtle border.
- On phones it fills the screen between the top bar and the bottom bar.
- Split views: the list sidebar is 300px by default, resizable between 240 and 560px, and collapsible with Mod+B.
  Below 900px only one pane shows at a time.
