#!/bin/bash
set -eu

raw=$(/opt/alist/alist --data /var/lib/alist admin token 2>&1)
admin_token=$(printf '%s' "$raw" | sed -n 's/.*Admin token:[[:space:]]*//p' | tr -d '"[:space:]')
test -n "$admin_token"
base=http://127.0.0.1:5244
auth=(-H "Authorization: $admin_token" -H 'Content-Type: application/json')
scope=/dorm-memorial/probe/3048

mkdir_response=$(curl -sS -X POST "${auth[@]}" \
  --data-binary "$(jq -nc --arg path "$scope" '{path:$path}')" \
  "$base/api/fs/mkdir")
mkdir_code=$(printf '%s' "$mkdir_response" | jq -r '.code')
if test "$mkdir_code" != 200; then
  get_response=$(curl -sS -X POST "${auth[@]}" \
    --data-binary "$(jq -nc --arg path "$scope" '{path:$path,password:"",refresh:true}')" \
    "$base/api/fs/get")
  test "$(printf '%s' "$get_response" | jq -r '.code')" = 200
fi

role=$(curl -fsS "${auth[@]}" "$base/api/admin/role/get?id=3" | jq -c '.data')
role_payload=$(printf '%s' "$role" | jq -c --arg path "$scope" \
  '.permission_scopes=[{path:$path,permission:13752}]')
role_response=$(curl -sS -X POST "${auth[@]}" --data-binary "$role_payload" \
  "$base/api/admin/role/update")
test "$(printf '%s' "$role_response" | jq -r '.code')" = 200

user=$(curl -fsS "${auth[@]}" "$base/api/admin/user/get?id=3" | jq -c '.data')
user_payload=$(printf '%s' "$user" | jq -c --arg path "$scope" \
  '.base_path=$path | .permission=136 | .role=[3] | .password=""')
user_response=$(curl -sS -X POST "${auth[@]}" --data-binary "$user_payload" \
  "$base/api/admin/user/update")
test "$(printf '%s' "$user_response" | jq -r '.code')" = 200

cp -a /etc/dorm-memorial/app.env /etc/dorm-memorial/app.env.before-chaoxing-bot
sed -i 's|^ALIST_ROOT=.*|ALIST_ROOT="/"|' /etc/dorm-memorial/app.env
if systemctl restart dorm-memorial && timeout 30 bash -c \
  'until curl -fsS http://127.0.0.1:8080/health/ready >/dev/null; do sleep 1; done'; then
  curl -fsS http://127.0.0.1:8080/health/ready
else
  cp -a /etc/dorm-memorial/app.env.before-chaoxing-bot /etc/dorm-memorial/app.env
  systemctl restart dorm-memorial
  exit 1
fi
