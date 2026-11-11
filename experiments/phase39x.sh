#!/usr/bin/env bash
# Phase 39x (10/18): XOX in the block-cutting experiment (phase 39 ran EPR, HTFabric and Fabric-X only), so that
# Fig. 9a carries the same four schemes as Fig. 9b-d. Same runner, sweep and refinement as phase 39; single host.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase39x.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
docker ps --format '{{.Names}}' | grep -qE '^(orderer-party|committer-|fabric-x-committer)' && { echo "multi-node stack still up on 116, abort" | tee -a $out; exit 1; }
U=$D/src/fabric-x-evm/integration/perf/testdata/USDC_dataset.json.gz; UG=$D/data/USDC_genesis_151k.json
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|PILOT_REEXEC|D16REEXEC"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; kill -TERM $$; exit 1; }
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
sweepx() { local mode=$1 tag=$2 ds=$3 gen=$4 p=$5 spec=$6; shift 6
  set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local first=1 down="" r
  for r in $((p/2)) $((p*3/4)) $p $((p*5/4)) $((p*3/2)) $((p*2)); do
    res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in $((p/4)) $((p/8)) $((p/16)); do
      res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${tag}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out
    done
  fi; }
# XOX's main-table peak on USDC (B/t = 100/200 ms) is 1,000 tx/s
for bt in "25 50ms" "250 500ms" "500 1s"; do
  read -r bs btime <<< "$bt"
  MOCK_BS=$bs MOCK_BT=$btime sweepx rk usdcB$bs $U $UG 1000 "$RXO2" FX_RETRY=50
done
python3 $D/scripts/refine_points.py $out rxo2 > $D/logs/refine_points_p39x.txt
echo "REFINE block points: $(wc -l < $D/logs/refine_points_p39x.txt)" | tee -a $out
while read -r mode wl sc r; do
  [ "$sc" = rxo2 ] || continue
  bs=${wl#usdcB}; case $bs in 25) btime=50ms;; 250) btime=500ms;; 500) btime=1s;; *) continue;; esac
  set -- $RXO2; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  MOCK_BS=$bs MOCK_BT=$btime run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $U $UG FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null
done < $D/logs/refine_points_p39x.txt
(cd $D/src/fabric-x-evm && docker compose -f compose.d16.yml down -v -t 5 >/dev/null 2>&1)
echo PHASE39X_DONE | tee -a $out
