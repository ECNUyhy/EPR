#!/usr/bin/env bash
# Phase 39 (after phase 38): block-cutting sensitivity with the final EPR configuration (the earlier figure came
# from an early prototype and closed-loop load). Same peak criterion and refinement as everywhere.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase39.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
X=$D/data/smallbank; S=$D/data/synth; XD=$D/data/dex
U=$D/src/fabric-x-evm/integration/perf/testdata/USDC_dataset.json.gz; UG=$D/data/USDC_genesis_151k.json
grep -q PHASE38_DONE $D/logs/phase38.txt || { echo "phase 38 missing, abort" | tee -a $out; exit 1; }
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|SBCHECK|PILOT_REEXEC|PILOT_EPR |PILOT_ESCROW|D16REEXEC"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; kill -TERM $$; exit 1; }
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
EPRX="eprz epr 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
EPR1="epr1 epr 1 1 oldqueue FX_EPR_PROG=1"
EPR12="epr12 epr 2 1 oldqueue FX_EPR_PROG=1"
HTF2="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"

# sweep from a capacity estimate p: p/2, 3p/4, p, 5p/4, 3p/2, 2p ascending until one collapses; if the first
# collapses, downward through p/4, p/8, p/16
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
dsof() { case $1 in sb0M) echo "$X/sb_t0M.json.gz $X/genesis_t0M.json";; sb*) echo "$X/sb_t${1#sb}.json.gz $X/genesis_t${1#sb}.json";;
  usdc) echo "$U $UG";; z0M) echo "$S/zipf_0M.json.gz $S/genesis_0M.json";; z*) echo "$S/zipf_${1#z}.json.gz $S/genesis_${1#z}.json";;
  dex*) echo "$XD/swaps_p${1#dex}.json.gz $XD/genesis_p${1#dex}.json";; esac; }
xtra() { case $1 in dex*) echo "OUTSTANDING=30000";; *0M) echo "FX_OL_SECONDS=30 OUTSTANDING=200000";; esac; }
refine() { local scs=$1 f=$D/logs/refine_points_p38_${1// /_}.txt
  python3 $D/scripts/refine_points.py $out $scs > $f
  echo "REFINE $scs points: $(wc -l < $f)" | tee -a $out
  while read -r mode wl sc r; do
    case $sc in eprz) spec="$EPRX";; epr1) spec="$EPR1";; epr12) spec="$EPR12";; *) continue;; esac
    set -- $spec; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    local extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    read -r ds gen <<< "$(dsof $wl)"
    run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null > /dev/null
  done < $f; }

# 1) block cutting (USDC, with retries): peak of EPR (final), HTFabric and Fabric-X for B transactions or t per
# block, B/t = 25/50ms, 250/500ms, 500/1s (100/200ms is the main configuration, measured in phases 32/38/24)
for bt in "25 50ms" "250 500ms" "500 1s"; do
  read -r bs btime <<< "$bt"
  for spec_p in "$EPRX|6400" "$HTF2|1480" "fifo base 0 0 oldqueue|200"; do
    spec=${spec_p%|*}; p=${spec_p#*|}
    MOCK_BS=$bs MOCK_BT=$btime sweepx rk usdcB$bs $U $UG $p "$spec" FX_RETRY=50
  done
done
python3 $D/scripts/refine_points.py $out eprz htf2 fifo > $D/logs/refine_points_p39.txt
echo "REFINE block points: $(wc -l < $D/logs/refine_points_p39.txt)" | tee -a $out
while read -r mode wl sc r; do
  case $sc in eprz) spec="$EPRX";; htf2) spec="$HTF2";; fifo) spec="fifo base 0 0 oldqueue";; *) continue;; esac
  bs=${wl#usdcB}; case $bs in 25) btime=50ms;; 250) btime=500ms;; 500) btime=1s;; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  MOCK_BS=$bs MOCK_BT=$btime run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $U $UG FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null
done < $D/logs/refine_points_p39.txt
echo PHASE39_DONE | tee -a $out
