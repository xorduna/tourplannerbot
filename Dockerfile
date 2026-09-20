FROM node:22-alpine AS web-builder
WORKDIR /app/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.26-alpine AS builder
WORKDIR /app
ARG VERSION=development
ARG BUILD_TIME=unknown
COPY go.mod go.sum ./
RUN go mod download
COPY . .
COPY --from=web-builder /app/internal/webapp/dist ./internal/webapp/dist
RUN CGO_ENABLED=0 go build -trimpath \
    -ldflags="-X tourplannerbot/internal/buildinfo.ApplicationVersion=${VERSION} -X tourplannerbot/internal/buildinfo.ApplicationBuildTime=${BUILD_TIME}" \
    -o bot ./cmd/bot

FROM alpine:3.19
RUN apk add --no-cache ca-certificates
WORKDIR /app
COPY --from=builder /app/bot .
COPY prompts/ ./prompts/
COPY tariffs/ ./tariffs/
EXPOSE 8080
CMD ["./bot"]
