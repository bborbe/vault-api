# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- feat: Detect changes vault-api did not write itself and publish one change event per changed task file. The refresh loop diffs the previous HEAD against the current one with `git diff-tree` — not `git diff`, because the vault volume is mounted read-only and `git diff` refreshes the index — and hands every added, modified or deleted `25 Tasks/*.md` to the `TaskPublisher`. A deletion is read from the old commit, so its tombstone keeps the identifier the file actually carried instead of a slug guess. A pull that touches only goals emits nothing, and the HEAD baseline advances only after a batch publishes, so a Kafka failure retries the same diff rather than dropping it. This is the pull-side producer that must exist before the controller's git scan can retire.

## v0.4.0

- feat: Add `pkg/publisher`, the producer half of the task change-event stream. `TaskPublisher` publishes one `lib.Task` record per task id onto `{prefix}-agent-task-v1-event` through `cdb.EventObjectSender` with `lib.TaskV1SchemaID` — so the topic name is derived from the schema, never assembled by hand — and a tombstone on delete. The interface takes only a context and a task, so the typed write handlers and the pull path that detects externally pushed changes can both call it.

## v0.3.0

- feat: Add `MergeFile`, a three-way per-key frontmatter merge for a diverged vault file. A key only one side changed is kept; a key both sides changed follows its field class — validated lifecycle transition for `status`/`phase`, larger integer for counters, order-preserving union for append-only lists, concatenation for `metrics_sessions`, later committer time otherwise. A body both sides edited differently quarantines with a named reason instead of being concatenated.

## v0.2.0

- feat: Add `vault_api_open_tasks{task_type,stage,vault}` reporting the number of open tasks — status neither `completed` nor `aborted` — per `task_type`, refreshed on every index build; a task without a `task_type` counts under `unknown`.
- feat: Add the `--stage` / `STAGE` flag and carry the vault name as a constant label, so one central VictoriaMetrics store can tell instances and stages apart.
- feat: Expose `task_type` on `GET /api/v1/tasks` items.

## v0.1.1

- Separate the task and goal warning counts, so each list endpoint's `total + warnings` equals its own directory's file count instead of sharing a combined figure.

## v0.1.0

- Add typed `GET /api/v1/tasks` and `GET /api/v1/goals` reads served from an in-memory index built from the vault working tree.
- Add `GET /readiness` reporting `vault`, `index_age_seconds` and the indexed commit `head`.
- Add `vault_api_file_reads_total` so a query window can be shown to serve from the index rather than from disk.
