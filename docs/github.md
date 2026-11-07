# GitHub: issues, tickets and pull requests

Milestone 15 turns issues into pull requests. A repository on this computer is connected; its issues (from GitHub,
or local ones) land in an inbox; deciding an issue makes a **ticket**: one issue worked on in its own git worktree
and branch, by one or more tasks; when the work is accepted the ticket's branch is pushed and a draft pull request
opened, and its CI checks are followed until it is merged or closed. Everything lives in `seelegent.db` (migration
`0010_github_automations.sql`). `seelegent migrate` does not import the old version's GitHub folder: connect the
repositories again and store the token again ([setup.md](setup.md)).

| Package | What it does |
|---|---|
| `internal/github` | access tiers, the token store, the REST client and the `gh` runner, hosts, remotes, checks, redaction |
| `internal/ticket` | repositories, the inbox, triage, tickets, the write-ahead outbox, the pull request scan, check polling |
| `internal/gitx` | the safe git runner the ticket flow uses (no hooks, no prompts, no credential helpers that ask) |
| `internal/worktree` | the ticket worktrees under `<home>/worktrees/tickets` and their environment |

Routes: `/github/*` ([api.md](api.md)); MCP tool `issues` ([mcp.md](mcp.md)); `seelegent issues ...`
([cli.md](cli.md)); the Issues page of the dashboard.

## Reaching GitHub

The access tier is detected again at most once a minute (`GET /github/access?refresh=true` forces it):

| Tier | When | What works |
|---|---|---|
| `gh` | the GitHub CLI is installed and signed in | everything; Seelegent runs `gh api` (never through a shell) |
| `token` | no signed-in `gh`, and a token was stored in the dashboard | everything, through the REST API |
| `manual` | neither | the inbox of a public repository, pushing branches; each pull request is opened by the user from a compare link and recorded with *link PR* |

**Hosts.** Only `github.com` and the hosts listed in the `githubEnterpriseHosts` setting count as GitHub (old bug
D4: any remote whose URL contained "github" was treated as GitHub and could get the token). A remote on any other
host is "not GitHub": its repository still works locally, but no GitHub call is made for it and no token is ever
sent to it. An enterprise host is a host name (optionally with a port) or an `https` URL; plain `http` is allowed
only for a loopback address (a local test server). The REST client never sends the `Authorization` header on a
redirect to another host (job logs redirect to a storage host) or to another scheme on the same host (an `https`
to `http` redirect would send the token in clear).

**The token** is set and removed only from the dashboard (`PUT/DELETE /github/token` answer only the dashboard's
own requests, as the automation confirmation does, see [automations.md](automations.md)); no MCP tool or command
can set it. It is stored in one private file (`<home>/github/token`): a DPAPI blob for the current Windows user on Windows;
elsewhere the token in plain text in a 0600 file, which any program running as the same user can read (no OS key
store is used; a fine-grained token limited to the needed repositories, or the `gh` CLI, limits what a leak gives).
It is shown only as its last four characters. It is masked in every log line, error message,
ticket log and outbox row (`github.Redact`: the stored token, anything shaped like a GitHub token, `user:pass@` in
URLs, `Authorization` values).

## Repositories and the inbox

`POST /github/repos {path}` connects the repository at an absolute path (`init: true` runs `git init` there first,
with an empty first commit). Origin is read once and stored without credentials. *Disconnect* forgets the
repository and its inbox; the folder, its branches and its worktrees stay.

`POST /github/repos/{id}/remote {name?, private?}` (gh or token tier, a repository without origin) creates the
GitHub repository on github.com (private unless `private: false`), adds it as origin and pushes the default branch
once, the only push of a branch other than a ticket's. That first push sends every commit, so the whole history of
the branch is scanned first with the rules of the pull request scan below, every blob any commit added at every path
it had: a secret committed and deleted long ago refuses it (`409`, `details.reason: "history_unsafe"`, with
`branch` and `findings`) before anything is created. The user removes it from the history (for example with `git
filter-repo`) or pushes the branch to GitHub themselves. The creation is write-ahead (an outbox row of the
repository), and the created repository is recorded at once. A retry adopts a repository only when an earlier
attempt may have made it: the one it recorded, or, after a stop cut an attempt off (`unknown`), one of that name;
a repository someone else made is never taken over after a plain failure. An adopted repository must have the asked
visibility and be empty (or hold exactly the branch this publish sends), else the request is refused.

*Sync* fetches up to 100 open issues of a GitHub repository (pull requests are left out); the poller does the same
every `githubPollMinutes`. A local issue (`local#N`) never reaches GitHub. Issue keys are `gh#12` (also accepted as
`#12` or `12`) and `local#3`.

Issue text, labels and authors are untrusted. The dashboard shows them as plain text, and a worker gets them only
inside a fence it cannot close (`~~~~`, with any run of tildes in the text broken up), under a warning that the
text is data, not instructions.

## Triage and tickets

The lead may record a *suggestion* (no side effect). A *decision* is one of:

- `assign`: makes a ticket. Its branch is `seelegent/<n>-<slug>` from the default branch, its worktree is under
  `<home>/worktrees/tickets`, and a task starts there with the lead's brief plus the fenced issue.
- `meeting`: makes a ticket and opens a meeting in its worktree ([team.md](team.md)).
- `skip`: records the decision; a reason is required.

A ticket's status follows its tasks: `working` while one is pending, `ready` when the last one was accepted,
`review` otherwise. Then `pr_open` once a pull request exists, `done` when it is merged or closed, `dropped` by the
user or lead, `failed` when the ticket could not be set up. New work after the pull request opened puts it back to
`working`; the next *pr* pushes to the same pull request.

The user's checkout is never changed. Seelegent only runs `git worktree add -b seelegent/...`, a plain `git worktree
remove` of a clean ticket worktree (*cleanup*, for a finished ticket), and status, diff, add and commit inside the
ticket worktree, and pushes a commit of its own to the ticket's branch (below). It never force-pushes, merges, closes
issues or pushes any branch but the ticket's own.

## The pull request

`POST /github/tickets/{id}/pr` (ticket `ready` or `pr_open`, the repository has an origin):

Seelegent never pushes the worker's commits (the review's B1: a secret a worker committed and deleted again in a
later commit stayed in the pushed history). It pushes one commit of its own whose tree is the worktree as the commit
step leaves it. Its parent is the ticket's base on the first push and the commit Seelegent pushed before on every
later one, so each update is a fast-forward. When the base moved between two pushes (main was merged into the
worktree), the commit has two parents, the commit pushed before and the new base, and GitHub shows a merge. Its
message is the pull request's title (or `#n: title`), its author the repository's identity (Seelegent's own when
the repository has none). The worker's commits stay in the local worktree.

1. **Check the remote** before anything is written:
   - the remote branch must still be the commit Seelegent pushed before (none on the first push). A branch someone
     pushed to or changed on GitHub is never built on or forced: the request is refused (`409`, `details.reason:
     "branch_diverged"`, with `branch`, `remote` and `pushed`) and the push's outbox row is `failed`. Two remote
     tips the ticket did not record are adopted instead (log step `push_adopted`): a commit one of the ticket's own
     push rows names (`args.sha`, in any state: a push GitHub took while its recording was lost to a stop or a
     cancelled request), with that row's base; and, on a ticket with no recorded push, a branch the previous version
     pushed (the worker's own commits) when it is an ancestor of the worktree. That branch is public already: its
     commits since the base are scanned once, and what looks like a secret or junk is kept on the ticket as
     `exposed` (the same findings as below). It is a warning, not a block: removing such a file now does not
     unpublish it, so the dashboard asks the user to rotate the keys it held;
   - the base must already be on GitHub: on the first push, and whenever the base moved, every commit of the base
     must be reachable from the remote base branch, or those commits would leave unscanned. Otherwise the request is
     refused (`409`, `details.reason: "base_not_pushed"`, with `branch` and `commits`, the number of base commits
     GitHub does not have; `0` when the base branch is not on GitHub at all). The user pushes the base branch first.
2. **Scan** (old bug D5: the old version ran `git add -A` and committed `.env` files, keys and a file named `nul`).
   The tree to push is written through a temporary index (the worktree plus every uncommitted file git does not
   ignore, never the shared-folder links), and every entry it adds is scanned: each path whose blob differs from
   all the commit's parents (`diff-tree -r --no-renames`). Content the base brought in is not the ticket's and is
   not scanned again. The request is refused (`409`, `details.reason: "unsafe_files"`, with each file and why) when
   one of them is:
   - an environment file: `.env` or `.env.*`, except `.env.example`, `.env.sample`, `.env.template`, `.env.dist`;
   - a key or certificate store: `*.pem *.key *.p12 *.pfx *.jks *.keystore *.ppk`, or `id_rsa`, `id_dsa`,
     `id_ecdsa`, `id_ed25519`;
   - a credentials file: `.git-credentials`, `.netrc`, `_netrc`, `.pypirc`;
   - junk: a reserved Windows device name (`nul`, `con`, `prn`, `aux`, `com1-9`, `lpt1-9`, with any extension);
   - a symbolic link that points outside the worktree;
   - larger than 5 MB;
   - a file whose content holds a GitHub token (`ghp_`, `gho_`, `ghu_`, `ghs_`, `ghr_`, `github_pat_`), the
     stored GitHub token itself, an AWS access key (`AKIA…`), a private key block, a Slack token (`xox?-`), a Google
     API key (`AIza…`) or an Anthropic key (`sk-ant-`).

   There is no override: nothing is committed or pushed; the user deletes the file or adds it to `.gitignore` (a
   key or token inside a source file: takes it out of the file) and asks again. A scan that cannot run (a git
   error, or the worktree changed while it was read) refuses the request too (`409`, `details.reason:
   "scan_failed"`): nothing is ever sent unscanned. Each finding carries `kind` (`env`, `private_key`, `key_store`,
   `credentials`, `device`, `secret`, `large`, `link`) and `detail` (the store's extension, what the secret is such
   as `aws_key`, or the size in bytes) next to its English `reason`.

   A refusal (`unsafe_files`, `scan_failed`, `base_not_pushed`, `branch_diverged`) is kept on the ticket as
   `refusal {reason, findings, branch, commits, cause, detail, message, at}` and `needsAttention: "push_refused"`,
   so the dashboard explains it in the reader's language with the next step for each file; `message` is the English
   text the lead's tools show. The next pr clears it, and so does a new task in the ticket.
3. **Commit** exactly the uncommitted files git does not ignore, when there are any, in the worktree.
4. **Push** Seelegent's commit: `commit-tree` of the scanned tree (checked against the worktree's tree), then
   `push origin <sha>:refs/heads/<branch>` with no hooks (`core.hooksPath` set to the null device), no prompts
   (`GIT_TERMINAL_PROMPT=0`, `GCM_INTERACTIVE=never`) and never with force. Right before it the remote branch is
   read again; a branch that changed meanwhile is refused as above. The push runs on the daemon's own context with
   a 5 minute limit, not the request's: a caller that goes away does not cut it off half way. The commit it sent is
   recorded on the ticket (`pushedSha`, `pushedBase`) in the same transaction that marks its outbox row `done`.
5. **Open** a draft pull request (or adopt the one the branch already has). Its body is generated from the tasks'
   summaries with local paths removed (`file://` links, the worktree, the repository and the home folder in every
   spelling: either slash, the 8.3 short form such as `C:\Users\JOHNSM~1`, and WSL's `/mnt/c/...`). In the manual tier the
   answer carries a compare link (`pr.manualUrl`) and the ticket shows `needsAttention: "open_pr"` (the branch is
   pushed, no pull request exists yet); the user opens the pull request and records its number with `link-pr`. No
   tier opens a pull request on its own: a ready ticket waits for the lead or the user to ask.

Each step is a row of the **outbox**, written as `running` before it starts and `done` or `failed` after (old bug
D6: records are SQLite rows, and a row that cannot be read is an error, never an empty list). A row still `running`
when the daemon starts is `unknown`; before anything is retried it is checked against git and GitHub (is the commit
Seelegent made on the remote branch, does the pull request exist, does the issue hold the comment's marker) and
marked done when it had happened.

*Comment* (`POST /github/tickets/{id}/comment`) posts on the GitHub issue with the local paths and anything
token-like removed. Each comment ends with a hidden line `<!-- seelegent:<outbox entry id> -->`. Posting the same
text again after an attempt a stop cut off (`unknown`) reuses that entry: the issue's comments are searched for its
marker first, and the comment is posted only when it is not there, so it never appears twice.

## Checks

While a pull request is open its head commit's check runs and commit statuses are read on every poll. The ticket
shows `checks.state` (`pending`, `success`, `failure`, `unknown`) and `needsAttention: "checks_failed"` while the
open pull request's checks failed (conclusions `failure`, `timed_out`, `startup_failure`, `error`).

*Retry* (`POST /github/tickets/{id}/retry`, body as add-task with the prompt optional) starts the task of a
`failed` ticket again, in its worktree, optionally with another member, backend or model: its first task or
meeting could not start (an unknown member, no usable account). The ticket is `working` again; a retry that fails
leaves it `failed` with the new error. A ticket whose worktree could not be made cannot be retried: drop it and
decide the issue again.

*Fix checks* (`POST /github/tickets/{id}/fix-checks`) starts a task in the ticket's worktree with the failing
checks' names, their summaries and the tail of each GitHub Actions job log (at most 4,000 characters, ANSI codes
and timestamps removed), all fenced as untrusted. It refuses while a task of the ticket is still pending.

## Events

`ticket.updated` carries the ticket summary after every change; `issues.changed {repoId}` says to read a
repository's inbox again (`repoId` empty: the access changed).
