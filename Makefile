.PHONY: fmt fmt-check quality test race-test coverage vet integration-test build docker-build smoke-test

fmt:
	go fmt ./...

fmt-check:
	test -z "$$(gofmt -l cmd internal)"

quality: fmt-check vet race-test build

test:
	go test ./...

race-test:
	go test -race ./...

coverage:
	go test -cover ./internal/config ./internal/handlers ./internal/service ./internal/storage

vet:
	go vet ./...

integration-test:
	@test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL must point to a disposable PostgreSQL database" && exit 1)
	go test ./internal/storage -run TestPostgresRepoIntegration -count=1 -v

build:
	go build -trimpath -o bin/booking-inventory-service ./cmd

docker-build:
	docker build --tag booking-inventory-service:local .

smoke-test:
	@test -n "$$API_KEY" || (echo "API_KEY is required" && exit 1)
	@test -n "$$ADMIN_API_KEY" || (echo "ADMIN_API_KEY is required" && exit 1)
	./scripts/smoke-test.sh
