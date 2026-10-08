DATABASE_URL ?= postgres://wallet:wallet@localhost:5432/wallet?sslmode=disable

.PHONY: fmt vet nofloat test race integration e2e migrate-up migrate-down

fmt:
	gofmt -l -w .

nofloat:
	@! grep -rnE 'float32|float64|ParseFloat|big\.Float' --include='*.go' $(wildcard internal/domain internal/app)

vet:
	go vet ./...

test:
	go test ./...

race:
	go test -race ./...

integration:
	go test -race -tags=integration ./...

e2e:
	go test -tags=e2e ./...

migrate-up:
	migrate -path migrations -database "$(DATABASE_URL)" up

migrate-down:
	migrate -path migrations -database "$(DATABASE_URL)" down 1
