#!/usr/bin/env bash
# Phase 38 (10/12): EPR's final configuration = read-only rule (nonce-only writers) + guarded fast path +
# concurrent value fetch with cached program decoding (notes 29.17; phase 37e A/B: better at every saturated
# rate, e.g. uncontended SmallBank at 18000 tx/s 15976 vs 14005, SmallBank 1.2 at 2000 1668 vs 503, DEX 64 at 3000 2813 vs 2534). Every EPR
# row, the ablation and the duration check are measured with it (label eprz); baselines are unaffected.
# Same procedure as phase 36 (which it supersedes for EPR); capacity estimates = phase 36's refined peaks.
#   1) image + unit tests + smoke with the serial-order oracle (SmallBank 1.2, DEX 64, USDC);
#   2) EPR (label eprz) on every workload, both modes: sweep from half the previous EPR peak rate upwards
#      (capacity estimate; the first rate collapsing falls back downwards), then the two-sided refinement;
#   3) ablation variants EPR-1, EPR-1+2 on the new image, same procedure;
#   4) duration check (600k transactions, 120 s) of EPR and HTFabric at their peaks, 100/95/90/85%;
#   5) copy the logs to /data.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase38.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
X=$D/data/smallbank; S=$D/data/synth; XD=$D/data/dex
U=$D/src/fabric-x-evm/integration/perf/testdata/USDC_dataset.json.gz; UG=$D/data/USDC_genesis_151k.json
export COMMITTER_DIR=$D/src/committer-v103
(GOFLAGS=-tags=http2legacy bash $D/patch/apply_epr2.sh > $D/logs/apply_p38.log 2>&1 && cd $COMMITTER_DIR && grep -q eprValidateReads service/vc/validator.go && grep -qF "eprPrefetched) values" service/vc/epr_prefetch.go && grep -q eprAccountPrefix service/vc/epr_pilot.go && GOFLAGS=-tags=http2legacy make build-image-test-node version=epr103 image_namespace=d16 docker_cmd=docker > $D/logs/build_p38.log 2>&1 && echo IMAGE_OK) | tee -a $out
grep -q IMAGE_OK $out || { echo "IMAGE build failed, abort" | tee -a $out; exit 1; }
grep -q "^ok.*service/vc" $D/logs/apply_p38.log && grep -q "^ok.*dependencygraph" $D/logs/apply_p38.log || { echo "UNIT tests failed, abort" | tee -a $out; exit 1; }
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

# 1) smoke
smoke() { local l=$1 gen=$2 ds=$3 rate=$4; shift 4
  env FX_PRIME_JSON=$gen FX_RATE=$rate FX_OL_SECONDS=30 FX_EPR_PROG=1 EPR_RO=1 ORACLE=1 "$@" bash $R $l epr 3 1 oldqueue $ds 2>&1 | grep -E "$G|D16ORACLE" | tee -a $out
  grep -A16 "^== $l exit=0" $out | grep -q "D16ORACLE.* mismatched=0 " || { echo "SMOKE $l oracle mismatch or failed, abort" | tee -a $out; exit 1; }; }
smoke smoke_eprz38_sb1.2 $X/genesis_t1.2.json $X/sb_t1.2.json.gz 1000 SBCHECK=1
grep -A16 "^== smoke_eprz38_sb1.2 exit=0" $out | grep -qE "SBCHECK.* [0-9]+ 0 0$" || { echo "SMOKE eprz38 overdraft, abort" | tee -a $out; exit 1; }
grep -A16 "^== smoke_eprz38_sb1.2 exit=0" $out | grep -qE "fast_progs=[1-9].* snapshot_reads=[1-9].* prefetch_hits=[1-9]" || { echo "SMOKE eprz38 fast path or read-only rule inactive, abort" | tee -a $out; exit 1; }
smoke smoke_eprz38_dex64 $XD/genesis_p64.json $XD/swaps_p64.json.gz 1000 OUTSTANDING=30000
smoke smoke_eprz38_usdc $UG $U 3000
echo "SMOKE eprz38 ok" | tee -a $out

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

# 2) EPR, final configuration; capacity estimates = phase 36 refined peaks
while read -r wl p modes; do
  read -r ds gen <<< "$(dsof $wl)"
  for mode in $modes; do
    extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    sweepx $mode $wl $ds $gen $p "$EPRX" $(xtra $wl) $extra
  done
done <<'TAB'
sb1.2 1859 rk pk
sb1.0 4000 rk pk
sb0.8 9562 rk pk
usdc 6500 rk pk
z0.99 3125 rk pk
z0.8 6000 rk pk
dex1 343 rk pk
dex16 1593 rk pk
dex64 2800 rk pk
sb0M 16875 pk
z0M 17000 pk
TAB
refine eprz
echo "EPRP done" | tee -a $out

# 3) ablation on the new image
while read -r wl p; do
  read -r ds gen <<< "$(dsof $wl)"
  for spec in "$EPR1" "$EPR12"; do
    for mode in rk pk; do extra=""; [ $mode = rk ] && extra="FX_RETRY=50"; sweepx $mode $wl $ds $gen $p "$spec" $extra; done
  done
done <<'TAB'
sb1.0 3000
usdc 3000
z0.99 2000
z0.8 4000
TAB
refine "epr1 epr12"
echo "ABLATION done" | tee -a $out

# 4) duration check
python3 $D/scripts/peak_rates.py $out eprz > $D/logs/peak_rates_p38.txt
python3 $D/scripts/peak_rates.py $D/logs/phase32.txt $D/logs/phase33.txt htf2 >> $D/logs/peak_rates_p38.txt
echo "PEAKS $(wc -l < $D/logs/peak_rates_p38.txt)" | tee -a $out
disk_need() { echo 12000000; }
lds() { case $1 in sb*) echo "$X/sb_t${1#sb}L.json.gz $X/genesis_t${1#sb}L.json";; z*) echo "$S/zipf_${1#z}L.json.gz $S/genesis_${1#z}L.json";; esac; }
for wl in sb1.2 sb1.0 sb0.8 z0.99 z0.8; do
  for mode in rk pk; do
    for spec in "$EPRX" "$HTF2"; do
      set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
      line=$(grep -E "^$mode $wl $s " $D/logs/peak_rates_p38.txt)
      [ -n "$line" ] || { echo "NOPEAK $mode $wl $s" | tee -a $out; continue; }
      r0=$(echo "$line" | awk '{print $4}'); read -r ds gen <<< "$(lds $wl)"
      extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
      for pct in 100 95 90 85; do
        r=$((r0 * pct / 100))
        res=$(run ${mode}_${wl}L_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $extra < /dev/null)
        if sustained "$res" $mode; then echo "DURATION ok ${mode}_${wl}L_${s} at r=$r (${pct}% of $r0)" | tee -a $out; break; fi
        echo "DURATION fail ${mode}_${wl}L_${s} at r=$r (${pct}% of $r0)" | tee -a $out
      done
    done
  done
done

# 5) archive (copy only)
A=/data/yhy/d16_archive/logs_$(date +%Y%m%d)
mkdir -p $A && cp -p $D/logs/*.txt $D/logs/*.gz $D/logs/*.prof $A/ 2>/dev/null; cp -rp $D/logs/cpu_* $A/ 2>/dev/null
echo "ARCHIVE $(ls $A | wc -l) files in $A" | tee -a $out
echo PHASE38_DONE | tee -a $out
