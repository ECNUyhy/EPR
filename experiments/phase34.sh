#!/usr/bin/env bash
# Phase 34 (after phase 33): the final EPR configuration (epro: EPR with read-only transactions at their
# snapshot, no A5, no folding) where it was never measured, and the duration check of the headline numbers.
#   0) the re-executing DEX baselines (HTFabric, XOX) refined around their phase-28 peaks by the same rule;
#   0b) uncontended ceilings refined, and CPU profiles of EPR and Fabric-X there (endorser side via HPROF);
#   1) DEX 1/16/64 pairs, both modes, epro (phase 28 measured EPR before the fast-path fix in no-retry mode);
#      coarse grid as for every scheme on DEX, refined by the two-sided rule like everything else;
#   2) duration check: 600k-transaction versions of SmallBank 0.8/1.0/1.2 and Zipf 0.8/0.99 (120 s of
#      submission instead of 15-60 s), EPR and HTFabric (the strongest baseline everywhere), both modes, at
#      each scheme's peak rate from phases 32/33; if a scheme does not sustain it, 95/90/85% of it are tried,
#      for both schemes alike (adaptive, no follow-up runs needed).
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase34.txt; : > $out
. $D/scripts/sweep_lib.sh
disk_need() { echo 12000000; }  # 120 s runs at up to ~9000 tx/s commit up to 600k transactions
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
grep -q PHASE33_DONE $D/logs/phase33.txt || { echo "phase 33 missing, abort" | tee -a $out; exit 1; }
X=$D/data/smallbank; S=$D/data/synth; XD=$D/data/dex
for f in $X/sb_t0.8L $X/sb_t1.0L $X/sb_t1.2L $S/zipf_0.8L $S/zipf_0.99L; do [ -f $f.json.gz ] || { echo "DATASET $f missing, abort" | tee -a $out; exit 1; }; done
# image with the read-only rule restricted to nonce-only writers (10/12, to match its proof; SmallBank's queries
# write only their nonce, so results must not change: checked by the smoke below)
export COMMITTER_DIR=$D/src/committer-v103
(GOFLAGS=-tags=http2legacy bash $D/patch/apply_epr2.sh > $D/logs/apply_p34.log 2>&1 && cd $COMMITTER_DIR && grep -q eprAccountPrefix service/vc/epr_pilot.go && grep -q eprROSnapshot service/vc/epr_pilot.go && GOFLAGS=-tags=http2legacy make build-image-test-node version=epr103 image_namespace=d16 docker_cmd=docker > $D/logs/build_p34.log 2>&1 && echo IMAGE_OK) | tee -a $out
grep -q IMAGE_OK $out || { echo "IMAGE build failed, abort" | tee -a $out; exit 1; }
grep -q "^ok.*service/vc" $D/logs/apply_p34.log && grep -q "^ok.*dependencygraph" $D/logs/apply_p34.log || { echo "UNIT tests failed, abort" | tee -a $out; exit 1; }
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|SBCHECK|PILOT_REEXEC|PILOT_EPR |D16REEXEC"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; kill -TERM $$; exit 1; }
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
EPRO="epro epr 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
HTF2="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
EPROFF="eproff epr 0 0 oldqueue"
FIFO="fifo base 0 0 oldqueue"
sweep() { local mode=$1 tag=$2 ds=$3 gen=$4 rates=$5 spec=$6; shift 6
  set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local first=1 down=""
  for r in $rates; do
    res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
    if ! sustained "$res" $mode; then
      echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break
    fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in 50 25 10; do
      res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${tag}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out
    done
  fi; }

# smoke: the final configuration on SmallBank 1.2 with the serial-order oracle (as in phase 33)
env FX_PRIME_JSON=$X/genesis_t1.2.json FX_RATE=1000 FX_OL_SECONDS=30 FX_EPR_PROG=1 EPR_RO=1 SBCHECK=1 ORACLE=1   bash $R smoke_epro34_sb1.2 epr 3 1 oldqueue $X/sb_t1.2.json.gz 2>&1 | grep -E "$G|D16ORACLE" | tee -a $out
blk=$(grep -A14 "^== smoke_epro34_sb1.2 exit=0" $out)
echo "$blk" | grep -q "D16ORACLE.* mismatched=0 " || { echo "SMOKE epro34 oracle mismatch, abort" | tee -a $out; exit 1; }
echo "$blk" | grep -qE "SBCHECK.* [0-9]+ 0 0$" || { echo "SMOKE epro34 overdraft, abort" | tee -a $out; exit 1; }
echo "$blk" | grep -qE "snapshot_reads=[1-9]" || { echo "SMOKE epro34 no snapshot reads, abort" | tee -a $out; exit 1; }
echo "SMOKE epro34 ok" | tee -a $out

# 0) DEX baselines refined by the same two-sided rule as EPR (phase 28 measured them on the coarse grid only;
# EPR's DEX points below are refined, so the re-executing baselines, the strongest on DEX, are refined too;
# FabricSharp, Fabric++ and Fabric-X stay coarse: at least 4x below every other scheme there)
python3 $D/scripts/refine_points.py $D/logs/phase28.txt htf2 rxo2 > $D/logs/refine_points_p34base.txt
echo "REFINE dex baseline points: $(wc -l < $D/logs/refine_points_p34base.txt)" | tee -a $out
while read -r mode wl sc r; do
  case $sc in htf2) spec="$HTF2";; rxo2) spec="$RXO2";; *) continue;; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $XD/swaps_p${wl#dex}.json.gz $XD/genesis_p${wl#dex}.json FX_RATE=$r "$@" OUTSTANDING=30000 $extra < /dev/null > /dev/null
done < $D/logs/refine_points_p34base.txt
echo "DEX baselines done" | tee -a $out

# 0b) uncontended ceilings (phase 33 step 2, coarse grid) refined by the same two-sided rule for EPR, EPR off,
# Fabric-X and HTFabric; then a CPU profile of EPR and Fabric-X on uncontended SmallBank, where EPR fell to
# 58% of Fabric-X (10931 vs 18722 tx/s), to locate the cost (endorser extraction or validation)
python3 $D/scripts/refine_points.py $D/logs/phase33.txt epro eproff fifo htf2 | grep -E " (sb0M|z0M) " > $D/logs/refine_points_p34u.txt
echo "REFINE uncontended points: $(wc -l < $D/logs/refine_points_p34u.txt)" | tee -a $out
while read -r mode wl sc r; do
  case $sc in epro) spec="$EPRO";; eproff) spec="$EPROFF";; fifo) spec="$FIFO";; htf2) spec="$HTF2";; *) continue;; esac
  case $wl in sb0M) ds=$X/sb_t0M.json.gz gen=$X/genesis_t0M.json;; z0M) ds=$S/zipf_0M.json.gz gen=$S/genesis_0M.json;; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_OL_SECONDS=30 OUTSTANDING=200000 < /dev/null > /dev/null
done < $D/logs/refine_points_p34u.txt
for spec in "$EPRO" "$FIFO"; do
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  env FX_PRIME_JSON=$X/genesis_t0M.json FX_RATE=12000 FX_OL_SECONDS=30 OUTSTANDING=200000 CPUPROF=1 CPUPROF_S=20 HPROF=1 "$@"     bash $R prof_${s}_sb0M_r12000 $img $vc $evm $q $X/sb_t0M.json.gz 2>&1 | grep -E "$G|CPUPROF" | tee -a $out
done
echo "UNCONTENDED done" | tee -a $out

# 1) DEX, final EPR configuration
for mode in pk rk; do
  extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  for P in 1 16 64; do sweep $mode dex$P $XD/swaps_p$P.json.gz $XD/genesis_p$P.json "100 250 500 1000 2000 4000" "$EPRO" OUTSTANDING=30000 $extra; done
done
python3 $D/scripts/refine_points.py $out epro > $D/logs/refine_points_p34.txt
echo "REFINE dex points: $(wc -l < $D/logs/refine_points_p34.txt)" | tee -a $out
while read -r mode wl sc r; do
  set -- $EPRO; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $XD/swaps_p${wl#dex}.json.gz $XD/genesis_p${wl#dex}.json FX_RATE=$r "$@" OUTSTANDING=30000 $extra < /dev/null > /dev/null
done < $D/logs/refine_points_p34.txt
echo "DEX done" | tee -a $out

# 2) duration check at the peak rates (EPR: epro on SmallBank, epr elsewhere, where the read-only rule never
# applies; the runs use the epro configuration throughout)
python3 $D/scripts/peak_rates.py $D/logs/phase32.txt $D/logs/phase33.txt epro epr htf2 > $D/logs/peak_rates_p34.txt
echo "PEAKS $(wc -l < $D/logs/peak_rates_p34.txt)" | tee -a $out
lds() { case $1 in sb*) echo "$X/sb_t${1#sb}L.json.gz $X/genesis_t${1#sb}L.json";; z*) echo "$S/zipf_${1#z}L.json.gz $S/genesis_${1#z}L.json";; esac; }
for wl in sb1.2 sb1.0 sb0.8 z0.99 z0.8; do
  for mode in rk pk; do
    for sc in epr htf2; do
      if [ $sc = epr ]; then
        line=$(grep -E "^$mode $wl epro " $D/logs/peak_rates_p34.txt || grep -E "^$mode $wl epr " $D/logs/peak_rates_p34.txt); spec="$EPRO"
      else
        line=$(grep -E "^$mode $wl htf2 " $D/logs/peak_rates_p34.txt); spec="$HTF2"
      fi
      [ -n "$line" ] || { echo "NOPEAK $mode $wl $sc" | tee -a $out; continue; }
      r0=$(echo "$line" | awk '{print $4}')
      read -r ds gen <<< "$(lds $wl)"
      set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
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
echo PHASE34_DONE | tee -a $out
