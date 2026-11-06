# JetBrains (IntelliJ Platform) file icons

- Source: https://github.com/JetBrains/intellij-community
  (`platform/icons/src/expui/fileTypes/`, `platform/icons/src/expui/nodes/`,
  `plugins/markdown/core/resources/icons/expui/`)
- Commit: ad5656c6ce5479854d74442b21118d3cd7b482b5 (release branch 261, committed 2026-11-05), fetched 2026-11-06.
  Pinned to branch 261 on purpose: there `LICENSE.txt` is the Apache License 2.0. On master and the 262/263
  branches `LICENSE.txt` was replaced by the "JetBrains Open-Source Build Terms" (which describe the builds
  and state that they consist of Apache-2.0 software); the icons copied here come from the Apache-2.0 tree.
- Licence: Apache License 2.0 (see `LICENSE.txt` and `NOTICE.txt` from that commit, copied unchanged).
  Each SVG keeps its JetBrains copyright comment.
- Changes: none to the artwork. Each SVG went through Seelegent's sanitizer (only drawing elements kept,
  no scripts, event handlers or external references; whitespace normalised).
- `x.svg` is IntelliJ's light-theme icon and `x_dark.svg` its dark-theme icon; Seelegent picks by theme.

| File (+ `_dark`) | Used for (ui/js/fileicons.js) |
| --- | --- |
| javaScript.svg | kind `js` |
| css.svg | kind `css` |
| json.svg | kind `json` |
| markdown.svg | kind `md` |
| html.svg | kind `html` |
| test.svg (nodes) | kind `test` |
| config.svg | kind `cfg` |
| shell.svg | kind `sh` |
| image.svg | kind `img` |
| folder.svg (nodes) | kind `folder` |
| anyType.svg | kind `file`, and `ts` / `rs` (no TypeScript or Rust icon in IntelliJ Community) |
| gitignore.svg (light only) | `.gitignore` |
