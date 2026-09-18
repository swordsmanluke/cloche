# Cloche Detailed Changelog

## v3.24.14 — 2026-09-18

### Fixes

- `537715c` Fixed the web console's repo sub-tab attribution: a host workflow run that touches multiple repositories, or declares none of its own, previously always fell back to "all repos" only; run attribution now unions the workflow's declared repo(s), a step's `repository` pin, repos a container sub-workflow actually extracted results into, and — for `cloche run` invoked from inside a `[[repositories]]` sub-repo path — the matched repo, with existing rows backfilled on daemon startup and unattributed runs counted separately rather than silently disappearing from a named sub-tab.

### UI/UX

- `f99ec48` The web console's Workflows/Requirements/Containers overlay panel no longer caps out at a fixed 1100px width; it now scales with the viewport (minus a constant gutter) so it uses the available space on wide screens.
- `b31bb24` Renamed the web console's "Views ▾" menu to "Tools ▾" and fixed it (and the idle-projects "More" menu) to open flush under their own trigger button instead of being anchored to the edge of the whole tab bar.

### Internal

- `383ea54` Routine `intent scan` bookkeeping commit updating `.cloche/intent/scan-state.yaml` cursors; no application code changes.
- `0e3a497` Added a research spike doc (`docs/plans/spikes/2026-09-17-intent-ab-pilot/`) recording an A/B pilot comparing intent-continuity arms; docs/data only, no application code changes.

## v3.24.11 — 2026-09-17

### Features

- `74e57ce` The task stack's "Running" group now always stays visible (showing a "0" count when empty), and the "Done" group's header can be clicked (or toggled with Enter/Space) to collapse its rows; the collapsed state persists in `localStorage` and survives poll re-renders.
- `852e54c` Added a density toggle to the console foot bar (also bound to the `d` key) that switches between "Comfortable" (default) and "Compact" spacing/font size, persisted in `localStorage`.

### Fixes

- `9c27a5c` Fixed duration/elapsed formatting overflowing to values like `2065h12m` for long-running poll tasks; the CLI and web dashboard now share one `internal/durationfmt` package that rolls hours into days past 24h and drops to days-only past 7 days.
- `d11f751` Fixed the console log pane's step-scope filter to match on `(run_id, step_name)` instead of step name alone, so a step name shared between a host run and a container run it dispatched no longer shows the wrong lines; scoping is now a live filter over the streaming log with an archive fetch only as a fallback, and a running step with no output yet shows a "waiting" placeholder instead of briefly flashing "no output".

### UI/UX

- `6ce98c6` Task stack rows now show the current step on its own line instead of crowding it into the elapsed-time line, and the task id is no longer truncated with an ellipsis.
- `bbeec47` The task detail "Facts row" is capped at two rows (a "run"/"attempt" row and a "status" row) regardless of how many facts a run has, and the step strip now highlights a single "focal" segment (the running step, or the first failed one) instead of showing every segment's dot and duration at once.
- `f4a77f0` The console header's Workflows/Requirements/Containers/Ledger buttons are consolidated behind a single "Views ▾" menu (their `w`/`i`/`c`/`l` shortcuts still open each view directly); the daemon version moved from the header into the foot bar.
- `5dac136` The console log pane now inserts a date divider between lines from different days, renders `step_started` status lines as section breaks instead of ordinary lines, and shows local time-of-day in the line prefix with the full timestamp available via tooltip.
- `8bec8aa` Task stack rows omit the title line when it's identical to the task id, and drop the redundant "succeeded" status word for a succeeded "Done" row since the dot color and group heading already say it.

### Internal

- `d3e7ffc` Introduced a shared four-step CSS font-size scale (`--fs-xs/sm/md/lg`) used throughout the console stylesheet, and adjusted the `--tx2` secondary text color.
- `48eada6` Routine `intent scan` bookkeeping commit updating `.cloche/intent/scan-state.yaml` cursors; no application code changes.
- `3d8a4c0` Routine `intent scan` bookkeeping commit extracting one new requirement (`req-9205`, on the shared duration formatter) and updating scan-state cursors; no application code changes.

## v3.24.0 — 2026-09-16

### Breaking

- `3a6f033` Removed the dashboard's `GET /api/runs`, `GET /api/projects/{name}/runs`, and `GET /api/failed-tasks` endpoints and their server-side grouping/filtering logic, now superseded by the task-stack API. Migration: switch any integration to `GET /api/projects/{slug}/tasks/stack` and the related task-stack/activity endpoints.

### Features

- `a2f61d0` Web console task stack can now be scoped to a single repository within a multi-repo project via a new sub-tab row, with per-repo running/needs-you badges and `/{project}/{repo}/{task}` routes.
- `adcd377` Intent scan's `collect-sources` step now collects docs, commits, and run history from every configured `[[repositories]]` entry, not just the project root, with independent per-repo cursors; `cloche intent scan` now blocks until the run finishes and prints a per-repo summary.
- `bdfe9f8` Web dashboard gains a "System" tab grouping tasks/runs with no owning project, which were previously invisible in the console.
- `819ba39` Task stack's "Done" group is now a fully paginated history of completed tasks (newest first, `page_size` query param) instead of only showing today's; the JSON field is renamed `done_today` to `done`.
- `41634bb` Task detail log pane gains step-scoped navigation: `[`/`]` jump to the previous/next step, including child-run steps; attempt switching moves to `Shift+[`/`Shift+]`.

### Fixes

- `9df0db0` Fixed the burn-rate display reading `0` while a step was still running; token usage now streams incrementally from the agent's output as a step executes, and usage queries window by step start time rather than completion time. In-flight totals are now marked with a `~` prefix in `cloche status`/`cloche get usage` and the web console.
- `a800852` Fixed `cloche intent apply-reconcile` failing when a reconcile agent had nothing to write because `extract` found zero candidates (now a no-op success via a new `--candidates-file` flag), while still failing closed if candidates existed but `reconcile.json` was never written. Host workflow runs that fail via a declared wire now record which step failed instead of leaving the error message blank.
- `97ef9ea` Fixed multi-repo intent scan attribution: sources collected from a `[[repositories]]` sub-repo are now correctly repo-tagged end-to-end, the dashboard's commit-diff links resolve against the correct sub-repo working tree, and per-repo scan-state cursors auto-migrate from the old format. `cloche intent scan` no longer aborts when `.cloche/config.toml` is missing.
- `3b8e8c6` Fixed `cloche intent scan --full` being a documented no-op; it now actually forces a full re-scan, resetting `collect-sources` cursors and forcing `discover-domains` to do a full survey, without discarding existing requirements.
- `4a2f2b3` Fixed runs waiting at a `poll` step being excluded from project health, active-run counts, and the task stack's "Running" group; a waiting run now shows a "waiting · poll `<step>` · last poll `<n>` ago" annotation.
- `35d543d` Fixed the Workflows view's tab strip dragging the whole DAG panel sideways when there were many tabs; tab bars now scroll independently and the active tab auto-scrolls into view.
- `9c64734` Fixed the ledger dashboard's prompt-revision backfill running inline, and shelling out to `git log`, on every page request; it now runs once as a resumable background sweep at daemon startup, with a `backfill_pending` flag so the UI can show partial data instead of blocking.
- `f8ffffa` Fixed the task detail step strip being squashed by the log pane below it on tasks with several steps.
- `6ed188b` Fixed the task stack's "Done today" boundary comparing against UTC midnight instead of the daemon's local midnight (uncertain — please review: this grouping was replaced later in this release by `819ba39`'s fully paginated "Done" history, so the fix's specific mechanism no longer applies to the final shipped behavior).

### UI/UX

- `6618a1d` Renamed the web console's "Intent" view/button to "Requirements" for clarity.
- `ae65282` Reordered the web console's workflow tabs to pin `list-tasks` and `main` first, and fixed the Container/Host location tabs sometimes showing the wrong one as active.
- `7b0eb5f` Task stack rail now hides the Needs you / Running / Queued groups entirely when empty, reappearing on the next poll; the Done group always stays visible.

### Internal

- `e99980e` Documented that `cloche intent scan --full` had no effect and that `collect-sources` didn't descend into `[[repositories]]` sub-repos — both addressed later in this release by `3b8e8c6` and `adcd377`.
- `7f2b055` Split the SQLite store's single connection into a dedicated write connection plus an 8-connection WAL read pool, so concurrent reads no longer queue behind each other or behind writes.
- `c21dd83` Eliminated N+1 query patterns in task/run listing (batched attempt and builtin-status lookups) and bounded the dashboard's task-stack queries to the visible window.
- `2513324` Replaced full-history run scans used for project health and loop-occupancy summaries with bounded, index-backed queries.
- `c728815` Added a design mockup comparing three UI approaches for grouping the console by repository; no shipped code.
- `807b25b` Added CLI regression tests for subcommand dispatch parsing; no behavior change.
- `01502b2` Added a startup migration creating secondary indexes on the daemon's core tables (runs, step_executions, attempts, log_files) to avoid full table scans on common lookups.
- `6014c14` Intent scan state refresh (cursor update, no new requirements).
- `ebe8530` Intent scan: created 5, superseded 0, merged 0, dropped 0 requirements.
- `c78f7ad` Intent scan: created 3, superseded 1, merged 8, dropped 0 requirements.
- `5aa33c9` Intent scan: created 2, superseded 0, merged 5, dropped 0 requirements.
- `824d0d1` Intent scan: created 2, superseded 0, merged 4, dropped 0 requirements.
- `2b8a626` Intent scan: created 3, superseded 0, merged 1, dropped 0 requirements; `domains.yaml` updated.
- `d9a286c` Intent scan: created 0, superseded 1, merged 3, dropped 0 requirements.
- `548cb5e` Intent scan: created 1, superseded 0, merged 0, dropped 0 requirements.
- `d3a8a49` Intent scan: state refresh, no requirement changes.
- `d94f381` Intent scan: created 3, superseded 0, merged 0, dropped 0 requirements.
- `346fbf0` Intent scan: created 1, superseded 0, merged 6, dropped 0 requirements.
- `d1ebe33` Intent scan: created 5, superseded 0, merged 0, dropped 0 requirements.
- `54b414b` Fixed the intent-ab research harness's wrapper to inline `DESIGN.md`, since one arm's agent returned empty content without it. Internal experiment tooling, not shipped in the product.
- `e058f65` Fixed the intent-ab research harness's pilot-mode task unclaim so the loop survives step failures. Internal experiment tooling, not shipped in the product.
- `ab02a7b` Increased the intent-ab research harness's wrapper timeout to 600s and made it catch socket timeouts gracefully. Internal experiment tooling, not shipped in the product.
- `3cd9a87` Armed the intent-ab research harness's arm driver against the real `bd` CLI and made it commit the overlay before dispatch. Internal experiment tooling, not shipped in the product.

## v3.23.0 — 2026-09-15

### Breaking

- `d35c42f` Renames the Go module path from `github.com/cloche-dev/cloche` to `github.com/swordsmanluke/cloche`, correcting `go.mod` and all internal imports to match the actual repository owner (`cloche-dev` was never a real org). Migration: any `go install github.com/cloche-dev/cloche/...` command or code importing the module must switch to `github.com/swordsmanluke/cloche`.

### Features

- `470c815` Adds a "commit" step to the built-in `intent-scan` workflow that stages and commits any changes under `.cloche/intent/` (scoped strictly to that path, retrying a few times on git index-lock contention), so a scan no longer leaves the working tree dirty for a human or a later merge step to clean up.

### Fixes

- `eee1ae2` Fixes a race in the in-container agent session where the daemon tearing down the gRPC stream for a step that just timed out could be misclassified as a transport error rather than a clean cancellation; also reworks the task detail facts row into a single instrument strip with attempt chips in place of a separate attempt-tabs row.

### UI/UX

- `a8a7d5a` Self-hosts the console's IBM Plex Mono/Sans fonts as vendored `.woff2` files and reworks the color palette and typography to match the reference console mock, removing the runtime dependency on Google Fonts.
- `57d05c4` Reworks the task detail step strip so a spawned child run's steps render inline (shaded and indented) immediately after their parent step, replacing the previous separate sub-row cluster.
- `a39aa43` Redesigns the task stack rows with a status dot, a two-line title, and a right-aligned elapsed/reason column, replacing the previous single-line title-plus-meta layout.
- `9ea3461` Reworks the console tab bar and daemon instruments strip (loop toggle, slot pips, burn rate, daemon version) into a flush bordered bar with a "Cloche" brand mark, and changes the idle-project overflow control to read "+N idle".
- `41a855f` Replaces the log pane's form-control toolbar with a flat status band (scope chip, type-filter chips, a click-to-toggle live/follow indicator, and a line count) and adds inline colorization of log line content for tool calls and pass/fail/warning keywords.
- `5e3161c` Adds keyboard shortcuts for following the live log (`f`) and releasing/closing the open needs-you task (`r`/`x`), replaces the foot bar's full keybinding list with a short set of hints contextual to the selected task's state, and makes the activity ticker pack several recent entries instead of showing only the latest one.

### Internal

- `b51d650` Vendors the "Console, restructured" reference design mock (`docs/design/console-restructured-mock.html`) as ground truth for this release's console-fidelity work.
- `fd33fbd` Updates the project's self-hosted documentation-audit workflow prompts to record a per-finding outcome (resolved / needs-code-change / not-resolved) and adds an escape hatch for findings where the code, not the doc, is actually wrong.
- `0d98172` Commits auto-generated `.cloche/intent/` output (13 new requirements, domain-map updates) from post-task intent scans run during this batch of work.
- `57d7754` Commits auto-generated `.cloche/intent/` output (one requirement superseded, `domains.yaml` updated) from a post-task intent scan.
- `f12222c` Corrects `docs/workflows.md` to state that a host-workflow script/poll step's working directory defaulting to the main git worktree is a default, not a guaranteed invariant.
- `07037c2` Corrects a code comment in `internal/intent/scan/builtin.go` that incorrectly claimed `CLOCHE_PROJECT_DIR` is the main git worktree for intent-scan runs; no behavior change.

## v3.22.0 — 2026-09-15

### Breaking

- `1dd72ee` Frames the `CLOCHE_RESULT` marker for agent steps with a per-step random nonce (`CLOCHE_RESULT:{{ $result_nonce }}:<name>`) so a stray mention of the literal marker string in an agent's own transcript can no longer be misclassified as the real terminal marker. Migration: custom `.cloche/prompts/*.md` templates or `agent_command` scripts that hardcode `CLOCHE_RESULT:<name>` must switch to `CLOCHE_RESULT:{{ $result_nonce }}:<name>` (or read `CLOCHE_RESULT_NONCE`).
- `718b5e4` Renames the intent-injection opt-out config key from `intent = "off"` to a typed boolean `intent_tracking = false`, and excludes opted-out steps' logs from intent collect-sources mining. This supersedes `5da99fb` (below) within this same release — `intent = "off"` never appeared in a published version, so no existing user file needs migration; use `intent_tracking = false` going forward.
- `793887b` Changes agent-step result classification so exiting 0 without ever emitting a `CLOCHE_RESULT` marker is now `fail` instead of `success`. Migration: custom `agent_command` scripts/agents relying on a bare `exit 0` for success must explicitly emit the marker.
- `44a06f1` Adds `is_builtin`/`user_initiated` tracking to runs and tasks, surfaced as a new TYPE column in `cloche list` and ORIGIN column in `cloche list --runs`. Migration: scripts parsing `cloche list`/`cloche list --runs` output by column position must account for the inserted columns.

### Features

- `ca1ff25` Ships the intent-scan pipeline: a host workflow (discover-domains → collect-sources → extract → reconcile → apply-reconcile), the `internal/intent/scan` package, `cloche intent collect-sources`/`apply-reconcile` plumbing subcommands, and orchestration-loop wiring to auto-trigger a scan after a task succeeds.
- `2c698e2` Adds the `cloche intent` CLI command family (`list`, `show`, `edit`, `disable`, `enable`, `add`, `preview`, `scan`) with shell-completion and help-text wiring.
- `5da99fb` Adds automatic prompt injection of a "## Standing project requirements" block for agent steps, plus a DSL opt-out keyword (renamed to `intent_tracking = false` by `718b5e4` later in this release).
- `ddcc9c6` Introduces a "built-in workflow" engine mechanism (Go-constructed workflows resolved after project `.cloche` discovery, overridable by a same-named project workflow) and makes intent-scan the first built-in, so extraction works in any project with no setup.
- `6837f35` Flips `intent.scan_after_tasks` default from false to true so new and existing projects get an automatic incremental intent-scan after each completed main task, and adds a per-project concurrency guard against duplicate enqueues.
- `4e81c1a` Adds an Intent panel to the web dashboard's Project Detail page: a requirements table with edit/enable/disable, a domain-map editor, and a "Scan now" button, backed by new `/api/projects/{name}/intent/...` endpoints.
- `0a3a0a3` Adds an in-process ONNX Runtime embedding adapter (tokenizer, mean-pooling, model download+cache) behind a `-tags onnx` build flag, plus an `intent.model` config key to select the embedding model.
- `63ffb3a` Replaces the multi-page web dashboard (Projects, Project Detail, Runs, Run Detail, Task Detail, Failed Open Tasks) with a single-page console: a project tab bar, a task stack (Needs you / Running / Queued / Done today), and a centre pane; old dashboard URLs redirect into the new routes for one release.
- `998ada0` Adds the web console's task detail pane: header/state actions, attempt tabs, a per-step strip with child-run inlining, and a full-width live/paginated log view.
- `5d39639` Adds a `GET /api/projects/{name}/tasks/stack` endpoint (needs-you / running / queued / done-today, with ETag support and cursor pagination) backing the console task stack.
- `7d8aeb3` Adds a per-project "Ledger" dashboard overlay showing pass-rate history, mean attempts/tokens to success, per-prompt-file outcome stats by git revision, and requirement-injection cross-references.
- `8c7aae0` Adds an activity-stream view to the web dashboard, plus new activity-log event kinds (`help_asked`, `help_answered`, `help_parked`, `help_resumed`).
- `9afb46c` Adds a "Containers" view to the web dashboard showing retained containers and their disk usage.
- `1e5f8ae` Adds a "parked" pane to the web console: when a run is parked awaiting a help-thread reply, the log pane is replaced by the thread transcript and a reply box.
- `040802e` Wires up a daemon-side attention cache computing and background-refreshing each project's "needs you" item set (parked help threads, stuck tasks, etc.), adds an `attention_count` gRPC field and a `GET .../attention` web endpoint, adds `attention.refresh_interval`/`max_parallel_refresh` config, and triggers an out-of-band refresh when a help thread is created or replied to.
- `ecc357c` Adds a "Needs you" attention set (`GetAttention` RPC, `cloche status` section, `[attention]` config block) surfacing parked runs, stale tracker claims, repeated task/workflow failures, and long-running polls.
- `66fde91` Extends the "Needs you" dashboard with actionable buttons (release claim, close-in-tracker via a new optional `close-task`/`cancel-task` host workflow contract, run-once, mute) and a compare-log view.
- `fb901c2` Adds orchestration-loop concurrency occupancy reporting: `GetLoopOccupancy`/`ListLoopOccupancy` RPCs, a "Slots: `<busy>`/`<max>` busy · `<queued>` queued" line in `cloche status`/`cloche loop status`, and a matching web endpoint.
- `8191b4d` Broadens `cloche doctor` with project-scoped checks (config, workflow syntax, image build, agent-binary version) and `--project`/`--timeout` flags; adds `cloche init --ssh-key`/`--non-interactive`, `cloche logs --step`, `cloche set -f <file>`, `cloche project repos list`, and `cloche loop status`; also fixes `cloche list --state`/`--limit` being silently ignored, parked tasks/runs missing from `cloche status`, `--no-color` being rejected by `cloche threads`, and shell completion offering a nonexistent `--workflow` flag for `cloche run`.

### Fixes

- `dd930fe` Fixes the orchestration loop treating a run parked at a `poll` step as still occupying a concurrency slot, which could block other tasks from launching even under `MaxConcurrent: 1`; also adds a live "Polls" table to the web dashboard's Runs page.
- `009b7a5` Fixes agent steps being wrongly classified as failed when a long-running session did real work but dropped the trailing `CLOCHE_RESULT` marker after many turns; the adapter now issues one recovery turn asking for the marker before falling back to fail.
- `e0ad5d4` Fixes the non-streaming (host-path) agent execution branch classifying a step as failed when its `CLOCHE_RESULT` marker was embedded inside a stream-JSON `result` event's string field instead of on its own raw stdout line.
- `f643d20` Fixes web dashboard links 404ing for projects that share a directory basename by separating the display label from a new URL-safe slug used in all project links/API routes (with one release of backward compatibility for old bookmarked URLs); also adds a `cloche health --project <dir>` flag and makes `cloche tasks --project` accept a full directory path.
- `d5efda2` Fixes agent runs being marked failed with no explanation when a prompt step declares no `results` (e.g. `cloche init`'s scaffolded fix-tests/fix-merge/implement prompts), by appending a generic `CLOCHE_RESULT` marker-protocol reminder to the assembled prompt whenever nothing already mentions it.
- `7acbccc` Fixes container-startup detection treating a container that exited 0 between polls (a fast-finishing command) as a stuck/failed start.
- `54c32bd` Fixes web console overlays (help, activity, ledger, secondary view) rendering visible on load/close because their `display: flex` rules beat the browser's default `[hidden]` styling.
- `db42ba5` Fixes the web dashboard silently failing to stay bound after a daemon restart (stale process holding the port): the daemon now retries binding with exponential backoff and reports web-listener health via `GetVersion`, surfaced by `cloche status`/`cloche health`; `make install` now waits for the old daemon to fully exit instead of a fixed 1s sleep.
- `6200c30` Frames host script-step `CLOCHE_RESULT` markers with a per-invocation nonce (`CLOCHE_RESULT_NONCE`) so a script's own output can't be mistaken for the real result marker, and fixes a bashism (`set -o pipefail`) in the intent-scan builtin scripts that failed under Ubuntu's default dash shell.
- `a1e98a2` Appends the required `CLOCHE_RESULT` marker instructions to 13 prompt files that lacked them, fixing container runs that started failing once exit-0-without-marker became a hard failure.
- `ec3da6f` Fixes `cloche status <task>` printing the wrong (project-wide) token total for every task instead of that task's own usage, and fixes token usage from steps inside nested host sub-workflows being silently dropped from `GetStatus`/`GetUsage`.
- `22c591b` Fixes agent (prompt) steps killed mid-flight (timeout, abort, container stop, park) leaving no LLM transcript on disk — output is now persisted line-by-line as it streams instead of only after the step completes.
- `54f4348` Fixes shell completion so project-local `.cloche/` workflow names always merge with (rather than being shadowed by) the daemon's built-ins, and makes the workflow-name completion test hermetic against a live daemon.
- `d8d7f25` Fixes the console tab bar collapsing every idle project into "More" when the current project has no live activity, and changes `GET /` to render the console shell directly instead of redirecting.
- `622d981` Fixes `workflow_name` dispatch steps whose target sub-workflow has long-running steps getting killed by an unrelated flat 30-minute default timeout instead of one derived from the target's own step timeouts; also adds a `cloche validate` warning when an explicit dispatch timeout is shorter than that derived sum.
- `b43e5a4` Fixes agent-name attribution for token usage: `agent_name` was read from the wrong config key so most workflows recorded no usage attribution; adds an `agent_name` field to the `TokenUsage` proto and a one-time SQLite backfill for previously unattributed rows.

### Internal

- `bfa5255` Commits an auto-generated update to `.cloche/intent/domains.yaml` and `scan-state.yaml` recording a post-task intent scan's results.
- `6efc370` Removes dead legacy web-dashboard handler code and CSS (old task-summary page and superseded API routes) and corrects `status`/`list` help text to match current task-oriented behavior.
- `feda0cd` Extends the intent-ab A/B experiment's `arm_driver` harness (contamination checks, metrics, overlay, seeding) and fixes a `.gitignore` rule that was dropping the harness's wrapper binary from git; also touches up unrelated `USAGE.md` wording.
- `4bb8fad` Hardens the intent-ab experiment's weak-model edit-applying harness against a model echoing the example path verbatim or inventing an unrelated file.
- `41e254c` Adds a stdlib-only Python `agent_command` wrapper (temperature/token clamping, chain-of-thought stripping) for driving a local model in the intent-ab experiment, plus its test harness.
- `46f22b2` Adds audit/judging tooling to the intent-continuity research experiment: automated drift-constraint and standing-constraint checkers plus a blinded-judge bundle preparation script for the toy "Bract" language eval harness, with fixtures and tests.
- `200789d` Commits auto-generated `.cloche/intent/` requirement files and a domain map produced by running the built-in intent scan on the Cloche repo itself.
- `941e3d8` Adds a corpus of sample programs plus a manifest and generator script for the intent-ab eval harness's toy "Bract" language.
- `9bb995b` Clarifies intent-embedder config documentation and adds seed task fixtures for the intent-ab experiment.
- `6079886` Adds a "seed" project (Bract interpreter, Dockerfile, develop/host workflows, task-management scripts) used as the target repo for the intent-ab eval harness; also touches up unrelated `USAGE.md` wording.
- `03b9518` Records bead ticket IDs (E1–E6) in the intent-ab experiment protocol doc.
- `f67966a` Updates the intent-ab experiment protocol doc to reflect that E1–E6 tickets were filed and artifacts live under `experiments/intent-ab/`, with E7–E8 staying manual.
- `d84725e` Drafts E1–E8 ticket descriptions for the Bract A/B intent-continuity experiment directly in the protocol doc, not yet filed in bead.
- `8637a92` Renames the internal A/B research experiment's toy subject language from "Sprout" to "Bract" (name-collision fix) and adds an anti-prior spec design plus a trap metric.
- `d385929` Adds a design doc for an A/B experiment protocol (intent tracking on/off) using a toy language and a weak local executor.
- `7e5a5b1` Updates CLAUDE.md's Intent Continuity description to reflect that scans are dormant until an automatic post-task trigger or manual `cloche intent scan`.
- `e175e01` Doc-only edit linking bead ticket IDs into the intent-builtin-migration design doc.
- `71c61de` Adds a design doc for migrating intent-scan into a built-in workflow.
- `2c70c8f` Adds a docs/intent.md user guide for the Intent Continuity feature and cross-links it from CLAUDE.md, docs/USAGE.md, docs/init/README.md, and docs/web-dashboard.md; also commits a batch of auto-generated `.cloche/intent/` requirement files from running the scan on this repo.
- `b3de6fb` Adds per-model similarity-floor and scoring/formatting logic to the intent-continuity retrieval engine — backend plumbing not yet exposed through any CLI/UI in this commit.
- `f3cedb5` Adds the embedding infrastructure for intent retrieval: an `Embedder` port with ollama/keyword adapters and a self-healing on-disk vector index — internal plumbing not yet wired to any CLI/DSL surface.
- `f90ed09` Adds a design doc for a reusable-modules proposal and lists the already-shipped `extract`/`complete` CLI subcommands in CLAUDE.md's architecture overview.
- `d48a745` Adds the `internal/intent` package's file-backed data layer: `Requirement`/`Domain` models with YAML-frontmatter parse/marshal, a `Store` for reading/writing `.cloche/intent/`, and scan-state cursor persistence.
- `f76daaf` Doc-only terminology fix in USAGE.md: renames "human step" to "poll step" in the `cloche status` output description.
- `0d39d19` Adds design docs for the intent-continuity feature and its retrieval spike, plus the spike's throwaway Go program and corpus/results fixtures.

## v3.20.2 — 2026-08-30

### Fixes

- `315d3f2` Host executor step logs are now partitioned per attempt (`.cloche/logs/<task>/<attempt>/`) instead of staged in a shared `<project>/.cloche/output/<step>.log` keyed only by step name; concurrent runs with same-named steps no longer interleave output, inherit prior runs' transcripts, or leak stale `CLOCHE_RESULT` markers into later `{previous_output}` prompts (cloche-1or8).

### Internal

- `4c68660` When an agent adapter's `OutputDir` is left unset but task and attempt IDs are present, it now derives the per-attempt logs directory automatically instead of falling back to the shared legacy layout; the in-container session and host executor still pin their output dirs explicitly.

## v3.20.1 — 2026-08-29

### Breaking

- `ef725cf` Removed the self-evolution system: the `internal/evolution` package, its daemon and gRPC wiring, the `EvolutionStore` port and SQLite implementation, and the `[evolution]` config block / `evolution_enabled` project-info field. The reserved proto field is never reused. Migration: remove `[evolution]` from `.cloche/config.toml`; existing databases keep their harmless `evolution_log` table.

### Features

- `412e059` `cloche init --new` now embeds the `docs/init/` tutorial series and writes it to `.cloche/docs/init/` in new project scaffolds, updating generated templates, next-steps output, and doctor/help text to reference the bundled paths.

### Fixes

- `166c40d` `cloche doctor`'s agent roundtrip check has been replaced with an agent-binary check (`cloche-agent --version` inside a container from the project image), fixing a check that always failed under the v3 stream-executor agent because it required `CLOCHE_ADDR`. The doctor epilogue also now distinguishes warnings from failures.

### Internal

- `1a7d908` Finished renaming "human" steps to "poll" steps throughout the codebase (domain types, SQLite store/table, host executor, engine, proto comments, docs, and the dogfood demo workflow); the on-disk `poll` DSL keyword and step config were unchanged, this only cleans up leftover internal naming, with a migration that preserves in-flight poll state.

## v3.19.8 — 2026-08-14

### Fixes

- `93371be` `cloche init --new` now commits the generated scaffold (`"Add cloche scaffold"`, skippable with `--no-commit`) after the LLM-fill and SSH-key phases, and `ContainerPool.SessionFor` now auto-builds the project's Docker image before starting a session — fixing two gaps that previously made a fresh project's first `cloche loop` run fail (invisible uncommitted scaffold, missing image).

## v3.19.7 — 2026-08-14

### Features

- `0f24868` Overhauled `cloche init --new`'s first-run scaffold: task tracking now bootstraps beads (`bd`) instead of `task_list.json`, `prepare-merge.py`'s hand-rolled worktree management is replaced by daemon-created branches consumed by new `merge.py`/`cleanup.py` scripts, a new `prepare-prompt.py` step passes task data explicitly via the KV store, `cloche doctor` gains a beads check, and six `docs/init/` tutorials were added and cross-linked from README, USAGE, and init's next-steps output.

### Fixes

- `f3b52d4` Fixed three issues surfaced by a fresh project's first loop run: the docker root chown+gosu ownership wrapper now also applies to session-mode `cloche-agent` invocations, `cloche list` gained a working `--issue`/`-i` filter, and `cloche list` now truncates titles/errors by rune instead of by byte to avoid emitting invalid UTF-8.

## v3.19.6 — 2026-08-11

### Breaking

- `b136142` Container seeding and result extraction now honor a workflow's `repos = [...]` declaration instead of always including every configured repository; workflows sharing a `container.id` get the union of their declared repos, and a latent bug where narrowing repos could corrupt the shared config for later readers is also fixed. Migration: ensure every `repos` declaration lists all repositories the workflow needs, since unlisted ones are no longer copied into the container.

### Fixes

- `c85c89b` Fixes the `.cloche/runs/<run-id>` bind mount to source from the live project directory instead of the (temporary, already-deleted) container seed snapshot, which had caused every containerized agent step to abort with a missing prompt since v3.19.0.

### Internal

- `8447f52` Updates the run-isolation architecture docs and diagrams to describe the container snapshot mechanism as a `git clone` (not `git archive`) and bumps the documented version range to v3.19.0.
- `2077077` Documents the `cloneAt` and `childRepoSeeds` snapshot-materialization helpers, including their skip/error behavior for missing or non-git nested repos, in the run-isolation architecture doc.
- `81687b5` Adds a source line-number reference and a historical note (bare-archive bug fixed in v3.18.10, nested repos added in v3.18.9) to the run-isolation architecture doc.
- `d32263a` Rewrites the run-isolation architecture and index docs to describe the git-clone-based snapshot approach (replacing the earlier git-archive description) and updates referenced source line numbers and version ranges.

## v3.19.0 — 2026-07-18

### Breaking

- `76162c9` The workflow step result name `parked` is now reserved, like `done`/`abort`: `Workflow.Validate` rejects any step that declares or wires it. Produced by the new help-channel park mechanism when a `clo ask` / `ask_user` call goes unanswered past `park_after`. Migration: rename any step result literally named `parked` in your `.cloche` files. (Also introduces the park mechanism itself — see Features.)

### Features

- `7e0a364` Add the help-channel foundation: the `ask_user` MCP tool and `clo ask` CLI open (or continue) a help thread and block for a human reply; the new `cloche threads [list|show|reply]` command lists, inspects, and answers threads; `cloche status`/`cloche list` surface an open thread as `Pending question: ...`. Adds `internal/mcpauth` token handling for MCP tool auth. ([design](docs/plans/2026-07-17-help-channel-design.md))
- `76162c9` Implement help-channel parking: an unanswered `clo ask`/`ask_user` call now commits and stops the run's container after `park_after` (default 5m) instead of blocking forever; replying via `cloche threads reply` automatically resumes the run. Adds `--no-park` to `clo ask` to opt a step out of parking. `cloche status`/`cloche list` show parked runs as `parked — awaiting reply: ...`. ([design](docs/plans/2026-07-17-help-channel-design.md))
- `9bad3a1` Add Slack as a help-channel integration: `[[help.channel]]` daemon-config tables (`type = "slack"`, bot/app token env vars, optional `channel_map` to route per-project) deliver `ask_user`/`clo ask` questions to Slack in addition to `cloche threads`. A channel that fails to initialize logs a warning and is skipped rather than failing daemon startup. ([design](docs/plans/2026-07-17-help-channel-design.md))
- `2935529` `cloche shutdown --restart` and daemon relaunches now redirect the new daemon process's stdout/stderr to `~/.config/cloche/cloched.log` (override with `CLOCHE_LOG`) instead of discarding them; `cloched` logs the configured log path on startup and `cloche shutdown` (initial launch) prints it.

### Fixes

- `0eac0a2` Clean container snapshots (used to seed a container from a pinned git state mid-host-workflow) are now produced via a local `git clone` instead of `git archive`, so the snapshot retains `.git` — workflow steps that run git inside the container (e.g. committing a report) no longer fail with `fatal: not a git repository`. The snapshot also checks out the branch that was active at capture time instead of a detached HEAD.
- `b9ac04a` Clean container snapshots now include nested `[[repositories]]` checkouts (resolved from project config and materialized at their own pinned commit), fixing workflows that `cd` into a gitignored nested repo (e.g. `repos/anarkana`) failing with "can't cd to" after the container is seeded from a snapshot that omitted it.
- `a3ad217` Extend the clean-snapshot isolation protection to container sub-workflows dispatched mid-run by a host workflow (`DaemonExecutor.executeContainerStep`), which previously seeded from the live, possibly-mid-checkout project tree; the seed git state (and nested-repo state) is now captured before any host step runs, mirroring the protection already applied to top-level runs. ([docs](docs/run-isolation/architecture.md))

### Internal

- `64167e9` Filter epics out of `.cloche/scripts/ready-tasks.sh` dispatch so an open epic issue is no longer claimed and run as if it were a leaf task; adds the help-channel design doc the phase tickets reference.

## v3.18.3 — 2026-07-14

### Breaking

- `aa1ca01` `cloche loop once` no longer blocks until the launched run completes. The old implementation waited for a new run to reach a terminal state on the CLI's 30s command context, so any task longer than 30s killed the wait with a context-deadline error and could leave the loop running while the CLI wrongly reported it stopped. Once mode is now driven by the daemon (`EnableLoopRequest.once`): it launches at most one task then stops itself, and the CLI only watches briefly to report whether a task was launched (exit 0) or nothing was assignable (exit 1). Migration: use `cloche poll` or `cloche list` to track the launched run's outcome instead of relying on `cloche loop once`'s exit code for success/failure.

## v3.18.2 — 2026-07-01

### Breaking

- `e663576` DSL: workflow blocks require bare identifiers (`workflow name {` instead of `workflow "name" {`). Migration: remove quotes from the workflow name in all `.cloche` files; `workflow_name = "..."` step fields are unaffected.
- `0631f26` Re-applies the above DSL identifier change on main after it had been on a merged feature branch.
- `4f5ec20` Re-applies the DSL identifier change a second time after a vertical-layer squash commit (`5e3fccd`) accidentally reverted it; also re-patches `.cloche` scaffold files, all test fixtures, and examples.

### Features

- `389bfda` Add `token-limit` config key to DSL and domain layer (L1): parser support, domain validation, implicit `token-limit → abort` wires on every step, workflow-level shorthand wire, and `docs/workflows.md` reference section.
- `835e557` Token-limit L2: engine enforcement for per-step output-token ceilings and cumulative workflow ceiling; `token-limit = 0` short-circuit aborts without invoking the executor; defaults 500 000/step and 2 000 000/workflow; `-1` disables.
- `b7622b2` Loop resume gate L1: add `QuiesceRuns` gRPC RPC to the proto and a stub server handler; wire `cloche loop quiesce` subcommand and `--quiesce` flag on `cloche loop stop` to the new RPC.
- `5a24b73` Loop resume gate L2: implement `QuiesceRuns` RPC to mark resumable runs as parked in the SQLite store; `cloche loop status` reports the parked count; BDD step definitions implemented.
- `a20cbbb` Rename operator surface from `cloche loop quiesce` / `--quiesce` to `cloche loop stop --hard`; help text, shell completion, and BDD scenarios updated; the underlying `QuiesceRuns` gRPC wire is unchanged.
- `550201f` Resume rebuild infrastructure: tar-stream workspace snapshot capture/inject helpers (`internal/adapters/grpc/snapshot.go`), design doc, and Docker pool `CommitForResume` helper.
- `d74ceb9` Resume rebuild: `--no-rebuild` / `--clean` flags on `cloche resume`; server-side rebuild fork that defaults to rebuilding the container fresh and re-applying the latest workspace snapshot; `ensureResumeImage` hook for runtimes that support image rebuilding.
- `5e3fccd` Vertical workflow: add design-preparation phase (Phase 0.5) with scripts (`vertical-prepare-design-branch.sh`, `vertical-open-design-pr.sh`, `vertical-record-design.sh`) and stub prompts for design doc authoring, PR open/review, and feedback addressing; also extends `.cloche/vertical.cloche` and removes the legacy PR-gate steps from implementation layers. Note: this commit also accidentally reverted the DSL identifier change (fixed by `4f5ec20`).
- `546a93a` Vertical workflow: expand and refine the design-prep prompts (`vertical-write-design.md`, `vertical-address-design-feedback.md`, `vertical-check-design-needed.md`) and scripts (`vertical-finalize.sh`, `vertical-open-design-pr.sh`); implement `vertical_design_prep_test.go` BDD step definitions.
- `c61eef4` BDD scenarios self-register via `func init() { registerScenarios(...) }` into a package-level registry; `TestMain` iterates the registry instead of a hardcoded list, so concurrent feature branches no longer conflict on the same line.

### Fixes

- `34e44f5` Containers are now seeded from a `git archive` snapshot at baseSHA rather than the live working tree, preventing host-workflow branch checkouts from corrupting subsequent container seeds and causing `finalize` to write stale state back to `main`.
- `317a722` Fix `token_limit.feature` DSL inline snippets to use bare identifier workflow names after the parser change in `0631f26`.
- `e68ad53` Fix three test failures introduced by the token-limit L1 layer (`389bfda`).

### UI/UX

- `8a5358f` Update GitHub repository URLs in `docs/INSTALL.md` and `docs/USAGE.md` from `swordsmanluke/cloche` to `cloche-dev/cloche` after the GitHub org rename.

### Internal

- `b7f431d` Add `docs/run-isolation/` guide: overview index, architecture walkthrough with implementation anchors, and D2 source + SVG diagrams for the per-run lifecycle and finalize flow.
- `428b1f1` Squash: vertical update-docs pass (`6hcr-vertical-update-docs`); documentation updates for recently-landed vertical workflow changes.
- `1ffd322` No-op squash placeholder (`6hcr-implement-vertical-layer`, second attempt); empty diff, no file changes.
- `89111e3` Add design docs `docs/plans/2026-05-28-run-state-step-view.md` and `docs/plans/2026-05-28-step-token-metrics.md`; expand `run_state_step_view_test.go` with L2 BDD stub step definitions.
- `9ba4db2` Add BDD test plan for the run-state per-step view design doc.
- `a532c9c` Add BDD test plan for the per-step token metrics design doc.
- `5e99727` Tests for workspace snapshot helpers, rebuild-mode predicate helpers (`modeUsesCommit`, `modeUsesSnapshot`, `shouldCaptureSnapshot`), and `parseResumeFlags` flag parsing.
- `c72ce8f` Update `docs/design/vertical-workflow.md`: document the drop-PR-gates and fail-stop-stuck-layers changes (more complete pass).
- `c92b72b` Update `docs/design/vertical-workflow.md`: document drop-PR-gates and fail-stop-stuck-layers changes (earlier pass).
- `2d43cea` Add BDD test plan for vertical workflow design-prep stage (Phase 0.5).
- `e999c53` Refactor `theTokenLimitDSLFileIsParsed` to use early returns for readability.
- `bb2c806` Extract `validateTokenLimit` helper to eliminate DRY violation identified in self-review.
- `ac42e25` Add BDD test plan for extract-base-SHA re-resolution feature (`features/extract_base_sha_reresolution.feature`); subsequently removed by `5e3fccd`.
- `b461e14` Merge pull request #36 (token-limit test plan branch into vertical stack).
- `33a092b` Squash: BDD test plan for token-limit config feature (`cavf-vertical-bdd-test-plan`).
- `2fc4cc6` Merge pull request #19 (vertical test-plan branch into main).
- `57d3f60` Add BDD test plan for token-limit config feature (`features/token_limit.feature`, `features/token_limit_test.go` stubs).
- `3ebdba3` Add BDD test plan for loop resume gate.
- `982a18e` Add BDD test plan for run-state per-step view design doc (earlier attempt, superseded by `9ba4db2`).
- `50c0f2c` Docs: vertical test-plan/docs phases are now idempotent on re-dispatch (more complete pass).
- `44e7664` Docs: vertical test-plan/docs phases idempotent on re-dispatch (earlier pass).
- `240be52` Docs: DSL identifier workflow names reference, step-token-metrics design, and `{{@ }}` fix notes (more complete pass).
- `111becd` Docs: DSL identifier workflow names, step-token-metrics design, `{{@ }}` fix (earlier pass).
- `e42286b` Docs: `token-limit` config key in DSL reference (`docs/workflows.md`) and usage guide.
- `65a9ffe` Docs: loop resume gate with `cloche loop quiesce` terminology (later superseded by `--hard` rename).
- `7ff70f0` Docs: loop resume gate (earlier pass).
- `08d6a37` Recovery: restore main to commit a131835 content (second stale-finalize reversion).
- `ebe9b2a` Recovery: restore main to commit a131835 (first stale-finalize reversion after a whole-tree corruption).
- `c6041a7` Version bump to 3.18.2.
- `a61e535` Version bump to 3.18.1.
- `ef5795d` Version bump to 3.18.0.
- `01291cf` Version bump to 3.17.0.

## v3.15.14 — 2026-05-21

### Fixes

- `6186e9a` `{{ $task_id }}` now resolves correctly in agent prompts inside host workflows; the host executor was assigning `adapter.RunID` but omitting `adapter.TaskID`, leaving the variable empty for any agent step in a host workflow.

## v3.15.13 — 2026-05-21

### Breaking

- `7c7139a` Prompt template directive bodies now resolve bare `$name` references (built-in or KV) instead of full `{{ $name }}` nested directives; `{{` and `}}` characters inside a body are literal and pass through to the shell or file path verbatim; the parser still depth-balances `{{`/`}}` to locate the true outer closing pair. Migration: replace any `{{ $var }}` inside `{{! }}` or `{{@ }}` bodies with bare `$var`. ([design](docs/plans/2026-05-18-prompt-templating-design.md))

## v3.15.12 — 2026-05-19

### Features

- Prompt files now support `{{ }}` template directives evaluated before the agent is invoked. Three forms: `{{ $name }}` (built-in variable or KV-store lookup), `{{! cmd }}` (sh -c; stdout substituted, stderr to step log, 30 s timeout, non-zero exit fails step), `{{@ path }}` (file contents substituted verbatim). Inner `{{ $var }}` references inside shell and file directives resolve before the outer directive executes. `$$` → `$` inside shell directives only. Unresolvable directives fail the step before the agent runs; error messages name the directive and cause. ([design](docs/plans/2026-05-18-prompt-templating-design.md))

### Internal

- Legacy `{task_description}` and `{previous_output}` placeholders now emit a per-step deprecation warning through the status writer; the substitution itself is unchanged.

## v3.15.10 — 2026-05-18

### Features

- `8aa5783` The streaming prompt adapter now supports opencode as a first-class agent command, parsing `--format json` events (text deltas, tool_use, step_finish) and extracting token usage.
- `45f6dde` Multi-repo extraction: container sub-workflows that declare `repos = [...]` now extract changes into per-repository worktrees and branches, with per-repo branch and path metadata written to the KV store. ([design](docs/plans/2026-04-14-cloche-extract-design.md))

### Fixes

- `2e5fb43` Container workflows now correctly propagate `container { agent_command = ... }` and `container { agent_args = ... }` into step config instead of silently falling back to Claude.

### Internal

- `d8a1f14` Extend godoc on `rollbackWorktrees` to document error handling.
- `c11a229` Update documentation references from `cloche run --workflow <name>` to `cloche run <name>` across README, USAGE, and design docs.

## v3.15.9 — 2026-05-16

### Fixes

- `7fa9315` Live log streaming and aggregation for nested host sub-workflow steps: `innerHostStatusHandler` now broadcasts inner step start/complete events to the parent run's log broadcaster so `cloche logs -f` reflects them in real time; `aggregateHostSubWorkflowLogs` concatenates per-step log files into a single `<step>.log` so the outer `full.log` receives them; `logstream.Writer.Append` writes pre-formatted log content without adding a `[script]` type wrapper; `hostStatusHandler.OnStepComplete` no longer re-broadcasts batch output for workflow steps (which was already streamed live by the inner handler or container `StepLog` messages), preventing duplicate lines.

## v3.15.7 — 2026-05-15

### Breaking

- `cf17793` The `default` field is removed from `[[repositories]]` config entries and from the `Repository` proto message (field 4 is now reserved to prevent future reuse); `cloche project repos list` column header changed from `FLAGS` to `URL`. Migration: remove `default = true` from `[[repositories]]` blocks in `.cloche/config.toml` — the field is silently ignored at parse time but has no effect; the implicit default repository is now the single declared entry when exactly one is configured.

### Features

- `5c08f33` New `skip` step config key accepted on any step type: a shell command run before the step with a 90 s timeout; exit 0 bypasses the step (routing via `success` or a `CLOCHE_RESULT:<wire>` marker on stdout), non-zero exits run the step normally; skip output is captured to `step.<name>.skip.log`; skipped steps appear as `skipped` in `cloche status`/`cloche list` and do not increment the `max_attempts` counter. ([design](docs/design/skip-scripts.md))
- `5eb58f8` Host-workflow agent steps now receive `CLOCHE_TASK_ID`, `CLOCHE_RUN_ID`, `CLOCHE_ATTEMPT_ID`, and `CLOCHE_PROJECT_DIR` as environment variables via the prompt adapter's new `ExtraEnv` field, enabling `cloche get`/`cloche set` from within host-workflow agent steps.
- `1fe0cd8` `cloche project` emits a `DEPRECATED:` warning with `[[repositories]]` migration instructions when no repository configuration is found in `config.toml`; `ListRepositories` auto-seeds a single entry (name = project directory basename, path = project root) on first access for projects with no stored repository rows.

### Fixes

- `893a00e` `GetProjectInfo` now uses `dsl.ParseAll` on every `.cloche` file and routes workflows to host or container by inspecting each workflow's `host {}` block; previously only `host.cloche` was parsed for host workflows and other files were always treated as container-workflow sources, causing misclassification for projects that define host workflows outside `host.cloche`.

### Internal

- `893a00e` Added `docs/design/skip-scripts.md` design document describing the skip-scripts feature (semantics, DSL, lifecycle, protocol, implementation surface).

## v3.15.1 — 2026-05-13

### Breaking

- `27d300a` Bot credential setup for the agent image changed from hard-required bare SSH key files to an optional `gituser.toml`-driven scheme (`name`/`email`/`ssh_key` fields); `make install` now creates an empty `.cloche/credentials/` placeholder so the build works on fresh clones. Migration: if you previously had `.cloche/credentials/id_ed25519` configured, create `.cloche/credentials/gituser.toml` referencing it (see `.cloche/setup-credentials.sh` for the full schema).

## v3.15.0 — 2026-05-13

### Features

- `45a71ff` / `2402097` / `714cac1` Adds the **Repository** primitive (cloche-em50). A project's `.cloche/config.toml` may now declare `[[repositories]]` entries with `name`, `path`, and `url` fields. Workflows declare which repos they consume via a top-level `repos = ["a", "b"]` field, and individual steps may pin a specific repo via `repository = "x"`. The proto's `Repository` message and `GetProjectInfoResponse.repositories` field expose the loaded set; `cloche project` renders a Repositories section, and `cloche project repos list` produces a machine-readable listing. The previously-prototyped top-level `repository "name" { }` DSL block was deliberately not shipped — repositories are declared only in `config.toml`. (Followup tickets: `cloche-yn27` to remove the `default` field in favor of a single-entry implicit default; `cloche-i6xn` to land the deprecation-warning and DB auto-seed BDD scenarios; `cloche-8m3c` to restore the SetContextKey 1KB cap.)
- `45b1238` (+ many follow-ups) Adds the **vertical development workflow** for layered feature delivery: `cloche run vertical` walks a feature task through BDD test-plan, layered implementation (each layer becomes its own PR), docs, and finalize phases. Each phase opens a PR the user must approve before the next phase begins. See `docs/design/vertical-workflow.md`.
- `c055b94` `verify-changes.sh` (used by `develop` and `vertical` workflows) now runs `go build ./...` after the changes check, so workflows fail fast on commits that don't compile.
- `05df2ec` Adds `[git]` config section with `name`, `email`, and `ssh_key` fields; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses the resolved identity for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `7128952` `cloche init` now prompts for SSH key setup interactively; adds `--non-interactive` flag to skip all prompts and `--ssh-key <path>` to write `ssh_key` into `.cloche/config.toml` non-interactively.
- `eea6192` `cloche init` SSH key setup now detects the project's GitHub remote origin and shows the direct deploy-key settings URL (`github.com/<owner>/<repo>/settings/keys`) when prompting for key generation.
- `b583c29` `cloche doctor` now verifies that the configured `[git] ssh_key` file exists and is readable, loading the merged global + project config; reports a warning (not a failure) when the file is missing.
- `e54c52f` New `cloche debug goroutines` and `cloche debug state` subcommands expose the running daemon's goroutine stacks, active run IDs, orchestration loops, and container session state; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG=<addr>` or `[daemon] debug` in global config.

### Fixes

- `a5c63e3` When the outer workflow context is cancelled (e.g. via `cloche stop`), the engine now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g. an `unclaim` step) before marking the run `cancelled`.
- `091673c` Broadcaster history is flushed to disk (as `full.log` and per-step logs) when a run is torn down, ensuring `cloche logs` returns output for runs that failed mid-execution with no on-disk log yet.
- `8152eef` Workflow-level `container { image = "..." }` is now read when dispatching a container sub-workflow via `workflow_name`, overriding the daemon default; also adds `child_branch` to the auto-seeded KV store (set before the sub-workflow runs) so merge scripts can reference the extracted branch name before `child_run_id` is available.
- `ca58e67` `cloche shutdown --restart` now polls the daemon address until the old process stops accepting connections before launching the replacement, preventing two daemons from running simultaneously.
- `2185d67` `SessionFor` now has a dedicated 2-minute AgentReady timeout; when exceeded, the container is stopped and its logs are included in the returned error, replacing the previous behavior of blocking until the step's 30-minute timeout.
- `4d6f87f` After `docker start`, `runtime.Start` polls until the container reaches `Running` state and returns an error if it does not transition; a background goroutine also watches for early container exit so `SessionFor` fails fast with logs rather than waiting the full ready timeout; `runtime.Start` now logs each sub-phase with wall-clock timing.
- `46f0158` External directory and file symlinks in the project are now inlined as regular entries in the tar archive sent to the container, preventing Docker tarslip protection from silently dropping them and leaving the workspace incomplete.
- `7ff07bb` External symlinks nested inside an already-dereferenced external directory are now recursively inlined rather than emitted as symlink entries that Docker's tarslip guard would reject.
- `0d1d24b` `addDereferencedEntry` now returns an error when an external symlink's target is inaccessible (previously it printed a warning to stderr and silently continued, producing an incomplete workspace).
- `fdb3d32` `copyProjectToContainer` now closes the tar pipe with an error on walk failure (so `docker cp` receives a broken stream and exits non-zero) and treats any `docker cp` stderr output as an error even when the process exits 0.
- `f62b7bf` The daemon now rejects `EnableLoop` requests for project directories nested inside an already-active loop's scope, and stops superseded child loops when a parent loop is enabled; startup deduplication also filters nested paths.
- `02dee9e` Step log files are now opened in append mode in the generic and prompt adapters, and the session and host status handler track per-step byte offsets so only new output is written to `full.log` per loop iteration.

### Internal

- `ef9ad29` Added `cloched --project` flag to scope the daemon to a single project directory.
- `414760e` Reverts the `--project` flag added in `ef9ad29` (the approach was wrong; the correct fix is loop deduplication, implemented in `f62b7bf`).
- `2c3b541` Release publish script now unsets `GITHUB_TOKEN` before pushing to avoid using the environment token instead of the configured SSH key.

## v3.14.21 — 2026-05-12

### Features

- Adds `[[repositories]]` array-of-tables section to `config.toml` for declaring named source-code repositories (`name`, `path`, `default` fields). Loaded by a new `internal/project` package into `domain.Project`.
- Adds top-level `repository "name" { path, url, default }` block to the `.cloche` DSL. `ParseRepositoriesFrom` reads repository blocks from a file; `ParseAll` silently skips them so existing workflow parsing is unaffected.
- Adds `repos = ["name", ...]` workflow-level field to the DSL, stored in `domain.Workflow.Repos`. Documents which repositories a workflow depends on.
- `cloche project` now includes a `Repositories:` section listing each repository's name, path, URL, and default flag when repositories are declared. New `cloche project repos list` subcommand prints the repository table in isolation.
- Adds `Repository` proto message to `GetProjectInfoResponse` (field 16); repositories are returned by the `GetProjectInfo` gRPC RPC.

## v3.14.18 — 2026-05-05

### Features

- `05df2ec` Adds `[git]` config section with `name`, `email`, and `ssh_key` fields; exports `CLOCHE_GIT_AUTHOR_NAME`, `CLOCHE_GIT_AUTHOR_EMAIL`, and `CLOCHE_GIT_SSH_COMMAND` to host scripts and uses the resolved identity for extraction commits. ([design](docs/plans/2026-04-21-git-identity-design.md))
- `7128952` `cloche init` now prompts for SSH key setup interactively; adds `--non-interactive` flag to skip all prompts and `--ssh-key <path>` to write `ssh_key` into `.cloche/config.toml` non-interactively.
- `eea6192` `cloche init` SSH key setup now detects the project's GitHub remote origin and shows the direct deploy-key settings URL (`github.com/<owner>/<repo>/settings/keys`) when prompting for key generation.
- `b583c29` `cloche doctor` now verifies that the configured `[git] ssh_key` file exists and is readable, loading the merged global + project config; reports a warning (not a failure) when the file is missing.
- `e54c52f` New `cloche debug goroutines` and `cloche debug state` subcommands expose the running daemon's goroutine stacks, active run IDs, orchestration loops, and container session state; requires `cloched --debug-addr <addr>` or `CLOCHE_DEBUG=<addr>` or `[daemon] debug` in global config.

### Fixes

- `a5c63e3` When the outer workflow context is cancelled (e.g., via `cloche stop`), the engine now synthesizes a `fail` result for the active step and walks fail-branch wires (e.g., an `unclaim` step) before marking the run `cancelled`.
- `091673c` Broadcaster history is flushed to disk (as `full.log` and per-step logs) when a run is torn down, ensuring `cloche logs` returns output for runs that failed mid-execution with no on-disk log yet.
- `8152eef` Workflow-level `container { image = "..." }` is now read when dispatching a container sub-workflow via `workflow_name`, overriding the daemon default; previously the daemon default was always used regardless of the workflow's own container config.
- `ca58e67` `cloche shutdown --restart` now polls the daemon address until the old process stops accepting connections before launching the replacement, preventing two daemons from running simultaneously.
- `2185d67` `SessionFor` now has a dedicated 2-minute AgentReady timeout; when exceeded, the container is stopped and its logs are included in the returned error, replacing the previous behavior of blocking until the step's 30-minute timeout.
- `4d6f87f` After `docker start`, `runtime.Start` polls until the container reaches `Running` state and returns an error if it does not transition; a background goroutine also watches for early container exit so `SessionFor` fails fast with logs rather than waiting the full ready timeout.
- `46f0158` External directory and file symlinks in the project are now inlined as regular entries in the tar archive sent to the container, preventing Docker tarslip protection from silently dropping them and leaving the workspace incomplete.
- `7ff07bb` External symlinks nested inside an already-dereferenced external directory are now recursively inlined rather than emitted as symlink entries that Docker's tarslip guard would reject.
- `0d1d24b` `addDereferencedEntry` now returns an error when an external symlink's target is inaccessible (previously it printed a warning to stderr and silently continued, producing an incomplete workspace).
- `fdb3d32` `copyProjectToContainer` now closes the tar pipe with an error on walk failure (so `docker cp` receives a broken stream and exits non-zero) and treats any `docker cp` stderr output as an error even when the process exits 0.
- `f62b7bf` The daemon now rejects `EnableLoop` requests for project directories nested inside an already-active loop's scope, and stops superseded child loops when a parent loop is enabled; startup deduplication also filters nested paths.
- `02dee9e` Step log files are now opened in append mode in the generic and prompt adapters, and the session and host status handler track per-step byte offsets so only new output is written to `full.log` per loop iteration.

### UI/UX

- `8152eef` Adds `child_branch` to the auto-seeded KV store, set to the extracted git branch name before the container sub-workflow runs, so merge scripts can reference it without waiting for `child_run_id`.
- `e54c52f` `runtime.Start` now logs each sub-phase (create, copy project, copy auth, start, verify running) with wall-clock timing to aid diagnosis of slow or stuck container startup.

### Internal

- `ef9ad29` Added `cloched --project` flag to scope the daemon to a single project directory.
- `414760e` Reverts the `--project` flag added in `ef9ad29` (the approach was wrong; the correct fix is loop deduplication, implemented in `f62b7bf`).
- `2c3b541` Release publish script now unsets `GITHUB_TOKEN` before pushing to avoid using the environment token instead of the configured SSH key.

## v3.14.0 — 2026-04-15

### Breaking

- `3106e73` Removes wire output mapping syntax (`step:result -> next [ VAR = output.field ]`) from the DSL parser. Migration: remove `[ KEY = output.field ]` clauses from all wires in `.cloche/*.cloche` files.
- `0fe3c40` Removes `OutputMapping`, `OutputPath`, and `PathSegment` domain types and wire mapping evaluation from the host executor and docs. Migration: same as above.
- `58e52b7` Removes wire output mapping documentation from `docs/workflows.md` and `docs/USAGE.md` and cleans up residual executor code. Migration: same as above.
- `6398be0` Removes the `CLOCHE_STEP_OUTPUT` environment variable from host step scripts. Migration: print step output to stdout rather than writing to `$CLOCHE_STEP_OUTPUT`.
- `1444009` Removes the `feedback = "true"` step config key from the prompt adapter, domain types, and docs. Migration: remove `feedback = "true"` from step configs; use `{previous_output}` in prompt templates or read `$CLOCHE_PREV_OUTPUT` in script steps to access the preceding step's output.

### Features

- `416064d` Adds `changelog` and `release` host workflows to `.cloche/host.cloche` for automated changelog drafting, release tagging, and GitHub release publication. ([design](docs/plans/2026-04-15-release-process-design.md))
- `6bd5c8d` Adds `cloche extract <id>` CLI command to copy a container's `/workspace` to a git branch/worktree (`--at`, `--branch`) or plain directory (`--no-git --at`); the container must be retained with `--keep-container`. ([design](docs/plans/2026-04-14-cloche-extract-design.md))
- `d161d05` Adds `version` as an explicit subcommand to `cloche`, `cloched`, `cloche-agent`, and `clo`, alongside the existing `-v`/`--version` flags.
- `876b83c` Adds compound step name support to `cloche logs`: the form `subWorkflow:step` (e.g., `develop:implement`) addresses a specific step's log within a sub-workflow's extracted log directory; a 4-part composite ID (`task:attempt:subWorkflow:step`) is also accepted.
- `876b83c` `cloche init` now creates `prompts/`, `overrides/`, and `scripts/` subdirectories automatically during initialization.

### Fixes

- `876b83c` Container logs are now extracted from a sub-workflow's container using a background context when the parent context is cancelled (e.g., step timeout), so logs are preserved for post-mortem investigation.
- `29b1425` Removes the stray `protoc-25.1-linux-x86_64.zip` committed to the repository root and adds `protoc-*.zip` to `.gitignore` to prevent recurrence.

### UI/UX

- `a553e05` Improves `cloche extract` error messages: the error for a removed container now names the run ID and suggests `--keep-container`; the error for missing git data suggests `--no-git`.

### Internal

- `0601fea` Adds design document for `cloche extract`; initial refactor of `ExtractResults` to accept an `ExtractOptions` struct (preserving existing call-site behavior).
- `9c1bdf0` Extends `ExtractOptions` with `TargetDir`, `Branch`, `NoGit`, and `Persist` fields; introduces `dockerCp` package-level hook for test overriding; adds comprehensive unit tests.
- `07fd4a4` Adds `ExtractRun` gRPC RPC: defines `ExtractRunRequest`/`ExtractRunResponse` proto types, regenerates bindings, and implements the server handler.
- `465d10e` Adds table-driven `TestExtractResultsOptions` test suite covering all `ExtractOptions` field combinations.
- `5895efb` Removes duplicate `branchExists` helper introduced in `extract_test.go`.
- `bfa750c` Removes the `Env` map field from the `ExecuteStep` proto message (unused after wire mapping removal); updates generated code and documentation.
- `53989c0` Fixes the changelog collection script to retain develop-workflow squash commits in the commit corpus; updates the agent prompt to explain how to handle auto-generated commit subjects.

