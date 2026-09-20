REGISTRY := registry.digitalocean.com/maulabs
IMAGE := tourplannerbot
GOOSE_VERSION := v3.25.0
GOOSE_INSTALL ?= $(HOME)/.goose
GOOSE_BINARY := $(GOOSE_INSTALL)/bin/goose
WEB_DIRECTORY := web

.PHONY: run build test tidy frontend-install frontend-build db-up db-down docker-up docker-down goose-install migrate-up migrate-status registry-push deploy

run: frontend-build
	export $$(grep -v '^#' .env | xargs) && go run ./cmd/bot

build: frontend-build
	CGO_ENABLED=0 go build -o bin/bot ./cmd/bot

test: frontend-build
	go test ./...

frontend-install:
	npm --prefix $(WEB_DIRECTORY) ci

frontend-build: frontend-install
	npm --prefix $(WEB_DIRECTORY) run build

tidy:
	go mod tidy

## Local dev: only the database
db-up:
	docker compose -f docker-compose.db.yml up -d

db-down:
	docker compose -f docker-compose.db.yml down

## Full stack: bot + database
docker-up:
	docker compose up --build

docker-down:
	docker compose down

## Database migrations use the standalone Goose binary.
goose-install:
	GOOSE_VERSION="$(GOOSE_VERSION)" GOOSE_INSTALL="$(GOOSE_INSTALL)" bash scripts/install-goose.sh

migrate-up:
	set -a; . ./.env; set +a; GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$DATABASE_URL" GOOSE_MIGRATION_DIR=migrations $(GOOSE_BINARY) up

migrate-status:
	set -a; . ./.env; set +a; GOOSE_DRIVER=postgres GOOSE_DBSTRING="$$DATABASE_URL" GOOSE_MIGRATION_DIR=migrations $(GOOSE_BINARY) status

## Local architecture image: never used by the production worker
docker-push:
	doctl registry login
	docker build -t $(REGISTRY)/$(IMAGE):local .
	docker push $(REGISTRY)/$(IMAGE):local

## Production images and deployments are handled by GitHub Actions on linux/amd64.
deploy:
	@echo "Push to main to deploy through GitHub Actions."
