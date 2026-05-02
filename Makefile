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

## DO Container Registry: build, push and create app for the first time
docker-push:
	doctl registry login
	docker build -t $(REGISTRY)/$(IMAGE):latest .
	docker push $(REGISTRY)/$(IMAGE):latest

deploy: docker-push
	doctl apps create --spec .do/app.yaml
