#!/usr/bin/env bash
set -euo pipefail

: "${DO_DATABASE_ID:?DO_DATABASE_ID is required}"
: "${GITHUB_OUTPUT:?GITHUB_OUTPUT is required}"

# findFirewallRuleUUID returns the first rule UUID matching the supplied source type and value.
findFirewallRuleUUID() {
  local firewall_rules_json="$1"
  local firewall_rule_type="$2"
  local firewall_rule_value="$3"

  jq -r --arg firewall_rule_type "${firewall_rule_type}" --arg firewall_rule_value "${firewall_rule_value}" \
    '[.. | objects | select(.type? == $firewall_rule_type and .value? == $firewall_rule_value) | .uuid? // empty] | first // empty' \
    <<< "${firewall_rules_json}"
}

echo "Resolving the migration runner public IP address."
runner_ip="$(curl --fail --retry 3 --silent --show-error https://api.ipify.org)"
echo "Runner IP address resolved. Listing firewall rules for database cluster ${DO_DATABASE_ID}."
if firewall_rules_json="$(doctl databases firewalls list "${DO_DATABASE_ID}" --output json)"; then
  :
else
  doctl_exit_status=$?
  echo "Unable to list database firewall rules (doctl exit status ${doctl_exit_status})." >&2
  echo "Verify that DO_DATABASE_ID is the DigitalOcean database cluster UUID and that DIGITALOCEAN_ACCESS_TOKEN can manage that cluster." >&2
  if [[ -n "${firewall_rules_json}" ]]; then
    echo "doctl output: ${firewall_rules_json}" >&2
  fi
  exit "${doctl_exit_status}"
fi

echo "Database firewall rules listed. Looking for an existing runner rule."
existing_firewall_rule_uuid="$(findFirewallRuleUUID "${firewall_rules_json}" "ip_addr" "${runner_ip}")"

echo "runner_ip=${runner_ip}" >> "${GITHUB_OUTPUT}"
if [[ -n "${existing_firewall_rule_uuid}" ]]; then
  echo "The migration runner already has a database firewall rule."
  echo "runner_firewall_rule_added=false" >> "${GITHUB_OUTPUT}"
  exit 0
fi

echo "Adding the migration runner to the database firewall."
doctl databases firewalls append "${DO_DATABASE_ID}" --rule "ip_addr:${runner_ip}"
echo "runner_firewall_rule_added=true" >> "${GITHUB_OUTPUT}"

echo "Looking up the temporary firewall rule UUID for cleanup."
firewall_rules_json="$(doctl databases firewalls list "${DO_DATABASE_ID}" --output json)"
runner_firewall_rule_uuid="$(findFirewallRuleUUID "${firewall_rules_json}" "ip_addr" "${runner_ip}")"
if [[ -z "${runner_firewall_rule_uuid}" ]]; then
  echo "The runner IP firewall rule could not be identified for cleanup." >&2
  exit 1
fi

echo "runner_firewall_rule_uuid=${runner_firewall_rule_uuid}" >> "${GITHUB_OUTPUT}"
echo "Runner firewall rule is ready for cleanup."
