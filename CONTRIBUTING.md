# Contributing

Thank you for improving Booking Inventory Service.

## Development setup

1. Install the Go version declared in `go.mod`.
2. Start a disposable PostgreSQL 15 instance.
3. Copy `.env.example` to `.env` and replace every placeholder.
4. Run the quality checks:

   ```bash
   make quality
   make coverage
   ```

## Integration tests

`TEST_DATABASE_URL` must point to a disposable database. The integration suite truncates `inventory` and `reservations`.

```bash
TEST_DATABASE_URL='postgres://postgres:password@localhost:5433/stock_test?sslmode=disable' \
  make integration-test
```

Never use a production or shared development database.

## End-to-end smoke test

Start the Compose stack and verify the public HTTP contract:

```bash
docker compose --project-name booking-smoke up --detach --build --wait
API_KEY='<value from .env>' ADMIN_API_KEY='<value from .env>' make smoke-test
docker compose --project-name booking-smoke down --volumes
```

## Pull requests

- Keep changes focused and include tests for changed behavior.
- Preserve domain error wrapping with `errors.Is` compatibility.
- Keep inventory mutations transactional.
- Document API contract changes in `api/openapi.yaml` and `README.md`.
- Run `make quality` and the integration or smoke tests affected by the change before opening a pull request.

Use clear commit messages such as `feat: add reservation lookup` or `fix: aggregate expired stock restoration`.
