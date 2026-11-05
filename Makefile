.PHONY: install dev-api dev-web test lint build docker

install:
	cd web && npm ci

dev-api:
	go run ./cmd/argos

dev-web:
	cd web && npm run dev

test:
	go test ./...

lint:
	go vet ./...
	cd web && npm run check

build:
	cd web && npm run build
	CGO_ENABLED=0 go build -trimpath -ldflags="-s -w" -o bin/argos ./cmd/argos

docker:
	docker build -t argos .
