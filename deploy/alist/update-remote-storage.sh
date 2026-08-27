#!/bin/bash
set -eu

payload=$(cat)
raw=$(/opt/alist/alist --data /var/lib/alist admin token 2>&1)
token=$(printf '%s' "$raw" | sed -n 's/.*Admin token:[[:space:]]*//p' | tr -d '"[:space:]')
test -n "$token"

response=$(curl -sS -X POST \
  -H "Authorization: $token" \
  -H 'Content-Type: application/json' \
  --data-binary "$payload" \
  http://127.0.0.1:5244/api/admin/storage/update)
code=$(printf '%s' "$response" | jq -r '.code')
if test "$code" != 200; then
  printf '%s' "$response" | jq -r '[.code,.message] | @tsv' >&2
  exit 1
fi

sleep 3
verify=$(curl -fsS -H "Authorization: $token" \
  'http://127.0.0.1:5244/api/admin/storage/list?page=1&per_page=100')
printf '%s' "$verify" | jq -r \
  '.data.content[] | select(.id==1) | [.id,.mount_path,.driver,.status] | @tsv'
