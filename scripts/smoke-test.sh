#!/usr/bin/env bash
set -Eeuo pipefail

: "${API_KEY:?API_KEY is required}"
: "${ADMIN_API_KEY:?ADMIN_API_KEY is required}"

base_url="${BASE_URL:-http://127.0.0.1:8080}"
product_id="11111111-1111-1111-1111-111111111111"
order_id="22222222-2222-2222-2222-222222222222"

post_json() {
  local token="$1"
  local path="$2"
  local payload="$3"

  curl --fail --silent --show-error \
    --request POST "${base_url}${path}" \
    --header "Authorization: Bearer ${token}" \
    --header "Content-Type: application/json" \
    --data "${payload}" >/dev/null
}

ready=false
for _ in {1..30}; do
  if curl --fail --silent "${base_url}/readyz" >/dev/null; then
    ready=true
    break
  fi
  sleep 1
done
if ! "${ready}"; then
  echo "Service did not become ready within 30 seconds" >&2
  exit 1
fi

curl --fail --silent --show-error "${base_url}/healthz" >/dev/null

unauthorized_status="$(curl --silent --output /dev/null --write-out '%{http_code}' \
  --request POST "${base_url}/api/v1/stock/restock" \
  --header "Content-Type: application/json" \
  --data '{"items":[]}')"
test "${unauthorized_status}" = "401"

post_json "${ADMIN_API_KEY}" "/api/v1/stock/restock" \
  "{\"items\":[{\"item_id\":\"${product_id}\",\"quantity\":10}]}"
post_json "${API_KEY}" "/api/v1/stock/reserve" \
  "{\"order_id\":\"${order_id}\",\"items\":[{\"item_id\":\"${product_id}\",\"quantity\":2}]}"
post_json "${API_KEY}" "/api/v1/stock/confirm" "{\"order_id\":\"${order_id}\"}"
post_json "${API_KEY}" "/api/v1/stock/confirm" "{\"order_id\":\"${order_id}\"}"

metrics="$(curl --fail --silent --show-error \
  --header "Authorization: Bearer ${ADMIN_API_KEY}" \
  "${base_url}/metrics")"
grep --quiet '^booking_http_requests_total' <<<"${metrics}"

echo "Smoke test passed"
