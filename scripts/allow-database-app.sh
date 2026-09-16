#!/usr/bin/env bash
set -euo pipefail

: "${DO_APP_ID:?DO_APP_ID is required}"
: "${DO_DATABASE_ID:?DO_DATABASE_ID is required}"

echo "Checking database firewall access for the App Platform application."
firewall_rules_json="$(doctl databases firewalls list "${DO_DATABASE_ID}" --output json)"
existing_app_firewall_rule_uuid="$(jq -r --arg application_id "${DO_APP_ID}" '[.. | objects | select(.type? == "app" and .value? == $application_id) | .uuid? // empty] | first // empty' <<< "${firewall_rules_json}")"

if [[ -n "${existing_app_firewall_rule_uuid}" ]]; then
  echo "The App Platform application already has database firewall access."
  exit 0
fi

echo "Adding the App Platform application to the database firewall."
doctl databases firewalls append "${DO_DATABASE_ID}" --rule "app:${DO_APP_ID}"
