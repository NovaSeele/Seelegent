# Security policy

## Supported versions

Security fixes go into the latest release only. Before you report, check that the problem still happens with the
latest release (`seelegent update` tells you whether a newer one exists; `seelegent version` prints yours).

| Version | Supported |
|---|---|
| Latest release | yes |
| Older releases | no: update with `seelegent update --apply` |
| The old Node version | no |

## Reporting a vulnerability

Please report security problems **privately**, not in a public issue, pull request or discussion.

1. Open a private report: **[Report a vulnerability](https://github.com/NovaSeele/Seelegent/security/advisories/new)**
   (the repository's **Security** tab, then **Report a vulnerability**). This is GitHub's private vulnerability
   reporting; only you and the maintainers can see the advisory.
2. Describe the problem, the version and OS, the steps to reproduce it, and what an attacker could do with it.
3. Leave out real secrets: mask keys, tokens, e-mail addresses and personal paths in logs and screenshots.

The maintainer replies in the advisory, works on a fix there, and publishes the advisory with the release that fixes
it. Say in the report whether and how you want to be credited.

## What is in scope

Problems in Seelegent's own code and release process, for example:

- **The local daemon and its HTTP API.** The daemon listens on `127.0.0.1` only. Every request must name a loopback
  `Host` (`127.0.0.1`, `localhost` or `[::1]` with the daemon's port), else it is refused with `421`, which guards
  against DNS rebinding. Changing requests need the `X-Seelegent: 1` header, and an `Origin`, when sent, must be the
  daemon's own, else `403`. A caller inside a worker's process tree may use only a small allow-list. Enabling an
  automation and storing the GitHub token answer only the dashboard's own requests. These checks stop web pages,
  MCP and CLI callers, and worker process trees; a web page or worker that gets around them is in scope. A program
  already running as your user that forges the headers is not (see the end of this page). See
  [docs/api.md](docs/api.md#request-checks-in-order).
- **Secrets.** API keys and the GitHub token are stored in their own files: encrypted with DPAPI on Windows, plain
  text with owner-only (0600) permissions elsewhere. The dashboard shows only their last four characters, the logs
  mask secrets, a worker's output is masked for its account's API key, and no MCP tool or command reaches the
  GitHub token. A secret that shows up
  unmasked in a log, a report, the dashboard or an API answer is in scope. See
  [docs/configuration.md](docs/configuration.md#data-directory).
- **The git guard and snapshots.** Workers are refused the git commands that can throw away uncommitted work or
  rewrite history, also when hidden inside shells, interpreters, pipes or git aliases. A command line that gets past
  the guard, or a turn that loses work the snapshots do not notice, is in scope. See [docs/safety.md](docs/safety.md).
- **The push to GitHub.** Before Seelegent pushes a ticket branch it scans every file it adds and refuses `.env`
  files, keys and certificates, credential files, tokens in file content and other risky files. A secret that gets
  through is in scope. See [docs/github.md](docs/github.md).
- **Automations.** Every automation is created **Off** and runs only after you confirm it in the dashboard. A way to
  enable one without that confirmation is in scope. See [docs/automations.md](docs/automations.md).
- **Installers and updates.** `scripts/install.ps1`, `scripts/install.sh` and `seelegent update` check every archive
  against the release's `checksums.txt`. A way to make them install something else is in scope. See
  [docs/release.md](docs/release.md).

## What Seelegent does not do

These are design decisions, not vulnerabilities:

- **It is not a proxy.** It does not pool accounts behind one endpoint or relay anyone's traffic. Every run is an
  ordinary call of a worker CLI (`agy`, `claude` or `codex`) on an account you own, and each provider's terms apply.
- **It never asks for passwords.** Each worker CLI signs in with its own flow and keeps its tokens in that account's
  profile folder under your data directory (`~/.seelegent/profiles/`), on your machine.
- **It sends no telemetry.** What leaves the machine is listed in the usage guide under
  [Data and privacy](docs/usage.md#data-and-privacy).
- **It is not a sandbox.** Worker CLIs run as your user, with your permissions. The git guard, the snapshots and the
  post-turn checks are guards that tell the lead and the dashboard what happened; they do not contain a worker. A
  worker that reaches outside its process tree through a service of the same user (the Windows Task Scheduler or
  WMI, `systemd-run --user`, launchd and the like) is a known limit.
- **Secrets outside Windows are plain files.** Outside Windows there is no OS key store Seelegent uses: anyone who
  can read your files can read the 0600 key and token files. Prefer a fine-grained GitHub token, or the `gh` CLI.

Out of scope as well: problems in the worker CLIs, lead hosts or providers themselves (report those to their
vendors), attacks that need an account already running as your user, and the development-only flags of
`seelegent daemon` (`--dev-origin`, `--pprof-addr`) used outside development.
