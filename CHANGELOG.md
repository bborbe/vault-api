# Changelog

All notable changes to this project will be documented in this file.

## v0.10.0

- feat: Add `--event-vault-name` / `EVENT_VAULT_NAME`, the vault slug stamped on task change events as `lib.Task.Vault`. It is separate from `--vault-name` because the two are different identities: `--vault-name` is the display name the read API reports, while the event field is the routing key a consumer compares against its own vault. Before this, an instance whose display name differs from its routing slug stamped the display name, and every task it published was dropped as foreign by the executor that owns it (`skip task <id>: foreign vault vault=<display> task_vault=<slug>`) — the task was never spawned. Empty falls back to `--vault-name`, so an instance where the two coincide is unchanged.

## v0.9.0

- feat: Add `POST /api/v1/merge`, the production caller of `ResolveDivergedPull`. It takes the three sides of a diverged file — `base`, `ours` and `theirs`, each with its committer time — resolves them per frontmatter key and writes the merged bytes through git-rest, answering `{"result":"merged"}` or `{"result":"quarantined","reason":…}`. The seam had shipped since v0.6.0 with no caller, so no running path ever invoked the merge; the endpoint supplies one without making `vault-api` a puller, which it is not — git-rest pulls and the refresh loop only compares HEAD.
- feat: Log one `pull: merged` line per genuinely reconciled file, naming the path and both sides' committer times, so a merge the API performed is distinguishable from one git-rest resolved on its own. The line is emitted only when both sides changed relative to the base and only after the write succeeds: a quarantined merge, a fast-forward and a failed write each log nothing, so the marker cannot be satisfied by an unconditional log.
- fix: Confine `POST /api/v1/merge`'s client-supplied `path` and give the route its own body cap. The path is refused when it carries a `..` segment, a control character, a leading `/` or surrounding whitespace: it becomes both a git-rest URL segment and part of the log line, and `url.PathEscape` leaves dots unreserved, so `..` would otherwise survive into the outbound URL. The cap is 32 MiB rather than the write endpoints' 64 KiB, which was chosen for a phase name and an approver and cannot hold three file contents. **This route also refuses to run when `GATEWAY_SECRET` is empty** (503 `merge_requires_gateway_secret`) instead of inheriting the group's "empty secret = no auth" contract: it writes caller-supplied content to an arbitrary vault-relative path, a strictly larger blast radius than the other write endpoints, which resolve their target through the index and enforce the phase and status graphs.

## v0.8.0

- feat: Publish a task change event from every successful write, so `vault-api` becomes the producer the executor's compacted task view reads. The three write handlers publish through `pkg/publisher.TaskPublisher` after git-rest accepts the file — at the single `document.write` chokepoint, so no handler carries its own copy. A publish failure is logged and the request still answers 200: the file is already in git, an error would make the client retry a transition that is now applied, and the pull path publishes the same change from the commit diff. `--kafka-brokers` / `KAFKA_BROKERS` enables the producer and `--topic-prefix` / `TOPIC_PREFIX` selects the per-cluster topic; both are optional, and unset means publishing is disabled while reads and writes are unaffected. The producer is built off the startup path behind a `publisher.Deferred` handle that **both** the write handlers and the pull feed share, so an unreachable broker cannot delay the read API and one change is published once: publishes report a not-ready error until construction succeeds. On the request path a publish is bounded by a 5 s timeout and runs under `context.WithoutCancel`, so neither a broker that goes away after startup nor a client hanging up can hold open or cancel a response that is already decided. With no brokers configured the handle holds a discarding publisher, so a deliberately disabled producer does not log an error on every successful write.

## v0.7.0

- feat: Detect changes vault-api did not write itself and publish one change event per changed task file. The refresh loop diffs the previous HEAD against the current one with `git diff-tree` — not `git diff`, because the vault volume is mounted read-only and `git diff` refreshes the index — and hands every added, modified or deleted `25 Tasks/*.md` to the `TaskPublisher`. A deletion is read from the old commit, so its tombstone keeps the identifier the file actually carried instead of a slug guess. A pull that touches only goals emits nothing, and the HEAD baseline advances only after a batch publishes, so a Kafka failure retries the same diff rather than dropping it. This is the pull-side producer that must exist before the controller's git scan can retire.

## v0.6.1

- fix: send the `X-Gateway-Initator` header on every git-rest write. git-rest's auth middleware answers **500** when that header is missing, and it checks it *before* the secret — so every write in v0.6.0 failed with `git_rest_failed` against a real git-rest. The misspelling is deliberate and part of git-rest's frozen public contract; the value is `vault-api`. Found by running the SC probes against the released binary and a local git-rest, not by the unit tests, which stop at a fake writer and never cross the real boundary.

## v0.6.0

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
