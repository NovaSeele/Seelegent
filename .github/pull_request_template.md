<!-- Title: a Conventional Commits summary, for example "fix(pool): skip accounts that are resting". -->

## Summary

<!-- What changed and why, in a few sentences. -->

## Linked issue

<!-- "Closes #123", or why there is none. -->

## How it was tested

<!-- The commands you ran and what you checked by hand (OS, worker CLI, lead host). -->

```sh

```

## Checklist

- [ ] `go build ./...`, `go vet ./...` and `go test ./...` pass
- [ ] `make lint` passes
- [ ] `web/` changed: `pnpm lint`, `pnpm typecheck`, `pnpm test` and `pnpm build` pass (or `web/` did not change)
- [ ] Docs updated for anything users or lead agents can see (`docs/`, `README.md`), or nothing visible changed
- [ ] No new Go or npm dependencies, or the maintainer agreed to them in the issue
- [ ] The title follows Conventional Commits
- [ ] No secrets, tokens, e-mail addresses or personal paths in the diff
- [ ] Tests use temp dirs and random ports, never real accounts

See [CONTRIBUTING.md](https://github.com/NovaSeele/Seelegent/blob/main/CONTRIBUTING.md) for the details.
