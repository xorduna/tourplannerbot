#!/usr/bin/env bash
set -euo pipefail

: "${ACCESS_PIN:?ACCESS_PIN is required}"
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${DO_APP_ID:?DO_APP_ID is required}"
: "${IMAGE_TAG:?IMAGE_TAG is required}"
: "${OPENAI_API_KEY:?OPENAI_API_KEY is required}"
: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN is required}"

envsubst '$IMAGE_TAG $TELEGRAM_BOT_TOKEN $DATABASE_URL $OPENAI_API_KEY $ACCESS_PIN' \
  < .do/app.yaml > .do/app.deploy.yaml

if grep -Eq '\$\{[A-Za-z_][A-Za-z0-9_]*\}' .do/app.deploy.yaml; then
  echo "The rendered App Platform spec contains unresolved environment variables." >&2
  exit 1
fi

echo "Verifying that the DigitalOcean token can read App Platform app ${DO_APP_ID}."
if ! doctl apps get "${DO_APP_ID}" --format ID,Spec.Name,DefaultIngress --no-header; then
  echo "The DigitalOcean token cannot read this app." >&2
  echo "Confirm DIGITALOCEAN_ACCESS_TOKEN belongs to the DigitalOcean account or team that owns ${DO_APP_ID}." >&2
  exit 1
fi

echo "Updating App Platform app ${DO_APP_ID} with image tag ${IMAGE_TAG}."
doctl apps update "${DO_APP_ID}" --spec .do/app.deploy.yaml --format ID,DefaultIngress,Updated --wait
