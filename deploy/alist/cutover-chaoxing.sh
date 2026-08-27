#!/bin/bash
set -eu

raw=$(/opt/alist/alist --data /var/lib/alist admin token 2>&1)
token=$(printf '%s' "$raw" | sed -n 's/.*Admin token:[[:space:]]*//p' | tr -d '"[:space:]')
test -n "$token"
auth=(-H "Authorization: $token" -H 'Content-Type: application/json')
base=http://127.0.0.1:5244

quark=$(curl -fsS "${auth[@]}" "$base/api/admin/storage/get?id=1" | jq -c '.data')
chaoxing=$(curl -fsS "${auth[@]}" "$base/api/admin/storage/get?id=2" | jq -c '.data')
quark_rollback=$(printf '%s' "$quark" | jq -c '.mount_path="/dorm-memorial/quark-rollback" | .status=""')
chaoxing_live=$(printf '%s' "$chaoxing" | jq -c '.mount_path="/dorm-memorial/probe" | .status=""')

update() {
  response=$(curl -sS -X POST "${auth[@]}" --data-binary "$1" "$base/api/admin/storage/update")
  test "$(printf '%s' "$response" | jq -r '.code')" = 200
}

update "$quark_rollback"
if ! update "$chaoxing_live"; then
  update "$quark"
  echo 'Chaoxing cutover failed; Quark path restored.' >&2
  exit 1
fi

sleep 3
curl -fsS -H "Authorization: $token" \
  "$base/api/admin/storage/list?page=1&per_page=100" | jq -r \
  '.data.content[] | [.id,.mount_path,.driver,.status] | @tsv'
