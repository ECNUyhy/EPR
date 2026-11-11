#!/usr/bin/env bash
# exp_restart4.sh (stage S12): coordinator crash/restart with EPR's final image eprw (srv_run_evm6w.sh), open loop at
# 3000 tx/s (half the peak) with retries, USDC: completion, conservation, and the final-state digest vs. an
# uninterrupted run.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/exp_restart4.txt; : > $out
R=$D/scripts/srv_run_evm6w.sh
export MOCK_BS=100 MOCK_BT=200ms FX_NO_READ_PRIME=1 FX_PRIME_JSON=$D/data/USDC_genesis_151k.json FX_RATE=3000 FX_OL_SECONDS=120 \
  RUN_TIMEOUT=300 OUTSTANDING=60000 FX_RETRY=50 FX_EPR_PROG=1 EPR_RO=1
G="==|EPR STATS|STATECHECK|GOODPUT"
digest(){ docker exec test-committer psql -U postgres -h localhost -p 5433 -At -c "SELECT md5(string_agg(encode(key,'hex')||':'||encode(value,'hex'), ',' ORDER BY key)), COUNT(*) FROM ns_basic WHERE key LIKE 'str:%'::bytea"; }
bash $R rs4_ref eprw 3 1 oldqueue 2>&1 | grep -E "$G" | tee -a $out
echo "DIGEST rs4_ref $(digest)" | tee -a $out
( ORACLE=1 bash $R rs4_crash eprw 3 1 oldqueue 2>&1 | grep -E "$G" > $D/logs/rs4_crash.part ) &
RUNPID=$!
for i in $(seq 1 180); do grep -q "Progress:" $D/logs/evm_rs4_crash.log 2>/dev/null && break; sleep 1; done
sleep 8
PID=$(docker exec test-committer sh -c "ps -eo pid,args | grep 'start coordinator' | grep -v grep | awk '{print \$1}'")
echo "KILL coordinator pid=$PID at $(date +%T)" | tee -a $out
docker exec test-committer kill -9 $PID
sleep 3
docker exec -d test-committer sh -c "/root/bin/committer start coordinator --config /root/config/coordinator.yaml > /tmp/coord2.log 2>&1"
echo "RESTARTED coordinator at $(date +%T)" | tee -a $out
wait $RUNPID
cat $D/logs/rs4_crash.part | tee -a $out
# wait until every transaction has committed (retries after the outage), then the digest
prev=-1; for i in $(seq 1 60); do n=$(docker exec test-committer psql -U postgres -h localhost -p 5433 -At -c "SELECT COUNT(*) FROM tx_status"); [ "$n" = "$prev" ] && break; prev=$n; sleep 5; done
echo "COMMITTED rows=$n" | tee -a $out
echo "DIGEST rs4_crash $(digest)" | tee -a $out
echo EXP_RESTART4_DONE | tee -a $out
