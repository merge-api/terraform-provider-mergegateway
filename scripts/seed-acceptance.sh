#!/usr/bin/env bash
# Seed the local Gateway stack (merge-gateway's docker-compose.local.yml) for
# acceptance tests: register a test user and org, mint a management key, grant
# it every management scope, and print the key on stdout. Progress goes to
# stderr so the key can be captured with KEY=$(scripts/seed-acceptance.sh).
#
# Same flow as the backend's verify-control-plane skill (`verify-cp identity`).
# Needs curl, jq, openssl and docker. Only for a local stack: it writes to the
# stack's Postgres container directly.
set -euo pipefail

BASE_URL="${MERGE_GATEWAY_BASE_URL:-http://localhost:8001}"
POSTGRES_CONTAINER="${POSTGRES_CONTAINER:-merge-gateway-postgres}"

# Every management API scope. New scopes aren't granted to keys automatically,
# so add them here as the API gains them.
SCOPES='["manage_keys", "read_usage", "manage_projects", "manage_routing", "manage_credentials",
  "manage_telemetry_export", "manage_org_settings", "manage_security_rules", "view_users"]'

run_id="$(openssl rand -hex 6)"
email="tf-acc-${run_id}@example.com"
password="TfAcc-${run_id}-Aa1!"
jar="$(mktemp)"
trap 'rm -f "$jar"' EXIT

echo "Registering $email at $BASE_URL" >&2
curl -sSf -o /dev/null -X POST "$BASE_URL/auth/register" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg e "$email" --arg p "$password" --arg c "tf-acc-$run_id" \
    '{email: $e, password: $p, full_name: "Terraform Acceptance", company: $c}')"

# DEBUG=true in the local stack skips email verification, so login works right away.
org_id="$(curl -sSf -c "$jar" -X POST "$BASE_URL/auth/login" -H 'Content-Type: application/json' \
  --data "$(jq -n --arg e "$email" --arg p "$password" '{email: $e, password: $p}')" \
  | jq -re '.memberships[0].organization_id')"
echo "Created org $org_id" >&2

key_json="$(curl -sSf -b "$jar" -X POST "$BASE_URL/organizations/$org_id/management-keys" \
  -H 'Content-Type: application/json' --data '{"name": "tf-acc"}')"
key_id="$(jq -re '.id' <<<"$key_json")"
key="$(jq -re '.key' <<<"$key_json")"

# New keys get the default scopes. Grant every scope, and let the org mint
# gateway keys without a payment method (the CMS admin's bypass).
docker exec -i "$POSTGRES_CONTAINER" psql -U postgres -d merge_gateway_control -v ON_ERROR_STOP=1 -q \
  -v key_id="$key_id" -v org_id="$org_id" -v scopes="$SCOPES" >&2 <<'SQL'
UPDATE management_api_keys SET scopes = :'scopes'::json WHERE id = :'key_id';
UPDATE organizations SET payment_method_bypass_until = now() + interval '1 day' WHERE id = :'org_id';
SQL
echo "Granted management key $key_id scopes $SCOPES" >&2

# Fail here, not in the tests, if the key doesn't work.
curl -sSf -o /dev/null -H "Authorization: Bearer $key" "$BASE_URL/v1/projects"

echo "$key"
