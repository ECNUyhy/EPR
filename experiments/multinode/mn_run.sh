#!/usr/bin/env bash
# One multi-node run of the full Fabric-X stack (run on 116):
#   Arma party p on 112/115/116/118 (p = 1..4), the org1 committer next to party 4 on 118 (COMMITTER_HOST=112 for
#   the earlier placement: 112's data volume syncs 10x slower than 116/118), client + gateway (in-process org1/org2
#   endorsers) on 124.
# usage: [env...] mn_run.sh <label> <committer image tag: epr103|base103> <vc 0-3> <evm_epr 0|1> <dataset> <genesis>
#   dataset/genesis are paths on 124 relative to ~/mnclient (e.g. integration/perf/testdata/USDC_dataset.json.gz,
#   data/USDC_genesis_151k.json). Scheme switches as in srv_run_full.sh (EPR_RO WARD EARLY_GOSSIP REEXEC_ALL
#   REEXEC REEXEC_BIN REEXEC_ARGS); every FX_* variable of the caller is forwarded to the client.
#   NETEM_MS=<one-way delay in ms> applies the emulated WAN delay (mn_netem.sh) before the run, 0/empty = none.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; M=$D/multinode; C="ssh -F $HOME/.ssh/config_d16mn -o BatchMode=yes"
LABEL=$1; TAG=$2; VC=$3; EVMEPR=$4; DS=$5; GEN=$6
# CLIENT_HOST=n112 runs the client + gateway next to org1's committer (one organization per site), default n124
CH=${CLIENT_HOST:-n124}
CM=${COMMITTER_HOST:-118}; CMN=n$CM; CMIP=${NET_PREFIX:-10.0.0}.$CM
case $CM in 112) CMDIR=/data/d2025yhy/mn; CMP=1;; 118) CMDIR=mn; CMP=4;; *) echo "== $1 exit=91 BAD_COMMITTER_HOST $CM"; exit 91;; esac
O="orderer-partyX-assembler orderer-partyX-consenter orderer-partyX-batcher orderer-partyX-router"
host_do() { case $1 in 112) $C n112 "bash bin/mn_host.sh /data/d2025yhy/mn 112 $2";; 115) $C n115 "bash bin/mn_host.sh mn 115 $2";;
  118) $C n118 "bash bin/mn_host.sh mn 118 $2";; 116) bash $D/scripts/mn_host.sh $M/cfg 116 $2;; esac; }

# 1. clean state everywhere
for h in 112 115 116 118; do host_do $h down >/dev/null 2>&1 & done; wait
$C $CH "pkill -u d2025yhy -f '[p]erf.test' 2>/dev/null; true"

# 2. committer compose for this scheme (image tag + EPR/baseline switches), from the clean template
VC=$VC CM=$CM python3 - "$TAG" <<'PY'
import os, re, sys
tag = sys.argv[1]; p = f"/home/yhy/d16/multinode/cfg/compose.{os.environ['CM']}.tpl.yml"; s = open(p).read()
s = re.sub(r"image: d16/fabric-x-committer:\S+", f"image: d16/fabric-x-committer:{tag}", s)
s = re.sub(r"\n      FX_[A-Z0-9_]+: \"[^\"]*\"", "", s)          # drop switches left from earlier runs
env = os.environ; vc = int(os.environ.get("VC", "0")); add = []
if vc >= 1: add.append('FX_EPR: "1"')
if vc >= 2: add.append('FX_EPR2: "1"')
if vc >= 3: add.append('FX_EPR3: "1"')
for k, v in (("EPR_RO", "FX_EPR_RO"), ("WARD", "FX_WARD"), ("EARLY_GOSSIP", "FX_EARLY_GOSSIP"), ("REEXEC_ALL", "FX_REEXEC_ALL"),
             ("FABRICSHARP", "FX_FABRICSHARP"), ("REORDER", "FX_REORDER")):
    if env.get(k) == "1": add.append(f'{v}: "1"')
if env.get("REEXEC") == "1": add.append('FX_REEXEC_URL: "http://127.0.0.1:7071/reexec"')
for k in ("FX_EPR_FAST_GUARDED", "FX_EPR_PREFETCH", "FX_EPR_FOLD", "FX_EPR_CHAIN"):
    if k in env: add.append(f'{k}: "{env[k]}"')
lines = "".join(f"\n      {a}" for a in add)
for svc in ("committer-coordinator:", "committer-validator:"):
    i = s.index("\n  " + svc); m = re.search(r"\n  [^ \n]", s[i + 3:]); j = i + 3 + m.start() if m else len(s)
    b = s[i:j]
    b = b.replace("    environment:", "    environment:" + lines, 1) if "    environment:" in b else \
        b.replace("    networks:", "    environment:" + lines + "\n    networks:", 1)
    s = s[:i] + b + s[j:]
open(f"/home/yhy/d16/multinode/cfg/compose.{os.environ['CM']}.yml", "w").write(s)
PY
scp -F $HOME/.ssh/config_d16mn -q $M/cfg/compose.$CM.yml $CMN:$CMDIR/compose.$CM.yml
# 112 then runs party 1 only (its template has no committer services)
[ $CM != 112 ] && scp -F $HOME/.ssh/config_d16mn -q $M/cfg/compose.112.tpl.yml n112:/data/d2025yhy/mn/compose.112.yml

# 3. emulated WAN delay (optional). Applied before any node starts, as in a real WAN: gRPC's BDP estimator only
#    grows a connection's flow-control window when the measured bandwidth beats the maximum seen so far, so a
#    connection that first ran at LAN latency keeps its 64 KB window (~64 KB per RTT) once the delay is added.
bash $D/scripts/mn_netem.sh clear >/dev/null
[ -n "$NETEM_MS" ] && [ "$NETEM_MS" != 0 ] && bash $D/scripts/mn_netem.sh set "$NETEM_MS" >/dev/null

# 4. Arma on four hosts, then the committer
host_do 112 "up ${O//X/1}" >/dev/null & host_do 115 "up ${O//X/2}" >/dev/null & host_do 118 "up ${O//X/4}" >/dev/null &
host_do 116 up >/dev/null & wait
host_do $CM "up committer-db committer-verifier committer-validator committer-coordinator committer-sidecar committer-query-service" >/dev/null
for i in $(seq 1 90); do nc -z $CMIP 20401 && nc -z $CMIP 20701 && break; sleep 2; done
nc -z $CMIP 20401 || { echo "== $LABEL exit=97 MN_STACK_NOT_READY"; exit 97; }
STACK=$( { $C n112 "docker ps --format '{{.Names}}'"; $C n115 "docker ps --format '{{.Names}}'"; $C n118 "docker ps --format '{{.Names}}'"; docker ps --format '{{.Names}}'; } )
no=$(echo "$STACK" | grep -c '^orderer-party'); nc_=$(echo "$STACK" | grep -c '^fabric-x-committer-\|^committer-')
echo "STACK orderer_containers=$no committer_containers=$nc_"
[ "$no" -ge 16 ] && [ "$nc_" -ge 6 ] || { echo "== $LABEL exit=93 MN_STACK_INCOMPLETE"; exit 93; }
VCC=$($C $CMN "docker ps --format '{{.Names}}' | grep committer-validator | head -1")
if [ "$VC" -ge 1 ]; then $C $CMN "docker exec $VCC env | grep -q '^FX_EPR=1'" || { echo "== $LABEL exit=92 EPR_ENV_MISSING"; exit 92; }; fi

# 5. namespace (fxconfig on the committer host's fabric-x network)
mkns() { $C $CMN "cd $CMDIR; for a in 1 2 3 4 5 6; do docker run --rm --network fabric-x --user \$(id -u):\$(id -g) --entrypoint sh \
  -e FX_NS=basic -e \"FX_POLICY=AND('Org1MSP.member')\" -v \$HOME/bin/fxconfig:/usr/local/bin/fxconfig:ro \
  -v \$PWD/testdata/fxconfig.yaml:/config/fxconfig.yaml:ro \
  -v \$PWD/testdata/crypto/peerOrganizations/org1.example.com/peers/fxconfig.org1.example.com/tls:/tls:ro \
  -v \$PWD/testdata/crypto/peerOrganizations/org1.example.com/users/channel_admin@org1.example.com/msp:/msp:ro \
  -v \$PWD/testdata/crypto/peerOrganizations/org1.example.com/msp/tlscacerts/tlsca.org1.example.com-cert.pem:/org-tls-ca.pem:ro \
  -v \$PWD/testdata/crypto/ordererOrganizations/orderer-org-$CMP/msp/tlscacerts/tlsca.orderer-org-$CMP-cert.pem:/orderer-tls-ca.pem:ro \
  postgres:18.3-alpine3.23 -c 'fxconfig namespace list --config=/config/fxconfig.yaml 2>/dev/null | grep -q \") \$FX_NS:\" || fxconfig namespace create \"\$FX_NS\" --policy=\"\$FX_POLICY\" --endorse --submit --wait --config=/config/fxconfig.yaml' > /tmp/ns_mn.log 2>&1 && exit 0; sleep \$((a*2)); done; tail -5 /tmp/ns_mn.log; exit 1"; }
mkns || { echo "== $LABEL exit=96 NAMESPACE_FAILED"; exit 96; }

# 6. re-execution baselines: the EVM service next to the validator-committer
if [ "$REEXEC" = 1 ]; then
  scp -F $HOME/.ssh/config_d16mn -q ${REEXEC_BIN:-$D/bin/d16reexec_v2} $CMN:/tmp/d16reexec
  $C $CMN "docker cp /tmp/d16reexec $VCC:/tmp/d16reexec >/dev/null && docker exec -d $VCC sh -c '/tmp/d16reexec $REEXEC_ARGS > /tmp/d16reexec.log 2>&1'"
fi

# 7. client on 124
FXENV=$(env | grep -E '^FX_[A-Z0-9_]+=' | grep -vE '^FX_(PRIME_JSON)=' | sed "s/'/'\\\\''/g; s/=\(.*\)/='\1'/" | tr '\n' ' ')
LOG=$D/logs/mn_$LABEL.log
$C $CH "cd ~/mnclient/integration/perf && env $FXENV FX_PRIME_JSON=\$HOME/mnclient/$GEN FX_EPR_EVM=$([ "$EVMEPR" = 1 ] && echo 1 || echo 0) \
  FX_FS_SNAP=$([ "$FABRICSHARP" = 1 ] && echo 1 || echo 0) FX_DRAIN_S=30 FX_PRIME=1000000000000000000 timeout ${RUN_TIMEOUT:-600} ./perf.test -test.timeout 580s -test.run '^TestReplayJSONDataset\$' -test.v -test.count=1 \
  -outstanding ${OUTSTANDING:-60000} ${CLIENT_FLAGS:-} -dataset \$HOME/mnclient/$DS -${QUEUE:-oldqueue} -namespace basic -gateway-config fabx-mn.yaml > /tmp/mn_client.log 2>&1; echo \$? > /tmp/mn_client.rc"
scp -F $HOME/.ssh/config_d16mn -q $CH:/tmp/mn_client.log $LOG
echo "== $LABEL exit=$($C $CH 'cat /tmp/mn_client.rc')"
[ -n "$NETEM_MS" ] && [ "$NETEM_MS" != 0 ] && bash $D/scripts/mn_netem.sh clear >/dev/null
sed 's/\x1b\[[0-9;]*m//g' $LOG | grep -E "EPR STATS|Replay complete" | sed 's/^ *replay_json_dataset_test.go:[0-9]*: //' | cut -c1-220
sed 's/\x1b\[[0-9;]*m//g' $LOG | grep -E "Progress:" | python3 -c '
import sys,re
best=None
for l in sys.stdin:
    m=re.search(r"Progress: (\d+)/(\d+) transfers processed \((\d+) successful, (\d+) failed.*\(recent\), ([0-9.]+) tx/s \(overall\)",l)
    if not m: continue
    p,t,s,f,o=int(m[1]),int(m[2]),int(m[3]),int(m[4]),float(m[5])
    if best is None or p>best[0]: best=(p,t,s,f,o)
if best:
    p,t,s,f,o=best; el=p/o if o>0 else float("nan")
    print(f"GOODPUT label='"$LABEL"' processed={p}/{t} success={s} failed={f} elapsed_s={el:.1f} goodput_tps={s/el:.1f}")
'
$C $CMN "DBC=\$(docker ps --format '{{.Names}}' | grep committer-db | head -1); echo \"STATE str_keys=\$(docker exec \$DBC psql -U sc_user -d sc_db -At -c \"SELECT COUNT(*) FROM ns_basic WHERE position(convert_to('str:','UTF8') in key)=1\" 2>/dev/null) statuses=\$(docker exec \$DBC psql -U sc_user -d sc_db -At -c \"SELECT string_agg(status||':'||n, ',') FROM (SELECT status, COUNT(*) n FROM tx_status GROUP BY status) s\" 2>/dev/null)\""
gzip -f $LOG 2>/dev/null
