# Releases, installation and updates

Seelegent ships as one binary per platform, with a small companion launcher next to it, built by
[GoReleaser](https://goreleaser.com) from a `v*` tag. The installers (`scripts/install.ps1`, `scripts/install.sh`)
and `seelegent update` download those release archives, check them against the release's `checksums.txt`, and put
the binary at a stable path that the lead hosts' MCP entries keep pointing at.

## Release files

| File | Content |
|---|---|
| `seelegent_<version>_windows_<arch>.zip` | `seelegent.exe`, `seelegent-launch.exe`, `LICENSE`, `README.md`, `THIRD_PARTY_NOTICES.md` |
| `seelegent_<version>_<linux\|darwin>_<arch>.tar.gz` | `seelegent`, `seelegent-launch` and the same files |
| `checksums.txt` | `<sha256>  <file name>` for every archive |

`<version>` has no `v` (`seelegent_1.2.0_linux_arm64.tar.gz`), `<arch>` is `amd64` or `arm64`. The binaries are
built with `CGO_ENABLED=0` and `-trimpath`, with the dashboard embedded, and report `v<version>`, the short commit
and the commit date (`seelegent version`). The installers and the updater rely on these names: change
`.goreleaser.yaml`, both scripts and `internal/update` together.

### The companion launcher

`seelegent-launch` (`cmd/seelegent-launch`) runs exactly what `seelegent __launch` runs (`launch.Main`): the
detached process that starts one task's worker, waits for it and writes its exit record, one per running task for
the whole turn. It is a separate executable because it links no crypto package: every Go binary that imports one
(even `crypto/sha1`) carries a 32 MiB zero-filled entropy buffer (`crypto/internal/fips140/drbg`) that Windows charges
as commit to each process, so a launcher started as `seelegent __launch` costs about 51 MB private and the companion
about 12 MB. `TestLinksNoCrypto` in `cmd/seelegent-launch` fails when a change pulls a `crypto/` package into it
(for Windows, Linux and macOS); see [development.md](development.md#the-companion-launcher).

The daemon uses it only when that is safe, and falls back to `seelegent __launch` otherwise (which always works):

- it must be the file `seelegent-launch[.exe]` in the folder of the daemon's own executable (`os.Executable`, links
  resolved);
- its build information, read from the file without running it (`debug/buildinfo`), must say it is
  `cmd/seelegent-launch` of the daemon's own build: the same module version, Go version and build settings (VCS
  revision, time and modified flag, `-ldflags` with the version stamp, `GOOS`, `GOARCH`, ...). So both must be built
  by the same command with the same flags: `.goreleaser.yaml` shares them through YAML anchors, `make build`, `make
  cross` and `scripts/build.ps1` build both.

The daemon looks at the file for every launch (one `stat`) and reads its build information again only when its
path, modification time or size changed, so a companion that appears, goes or is replaced is noticed at the next
launch. Each change of decision is logged once in `logs/daemon.log`: info when it is used or absent, a warning with
the reason when a file is there but not of this build. `seelegent doctor` reports it as the `launcher` line (always
`ok`: launches work either way).

## Cutting a release

1. `main` is green in CI. CI also runs `goreleaser check` and a snapshot build (`goreleaser release --snapshot
   --clean`) of every archive, so a broken config shows up before the tag.
2. Tag the commit and push the tag (the lead asks the user first; agents never push or tag):
   `git tag -a v1.2.0 -m "Seelegent v1.2.0" && git push origin v1.2.0`. A tag with a pre-release part
   (`v1.2.0-rc.1`) becomes a GitHub pre-release; `releases/latest`, which the installers and `update` read, never
   points at a pre-release.
3. `.github/workflows/release.yml` runs on the tag in two jobs, so the token that can write the release never meets
   third-party build code (a compromised npm package could otherwise replace the archives and `checksums.txt`, the
   installers' only integrity anchor):
   - `build` (`contents: read`): Go from `go.mod`, pnpm and Node 24, `go test ./...`, then the dashboard
     (`pnpm --dir web install --frozen-lockfile`, `pnpm --dir web build`), handed on as an artifact.
   - `release` (`contents: write`): takes `web/dist` from the artifact and runs GoReleaser (version pinned in the
     workflow and the Makefile) with `release --clean --skip=before`, so the before hooks (pnpm again) do not run.
     It does not restore the Go cache the first job may have written. GoReleaser builds the six targets, writes the
     archives and `checksums.txt`, and publishes the GitHub release with a changelog from the commits (docs, test,
     chore and ci commits left out).

   Checkouts keep no credentials (`persist-credentials: false`), and every action in both workflows is pinned to a
   full commit SHA with its version in a comment; update them together, by SHA. A later step: build provenance
   with `actions/attest-build-provenance`, which the installers and `update` could then verify beyond the checksum.

Locally: `make cross` builds the six binaries into `dist/cross/` without GoReleaser; `make snapshot` and `make
release-check` run the pinned GoReleaser through `go run` (the first run downloads it).

## Installing

Windows (Windows PowerShell 5.1 or PowerShell 7):

```powershell
irm https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.ps1 | iex
```

Linux and macOS:

```sh
curl -fsSL https://raw.githubusercontent.com/NovaSeele/Seelegent/main/scripts/install.sh | sh
```

What the installers do, in order:

1. Read the release information: `https://api.github.com/repos/NovaSeele/Seelegent/releases/latest`, or the tag
   given with `-Version` / `--version` / `SEELEGENT_VERSION` (`v1.2.3` or `1.2.3`).
2. Download the archive for this OS and processor and `checksums.txt` into a temporary folder, and refuse to go on
   unless the archive's SHA-256 matches its line. Then run the extracted binary's `version --json`: it must run here
   and report the release's version. When the data folder has a database, the binary's `version --check-database`
   must accept it (see [Database schema](#database-schema-and-going-back)): a release that knows fewer migrations is
   refused, one that cannot tell (a build from before the check, which answers with code `usage`) is refused unless
   `-Force` / `--force` / `SEELEGENT_FORCE=1`, and any other failure of the check (a database that cannot be read,
   say) is refused with the binary's own error, `--force` or not.
3. Stop a running daemon of an earlier install (`seelegent stop`), then put the binary in place with renames in its
   folder only, so even a running `seelegent.exe` is replaced safely: the replaced binary is kept as
   `seelegent.exe.previous` (`seelegent.previous`) for `seelegent update --rollback`. An older kept binary is deleted
   only once the new one is in place. When this step fails, the old binary stays and the stopped daemon is started
   again. The companion launcher `seelegent-launch[.exe]` from the archive is then put next to it the same way (the
   replaced one kept as `seelegent-launch[.exe].previous`; a running one, a launcher of a task that goes on, is
   renamed aside); a release without one moves the installed one to `.previous`. A failure here is only a warning:
   the daemon runs launches as `seelegent __launch` without it. The installers do not read its build information;
   the daemon checks it before every use.
   - Windows: `%LOCALAPPDATA%\Programs\seelegent\bin\seelegent.exe`, the old version's install root.
   - Linux and macOS: `~/.local/bin/seelegent`.
   - `-InstallDir` / `--install-dir` / `SEELEGENT_INSTALL_DIR` choose another folder. Its path goes into PATH as
     text, so `install.sh` refuses `"`, `$`, a backquote, a backslash or a line break in it, both as given and once
     links are resolved, and `install.ps1` refuses `;`, `%` and `"`.
4. Windows only: the old (Node) installer wrote `bin\seelegent.cmd`, a launcher for `node ...\src\cli.js`. It is
   renamed to `seelegent-node.cmd`, so `seelegent` always runs the new binary (also for programs that look for
   `seelegent.cmd`) while the old version stays runnable as `seelegent-node`. A `seelegent.cmd` that is not the old
   launcher is left alone with a warning. The rest of the old version's files stay where they are.
5. Add the folder to PATH when it is missing: on Windows the user `Path` in the registry (kept `REG_EXPAND_SZ`, its
   `%VARIABLES%` unexpanded, then a settings-change broadcast so new terminals see it); elsewhere one `export
   PATH=...` line in the shell's profile (`~/.zshrc`, `~/.bashrc`, `~/.bash_profile` on macOS, a fish `conf.d` file,
   else `~/.profile`). `-NoPath` / `--no-modify-path` / `SEELEGENT_NO_MODIFY_PATH=1` leave PATH alone. A warning
   names another `seelegent` that comes first on PATH.
6. Run `seelegent migrate` when the data folder (`SEELEGENT_HOME`, else `~/.seelegent`) holds the old version's data
   (`settings.json`, `accounts.json`, `roles.json` or `memory/`) and no `seelegent.db` yet: the first install
   imports it, a later one never imports it again (run `seelegent migrate` yourself for that). When it does not run
   migrate but the last migrate left `migrate-errors.json` in the data folder (some items failed; a run without
   errors removes it), a warning says to fix what it reported and run `seelegent migrate` again.
7. Run `seelegent setup` (every detected lead host), unless `-NoSetup` / `--no-setup` / `SEELEGENT_NO_SETUP=1`.
8. Start the daemon again when step 3 stopped one, and print the next steps. When the daemon cannot start (for
   example the old version's daemon still holds the port), a warning says so.

Both scripts end with an error (exit code 1, or a terminating error under `irm | iex`, which keeps the window open)
on any failure. A failed `migrate` or `setup` does not undo the install; the error names the step to run again. Both
refuse to run inside a Seelegent worker. `install.ps1` is plain ASCII without a BOM, which Windows PowerShell 5.1
reads correctly, and it writes no text file (old C-B7: a BOM in `current.txt` broke every call); everything runs
inside one function, so `irm | iex` leaves no settings behind in the session.

## Updating

```text
seelegent update [--check | --apply | --rollback] [--version vX.Y.Z] [--force] [--json]
```

See [cli.md](cli.md) for the output. The steps of `--apply`:

1. Read the release information (latest, or `--version`) and compare it with this build. A development build
   (`dev`, a version between tags such as `v1.2.3-4-gabcdef`, `-dirty`, a GoReleaser snapshot) cannot tell what is
   newer: `--check` says so and `--apply` needs `--version`. With `--version`, that release is installed even when
   it is older.
2. Under `<data folder>/update/` (with a lock there and one for the executable in the user's cache folder,
   `<cache>/seelegent/update-<hash of the executable's path>.lock`, so two updates never run at once on the same data
   folder or the same executable; only without a cache folder is that lock `<exe>.update.lock` next to the
   executable; a `download-*` folder an interrupted update left there is removed first): download the archive, verify its
   SHA-256 against `checksums.txt` (no line, or another hash: refused, code `checksum_mismatch`), extract the binary
   and run its `version --json`. Its `schemaVersion` is compared with the database (read-only): see
   [Database schema](#database-schema-and-going-back) (`schema_too_new`, `schema_unknown`, `--force`). When the
   archive holds the companion launcher, it is extracted too and its build information must match the new binary's
   (as the daemon checks it, without running either); otherwise nothing is installed.
3. Copy them next to the installed files as `<exe>.new` and `<companion>.new`. Stop the daemon if it runs (`POST
   /api/v1/daemon/stop`, the same graceful stop as `seelegent stop`: open waits are answered with `daemon_stopping`
   and a lead's wait goes on against the new daemon).
4. Rename the installed companion to `<companion>.previous` and `<companion>.new` to `<companion>`, then the same for
   the binary: `<exe>` to `<exe>.previous` (an older kept file is moved to the trash first) and `<exe>.new` to
   `<exe>`. A release without a companion moves the installed one to `<companion>.previous`; a kept companion with
   no installed one belongs to an older version and goes to the trash. Windows allows renaming a running
   executable, so this works from the running `seelegent update` itself, while lead hosts run `seelegent mcp` and
   while launchers of running tasks run the companion (their tasks go on). A failed rename puts everything back.
5. Start the daemon again from the new binary when it ran before, and wait up to two minutes for it to answer as the
   new version (its first start may run database migrations). If it fails, answers late or answers as another
   build, that daemon is stopped (gracefully when it answers, else the exact process the update started is killed)
   and its lock must come free. Then the database's schema is read again: when that daemon already ran migrations
   the old binary does not know, the old binary could not open the database, so nothing is put back. The new binary
   stays installed (the old one is `<exe>.previous`, as after an update), no daemon runs and the update fails with
   `migrated_then_failed`: fix what `logs/daemon.log` says, then `seelegent restart` (or install a release that
   knows that schema). Otherwise the old binary is put back, its daemon started and its version checked; the update
   fails, the error says what runs now, and the executables are as before. Ctrl+C while the daemon starts still
   finishes this step. The companion goes back with the binary (a companion that cannot be put back is named in the
   error; launches then run as `seelegent __launch` until the next update).
6. Refresh the lead hosts (old C-B1): run the new binary's `setup --dry-run --json`, then `setup --host <ids>` for
   the hosts whose MCP entry already exists (action `unchanged` or `updated`). The entry's path did not change, so
   this mostly brings the lead skill to the new version. Hosts without an entry are not added, the reminder hook is
   not touched. Problems here are warnings; the update itself is done.
7. Delete leftovers: `<exe>.old-*` and `<companion>.old-*` files that could not be deleted while they ran (an MCP
   server of the old version keeps its file in use until its host restarts it, a launcher until its task's turn
   ends) are removed by a later update or rollback.

`--rollback` puts `<exe>.previous` back the same way (schema check, stop, rename, start, refresh), with
`<companion>.previous` (or, when none is kept, without a companion), and discards the version it rolls back from, so a second rollback reports `no_previous` instead of rolling forward again (old
C-B15). To go forward again, run `seelegent update --apply`. When the kept version's daemon does not start, the
rollback is undone and the kept executable stays for another try, unless that daemon migrated the database past the
running version (a kept version newer than the running one): then the rollback stays, as in step 5.

Lead hosts keep running the MCP server they started until they restart it; the daemon runs the new version at once.

### Database schema and going back

The database (`seelegent.db`) records which migrations ran. A daemon refuses a database with more migrations than
its build knows ("database schema is newer than this version of Seelegent", in `logs/daemon.log`), and migrations
are never undone: neither `--rollback` nor installing an older release takes the database back. So every build
reports `schemaVersion` (`seelegent version --json`, the number of migrations it has), and before anything is
stopped or replaced:

- `update --apply`, `update --rollback` and both installers refuse a build whose `schemaVersion` is lower than the
  database's (`schema_too_new`), `--force` or not. Going back past a migration is not possible: stay on a release
  that knows the database (or move the data folder aside and start fresh).
- A build that does not report `schemaVersion` cannot be checked (`schema_unknown`). `--force` (`-Force`,
  `SEELEGENT_FORCE=1` for the installers) installs it anyway with a warning; if its daemon then does not start, the
  database is newer than it.
- Without a database (a first install) there is nothing to check.

A first start after an update that adds migrations can take a while on a large database: `update` waits up to two
minutes. A daemon that still has not answered is treated as failed (above); its reason, like every start failure,
is in `logs/daemon.log`.

## Overrides

| Variable | Used by | Effect |
|---|---|---|
| `SEELEGENT_INSTALL_URL` | installers | Release information address (first choice). |
| `SEELEGENT_UPDATE_URL` | installers, `update` | Release information address (the installers' second choice), as in the old version. |
| `SEELEGENT_VERSION` | installers | Release to install. |
| `SEELEGENT_INSTALL_DIR` | installers | Folder of the binary. `update` always replaces the running binary. |
| `SEELEGENT_NO_MODIFY_PATH=1` | installers | Leave PATH alone. |
| `SEELEGENT_NO_SETUP=1` | installers | Do not run `seelegent setup`. |
| `SEELEGENT_FORCE=1` | installers | Install a release that does not report its `schemaVersion` (like `-Force` / `--force`). |

An address ending in `/releases/latest` is turned into `/releases/tags/<tag>` when a version is asked for; any other
address is used as it is and must name that version. Addresses must be `https`, or `http` to exactly `127.0.0.1`,
`localhost` or `[::1]` (a local test server); an address with user information (`user@host`) is refused. Downloads
follow at most five redirects, and every hop is checked the same way before it is requested: `update` checks each
redirect, and the installers follow redirects themselves instead of letting curl, wget or PowerShell do it (old
C-B15). `install.sh` uses curl, else GNU wget (BusyBox wget cannot stop at a redirect). The integrity check is the
release's own `checksums.txt`, fetched from the same release; there is no signature yet.

Testing the installers against a local server: serve a folder with GitHub-like JSON (`tag_name`, and `assets` with
`name` and `browser_download_url`) next to the archives and `checksums.txt`, then run the scripts with temporary
`USERPROFILE`/`HOME`, `LOCALAPPDATA`, `APPDATA`, `SEELEGENT_HOME`, a free `SEELEGENT_PORT`,
`SEELEGENT_NO_MODIFY_PATH=1` and `SEELEGENT_INSTALL_URL=http://127.0.0.1:<port>/releases/latest`, and a PATH
without real host CLIs (setup would otherwise run `claude mcp add`).
