# Material Icon Theme file icons

- Source: https://github.com/PKief/vscode-material-icon-theme (GitHub now redirects it to
  material-extensions/vscode-material-icon-theme), folder `icons/`
- Commit: f368264282ef14ee7feb012fd54e825181eff9c3 (branch main, committed 2026-11-03), fetched 2026-11-06
- Licence: MIT, Copyright (c) 2025 Material Extensions (see `LICENSE`, copied unchanged)
- Changes: none to the artwork. Each SVG went through Seelegent's sanitizer (only drawing elements kept,
  no scripts, event handlers or external references; whitespace normalised).

| File | Used for (ui/js/fileicons.js) |
| --- | --- |
| javascript.svg | kind `js` |
| typescript.svg | kind `ts` |
| css.svg | kind `css` |
| json.svg | kind `json` |
| markdown.svg | kind `md` |
| html.svg | kind `html` |
| test-js.svg | kind `test` |
| test-ts.svg | `*.test.ts`, `*.spec.ts` |
| settings.svg | kind `cfg` |
| console.svg | kind `sh` |
| image.svg | kind `img` |
| rust.svg | kind `rs` |
| nodejs.svg | `package.json` |
| git.svg | `.gitignore` |
| readme.svg | `README.md` |
| docker.svg | `Dockerfile` |
| tsconfig.svg | `tsconfig.json` |
| folder-src.svg | folder `src/` |

Not available: Material's plain `file.svg` and `folder.svg` are generated at build time and are not in the
repository (see its `.gitignore`), so kinds `file` and `folder` keep the Seelegent glyph.
None of these icons has a `_light` variant in the repository.
