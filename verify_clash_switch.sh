#!/bin/bash

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
CONFIG_PATH="${1:-$SCRIPT_DIR/config.json}"
LIMIT="${2:-5}"

if ! command -v python3 >/dev/null 2>&1; then
  echo "错误: 需要 python3" >&2
  exit 1
fi

if ! command -v curl >/dev/null 2>&1; then
  echo "错误: 需要 curl" >&2
  exit 1
fi

if [ ! -f "$CONFIG_PATH" ]; then
  echo "错误: 未找到配置文件: $CONFIG_PATH" >&2
  exit 1
fi

readarray -t CLASH_INFO < <(python3 - "$CONFIG_PATH" <<'PY'
import json
import sys

config_path = sys.argv[1]
with open(config_path, 'r', encoding='utf-8') as f:
    data = json.load(f)

clash = data.get('clash') or {}
fields = [
    clash.get('external_controller', ''),
    clash.get('secret', ''),
    clash.get('proxy_group', ''),
    clash.get('mixed_proxy', ''),
    '\u0000'.join(clash.get('include') or []),
    '\u0000'.join(clash.get('exclude') or []),
]

for item in fields:
    print(item)
PY
)

EXTERNAL_CONTROLLER="${CLASH_INFO[0]:-}"
SECRET="${CLASH_INFO[1]:-}"
PROXY_GROUP="${CLASH_INFO[2]:-}"
MIXED_PROXY="${CLASH_INFO[3]:-}"
INCLUDE_RAW="${CLASH_INFO[4]:-}"
EXCLUDE_RAW="${CLASH_INFO[5]:-}"

if [ -z "$EXTERNAL_CONTROLLER" ] || [ -z "$PROXY_GROUP" ] || [ -z "$MIXED_PROXY" ]; then
  echo "错误: config 中缺少 clash.external_controller / clash.proxy_group / clash.mixed_proxy" >&2
  exit 1
fi

if [[ "$LIMIT" =~ ^[0-9]+$ ]] && [ "$LIMIT" -gt 0 ]; then
  :
else
  echo "错误: 第二个参数 limit 必须是正整数" >&2
  exit 1
fi

auth_args=()
if [ -n "$SECRET" ]; then
  auth_args=(-H "Authorization: Bearer $SECRET")
fi

echo "========================================"
echo "Clash 节点切换最小验证"
echo "配置文件: $CONFIG_PATH"
echo "Controller: $EXTERNAL_CONTROLLER"
echo "Group: $PROXY_GROUP"
echo "Mixed Proxy: $MIXED_PROXY"
echo "Limit: $LIMIT"
echo "========================================"

python3 - "$CONFIG_PATH" "$LIMIT" <<'PY' > "$SCRIPT_DIR/.clash_verify_nodes.tmp"
import json
import sys
import urllib.parse
import urllib.request

config_path = sys.argv[1]
limit = int(sys.argv[2])

with open(config_path, 'r', encoding='utf-8') as f:
    data = json.load(f)

clash = data.get('clash') or {}
controller = clash.get('external_controller', '').strip()
if controller and '://' not in controller:
    controller = 'http://' + controller
controller = controller.rstrip('/')

secret = (clash.get('secret') or '').strip()
group = clash.get('proxy_group', '').strip()
include = [x.strip().lower() for x in (clash.get('include') or []) if x and x.strip()]
exclude = [x.strip().lower() for x in (clash.get('exclude') or []) if x and x.strip()]

req = urllib.request.Request(controller + '/proxies', headers={'Accept': 'application/json'})
if secret:
    req.add_header('Authorization', 'Bearer ' + secret)

with urllib.request.urlopen(req, timeout=15) as resp:
    data = json.load(resp)

group_proxy = data.get('proxies', {}).get(group)
if not group_proxy:
    raise SystemExit(f'未找到代理组: {group}')

nodes = []
for raw in group_proxy.get('all', []):
    name = (raw or '').strip()
    if not name:
        continue
    lower = name.lower()
    if include and not any(k in lower for k in include):
        continue
    if any(k in lower for k in exclude):
        continue
    nodes.append(name)

for name in nodes[:limit]:
    print(name)
PY

mapfile -t NODES < "$SCRIPT_DIR/.clash_verify_nodes.tmp"
rm -f "$SCRIPT_DIR/.clash_verify_nodes.tmp"

if [ "${#NODES[@]}" -eq 0 ]; then
  echo "错误: 过滤后没有可验证的节点" >&2
  exit 1
fi

for node in "${NODES[@]}"; do
  echo
  echo "--- 切换到节点: $node ---"

  curl -fsS -X PUT \
    "${auth_args[@]}" \
    -H 'Content-Type: application/json' \
    -H 'Accept: application/json' \
    "$EXTERNAL_CONTROLLER/proxies/$(python3 - <<'PY' "$PROXY_GROUP"
import sys, urllib.parse
print(urllib.parse.quote(sys.argv[1], safe=''))
PY
)" \
    --data "$(python3 - <<'PY' "$node"
import json, sys
print(json.dumps({'name': sys.argv[1]}))
PY
)" >/dev/null

  sleep 1

  CURRENT_NODE="$(python3 - <<'PY' "$EXTERNAL_CONTROLLER" "$SECRET" "$PROXY_GROUP"
import json
import sys
import urllib.parse
import urllib.request

controller, secret, group = sys.argv[1:4]
if '://' not in controller:
    controller = 'http://' + controller
url = controller.rstrip('/') + '/proxies/' + urllib.parse.quote(group, safe='')
req = urllib.request.Request(url, headers={'Accept': 'application/json'})
if secret:
    req.add_header('Authorization', 'Bearer ' + secret)
with urllib.request.urlopen(req, timeout=15) as resp:
    data = json.load(resp)
print(data.get('now', ''))
PY
)"

  EGRESS_INFO="$(
    if curl -fsS --proxy "$MIXED_PROXY" --max-time 20 https://ipinfo.io/json | python3 -c 'import json, sys; data=json.load(sys.stdin); parts=[data.get("ip", "").strip()]; city=data.get("city", "").strip(); region=data.get("region", "").strip(); country=data.get("country", "").strip(); location=", ".join([x for x in [city, region, country] if x]); print(" | ".join([x for x in parts + ([location] if location else []) if x]))'; then
      :
    else
      curl -fsS --proxy "$MIXED_PROXY" --max-time 20 https://api.ipify.org
    fi
  )"

  echo "selector now : $CURRENT_NODE"
  echo "egress       : $EGRESS_INFO"
done

echo
echo "说明: 如果 selector now 明确变化，但 egress IP 相同，说明节点切换成功，但这些节点共享出口 IP。"
