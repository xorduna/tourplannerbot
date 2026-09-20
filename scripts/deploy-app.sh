#!/usr/bin/env bash
set -euo pipefail

: "${ACCESS_PIN:?ACCESS_PIN is required}"
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${DO_APP_ID:?DO_APP_ID is required}"
: "${IMAGE_TAG:?IMAGE_TAG is required}"
: "${OPENAI_API_KEY:?OPENAI_API_KEY is required}"
: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN is required}"

envsubst '$IMAGE_TAG $TELEGRAM_BOT_TOKEN $DATABASE_URL $OPENAI_API_KEY $ACCESS_PIN $APP_BASE_URL' \
  < .do/app.yaml > .do/app.deploy.yaml
doctl apps update "${DO_APP_ID}" --spec .do/app.deploy.yaml --wait
