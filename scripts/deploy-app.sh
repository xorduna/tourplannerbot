#!/usr/bin/env bash
set -euo pipefail

: "${ACCESS_PIN:?ACCESS_PIN is required}"
: "${DATABASE_URL:?DATABASE_URL is required}"
: "${DO_APP_ID:?DO_APP_ID is required}"
: "${IMAGE_TAG:?IMAGE_TAG is required}"
: "${OPENAI_API_KEY:?OPENAI_API_KEY is required}"
: "${TELEGRAM_BOT_TOKEN:?TELEGRAM_BOT_TOKEN is required}"
: "${TOOLS_BIGIN_CLIENT_ID:?TOOLS_BIGIN_CLIENT_ID is required}"
: "${TOOLS_BIGIN_CLIENT_SECRET:?TOOLS_BIGIN_CLIENT_SECRET is required}"
: "${TOOLS_BIGIN_REFRESH_TOKEN:?TOOLS_BIGIN_REFRESH_TOKEN is required}"

envsubst '$IMAGE_TAG $TELEGRAM_BOT_TOKEN $DATABASE_URL $OPENAI_API_KEY $ACCESS_PIN $TOOLS_BIGIN_CLIENT_ID $TOOLS_BIGIN_CLIENT_SECRET $TOOLS_BIGIN_REFRESH_TOKEN' \
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

echo "Validating the rendered spec as an update to App Platform app ${DO_APP_ID}."
if ! doctl apps propose --app "${DO_APP_ID}" --spec .do/app.deploy.yaml > /dev/null; then
  echo "DigitalOcean rejected the proposed spec before applying it." >&2
  echo "Review the validation error above; the active app has not been modified." >&2
  exit 1
fi

echo "The proposed target spec is valid. Applying the update."
echo "Updating App Platform app ${DO_APP_ID} with image tag ${IMAGE_TAG}."
if ! doctl apps update "${DO_APP_ID}" --spec .do/app.deploy.yaml --format ID,DefaultIngress,Updated --wait; then
  echo "DigitalOcean validated the target spec but forbade the update operation." >&2
  echo "Confirm the GitHub DIGITALOCEAN_ACCESS_TOKEN is the intended token and grants app:update." >&2
  echo "If a Full Access token also fails, the DigitalOcean team is restricted; provide the request ID above to support." >&2
  exit 1
fi
