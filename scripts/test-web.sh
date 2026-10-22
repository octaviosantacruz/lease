#!/usr/bin/env bash
# Real browser smoke on a new isolated lab. No live target or deployment.
set -euo pipefail
cd "$(dirname "$0")/.."
command -v agent-browser > /dev/null
lab_dir="$(mktemp -d /tmp/lease-web-test.XXXXXX)"
evidence_dir="${LEASE_WEB_EVIDENCE:-$lab_dir/evidence}"
mkdir -p "$evidence_dir"
browser_session="lease-qa-$$"
browser=(agent-browser --session "$browser_session")
launch=("${browser[@]}" --allowed-domains 127.0.0.1)
if [[ -n "${LEASE_BROWSER_EXECUTABLE:-}" ]]; then launch+=(--executable-path "$LEASE_BROWSER_EXECUTABLE"); fi
if [[ -n "${LEASE_BROWSER_ARGS:-}" ]]; then launch+=(--args "$LEASE_BROWSER_ARGS"); fi
server_pid=""
cleanup() {
  result=$?
  if [[ "$result" != 0 ]]; then agent-browser doctor --offline --quick >&2 || true; fi
  "${browser[@]}" close > /dev/null 2>&1 || true
  if [[ -n "$server_pid" ]]; then kill -TERM "$server_pid" 2>/dev/null || true; wait "$server_pid" 2>/dev/null || true; fi
  rm -rf -- "$lab_dir"
}
trap cleanup EXIT
assert_js() { "${browser[@]}" eval "$1"; }
audit() {
  "${browser[@]}" a11y --tags wcag2a,wcag2aa --json > "$evidence_dir/$1-axe.json"
  python3 - "$evidence_dir/$1-axe.json" <<'PY'
import json,sys
report=json.load(open(sys.argv[1]));found=[]
def visit(obj):
    if isinstance(obj,dict):
        for k,v in obj.items():
            if k=='violations' and isinstance(v,(list,int)):found.append(v)
            else:visit(v)
    elif isinstance(obj,list):
        for v in obj:visit(v)
visit(report)
if not found or any(bool(v) for v in found):raise SystemExit('axe failed: '+sys.argv[1])
PY
}
go build -o "$lab_dir/lease" ./cmd/lease
"$lab_dir/lease" demo --dir "$lab_dir/state" --listen 127.0.0.1:0 --seed > "$lab_dir/server.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 100); do
  if rg -q '^Lease lab:' "$lab_dir/server.log"; then break; fi
  if ! kill -0 "$server_pid" 2>/dev/null; then cat "$lab_dir/server.log" >&2; exit 1; fi
  sleep 0.1
done
url="$(python3 - "$lab_dir/server.log" <<'PY'
import re,sys
s=open(sys.argv[1]).read();m=re.search(r'^Lease lab: (http://127\.0\.0\.1:\d+)$',s,re.M)
if not m:raise SystemExit('lab did not start')
print(m.group(1))
PY
)"
"${launch[@]}" open "$url/console/"
"${browser[@]}" wait --text 'El acceso, con principio y fin.'
"${browser[@]}" set viewport 1360 900
"${browser[@]}" select '#actor' user:mateo
"${browser[@]}" screenshot "$evidence_dir/console-desktop.png"
audit console-initial
"${browser[@]}" click '[data-action="approve"]'
"${browser[@]}" wait --text 'Acceso concedido.'
"${browser[@]}" select '#actor' agent:diagnostico
"${browser[@]}" select '#filter' mine
"${browser[@]}" click '[data-action="revoke"]'
"${browser[@]}" wait --text 'Revocación confirmada'
assert_js "(async()=>{const s=await(await fetch('/api/state')).json();if(s.snapshot.grants.find(g=>g.key.Principal==='agent:diagnostico').state!=='revoked')throw Error('revoke not durable');return 'revocation verified'})()"
"${browser[@]}" select '#actor' user:ines
"${browser[@]}" click '[data-action="request"]'
"${browser[@]}" fill '#reason' 'INC-302 prueba <etiqueta>'
"${browser[@]}" select '#duration' 10
audit request-dialog
"${browser[@]}" click 'button[type="submit"]'
"${browser[@]}" wait --text 'Solicitud registrada.'
"${browser[@]}" select '#actor' user:mateo
"${browser[@]}" click '[data-action="approve"]'
"${browser[@]}" wait --text 'Acceso concedido.'
"${browser[@]}" wait 12000
assert_js "(async()=>{const s=await(await fetch('/api/state')).json();const g=s.snapshot.grants.find(g=>g.reason==='INC-302 prueba <etiqueta>');if(g?.state!=='revoked')throw Error('expiry not reconciled');return 'expiry verified'})()"
for width in 320 375 768 1024 1440; do
  "${browser[@]}" set viewport "$width" 900
  assert_js "if(document.documentElement.scrollWidth>innerWidth)throw Error('horizontal overflow');true"
done
"${browser[@]}" set viewport 375 900
"${browser[@]}" screenshot "$evidence_dir/console-mobile.png"
audit console-mobile
"${browser[@]}" click '[data-view="catalog"]'
"${browser[@]}" wait --text 'Sólo el acceso que hace falta.'
audit catalog
"${browser[@]}" click '[data-view="evidence"]'
"${browser[@]}" wait --text 'La historia de cada permiso.'
audit evidence
"${browser[@]}" set media dark
"${browser[@]}" set viewport 1360 900
"${browser[@]}" screenshot "$evidence_dir/evidence-dark.png"
audit evidence-dark
"${browser[@]}" open "$url/"
"${browser[@]}" set media light
"${browser[@]}" screenshot "$evidence_dir/site-desktop.png"
audit site-light
"${browser[@]}" set viewport 375 900
assert_js "if(document.documentElement.scrollWidth>innerWidth)throw Error('site overflow');true"
"${browser[@]}" screenshot "$evidence_dir/site-mobile.png"
"${browser[@]}" set media dark
audit site-dark
"${browser[@]}" open "$url/guide/"
audit guide
"$lab_dir/lease" site export --out "$lab_dir/exported"
echo "OK: browser workflow, expiry, responsive checks, axe and static export. Evidence: $evidence_dir"
