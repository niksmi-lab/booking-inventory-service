.PHONY: fmt fmt-check test race-test vet integration-test build docker-build

fmt:
	go fmt ./...

fmt-check:
	test -z "$$(gofmt -l cmd internal)"

test:
	go test ./...

race-test:
	go test -race ./...

vet:
	go vet ./...

integration-test:
	@test -n "$$TEST_DATABASE_URL" || (echo "TEST_DATABASE_URL must point to a disposable PostgreSQL database" && exit 1)
	go test ./internal/storage -run TestPostgresRepoIntegration -count=1 -v

build:
	go build -trimpath -o bin/booking-inventory-service ./cmd

docker-build:
	docker build --tag booking-inventory-service:local .
