#!/usr/bin/env bash
# Phase 20: peak sustainable throughput per (workload, scheme) under open-loop load. Offered rate steps up
# until the scheme collapses (no EPR STATS, i.e. not finished in time, or median latency > 2 s); the peak is
# the best committed throughput among non-collapsed steps. Replaces phase 19, whose fixed 20k tx/s offered
# rate dumped each dataset within seconds (a burst, not a sustained load).
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase20.txt; : > $out
# a reboot resets the CPU governor to powersave, which halves the pipelined committer's capacity
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
R=$D/scripts/srv_run_evm6.sh; X=$D/data/smallbank; S=$D/data/synth; XD=$D/data/dex
U=$D/src/fabric-x-evm/integration/perf/testdata/USDC_dataset.json.gz; UG=$D/data/USDC_genesis_151k.json
export FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms
G="==|EPR STATS|Replay complete|GOODPUT"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
# sweep <tag> <dataset> <genesis> <outstanding> <scheme-spec...>; scheme spec "name img vc evm queue extra-env..."
sweep() { local tag=$1 ds=$2 gen=$3 os=$4; shift 4
  for spec in "$@"; do set -- $spec; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    local rates="1000 2000 3000 4000 6000 8000 12000 16000"; [ $s = depg ] && rates="100 250 500 1000 2000"
    for r in $rates; do
      res=$(run pk_${tag}_${s}_r$r $img $vc $evm $q $ds $gen OUTSTANDING=$os FX_RATE=$r "$@")
      p50=$(echo "$res" | grep -oE "p50=[0-9.]+" | head -1 | cut -d= -f2)
      { [ -z "$p50" ] || [ "${p50%.*}" -gt 2000 ]; } && { echo "COLLAPSE pk_${tag}_${s} at r=$r" | tee -a $out; break; }
    done
  done; }
EPR="epr epr 3 1 oldqueue FX_EPR_PROG=1"; FIFO="fifo base 0 0 oldqueue"; RO="ro epr 0 0 oldqueue REORDER=1"
RXO="rxo epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_opt FX_EPR_PROG=1"; NG="ng epr 2 1 oldqueue NOGUARD=1"
DEPG="depg base 0 0 depgraph"; EPRNP="eprnp epr 3 1 oldqueue"
for T in 1.0 1.2 0.8 0; do sweep sb$T $X/sb_t$T.json.gz $X/genesis_t$T.json 60000 "$EPR" "$RO" "$FIFO" "$RXO" "$NG" "$DEPG" "$EPRNP"; done
sweep usdc $U $UG 60000 "$EPR" "$RO" "$FIFO" "$RXO" "$NG" "$DEPG"
for th in 0.8 0.99 0; do sweep z$th $S/zipf_$th.json.gz $S/genesis_$th.json 60000 "$EPR" "$RO" "$FIFO" "$RXO" "$DEPG"; done
for P in 1 16 64; do sweep dex$P $XD/swaps_p$P.json.gz $XD/genesis_p$P.json 30000 "$EPR" "$FIFO" "$DEPG"; done
echo PHASE20_DONE | tee -a $out
