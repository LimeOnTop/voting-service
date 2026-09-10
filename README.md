# voting-service

High-load anonymous survey voting service: **stateless Go API + Redis (dedup / rate limit / counters) + PostgreSQL (polls & durable aggregates)**.

Votes are accepted on the Redis hot path and flushed asynchronously to PostgreSQL. The API does not store voter identity in Postgres.

## Architecture

```text
CDN / LB
   │
   ▼
Go API (N replicas, stateless)
   │
   ├── Redis  — SET NX dedup, rate limits, sharded counters
   │
   └── CronJob (voting-sync) — periodic absolute flush Redis → PostgreSQL
          │
          ▼
     PostgreSQL — polls, options, aggregated poll_results
```

**Why Go:** efficient concurrency for bursty HTTP, simple horizontal scale of stateless processes.

**Why Redis:** atomic `SET NX` for dedup, atomic counters, shared rate limits across replicas, fail-closed if unavailable.

**Why PostgreSQL:** durable poll configuration and aggregated results (`BIGINT` votes), not per-vote inserts.

**Why CronJob for sync:** API replicas scale independently; a one-shot `voting-sync` job (`concurrencyPolicy: Forbid`) persists aggregates without multiplying long-lived workers. Live admin results still read Redis; Postgres may lag by the schedule (default 1 minute).

**Dedup:** cookie `voter_id` → `SHA256(poll_id + token)` → Redis `SET NX`.

**Horizontal scaling:** no in-process vote state; any API instance can handle any request.

## Quick start

```bash
cp env.example .env
make up          # postgres + redis, migrations, then api + sync
```

One-shot sync locally (loads `.env` via `godotenv`):

```bash
make sync-once
```

Apply migrations only (Postgres must be up):

```bash
make migrate
```

Host ports (chosen to avoid clashes with other local stacks):

| URL | Purpose |
|-----|---------|
| http://localhost:8081/health/live | liveness |
| http://localhost:8081/health/ready | readiness |
| http://localhost:8081/api/v1/... | public + admin API |
| localhost:5433 | Postgres |
| localhost:6380 | Redis |

Admin auth: `Authorization: Bearer $ADMIN_TOKEN` (see `env.example`, ≥32 characters).

Run API on the host against compose Postgres/Redis (after `make up`, or with matching ports):

```bash
cp env.example .env   # REDIS_ADDRESSES=localhost:6380, DATABASE_URL on :5433
make run-api           # listens on HTTP_ADDRESS from .env (:8080 by default)
```

To expose the same port as compose’s published API:

```bash
HTTP_ADDRESS=:8081 make run-api
```

## API

OpenAPI 3: [api/openapi.yaml](api/openapi.yaml)

### Public

```http
GET  /api/v1/polls/{poll_id}
POST /api/v1/polls/{poll_id}/votes
```

Vote body (one or more options; multiple only when poll `type` is `multiple`):

```json
{ "option_ids": ["<uuid>", "<uuid>"] }
```

`POST …/votes` sets an HttpOnly cookie `voter_id` when missing (used for per-device dedup).

Statuses: `201` accepted, `400` bad request, `404` not found, `409` already voted / not active, `429` rate limited, `503` Redis unavailable.

Vote response:

```json
{ "status": "accepted" }
```

### Admin

```http
GET  /api/v1/admin/polls?limit=20&offset=0
POST /api/v1/admin/polls
POST /api/v1/admin/polls/{poll_id}/activate
POST /api/v1/admin/polls/{poll_id}/finish
GET  /api/v1/admin/polls/{poll_id}/results
```

Create body:

```json
{
  "question": "Which option?",
  "type": "single",
  "options": ["A", "B"],
  "max_choices": 1
}
```

`type` defaults to `single` (`max_choices` forced to 1). For `multiple`, `max_choices` defaults to the number of options when omitted.

Poll lifecycle: `draft` → `active` → `finished`. Voting only while `active`.

## Configuration

See [env.example](env.example). Secrets must not be committed (`.env` is gitignored).

| Variable | Default / example | Meaning |
|----------|-------------------|---------|
| `APP_ENV` | `development` | `development` or `production` |
| `HTTP_ADDRESS` | `:8080` | Listen address (compose maps host `8081` → container `:8080`) |
| `LOG_LEVEL` | `info` | `debug` / `info` / `warn` / `error` |
| `DATABASE_URL` | (required) | Postgres DSN |
| `REDIS_ADDRESSES` | (required) | Comma-separated `host:port` list |
| `REDIS_DB` | `0` | Redis DB (standalone) |
| `ADMIN_TOKEN` | (required, ≥32 chars) | Admin bearer token |
| `SYNC_TIMEOUT` | `30s` | Max duration of one sync job |
| `SYNC_SETTLE_AFTER` | `10m` | Keep syncing a finished poll this long |
| `SYNC_CRON_SECONDS` | `60` | Compose sync loop only (not read by Go) |
| `VOTER_COOKIE_NAME` | `voter_id` | Dedup cookie name |
| `VOTER_COOKIE_SECURE` | `true` (`false` in `env.example`) | Secure flag on cookie |

## Testing

```bash
make test
make lint
```

Covered: poll validation/create, activate/finish rules, vote path, atomic dedup under concurrency, rate limiting, percentage calculation, Redis fail-closed, invalid option.

## Performance notes

Horizontal API scaling is supported (stateless handlers, Redis shared state). Measure on your machine and record results here if needed for the write-up.

## Makefile

| Target | Action |
|--------|--------|
| `make up` | postgres/redis → migrate → api + sync |
| `make down` | stop & remove volumes |
| `make migrate` | apply SQL migrations |
| `make run` / `make run-api` | run API locally (`go run ./cmd/api`) |
| `make sync-once` | one sync tick (`go run ./cmd/worker`) |
| `make test` | unit tests |
| `make tidy` / `make lint` | go mod tidy / go vet |

## Project layout

```text
cmd/api                 HTTP API entrypoint
cmd/worker              one-shot sync job (CronJob)

# Architectural layers
internal/controller     HTTP handlers / routing (API layer)
internal/usecase        contracts (interfaces + port DTOs)
internal/service        business logic
internal/repository     PostgreSQL adapter
internal/middleware     HTTP middleware
internal/entity         domain model

# Purpose-named packages
internal/votestore      Redis vote hot path
internal/pollcache      poll config cache
internal/ttlcache       generic in-process TTL cache
internal/syncjob        CronJob sync runner
internal/httpjson       JSON request/response helpers
internal/clients        Postgres/Redis client wiring
internal/health         liveness/readiness probes
internal/config         env configuration

deploy/k8s              CronJob manifest
migrations/             SQL migrations
api/openapi.yaml        OpenAPI 3
```

## Production evolution

Redis Sentinel/Cluster or managed Redis; CDN for `GET /polls/{id}`; optional Kafka as durable buffer before aggregation; Postgres HA. Kafka is intentionally out of scope for this assignment.
