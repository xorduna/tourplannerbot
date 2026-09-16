REGISTRY := registry.digitalocean.com/maulabs
IMAGE := tourplannerbot

.PHONY: run build tidy db-up db-down docker-up docker-down registry-push deploy

run:
	export $$(grep -v '^#' .env | xargs) && go run ./cmd/bot

build:
	CGO_ENABLED=0 go build -o bin/bot ./cmd/bot

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

## Local architecture image: never used by the production worker
docker-push:
	doctl registry login
	docker build -t $(REGISTRY)/$(IMAGE):local .
	docker push $(REGISTRY)/$(IMAGE):local

## Production images and deployments are handled by GitHub Actions on linux/amd64.
deploy:
	@echo "Push to main to deploy through GitHub Actions."
