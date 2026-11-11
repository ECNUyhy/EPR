#!/usr/bin/env bash
# Phase 40 (10/13): a second real workload, 88.9k real USDT transfers from 2,000 consecutive mainnet blocks,
# for every scheme in both modes, same grid, same refinement (two-sided), same peak criterion as the main table.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase40.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
T=$D/data/usdt; mkdir -p $T
cd $D/src/fabric-x-evm
if [ ! -f $T/usdt.json.gz ]; then
  python3 $D/scripts/usdt_tsv.py $D/data/usdt-flextender-range-2000blk-20261004.csv $T/usdt.tsv.gz | tee -a $out
  go run -tags=perf ./integration/perf -mode generate -input $T/usdt.tsv.gz -output $T/usdt.json.gz > $T/gen.log 2>&1
  go run ./d16prefund integration/perf/testdata/USDC_contract.json $T/usdt.json.gz $T/genesis.json 1000000000000000000 | tail -1 | tee -a $out
fi
c=$(zcat $T/usdt.json.gz | python3 -c "import json,sys; d=json.load(sys.stdin); print(len(d) if isinstance(d,list) else len(d.get('transfers', d.get('transactions', []))))")
echo "DATASET usdt txs=$c" | tee -a $out
[ "$c" -gt 80000 ] || { echo "DATASET usdt too small, abort" | tee -a $out; exit 1; }
cd $D
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|PILOT_REEXEC|PILOT_EPR |PILOT_FABRICSHARP|PILOT_REORDER|D16REEXEC"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; kill -TERM $$; exit 1; }
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
EPRZ="eprz epr 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
HTF2="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
FS="fs epr 0 0 oldqueue FABRICSHARP=1"
FPP="fpp epr 0 0 oldqueue REORDER=1"
FIFO="fifo base 0 0 oldqueue"
sweep() { local mode=$1 tag=$2 ds=$3 gen=$4 rates=$5 spec=$6; shift 6
  set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local first=1 down=""
  for r in $rates; do
    res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in 500 250 100; do
      res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${tag}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out
    done
  fi; }
GRID="1000 2000 3000 4000 6000 8000 12000 16000"
for mode in rk pk; do
  extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  for spec in "$EPRZ" "$HTF2" "$RXO2" "$FS" "$FPP" "$FIFO"; do sweep $mode usdt $T/usdt.json.gz $T/genesis.json "$GRID" "$spec" $extra; done
done
python3 $D/scripts/refine_points.py $out eprz htf2 rxo2 fs fpp fifo > $D/logs/refine_points_p40.txt
echo "REFINE points: $(wc -l < $D/logs/refine_points_p40.txt)" | tee -a $out
while read -r mode wl sc r; do
  case $sc in eprz) spec="$EPRZ";; htf2) spec="$HTF2";; rxo2) spec="$RXO2";; fs) spec="$FS";; fpp) spec="$FPP";; fifo) spec="$FIFO";; *) continue;; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $T/usdt.json.gz $T/genesis.json FX_RATE=$r "$@" $extra < /dev/null > /dev/null
done < $D/logs/refine_points_p40.txt
echo PHASE40_DONE | tee -a $out
