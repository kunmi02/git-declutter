# GitDeclutter — safe local Git branch cleanup

**Know what's safe to delete before you delete it.**

GitDeclutter (`git declutter`) is a command-line tool that finds stale local Git branches and classifies each one as **SAFE**, **REVIEW**, **KEEP**, or **PROTECTED** before you delete anything.

This is the official repository: https://github.com/kunmi02/git-declutter

It is not the older Python project also named `git-declutter`. That tool turns a folder of copied files into a Git repo. This project is a Go CLI for safe branch cleanup.

A merged pull request does not always mean your current local branch contains no unique work. GitDeclutter combines local Git history, remote tracking state, and GitHub/GitLab pull request metadata — and explains why.

It checks for things like:

- local-only commits
- commits added after a PR was merged
- diverged branches
- deleted remote branches
- active worktrees
- protected branch patterns
- incomplete or ambiguous merge state

If GitDeclutter is not confident that a branch is safe to remove, it will not recommend automatic deletion.

---

## Install

GitDeclutter is a compiled binary. You need **Git** to run it. You do **not** need Go after it is installed.

Put `git-declutter` on your `PATH`. Git then exposes it as a subcommand:

```bash
git declutter scan
```

### With Go

Requires [Go](https://go.dev/dl/) 1.25+:

```bash
go install github.com/kunmi02/git-declutter@latest
```

`GOBIN` (or `$(go env GOPATH)/bin`) must be on your `PATH`. Without Go, this command fails with `command not found: go`.

### Without Go

Download a prebuilt archive for your OS and CPU from [GitHub Releases](https://github.com/kunmi02/git-declutter/releases), then put `git-declutter` on your `PATH`.

macOS / Linux:

```bash
tar -xzf git-declutter_*_darwin_arm64.tar.gz   # or linux_amd64, darwin_amd64, …
sudo mv git-declutter /usr/local/bin/
git declutter version
```

Windows: unzip `git-declutter_*_windows_amd64.zip` and add `git-declutter.exe` to `PATH`.

Homebrew will be the long-term primary install (`brew install git-declutter`). It is not available yet.

Until a `v*` tag has been pushed, Releases will be empty and `go install` is the only path.

## Example

```text
$ git declutter scan

GitDeclutter
Repository: payments-api

Scanning 34 local branches...

SAFE TO REMOVE                                    12

✓ feature/oauth
  PR #418 merged · remote deleted · no local changes

✓ fix/payment-timeout
  Fully merged into main


NEEDS REVIEW                                      3

⚠ experiment/cache
  Remote deleted · no merged PR found


KEEP                                              4

✕ feature/refunds
  3 commits exist only locally


PROTECTED                                         3

🔒 main
🔒 release/*
🔒 hotfix/production

12 safe · 3 review · 4 keep · 3 protected
```

## Commands

| Command | Purpose |
| --- | --- |
| `git declutter scan` | Analyze local branches. Never deletes anything. |
| `git declutter why <branch>` | Explain one branch's classification. |
| `git declutter clean` | Interactively remove SAFE branches (recoverable). |
| `git declutter clean --dry-run` | Preview cleanup with no changes. |
| `git declutter clean --safe-only --yes` | Non-interactive SAFE cleanup. |
| `git declutter clean --permanent` | Delete without GitDeclutter recovery refs. |
| `git declutter restore <branch>` | Restore a recoverable branch. |
| `git declutter history` | List recoverable deletions. |
| `git declutter gc` | Expire recovery refs past retention. |
| `git declutter config` | View or change configuration. |

### Scan flags

```bash
git declutter scan --json
git declutter scan --offline
git declutter scan --refresh
git declutter scan --branch feature/foo
```

`--refresh` runs:

```bash
git fetch --prune
```

and prints:

```text
Refreshing remote references...
```

Default scans do not prune remote-tracking references.

---

## Safety model

GitDeclutter is designed around one principle:

> **Safety analysis first. Cleanup second.**

| Status | Meaning |
| --- | --- |
| **SAFE** | Strong evidence that deleting the local branch will not lose unique work. Eligible for automatic cleanup. |
| **REVIEW** | The branch looks stale, but available evidence is incomplete or ambiguous. Never selected automatically. |
| **KEEP** | Evidence suggests active or unique work exists. Never automatically deleted. |
| **PROTECTED** | Current branch, default branch, worktree branch, or a configured protected pattern. |

False negatives are acceptable.

False positives are not.

If GitDeclutter is uncertain, it chooses the safer classification.

A merged PR alone does **not** automatically make a branch SAFE.

For example:

```text
PR merged at C

A──B──C

Local branch later becomes:

A──B──C──D──E
```

Even though the PR was merged, commits `D` and `E` may only exist locally.

GitDeclutter detects situations like this before recommending cleanup.

---

## How branch analysis works

GitDeclutter uses multiple signals rather than relying only on:

```bash
git branch --merged
```

Depending on the repository and provider, it can consider:

- whether the branch is fully merged into the default branch
- whether a GitHub pull request or GitLab merge request was merged
- the PR/MR head commit
- whether local HEAD moved after the PR was merged
- whether local commits exist only on the machine
- whether the branch diverged from its previously merged state
- whether the remote branch still exists
- whether the branch is currently checked out
- whether another Git worktree is using the branch
- whether the branch matches configured protection rules

Provider failures or missing metadata are never treated as positive evidence that deletion is safe.

---

## Explain a decision

Use `why` to inspect exactly how GitDeclutter classified a branch:

```bash
git declutter why feature/payments
```

Example:

```text
feature/payments

Status: KEEP

✓ Pull request was merged
✓ Remote branch was deleted
⚠ Local branch contains commits added after the merged PR
⚠ 2 commits exist only locally

Recommendation: KEEP
```

The goal is that every recommendation is explainable rather than being a simple "delete / don't delete" decision.

---

## Recovery

Normal cleanup is recoverable by default.

Before deleting a local branch, GitDeclutter preserves its commit using a hidden Git reference:

```text
refs/git-declutter/recovery/<event-id>/<branch>
```

The default retention period is:

```text
30 days
```

During that period, you can restore the branch:

```bash
git declutter restore feature/login
```

You can also inspect previous cleanup operations:

```bash
git declutter history
```

### Configure retention

```bash
git declutter config set recovery.retention 7d
git declutter config set recovery.retention 30d
git declutter config set recovery.retention 90d
git declutter config set recovery.retention forever
```

### Permanent deletion

```bash
git declutter clean --permanent
```

or:

```bash
git declutter clean --hard
```

Permanent deletion creates no GitDeclutter recovery reference.

Git itself may still retain unreachable objects through reflogs and normal garbage collection behavior. GitDeclutter does not claim to physically erase Git objects from the repository.

---

## Cleanup safety

GitDeclutter revalidates branches immediately before deletion.

If a branch changes after it was analyzed, it will be skipped instead of being deleted.

For example:

```text
Skipping feature/payments:
branch changed since it was analyzed.
```

Branches classified as:

```text
REVIEW
KEEP
PROTECTED
```

are never automatically deleted.

Only **SAFE** branches are eligible for automatic cleanup.

---

## Configuration

Global configuration is stored in the operating system's standard configuration directory.

Examples:

```text
macOS:
~/Library/Application Support/git-declutter/

Linux:
~/.config/git-declutter/
```

You can also define repository-specific configuration using:

```text
.gitdeclutter.yml
```

Example:

```yaml
version: 1

protected:
  - main
  - develop
  - release/*
  - production

recovery:
  enabled: true
  retention: 30d

cleanup:
  requireRemoteDeleted: true
```

Default protected patterns include:

```text
main
master
develop
development
release/*
production
prod
```

---

## GitHub and GitLab

GitDeclutter can enrich local Git analysis using pull request or merge request metadata.

Supported authentication methods include existing developer tooling and environment variables.

### GitHub

```text
gh
GITHUB_TOKEN
```

### GitLab

```text
glab
GITLAB_TOKEN
```

GitDeclutter does not copy provider tokens into its own configuration.

---

## Offline mode

GitDeclutter can operate without contacting GitHub or GitLab:

```bash
git declutter scan --offline
```

Offline mode relies only on local Git information such as:

- refs
- commit ancestry
- remote-tracking branches
- worktree state
- configuration

If Git alone cannot prove that a branch is safe, the branch may be classified as **REVIEW**.

---

## JSON output

GitDeclutter can produce machine-readable output:

```bash
git declutter scan --json
```

This can be used for:

- scripts
- automation
- editor integrations
- future desktop or IDE interfaces

Example:

```json
{
  "repository": {
    "defaultBranch": "main",
    "provider": "github"
  },
  "summary": {
    "safe": 12,
    "review": 3,
    "keep": 4,
    "protected": 3
  }
}
```

---

## Privacy

GitDeclutter is local-first.

It does not require a GitDeclutter account.

It does not upload:

- repository source code
- file contents
- patches
- diffs
- commit messages

to GitDeclutter-owned infrastructure.

Provider APIs are contacted only when necessary to retrieve repository metadata such as:

- repository owner/name
- branch names
- commit SHAs
- pull request state

There is currently **no telemetry**.

---

## Supported platforms

GitDeclutter is designed to run on:

- macOS
- Linux
- Windows

Prebuilt binaries are published through GitHub Releases.

---

## Development

Clone the official repository:

```bash
git clone https://github.com/kunmi02/git-declutter.git
cd git-declutter
```

Run tests:

```bash
make test
```

Build locally:

```bash
make build
make dist        # cross-compile into ./dist (needs Go)
```

### Cut a GitHub Release

Pushing a version tag publishes macOS, Linux, and Windows binaries via GoReleaser (see `.github/workflows/release.yml`).

```bash
git checkout main
git pull
git tag v0.1.0
git push origin v0.1.0
```

`git declutter version` in those binaries reports the tag (for example `0.1.0`). Local `make build` without a tag reports `0.1.0-dev`.

Dry-run without publishing (needs [GoReleaser](https://goreleaser.com)):

```bash
make snapshot
```

---

## Project philosophy

There are already many ways to delete Git branches.

GitDeclutter is not trying to make:

```bash
git branch -D
```

easier.

The goal is to make the decision **before** deletion safer.

The core product promise is:

> **Know what's safe to delete before you delete it.**

---

## FAQ

### How do I delete merged Git branches safely?

Run `git declutter scan` first. It never deletes anything. Only branches classified **SAFE** are eligible for `git declutter clean`. **REVIEW**, **KEEP**, and **PROTECTED** branches are never deleted automatically.

### Does a merged pull request mean the local branch is safe to delete?

No. Commits added after the PR merged, or commits that exist only on your machine, can still be unique local work. GitDeclutter checks for that before recommending cleanup.

### Is this the same as the Python `git-declutter` project?

No. [dustmop/git-declutter](https://github.com/dustmop/git-declutter) rebuilds a tidy repo from copied files. This repository is the Go CLI `git declutter` for stale local branch cleanup.

### Where is the official GitDeclutter repository?

**https://github.com/kunmi02/git-declutter**

Install from that module path only:

```bash
go install github.com/kunmi02/git-declutter@latest
```

---

## Official project

GitDeclutter is developed and maintained from the canonical repository:

**https://github.com/kunmi02/git-declutter**

If you find GitDeclutter useful, consider giving the repository a ⭐ **star**.

If you want to experiment with the project or contribute improvements, feel free to 🍴 **fork** the repository and open a pull request.
