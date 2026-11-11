#!/usr/bin/env bash
# usage: [MOCK_BS=n MOCK_BT=dur] srv_run_evm5.sh <label> <image: official|base|epr> <vc: 0|1|2|3> <evm_epr 0|1> <queue: oldqueue|depgraph|v2> [dataset]
#   vc: 0 off, 1 EPR-1 (ordered repair), 2 EPR-1+2 (commuting credits), 3 EPR-1+2+3 (escrow debits)
. /home/yhy/d16/env.sh
D=/home/yhy/d16; cd $D/src/fabric-x-evm
LABEL=$1; IMG=$2; VC=$3; EVMEPR=$4; Q=$5; DS=${6:-integration/perf/testdata/USDC_dataset.json.gz}
case $IMG in
  official) TN=docker.io/hyperledger/fabric-x-committer-test-node:1.0.3;;
  epr) TN=d16/committer-test-node:epr103;;
  base) TN=d16/committer-test-node:base103;;
  eprb5) TN=d16/committer-test-node:epr103b5;;
esac
docker compose -f compose.d16.yml down -v -t 5 >/dev/null 2>&1
sed "s#image: docker.io/hyperledger/fabric-x-committer-test-node:1.0.3#image: $TN#" compose.fabric-x.yml > compose.d16.yml
ENVLINES=""
[ -n "$B5_SPIN_US" ] && ENVLINES="$ENVLINES      FX_B5_SPIN_US: \"$B5_SPIN_US\"
"
[ "$VC" -ge 1 ] && ENVLINES="$ENVLINES      FX_EPR: \"1\"\n"
[ "$VC" -ge 2 ] && ENVLINES="$ENVLINES      FX_EPR2: \"1\"\n"
[ "$VC" -ge 3 ] && ENVLINES="$ENVLINES      FX_EPR3: \"1\"\n"
if [ -n "$ENVLINES" ]; then
  ENVLINES="$ENVLINES" python3 - <<'PY'
import os
p="compose.d16.yml"; s=open(p).read()
i=s.index("container_name: test-committer"); j=s.index("    environment:\n", i)
add=os.environ["ENVLINES"].encode().decode("unicode_escape")
s=s[:j]+"    environment:\n"+add+s[j+len("    environment:\n"):]
open(p,"w").write(s)
PY
fi
# optional orderer block cutting (MOCK_BS txs per block, MOCK_BT max wait); the compose file
# sets SC_ORDERER_BLOCK_SIZE, which overrides the mock orderer config file, so rewrite that entry
if [ -n "$MOCK_BS" ]; then python3 $D/scripts/set_blocks.py compose.d16.yml "$MOCK_BS" "${MOCK_BT:-30s}" || exit 1; fi
bash $D/scripts/srv_startx_ns.sh >/dev/null 2>&1 || { echo "startx failed"; exit 1; }
EXTRA=""; [ "$Q" = oldqueue ] && EXTRA="-oldqueue"; [ "$Q" = depgraph ] && EXTRA="-depgraph"
LOG=$D/logs/evm_$LABEL.log
FX_DRAIN_S=30 FX_PRIME=1000000000000000000 FX_EPR_EVM=$([ "$EVMEPR" = 1 ] && echo 1 || echo 0) timeout ${RUN_TIMEOUT:-1500} go test -timeout 1400s -tags=perf -run '^TestReplayJSONDataset$' -v -count=1 ./integration/perf/... \
  -outstanding ${OUTSTANDING:-500} -dataset $DS $EXTRA -namespace basic > $LOG 2>&1
echo "== $LABEL exit=$?"
sed 's/\x1b\[[0-9;]*m//g' $LOG | grep -E "PERF RESULT|EPR EXTRACT|Replay complete|EPR DRAIN|EPR STATS" | sed 's/^ *replay_json_dataset_test.go:[0-9]*: //' | cut -c1-220
sed 's/\x1b\[[0-9;]*m//g' $LOG | grep -oE "fabric-invalid status=[A-Z_]+" | sort | uniq -c
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
echo "STATECHECK(label words total highbytes raw_deltas empty committed): $(bash $D/scripts/srv_statecheck.sh $LABEL)"
docker logs test-committer 2>&1 | sed "s/\x1b\[[0-9;]*m//g" | grep -E "PILOT_EPR |PILOT_ESCROW" | grep -v "repaired_reads=0 applied=0" | tail -1
