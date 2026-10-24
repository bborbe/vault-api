# vault-api

Typed read API over an Obsidian vault working tree. It serves typed tasks and goals from an in-memory index instead of one Markdown read per file, using `vault-cli`'s domain types rather than a parallel model.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/tasks` | List tasks; filters `assignee`, `status`, `phase`, `goal`, `cluster` |
| `GET /api/v1/tasks/{id}` | One task by `task_identifier` or filename slug; 422 when its frontmatter is invalid |
| `GET /api/v1/goals` | List goals |
| `GET /api/v1/goals/{id}` | One goal by `goal_identifier` or filename slug |
| `GET /readiness` | 200 with `vault`, `index_age_seconds`, `tree_age_seconds`, `head`, `warnings`; 503 until the first index build, and again whenever either freshness signal leaves `--stale-after` |
| `GET /healthz`, `/metrics`, `/setloglevel/{level}`, `/gc` | Fleet admin endpoints |

List responses carry `items`, `total`, `warnings` and `index_age_seconds`. A file whose frontmatter does not parse, or whose `status`/`phase` is present but not a known value, is excluded from list results and counted in `warnings` — never coerced.

## Readiness

`GET /readiness` answers `200` only while **all three** hold, and `503` otherwise:

1. the last build succeeded — a failed rebuild leaves the previous index serving but clears the flag, and the next good build restores it;
2. `index_age_seconds` — how long since the index was last confirmed to match the tree — is inside `--stale-after`. Every refresh tick that finds the HEAD unmoved counts as a confirmation, so a steady tree does not age the index;
3. `tree_age_seconds` — how long since `.git/FETCH_HEAD` was written, i.e. since the tree was last fetched from its remote — is inside `--stale-after`. Every `git fetch` rewrites that file, including one that brings nothing new, so its age is the puller's heartbeat. `null` when the tree carries no `FETCH_HEAD` (a clone that was never fetched), in which case this signal is skipped.

The two ages are separate because they fail independently: a rebuild can fail on a live tree, and a tree can freeze under a perfectly healthy loop. The second is what catches a tree nothing advances — a frozen tree and a quiet one both leave the HEAD unmoved, so a pod comparing HEAD to HEAD sees nothing wrong while the tree falls arbitrarily far behind the remote.

`--stale-after` defaults to `5m`, ten times the 30 s `--refresh-interval`; `0` disables the rule, which is what a local run against a tree nothing fetches wants. Because `/readiness` is the `readinessProbe` on every deployment, a pod whose tree stops being fetched leaves its Service endpoints rather than serving stale data — `/healthz` is untouched, so it is never restarted for it.

When `GATEWAY_SECRET` is set, every `/api/v1/*` request must carry it in `X-Gateway-Secret`.

## Run

```bash
go run . \
  --vault-path /path/to/vault \
  --vault-name private-personal \
  --event-vault-name personal \
  --tasks-dir "25 Tasks" \
  --goals-dir "24 Goals"
```

`--vault-name` is the display name the read API reports. `--event-vault-name` is the
routing slug stamped on task change events as `lib.Task.Vault`, and it must equal the
`agent-task-controller`'s `VAULT_NAME` and the `agent-task-executor`'s `TASK_VAULT` for
the same vault. A consumer owns a task only when the event's value equals its own, so a
mismatch drops every task this instance publishes as `foreign vault` and nothing is
spawned. Omitted, it falls back to `--vault-name` — correct only where the two names are
the same.

## Build

```bash
make test
make precommit
```

The image is `docker.io/bborbe/vault-api`; `make build upload` publishes it from a tagged tree.
