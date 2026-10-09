-include .env
export

.PHONY: fmt vet nofloat test race integration e2e up down migrate-up migrate-down

fmt:
	gofmt -l -w .

nofloat:
	@! grep -rnE 'float32|float64|ParseFloat|big\.Float' --include='*.go' $(wildcard internal/domain internal/app)

vet:
	go vet ./...
	go vet -tags=integration ./...
	go vet -tags=e2e ./...

test:
	go test ./...

race:
	go test -race ./...

integration:
	go test -race -p 1 -tags=integration ./...

e2e:
	PENDING_REFERENCE_TTL=20s docker compose up --build --wait
	go test -count=1 -timeout=20m -tags=e2e ./test/e2e/...

up:
	docker compose up --build --wait

down:
	docker compose down --volumes

migrate-up:
	docker compose run --rm migrations

migrate-down:
	docker compose run --rm migrations -path=/migrations -database="$(DATABASE_URL)" down 1
