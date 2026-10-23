# Changelog

## Unreleased

- feat: Add `PATCH /api/v1/tasks/{id}/phase`, `PATCH /api/v1/tasks/{id}/status` and `POST /api/v1/tasks/{id}/approve`, enforcing the phase and status graphs server-side and writing the rendered file through git-rest. A refusal names its class — `invalid_transition`, `approval_required`, `approval_is_a_verb`, `not_approvable`, `already_approved`, `blank_approver`, `unknown_phase`, `unknown_status` — and carries `from`, `to`, `allowed` and `path` where they apply.
- feat: Add the `--git-rest-url` / `GIT_REST_URL` flag naming the vault's git-rest service the write endpoints post to.

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
