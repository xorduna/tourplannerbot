#!/usr/bin/env bash
set -euo pipefail

: "${DO_DATABASE_ID:?DO_DATABASE_ID is required}"

runner_firewall_rule_uuid="${RUNNER_FIREWALL_RULE_UUID:-}"
if [[ -z "${runner_firewall_rule_uuid}" ]]; then
  : "${RUNNER_IP:?RUNNER_IP is required when RUNNER_FIREWALL_RULE_UUID is empty}"
  echo "No stored firewall UUID; looking it up from the runner IP address."
  firewall_rules_json="$(doctl databases firewalls list "${DO_DATABASE_ID}" --output json)"
  runner_firewall_rule_uuid="$(jq -r --arg runner_ip "${RUNNER_IP}" '[.. | objects | select(.type? == "ip_addr" and .value? == $runner_ip) | .uuid? // empty] | first // empty' <<< "${firewall_rules_json}")"
fi

if [[ -z "${runner_firewall_rule_uuid}" ]]; then
  echo "The runner IP firewall rule could not be identified for cleanup." >&2
  exit 1
fi

echo "Removing the temporary migration runner firewall rule."
doctl databases firewalls remove "${DO_DATABASE_ID}" --uuid "${runner_firewall_rule_uuid}"
echo "Temporary migration runner firewall rule removed."
