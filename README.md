# vault-api

Typed read API over an Obsidian vault working tree. It serves typed tasks and goals from an in-memory index instead of one Markdown read per file, using `vault-cli`'s domain types rather than a parallel model.

## Endpoints

| Endpoint | Purpose |
|---|---|
| `GET /api/v1/tasks` | List tasks; filters `assignee`, `status`, `phase`, `goal`, `cluster` |
| `GET /api/v1/tasks/{id}` | One task by `task_identifier` or filename slug; 422 when its frontmatter is invalid |
| `GET /api/v1/goals` | List goals |
| `GET /api/v1/goals/{id}` | One goal by `goal_identifier` or filename slug |
| `GET /readiness` | 200 with `vault`, `index_age_seconds`, `head`; 503 until the first index build |
| `GET /healthz`, `/metrics`, `/setloglevel/{level}`, `/gc` | Fleet admin endpoints |

List responses carry `items`, `total`, `warnings` and `index_age_seconds`. A file whose frontmatter does not parse, or whose `status`/`phase` is present but not a known value, is excluded from list results and counted in `warnings` — never coerced.

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
