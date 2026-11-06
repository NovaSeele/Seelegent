# Seti file icons

- Source: https://github.com/jesseweed/seti-ui (folder `icons/`)
- Commit: 2d6c5e68b4ded73c92dac291845ee44e1182d511 (branch master, committed 2025-10-28), fetched 2026-11-06
- Licence: MIT, Copyright (c) 2014 Jesse Weed (see `LICENSE.md`, copied unchanged)
- Changes: none to the artwork. Each SVG went through Seelegent's sanitizer (only drawing elements kept,
  no scripts, event handlers or external references; whitespace normalised).

| File | Used for (ui/js/fileicons.js) |
| --- | --- |
| javascript.svg | kind `js`, kind `test` (Seti shows tests with the language icon) |
| typescript.svg | kind `ts`, `*.test.ts`, `*.spec.ts` |
| css.svg | kind `css` |
| json.svg | kind `json` |
| markdown.svg | kind `md` (README.md included) |
| html.svg | kind `html` |
| config.svg | kind `cfg` |
| shell.svg | kind `sh` |
| image.svg | kind `img` |
| folder.svg | kind `folder` |
| npm.svg | `package.json` |
| git.svg | `.gitignore` |
| docker.svg | `Dockerfile` |
| tsconfig.svg | `tsconfig.json` |

Not used: `rust.svg`, `default.svg` and `info.svg` have no fill colour (they would draw black, invisible
on dark themes), so kinds `rs` and `file` keep the Seelegent glyph and README.md uses `markdown.svg`.
