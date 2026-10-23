# Changelog

All notable changes to this project will be documented in this file.

## Unreleased

- feat: Add `PATCH /api/v1/tasks/{id}/phase`, `PATCH /api/v1/tasks/{id}/status` and `POST /api/v1/tasks/{id}/approve`, enforcing the phase and status graphs server-side and writing the rendered file through git-rest. A refusal names its class — `invalid_transition`, `approval_required`, `approval_is_a_verb`, `not_approvable`, `already_approved`, `blank_approver`, `unknown_phase`, `unknown_status` — and carries `from`, `to`, `allowed` and `path` where they apply.
- feat: Add the `--git-rest-url` / `GIT_REST_URL` flag naming the vault's git-rest service the write endpoints post to.

## v0.5.0

- feat: Label `vault_api_open_tasks` by each task's own frontmatter `stage`, not by the instance constant, so the gauge reports the environment a task targets instead of the cluster serving it; a task with no `stage` counts under `unknown`. **Breaking for queries:** the instance's own stage moved from the `stage` label to `instance_stage`, so `stage="prod"` now selects tasks targeting prod — a per-cluster selector needs `instance_stage` as well. The `--stage` / `STAGE` flag is now `--instance-stage` / `INSTANCE_STAGE`.
- feat: Expose `stage` on `GET /api/v1/tasks` items.

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
