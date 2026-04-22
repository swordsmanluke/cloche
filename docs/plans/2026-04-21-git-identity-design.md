# Bot Git Identity & Push Credentials Design

**Date:** 2026-04-21
**Status:** Implemented

## Problem

Cloche commits — both the extraction squash commit produced when a container
workflow finishes, and the host-side rebase/merge commits produced by the
scaffolded merge scripts — were hardcoded to the `cloche <cloche@local>`
identity. Two consequences fall out of that:

1. **Review separation.** A maintainer who wants to review and approve a
   cloche-authored PR on GitHub can't do so when the commits appear to come
   from their own account. GitHub requires a distinct author for PR approval.
2. **Auditability.** There is no way in `git log` / `git blame` to tell apart
   changes made by the developer from changes made by a cloche-driven run.

Separately, any workflow script that wants to `git push` the extracted branch
(so a PR opens automatically after a run) has no standard way to do so under
the bot identity — the user has to roll their own SSH key plumbing and hope
their script doesn't pick up the personal default from `~/.ssh/`.

## Solution

Three changes, layered:

**1. `[git]` config block** — Add a first-class config section for the bot
identity. Global defaults live in `~/.config/cloche/config`; per-project
overrides live in `.cloche/config.toml`. A new `config.LoadMerged` helper
returns the merged view (project wins when set).

**2. Identity surfaced to every commit path** — Extraction commits in
`internal/adapters/docker/extract.go` now take `AuthorName` / `AuthorEmail`
via `ExtractOptions`, populated from the merged config at the gRPC
executor. Host scripts and agent steps receive
`CLOCHE_GIT_AUTHOR_NAME` / `CLOCHE_GIT_AUTHOR_EMAIL` env vars, injected by
`internal/host/executor.go`. The scaffolded `prepare-merge` / `merge`
Python scripts, `merge-to-base.sh`, and the `fix-merge-conflict` prompt
all read those env vars with a `cloche` / `cloche@local` fallback so they
still work when nothing is configured.

**3. Push credential convention** — A new `[git] ssh_key` config key names
a private key file. The host executor composes
`CLOCHE_GIT_SSH_COMMAND=ssh -i "<expanded-path>" -o IdentitiesOnly=yes`
and passes it to host scripts. Any workflow script that pushes follows
the convention:

```bash
GIT_SSH_COMMAND="$CLOCHE_GIT_SSH_COMMAND" git push …
```

Cloche itself never runs `git push`; the contract is between the config
and the scripts.

The `cloche init` flow is extended to help a user set up a per-project
overriding key when that's what they want.

## Design Details

### Config shape

```toml
# ~/.config/cloche/config  or  <project>/.cloche/config.toml
[git]
name    = "cloche-bot"
email   = "123+cloche-bot@users.noreply.github.com"
ssh_key = "~/.ssh/cloche_bot"       # expanded; used for push only
```

All three fields are optional. Unset values fall through from global →
project; if neither is set, commits attribute to `cloche <cloche@local>`
and no SSH command is composed.

`mergeInto` currently only merges fields under `[git]` — extend it as
new overrideable settings appear. Merge semantics for strings: non-empty
wins. No deep merge.

### Identity flow for commits

**Extraction commit (daemon-side, host execution).** `ExtractOptions`
gains `AuthorName` / `AuthorEmail` fields. Empty values fall back to
the built-in defaults inside `extractGit`, so every existing caller
(including tests) continues to work unchanged. The gRPC executor calls
`config.LoadMerged(projectDir)` once per extraction and passes the
resolved identity into the options.

**Host scripts / agents.** `host.Executor.gitIdentityEnv()` loads the
merged config and emits `CLOCHE_GIT_AUTHOR_NAME` /
`CLOCHE_GIT_AUTHOR_EMAIL` / `CLOCHE_GIT_SSH_COMMAND` entries as
appropriate. Both `executeScript` and `executeAgent` append the result
to `cmd.Env`. Scripts that commit honor the env vars with a local
fallback:

```bash
export GIT_AUTHOR_NAME="${CLOCHE_GIT_AUTHOR_NAME:-cloche}"
export GIT_AUTHOR_EMAIL="${CLOCHE_GIT_AUTHOR_EMAIL:-cloche@local}"
```

### Push credential convention

The host executor expands `~` in `ssh_key` to the user's home directory
at compose time and passes the resolved path into `ssh -i`. Scripts
don't need to know the flag format — they treat
`$CLOCHE_GIT_SSH_COMMAND` as an opaque value suitable for
`GIT_SSH_COMMAND`.

If no key is configured, `CLOCHE_GIT_SSH_COMMAND` is unset. Scripts that
need a push key and find the variable missing should fail loudly with a
clear message ("configure `[git] ssh_key` in `.cloche/config.toml` or
`~/.config/cloche/config`").

Containers are **out of scope**. The container path never pushes today;
the extract step runs daemon-side on the host. If a future workflow needs
to push from inside a container, a follow-up design can decide whether
to mount the key in or pin a separate in-container key.

### `cloche init` interactive prompt

The remaining work. `cloche init` today is non-interactive — it scaffolds
files and exits. This feature adds a single interactive question as the
last step, gated by TTY detection and an opt-out flag.

**New flags:**

- `--non-interactive` — skip all prompts. Combined with the absence of
  other flags, this writes the scaffold with `[git]` fields commented
  out. Required for CI / scripted init.
- `--ssh-key <path>` — write `ssh_key = "<path>"` into the generated
  `.cloche/config.toml`. Works in both interactive and non-interactive
  modes. Path is written verbatim (no expansion at init time — host
  executor expands `~` when composing the env var).

**Generation is never silent.** A key is only generated when the user
explicitly picks "generate new" in the interactive prompt. If
`--non-interactive` is set without `--ssh-key`, nothing is generated
and the field stays commented.

**Interactive flow** (after scaffold is written, only if stdin is a TTY
and `--non-interactive` is not set):

1. *"Configure a project-specific git push key for this project? [y/N]"*
   — default no. A `no` ends the flow; all `[git]` fields stay commented.
2. *"Use an existing key, generate a new one, or skip? [existing/generate/skip]"*
   — default `skip`.
   - **existing** → prompt for a path. Validate that the file exists and
     is readable before writing. Write `ssh_key = "<path>"` into
     `.cloche/config.toml`.
   - **generate** → run `ssh-keygen -t ed25519 -f ~/.ssh/cloche_<project-basename>
     -C "cloche-bot@<project-basename>" -N ""`. Chmod `0600` on the
     private key, `0644` on the `.pub`. Write the resolved path into
     `.cloche/config.toml`. Print the public key contents and the GitHub
     URL for adding it (`https://github.com/settings/ssh/new` for a user
     key; a note that a deploy key can be added at
     `https://github.com/<owner>/<repo>/settings/keys`). Ask nothing
     further — key registration is manual.
   - **skip** → same as answering `no` at step 1.

**Key location rationale.** Keys live in `~/.ssh/` rather than under
the project tree (`.cloche/ssh/`) or under cloche's state dir
(`~/.config/cloche/ssh/`). Rationale: `~/.ssh/` is the conventional
place for SSH private keys, discoverable by the user via standard tools
(`ssh-keygen`, `ssh-add`, `ssh -i`), already managed with appropriate
permissions and security posture. Keeping the key inside the project
tree risks accidental commit (gitignore is a thin defense) and
inadvertent cloud sync; keeping it under `~/.config/cloche/ssh/` is
safer but less discoverable and fragments SSH key management across
two directories.

**Project basename.** The `<project-basename>` used for both the key
file name and the key comment is the base name of the project directory
at init time. Non-alphanumeric characters are replaced with `_` to keep
the filename clean.

**Generated `.cloche/config.toml` `[git]` section.** After init, a
project that did nothing interactive has:

```toml
[git]
# name     = ""         # override global git identity for this project
# email    = ""
# ssh_key  = ""         # path to private key used for `git push`
```

If the user supplied `--ssh-key <path>` or picked a key interactively,
only `ssh_key` is written uncommented; `name` and `email` stay commented
so they continue to inherit from global.

### Backward compatibility

- Unset `[git]` fields produce today's behavior exactly (`cloche <cloche@local>`
  identity, no SSH command composed, scripts use fallback).
- `ExtractOptions` gains two new fields; existing zero-valued callers get
  the same identity as before via the fallback in `extractGit`.
- No existing flags or commands change semantics. `--non-interactive` and
  `--ssh-key` are additions.

### Testing

- `internal/config/config_test.go` — `TestLoadGitConfig`,
  `TestLoadMergedProjectOverridesGlobal` cover merge semantics.
- `internal/adapters/docker/extract_test.go` —
  `TestExtractResultsUsesConfiguredIdentity`,
  `TestExtractResultsFallsBackToDefaultIdentity` cover commit author wiring.
- `internal/host/executor_test.go` — `TestGitIdentityEnv`,
  `TestGitIdentityEnvEmpty` cover env var composition including `~`
  expansion.
- For the remaining `cloche init` work, add:
  - A test that `--non-interactive --ssh-key <path>` writes the expected
    `.cloche/config.toml` with only `ssh_key` uncommented.
  - A test that `--non-interactive` alone leaves all three fields
    commented.
  - A test of the interactive prompt driven by a fake stdin
    (table-driven: answers → expected config file + expected
    generated-key invocation). Use a hook/indirection to stub out
    `ssh-keygen` in tests.

## Open Questions

- **Deploy keys vs user keys on GitHub.** The "generate new" flow prints
  the user key URL. Should it detect the remote origin and preferentially
  print the deploy-keys URL for that repo? Low priority; good follow-up.
- **Warn when `ssh_key` points at a file that doesn't exist** at daemon
  startup / `cloche doctor`? Currently the error only surfaces when a
  script actually tries to push. A `doctor` check would catch it earlier.

## Version impact

Build bump — the feature is additive. No CLI flags or config keys are
removed, no existing defaults change, and scripts that ignore the new
env vars continue to work. Tag the `cloche init` bead ticket accordingly.
