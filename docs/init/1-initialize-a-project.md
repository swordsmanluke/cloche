# 1. Initialize a Project

From your project root:

```bash
cloche init --new
```

This does five things:

1. **Registers the project** — creates `.cloche/config.toml` (with
   `active = true`), adds gitignore entries for runtime state, and writes the
   global daemon config if you've never run cloche before.
2. **Scaffolds the workflows** — every file listed below, skipping any that
   already exist.
3. **Bootstraps task tracking** — runs `bd init` (if `.beads/` doesn't exist)
   and creates two starter tasks. If `bd` isn't installed you get a warning
   and can install it later, then re-run `cloche init --new`.
4. **Fills in the blanks with an LLM** — a 30-second pass that reads your
   project (`go.mod`, `package.json`, ...) and replaces the
   `TODO(cloche-init)` placeholders in the Dockerfile, workflow, and
   implement prompt. Skip it with `--no-llm` and fill them in by hand.
5. **Commits the scaffold** — see below; skip with `--no-commit`.

## What was generated

| File | Purpose |
|------|---------|
| `.cloche/develop.cloche` | The container workflow: implement → commit → test → fix-tests |
| `.cloche/host.cloche` | Host orchestration: find tasks, run develop, merge results |
| `.cloche/Dockerfile` | The container image agents run in |
| `.cloche/prompts/*.md` | Prompts for the agent steps |
| `.cloche/scripts/*.py` | Host scripts: beads integration, merge, cleanup |
| `.clocheignore` | Files excluded from the container workspace |
| `cloche_init_test/` | A tiny test the starter tasks use to validate the setup |

## The scaffold is committed for you

Init's last step commits the generated files (`Add cloche scaffold`). That
commit matters: containers are seeded from a **clean git snapshot** of your
last commit, so uncommitted files are invisible inside the container. The
rule to internalize — *whenever you edit the scaffold (or anything the agent
needs), commit before running the loop.* Runtime state (`.cloche/runs/`,
logs, the beads database) stays gitignored.

The auto-commit is skipped with `--no-commit`, outside a git repo, or if you
had staged changes; init tells you when that happens, and then committing the
scaffold yourself is required before the first run.

## Check the stack

```bash
cloche doctor
```

`cloche doctor` checks Docker, the daemon, agent credentials, and the `bd`
CLI, and tells you how to fix anything that's missing. The project image
builds automatically the first time a run needs it; pre-build it (and warm
the cache) with:

```bash
docker build -t <project>-cloche-agent:latest -f .cloche/Dockerfile .
```

The exact image name is printed in init's "Next steps" output and stored in
`.cloche/config.toml`.

Look for leftover placeholders before your first run:

```bash
grep -r 'TODO(cloche-init)' .cloche/
```

**Next:** [2. Create a bead task](2-create-a-bead-task.md)
