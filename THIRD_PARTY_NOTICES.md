# Third-party notices

Seelegent ships the third-party works listed here. Each one keeps its own licence text and source notes next
to its files. Those notes are copied unchanged from the old dashboard (`ui/icons/filesets/`).

## File icon sets

These are the icon sets under Settings › Appearance › File icons. They live in `web/public/fileicons/`, and the
web build copies them, licence files included, to `web/dist/fileicons/`. The artwork is unchanged. Each SVG
went through Seelegent's sanitizer: only drawing elements are kept, with no scripts, event handlers or outside
references, and `web/src/lib/fileicons.spec.ts` checks this. The icons are drawn as `<img>`, which never runs
scripts. Which file draws which file type is in `web/src/lib/fileicons.ts`; the `SOURCE.md` tables were written
for the old `ui/js/fileicons.js` and match the same mapping.

| Set | Folder | Licence | Source |
| --- | --- | --- | --- |
| Seti | `web/public/fileicons/seti/` | MIT, Copyright (c) 2014 Jesse Weed (`LICENSE.md`) | https://github.com/jesseweed/seti-ui, commit 2d6c5e68b4ded73c92dac291845ee44e1182d511 |
| Material Icon Theme | `web/public/fileicons/material/` | MIT, Copyright (c) 2025 Material Extensions (`LICENSE`) | https://github.com/PKief/vscode-material-icon-theme (now material-extensions/vscode-material-icon-theme), commit f368264282ef14ee7feb012fd54e825181eff9c3 |
| JetBrains (IntelliJ Platform) | `web/public/fileicons/jetbrains/` | Apache License 2.0, Copyright JetBrains s.r.o. (`LICENSE.txt`, `NOTICE.txt`) | https://github.com/JetBrains/intellij-community, commit ad5656c6ce5479854d74442b21118d3cd7b482b5 (branch 261) |
| Catppuccin | `web/public/fileicons/catppuccin/` | MIT, Copyright (c) 2023 Catppuccin and Copyright (c) 2023 thang-nm (`LICENSE`) | https://github.com/catppuccin/vscode-icons, commit b6915da9f6889b683a110aa747de96c2820a537d |

### Notes on individual sets

- **JetBrains** is pinned to commit ad5656c6 on purpose. At that commit `LICENSE.txt` is the Apache License 2.0.
  Later branches replaced it with the "JetBrains Open-Source Build Terms". Every SVG keeps its JetBrains
  copyright comment, and `NOTICE.txt` ships with the icons.
- **Catppuccin** keeps all four flavour folders as upstream has them (latte, frappe, macchiato, mocha). The
  dashboard draws Latte on light themes and Mocha on dark ones.
- **Seelegent** and **Seelegent mono** are Seelegent's own glyphs and are covered by this repository's `LICENSE`.
