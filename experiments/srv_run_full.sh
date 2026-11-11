#!/usr/bin/env bash
# Full Fabric-X stack run (4-party Arma BFT orderer + Org1 committer as separate services), single host:
# the dry run of the multi-node deployment. Same harness and outputs as srv_run_evm6.sh.
# usage: [env...] srv_run_full.sh <label> <committer image tag: epr103|base103> <vc 0-3> <evm_epr 0|1> [dataset]
. /home/yhy/d16/env.sh
D=/home/yhy/d16; cd $D/src/fabric-x-evm
LABEL=$1; TAG=$2; VC=$3; EVMEPR=$4; DS=${5:-integration/perf/testdata/USDC_dataset.json.gz}
F=compose.d16full.yml
# the compose file runs the services as ${UID}:${GID} (the Makefile exports them): data directories are ours
export UID; export GID=$(id -g)
docker image inspect d16/fabric-x-committer:$TAG >/dev/null 2>&1 || { echo "== $LABEL exit=95 IMAGE_MISSING d16/fabric-x-committer:$TAG"; exit 95; }
# the single-host test node listens on the same ports: it must be down, or the harness would reach it instead
docker compose -f compose.d16.yml down -v -t 5 >/dev/null 2>&1
docker compose -f $F down -v -t 5 >/dev/null 2>&1; rm -rf data/orderers data/committer-org1
for p in 7001 4001 7050; do nc -z localhost $p 2>/dev/null && { echo "== $LABEL exit=94 PORT_BUSY $p"; exit 94; }; done
sed "s#image: ghcr.io/hyperledger/fabric-x-committer:1.0.3#image: d16/fabric-x-committer:$TAG#" compose.fabric-x.full.yaml > $F
# drop the monitoring stack (Prometheus, Grafana): not part of the system under test
python3 - <<'PY'
p = "compose.d16full.yml"; s = open(p).read()
i = s.find("  # ─── Monitoring Stack")
if i > 0:
    s = s[:i].rstrip() + "\n"
open(p, "w").write(s)
PY
ENVLINES=""
[ "$VC" -ge 1 ] && ENVLINES="$ENVLINES      FX_EPR: \"1\"\n"
[ "$VC" -ge 2 ] && ENVLINES="$ENVLINES      FX_EPR2: \"1\"\n"
[ "$VC" -ge 3 ] && ENVLINES="$ENVLINES      FX_EPR3: \"1\"\n"
[ "$EPR_RO" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_RO: \"1\"\n"
[ "$WARD" = 1 ] && ENVLINES="$ENVLINES      FX_WARD: \"1\"\n"
[ "$EARLY_GOSSIP" = 1 ] && ENVLINES="$ENVLINES      FX_EARLY_GOSSIP: \"1\"\n"
[ "$REEXEC_ALL" = 1 ] && ENVLINES="$ENVLINES      FX_REEXEC_ALL: \"1\"\n"
[ "$REEXEC" = 1 ] && ENVLINES="$ENVLINES      FX_REEXEC_URL: \"http://127.0.0.1:7071/reexec\"\n"
if [ -n "$ENVLINES" ]; then
  ENVLINES="$ENVLINES" python3 - <<'PY'
import os, re
p = "compose.d16full.yml"; s = open(p).read()
add = os.environ["ENVLINES"].encode().decode("unicode_escape")
for svc in ("committer-coordinator:", "committer-validator:"):
    i = s.index("\n  " + svc)
    m = re.search(r"\n  [^ \n]", s[i + 3:])  # next service: exactly two spaces of indentation
    j = i + 3 + m.start() if m else len(s)
    block = s[i:j]
    if "    environment:\n" in block:
        nb = block.replace("    environment:\n", "    environment:\n" + add, 1)
    else:
        nb = block.replace("    networks:\n", "    environment:\n" + add + "    networks:\n", 1)
    s = s[:i] + nb + s[i + len(block):]
open(p, "w").write(s)
PY
fi
mkdir -p data/orderers/party{1,2,3,4}-{router,batcher,consenter,assembler} data/committer-org1/db data/committer-org1/sidecar-ledger
docker compose -f $F up -d >/dev/null 2>&1
for i in $(seq 1 120); do nc -z localhost 7001 && nc -z localhost 4001 && break; sleep 2; done
nc -z localhost 4001 || { echo "== $LABEL exit=97 FULL_STACK_NOT_READY"; exit 97; }
# EPR switches must have reached the services (a parsing slip once left them out, silently running plain MVCC)
if [ "$VC" -ge 1 ]; then
  VCC=$(docker ps --format '{{.Names}}' | grep 'committer-validator' | head -1)
  docker exec $VCC env | grep -q '^FX_EPR=1' || { echo "== $LABEL exit=92 EPR_ENV_MISSING"; exit 92; }
fi
no=$(docker ps --format '{{.Names}}' | grep -c 'orderer-party'); nc_=$(docker ps --format '{{.Image}}' | grep -c "d16/fabric-x-committer:$TAG")
echo "STACK orderer_containers=$no committer_containers=$nc_"
[ "$no" -ge 16 ] && [ "$nc_" -ge 5 ] || { echo "== $LABEL exit=93 FULL_STACK_INCOMPLETE"; docker compose -f $F ps -a | head -30; exit 93; }
mkns(){ ns=$1; pol=$2
  for a in 1 2 3 4 5 6; do
    if docker run --rm --network fabric-x --user "$(id -u):$(id -g)" --entrypoint sh \
      -e FX_NS="$ns" -e FX_POLICY="$pol" \
      -v $D/data/bin/fxconfig:/usr/local/bin/fxconfig:ro \
      -v $PWD/testdata/fxconfig.yaml:/config/fxconfig.yaml:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/peers/fxconfig.org1.example.com/tls:/tls:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/users/channel_admin@org1.example.com/msp:/msp:ro \
      -v $PWD/testdata/crypto/peerOrganizations/org1.example.com/msp/tlscacerts/tlsca.org1.example.com-cert.pem:/org-tls-ca.pem:ro \
      -v $PWD/testdata/crypto/ordererOrganizations/orderer-org-1/msp/tlscacerts/tlsca.orderer-org-1-cert.pem:/orderer-tls-ca.pem:ro \
      postgres:18.3-alpine3.23 -c 'fxconfig namespace list --config=/config/fxconfig.yaml 2>/dev/null | grep -q ") $FX_NS:" || fxconfig namespace create "$FX_NS" --policy="$FX_POLICY" --endorse --submit --wait --config=/config/fxconfig.yaml' >/tmp/ns_full.log 2>&1; then
      return 0; fi
    sleep $((a*2))
  done; return 1; }
mkns basic "AND('Org1MSP.member')" || { echo "== $LABEL exit=96 NAMESPACE_FAILED"; tail -5 /tmp/ns_full.log; exit 96; }
if [ "$REEXEC" = 1 ]; then  # re-execution baselines: the EVM service runs next to the validator-committer
  VCC=$(docker ps --format '{{.Names}}' | grep 'committer-validator' | head -1)
  docker cp ${REEXEC_BIN:-$D/bin/d16reexec_v2} $VCC:/tmp/d16reexec >/dev/null
  docker exec -d $VCC sh -c "/tmp/d16reexec $REEXEC_ARGS > /tmp/d16reexec.log 2>&1"
fi
LOG=$D/logs/evm_$LABEL.log
FX_EPR_EVM=$([ "$EVMEPR" = 1 ] && echo 1 || echo 0) FX_DRAIN_S=30 FX_PRIME=1000000000000000000 timeout ${RUN_TIMEOUT:-600} \
  go test -timeout 580s -tags=perf -run '^TestReplayJSONDataset$' -v -count=1 ./integration/perf/... \
  -outstanding ${OUTSTANDING:-60000} -dataset $DS -oldqueue -namespace basic -gateway-config ${GWCFG:-fabx-full.yaml} > $LOG 2>&1
echo "== $LABEL exit=$?"
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
# validity: the genesis state must have reached the committer (contract storage present) and statuses are real
DBC=$(docker ps --format '{{.Names}}' | grep 'committer-db' | head -1)
echo "STATE str_keys=$(docker exec $DBC psql -U sc_user -d sc_db -At -c "SELECT COUNT(*) FROM ns_basic WHERE position(convert_to('str:','UTF8') in key)=1" 2>/dev/null) statuses=$(docker exec $DBC psql -U sc_user -d sc_db -At -c "SELECT string_agg(status||':'||n, ',') FROM (SELECT status, COUNT(*) n FROM tx_status GROUP BY status) s" 2>/dev/null)"
gzip -f $LOG 2>/dev/null
