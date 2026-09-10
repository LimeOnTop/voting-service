# voting-service

Anonymous high-load voting API: Go + Redis (hot path) + PostgreSQL (polls and durable aggregates).

Votes are accepted in Redis and flushed to PostgreSQL by a one-shot sync process. Voter identity is never stored in Postgres.

## Quick start

```bash
cp env.example .env
make up                 # postgres, redis, migrations, api, sync loop
make test               # unit tests
make lint
```

| URL | Purpose |
|-----|---------|
| http://localhost:8081/health/live | liveness |
| http://localhost:8081/health/ready | readiness (Redis + Postgres) |
| http://localhost:8081/api/v1/... | public + admin API |
| localhost:5433 | Postgres |
| localhost:6380 | Redis |

Admin: `Authorization: Bearer $ADMIN_TOKEN` (see `env.example`, ≥32 characters).

### Smoke check

```bash
export BASE=http://localhost:8081
export ADMIN_TOKEN=0123456789abcdef0123456789abcdef

curl -sS "$BASE/health/ready"

curl -sS -X POST "$BASE/api/v1/admin/polls" \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H "Content-Type: application/json" \
  -d '{"question":"Favourite colour?","type":"single","options":["Red","Blue"]}'

# use id / option id from the create response:
curl -sS -X POST "$BASE/api/v1/admin/polls/<poll_id>/activate" \
  -H "Authorization: Bearer $ADMIN_TOKEN"

curl -sS -X POST "$BASE/api/v1/polls/<poll_id>/votes" \
  -H "Content-Type: application/json" \
  -c /tmp/voter.txt \
  -d '{"option_ids":["<option_id>"]}'

curl -sS "$BASE/api/v1/admin/polls/<poll_id>/results" \
  -H "Authorization: Bearer $ADMIN_TOKEN"
```

Stop the stack: `make down`.

### API on the host (optional)

With compose Postgres/Redis already up:

```bash
cp env.example .env
make migrate            # if DB is empty
HTTP_ADDRESS=:8081 make run-api
```

One-shot sync (same binary as the compose sync tick):

```bash
make sync-once
```

## Architecture

```text
Go API (N replicas, stateless)
   ├── Redis   — dedup (SET NX), rate limits, sharded counters
   └── sync    — absolute flush Redis → PostgreSQL (compose loop)
          └── PostgreSQL — polls, options, poll_results
```

- **Dedup:** cookie `voter_id` → `SHA256(poll_id + token)` → Redis `SET NX`
- **Results:** admin reads `max(Redis, Postgres)`; Postgres may lag by the sync interval
- **Scale:** no in-process vote state; any API replica can serve any request

## API

Contract: [api/openapi.yaml](api/openapi.yaml)

| Method | Path | Auth |
|--------|------|------|
| `GET` | `/api/v1/polls/{poll_id}` | — |
| `POST` | `/api/v1/polls/{poll_id}/votes` | cookie `voter_id` |
| `GET` | `/api/v1/admin/polls` | Bearer |
| `POST` | `/api/v1/admin/polls` | Bearer |
| `POST` | `/api/v1/admin/polls/{poll_id}/activate` | Bearer |
| `POST` | `/api/v1/admin/polls/{poll_id}/finish` | Bearer |
| `GET` | `/api/v1/admin/polls/{poll_id}/results` | Bearer |

Vote body: `{ "option_ids": ["<uuid>"] }` → `{ "status": "accepted" }` (`201`).

Create body:

```json
{
  "question": "Which option?",
  "type": "single",
  "options": ["A", "B"],
  "max_choices": 1
}
```

Lifecycle: `draft` → `active` → `finished`. Voting only while `active`.

Statuses: `400` bad request, `401` unauthorized, `404` not found, `409` conflict / already voted, `429` rate limited, `503` Redis unavailable.

## Configuration

Template: [env.example](env.example). Do not commit `.env`.

| Variable | Example | Meaning |
|----------|---------|---------|
| `APP_ENV` | `development` | `development` / `production` |
| `HTTP_ADDRESS` | `:8080` | listen address (compose publishes `8081→8080`) |
| `DATABASE_URL` | required | Postgres DSN |
| `REDIS_ADDRESSES` | required | comma-separated `host:port` |
| `ADMIN_TOKEN` | ≥32 chars | admin bearer token |
| `SYNC_TIMEOUT` | `30s` | max duration of one sync run |
| `SYNC_SETTLE_AFTER` | `10m` | keep syncing a finished poll this long |
| `SYNC_CRON_SECONDS` | `60` | compose sync loop only |
| `VOTER_COOKIE_SECURE` | `false` locally | must be `true` in production |

## Makefile

| Target | Action |
|--------|--------|
| `make up` | postgres/redis → migrate → api + sync |
| `make down` | stop and remove volumes |
| `make migrate` | apply SQL migrations |
| `make run-api` | run API on the host |
| `make sync-once` | one Redis→Postgres sync |
| `make test` | `go test ./...` |
| `make lint` | `go vet ./...` |
