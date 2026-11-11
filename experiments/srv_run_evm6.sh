#!/usr/bin/env bash
# usage: [MOCK_BS=n MOCK_BT=dur] srv_run_evm5.sh <label> <image: official|base|epr> <vc: 0|1|2|3> <evm_epr 0|1> <queue: oldqueue|depgraph|v2> [dataset]
#   vc: 0 off, 1 EPR-1 (ordered repair), 2 EPR-1+2 (commuting credits), 3 EPR-1+2+3 (escrow debits)
. /home/yhy/d16/env.sh
D=/home/yhy/d16; cd $D/src/fabric-x-evm
# the disk is shared; a full disk makes runs (and the phase scripts) fail silently
avail_kb=$(df --output=avail / | tail -1)
[ "$avail_kb" -lt 3000000 ] && { echo "== $1 exit=99 DISK_LOW avail_kb=$avail_kb"; exit 99; }
LABEL=$1; IMG=$2; VC=$3; EVMEPR=$4; Q=$5; DS=${6:-integration/perf/testdata/USDC_dataset.json.gz}
case $IMG in
  official) TN=docker.io/hyperledger/fabric-x-committer-test-node:1.0.3;;
  epr) TN=d16/committer-test-node:epr103;;
  base) TN=d16/committer-test-node:base103;;
  eprb5) TN=d16/committer-test-node:epr103b5;;
  epropt) TN=d16/committer-test-node:epr103opt;;  # 11/03: uncontended-cost optimization candidate (built only if S3 points to it)
esac
docker compose -f compose.d16.yml down -v -t 5 >/dev/null 2>&1
sed "s#image: docker.io/hyperledger/fabric-x-committer-test-node:1.0.3#image: $TN#" compose.fabric-x.yml > compose.d16.yml
# the server is shared: cap the committer's memory (Fabric++ enumerates every cycle of a block's conflict
# graph, which can grow without bound under contention); an OOM kill fails the run instead of the host
sed -i 's/^    container_name: test-committer$/    container_name: test-committer\n    mem_limit: 32g/' compose.d16.yml
grep -q "mem_limit: 32g" compose.d16.yml || { echo "== $1 exit=98 MEM_LIMIT_MISSING"; exit 98; }
ENVLINES=""
[ -n "$B5_SPIN_US" ] && ENVLINES="$ENVLINES      FX_B5_SPIN_US: \"$B5_SPIN_US\"
"
[ "$REEXEC" = 1 ] && ENVLINES="$ENVLINES      FX_REEXEC_URL: \"http://127.0.0.1:7071/reexec\"\n"
[ "$CPUPROF" = 1 ] && ENVLINES="$ENVLINES      FX_CPUPROF: \"/tmp/cpu.prof\"\n      FX_CPUPROF_S: \"${CPUPROF_S:-20}\"\n"
[ "$NOGUARD" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_NOGUARD: \"1\"\n"
[ "$CHAIN" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_CHAIN: \"1\"\n"
[ "$REORDER" = 1 ] && ENVLINES="$ENVLINES      FX_REORDER: \"1\"\n"
[ "$FABRICSHARP" = 1 ] && ENVLINES="$ENVLINES      FX_FABRICSHARP: \"1\"\n"
[ "$WARD" = 1 ] && ENVLINES="$ENVLINES      FX_WARD: \"1\"\n"
[ "$EARLY_GOSSIP" = 1 ] && ENVLINES="$ENVLINES      FX_EARLY_GOSSIP: \"1\"\n"
[ "$REEXEC_ALL" = 1 ] && ENVLINES="$ENVLINES      FX_REEXEC_ALL: \"1\"\n"
[ -n "$TXN_SPAN_LIMIT" ] && ENVLINES="$ENVLINES      TXN_SPAN_LIMIT: \"$TXN_SPAN_LIMIT\"\n"
[ "$EPR_FAST" = 0 ] && ENVLINES="$ENVLINES      FX_EPR_FAST: \"0\"\n"
[ "$EPR_RO" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_RO: \"1\"\n"
[ "$EPR_FOLD" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_FOLD: \"1\"\n"
[ "$EPR_FAST_GUARDED" = 1 ] && ENVLINES="$ENVLINES      FX_EPR_FAST_GUARDED: \"1\"\n"
[ "$EPR_PREFETCH" = 0 ] && ENVLINES="$ENVLINES      FX_EPR_PREFETCH: \"0\"\n"
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
if [ "$REEXEC" = 1 ]; then  # colocated EVM re-execution service of the re-execution baseline
  docker cp ${REEXEC_BIN:-$D/bin/d16reexec} test-committer:/usr/local/bin/d16reexec >/dev/null
  docker exec -d test-committer sh -c "/usr/local/bin/d16reexec $REEXEC_ARGS > /tmp/d16reexec.log 2>&1"
fi
EXTRA=""; [ "$Q" = oldqueue ] && EXTRA="-oldqueue"; [ "$Q" = depgraph ] && EXTRA="-depgraph"
LOG=$D/logs/evm_$LABEL.log
if [ "$CPUPROF" = 1 ]; then  # start the committer CPU profile once the replay is under way
  rm -f $LOG
  ( until grep -q "Progress:" $LOG 2>/dev/null; do sleep 1; done; sleep 2; docker exec test-committer touch /tmp/cpu.prof.start ) &
fi
ORDERLOG=""; [ "$ORACLE" = 1 ] && ORDERLOG=$D/logs/order_$LABEL.log && rm -f $ORDERLOG
FX_ORDER_LOG=$ORDERLOG FX_REEXEC=${REEXEC:-0} FX_FS_SNAP=$([ "$FABRICSHARP" = 1 ] && echo 1 || echo 0) FX_DRAIN_S=30 FX_PRIME=1000000000000000000 FX_EPR_EVM=$([ "$EVMEPR" = 1 ] && echo 1 || echo 0) timeout ${RUN_TIMEOUT:-1500} go test -timeout 1400s -tags=perf -run '^TestReplayJSONDataset$' -v -count=1 ./integration/perf/... \
  -outstanding ${OUTSTANDING:-500} -dataset $DS $EXTRA -namespace basic ${HPROF:+-cpuprofile $D/logs/hprof_$LABEL.prof} > $LOG 2>&1
echo "== $LABEL exit=$?"
if [ "$CPUPROF" = 1 ]; then  # one profile per committer process (sidecar, coordinator, verifier, vc)
  sleep $(( ${CPUPROF_S:-20} + 3 )); mkdir -p $D/logs/cpu_$LABEL
  for f in $(docker exec test-committer sh -c 'ls /tmp/cpu.prof.* 2>/dev/null' | grep -v '\.start$'); do
    docker cp test-committer:$f $D/logs/cpu_$LABEL/ >/dev/null 2>&1
  done
  echo "CPUPROF saved $(ls $D/logs/cpu_$LABEL | wc -l) profiles in logs/cpu_$LABEL"
fi
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
[ "$SBCHECK" = 1 ] && echo "SBCHECK(label balances overdrawn raw_encoded): $(bash $D/scripts/srv_sbcheck.sh $LABEL)"
if [ "$ORACLE" = 1 ]; then  # serial-equivalence check: replay valid txs in ledger order, compare all slots
  TAB=$(printf '\t')
  docker exec test-committer psql -U postgres -h localhost -p 5433 -At -F "$TAB" -c "SELECT convert_from(key,'UTF8'), encode(value,'hex') FROM ns_basic WHERE key LIKE 'str:%'::bytea" > $D/logs/dbdump_$LABEL.tsv
  $D/bin/d16oracle "$FX_PRIME_JSON" $ORDERLOG $D/logs/dbdump_$LABEL.tsv 2>&1 | head -6
fi
docker logs test-committer 2>&1 | sed "s/\x1b\[[0-9;]*m//g" | grep -E "PILOT_REEXEC" | grep -v "calls=0 " | tail -1
[ "$REEXEC" = 1 ] && docker exec test-committer tail -1 /tmp/d16reexec.log
# one line per counter family, from the process that counts (the other processes log zeros)
PL=$D/logs/pilot_$LABEL.tmp
docker logs test-committer 2>&1 | sed "s/\x1b\[[0-9;]*m//g" > $PL
grep -E "PILOT_EPR " $PL | grep -v "repaired_reads=0 applied=0 guard_fails=0 fast_deltas=0 fast_progs=0 snapshot_reads=0" | tail -1
grep -E "PILOT_ESCROW" $PL | grep -v "fast_debits=0 slow_debits=0 credits=0" | tail -1
grep -E "PILOT_FOLD" $PL | grep -v "joined=0 groups=0 no_group=0" | tail -1
grep -E "PILOT_CHAIN " $PL | grep -v "grouped_txs=0 " | tail -1
rm -f $PL
docker logs test-committer 2>&1 | sed "s/[[0-9;]*m//g" | grep -E "PILOT_FABRICSHARP|PILOT_REORDER" | grep -vE "blocks=0 |batches=0 " | tail -1  # only the coordinator process counts
# keep the per-run log compressed (the disk is shared)
gzip -f $LOG 2>/dev/null
