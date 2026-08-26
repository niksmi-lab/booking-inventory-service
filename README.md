# Booking Inventory Service

[![CI](https://github.com/niksmi-lab/booking-inventory-service/actions/workflows/ci.yml/badge.svg)](https://github.com/niksmi-lab/booking-inventory-service/actions/workflows/ci.yml)
[![Go](https://img.shields.io/badge/Go-1.25-00ADD8?logo=go&logoColor=white)](https://go.dev/)
[![PostgreSQL](https://img.shields.io/badge/PostgreSQL-15-4169E1?logo=postgresql&logoColor=white)](https://www.postgresql.org/)
[![OpenAPI](https://img.shields.io/badge/OpenAPI-3.1-6BA539?logo=openapiinitiative&logoColor=white)](./api/openapi.yaml)

A production-minded Go microservice for atomic inventory reservations. It prevents overselling under concurrent requests, supports idempotent order workflows, automatically releases expired reservations, and exposes operational health and Prometheus metrics.

[Русская версия](./docs/README.ru.md) · [OpenAPI specification](./api/openapi.yaml) · [Production hardening notes](./PRODUCTION_CHANGES.md)

## Why this project is interesting

- Transactional reservations with PostgreSQL row locks.
- Cross-replica idempotency through transaction-scoped advisory locks.
- Deterministic product ordering to reduce deadlock risk.
- Explicit reservation state transitions: `pending → confirmed / cancelled / expired`.
- Safe aggregated stock restoration for multiple reservations of the same product.
- Strict JSON contracts, typed domain errors, request IDs and separate admin credentials.
- Graceful shutdown, readiness checks, structured logs and Prometheus metrics.
- Unit, race and real-PostgreSQL integration tests.

## Architecture

```mermaid
flowchart LR
    Client[Order service] --> HTTP[HTTP / Auth / Validation]
    Admin[Admin service] --> HTTP
    HTTP --> Service[Inventory service]
    Service --> Repo[PostgreSQL repository]
    Worker[Expiration worker] --> Repo
    Repo --> DB[(PostgreSQL)]
    Prometheus[Prometheus] --> Metrics[/metrics]
    Metrics --> HTTP
```

The HTTP layer knows only service contracts. The service owns validation and normalization. PostgreSQL remains the source of truth for concurrency and state transitions.

## Reservation flow

```mermaid
sequenceDiagram
    participant O as Order service
    participant I as Inventory API
    participant P as PostgreSQL

    O->>I: POST /reserve (order_id, items)
    I->>P: advisory lock(order_id)
    I->>P: SELECT inventory FOR UPDATE
    I->>P: decrement stock + create pending reservation
    P-->>I: COMMIT
    I-->>O: 200 success

    alt payment completed
        O->>I: POST /confirm
        I->>P: pending → confirmed
    else order cancelled or TTL elapsed
        O->>I: POST /cancel
        I->>P: restore stock + cancelled
    end
```

## Quick start

Requirements: Docker and Docker Compose.

```bash
cp .env.example .env
```

Replace `POSTGRES_PASSWORD`, `API_KEY` and `ADMIN_API_KEY` in `.env`. Both API keys must contain at least 32 characters and must be different.

```bash
docker compose up --build
```

Check the service:

```bash
curl http://localhost:8080/healthz
curl http://localhost:8080/readyz
```

- `/healthz` confirms that the process is alive.
- `/readyz` verifies PostgreSQL connectivity and the required schema.
- `/metrics` exposes Prometheus metrics and requires the admin Bearer token.

## API overview

| Method and path | Credential | Purpose |
|---|---|---|
| `POST /api/v1/stock/restock` | `ADMIN_API_KEY` | Increase stock |
| `POST /api/v1/stock/reserve` | `API_KEY` | Reserve order items |
| `POST /api/v1/stock/confirm` | `API_KEY` | Confirm a reservation |
| `POST /api/v1/stock/cancel` | `API_KEY` | Cancel and restore stock |
| `POST /api/v1/stock/clear` | `API_KEY` | Backward-compatible cancel alias |
| `GET /metrics` | `ADMIN_API_KEY` | Prometheus metrics |

Example:

```bash
curl -X POST http://localhost:8080/api/v1/stock/reserve \
  -H 'Content-Type: application/json' \
  -H 'Authorization: Bearer <API_KEY>' \
  -d '{
    "order_id": "22222222-2222-2222-2222-222222222222",
    "items": [
      {
        "item_id": "11111111-1111-1111-1111-111111111111",
        "quantity": 2
      }
    ]
  }'
```

Successful commands return:

```json
{"status":"success"}
```

Errors use a stable envelope and never expose database diagnostics:

```json
{
  "error": {
    "code": "insufficient_stock",
    "message": "one or more products are unavailable",
    "request_id": "019..."
  }
}
```

See [`api/openapi.yaml`](./api/openapi.yaml) for the complete contract.

## Consistency guarantees

1. Duplicate cart lines are aggregated and validated before reaching storage.
2. Requests for the same `order_id` are serialized across service replicas.
3. Requested inventory rows are locked in stable UUID order.
4. Stock decrement and reservation creation commit atomically.
5. Repeating an identical reserve, confirm or cancel operation is safe.
6. Expired reservations cannot be confirmed.
7. Cancellation and expiration preserve the reservation audit trail.
8. Stock restoration uses `SUM(qty) GROUP BY product_id` to avoid lost updates.

## Configuration

| Variable | Default | Notes |
|---|---:|---|
| `DATABASE_URL` | — | Required outside Compose |
| `API_KEY` | — | Required, at least 32 characters |
| `ADMIN_API_KEY` | — | Required, different from `API_KEY` |
| `PORT` | `8080` | HTTP port |
| `RESERVATION_TTL` | `15m` | Pending reservation lifetime |
| `CLEANUP_INTERVAL` | `1m` | Expiration worker interval |
| `DB_OPERATION_TIMEOUT` | `3s` | Per-operation DB timeout |
| `SHUTDOWN_TIMEOUT` | `10s` | Graceful shutdown deadline |
| `DB_MAX_CONNECTIONS` | `20` | Pool upper bound |
| `DB_MIN_CONNECTIONS` | `2` | Warm pool size |
| `AUTO_MIGRATE` | `true` | Apply embedded migrations at startup |
| `TRUSTED_PROXIES` | empty | Comma-separated CIDRs |

## Development

```bash
make fmt
make test
make vet
```

Run the PostgreSQL integration suite only against a disposable database:

```bash
TEST_DATABASE_URL='postgres://postgres:password@localhost:5433/stock_test?sslmode=disable' \
  make integration-test
```

The CI workflow runs formatting checks, `go vet`, race-enabled tests, PostgreSQL integration tests and a Docker build.

## Repository guide

- [`internal/domain`](./internal/domain) — domain model and errors.
- [`internal/service`](./internal/service) — use cases, validation and normalization.
- [`internal/storage`](./internal/storage) — PostgreSQL transactions and migrations.
- [`internal/handlers`](./internal/handlers) — HTTP contract and middleware.
- [`cmd/main.go`](./cmd/main.go) — dependency wiring and process lifecycle.
- [`PRODUCTION_CHANGES.md`](./PRODUCTION_CHANGES.md) — detailed hardening rationale.
- [`CONTRIBUTING.md`](./CONTRIBUTING.md) — local development workflow.

## Production boundary

This service is designed as an internal service-to-service component. A public deployment should be placed behind a TLS-enabled gateway with rate limiting and OIDC or mTLS. Database backups, secret management, alert rules and deployment orchestration belong to the platform layer.
