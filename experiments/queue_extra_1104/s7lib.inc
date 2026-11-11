# Shared part of stages S7a (30_s7a.sh) and S7b (40_s7b.sh) (11/04): the final re-measurement of every single-host EPR
# result of the paper with the final image, after the optimization is settled (optimize first, then
# re-measure EPR once with the final version; baselines keep their data, since only EPR's code path changed).
# Released by hand: logs/s7_go holds "<image alias> <label suffix> [env...]", e.g. "eprw x EPR_PREFETCH=0" (labels
# eprzx, epr1x, epr12x; the env assignments are passed to every EPR run). Without that file both stages are skipped.
# Procedures and rates as in the paper:
#   S7a: a) phase 38 main sweeps with retries (phase 38's capacity estimates, so the same rates are tested) + refinement;
#        b) uniform SmallBank and Zipf (theta = 0, 60k) on the 46c grid + two-sided refinement + two repeats of the
#           SmallBank peak (the knee is refined as for every scheme); c) phase 46b skews (Zipf 0.6/0.9/1.2, SB 0.6).
#   S7b: d) phase 38 main sweeps without retries (incl. the 1M uniform sets); e) phase 40 USDT, both modes;
#        f) phase 38 ablation (EPR-1, EPR-1+2 on the final image), both modes; g) phase 39 block cutting;
#        h) phase 38 duration check (600k transactions, 120 s) at the new peaks, 100/95/90/85%.
# Not covered: the five-server deployment of phase 44 (Fig. Q2 b-d).
S7OK=0; S7IMG=""; S7SUF=""; S7ENV=""
if [ -f $D/logs/s7_go ]; then read -r S7IMG S7SUF S7ENV < $D/logs/s7_go; [ -n "$S7IMG" ] && [ -n "$S7SUF" ] && S7OK=1; fi
EPRZ7="eprz$S7SUF $S7IMG 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1 $S7ENV"
EPR17="epr1$S7SUF $S7IMG 1 1 oldqueue FX_EPR_PROG=1 $S7ENV"
EPR127="epr12$S7SUF $S7IMG 2 1 oldqueue FX_EPR_PROG=1 $S7ENV"
dsof7() { case $1 in usdcB*) echo "$U $UG";; *) dsof $1;; esac; }
xtra7() { case $1 in dex*) echo "OUTSTANDING=30000";; *0M) echo "FX_OL_SECONDS=30 OUTSTANDING=200000";; esac; }
bt7() { case $1 in usdcB25) echo "MOCK_BS=25 MOCK_BT=50ms";; usdcB250) echo "MOCK_BS=250 MOCK_BT=500ms";; usdcB500) echo "MOCK_BS=500 MOCK_BT=1s";; esac; }

# sweepx7 <mode> <workload> <p> <spec>: phase 38's sweep from a capacity estimate p: p/2, 3p/4, p, 5p/4, 3p/2, 2p
# ascending until one collapses; if the first collapses, downward through p/4, p/8, p/16
sweepx7() { local mode=$1 wl=$2 p=$3 spec=$4 s img vc evm q ds gen first=1 down="" r res extra=""
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  read -r ds gen <<< "$(dsof7 $wl)"; [ $mode = rk ] && extra="FX_RETRY=50"
  for r in $((p/2)) $((p*3/4)) $p $((p*5/4)) $((p*3/2)) $((p*2)); do
    res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra7 $wl) $(bt7 $wl) $extra < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in $((p/4)) $((p/8)) $((p/16)); do
      res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra7 $wl) $(bt7 $wl) $extra < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${wl}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out
    done
  fi
  save; }

# sweepg7 <mode> <workload> <rates> <spec>: the queue's grid sweep (46b/46c/phase 40)
sweepg7() { local mode=$1 wl=$2 rates=$3 spec=$4 s img vc evm q ds gen first=1 down="" r res extra=""
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  read -r ds gen <<< "$(dsof7 $wl)"; [ $mode = rk ] && extra="FX_RETRY=50"
  for r in $rates; do
    res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra7 $wl) $extra < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in 500 250 100; do
      res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra7 $wl) $extra < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${wl}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out
    done
  fi
  save; }

# refine7 <tag> <schemes...>: two-sided refinement of this stage's sweeps so far (points already run are skipped)
refine7() { local st=$1; shift; local schemes="$*" mode wl sc r spec s img vc evm q ds gen extra
  python3 $D/scripts/refine_points.py $out $schemes > $D/logs/refine_q1104_s7$st.txt
  echo "REFINE $st points: $(wc -l < $D/logs/refine_q1104_s7$st.txt)" | tee -a $out
  while read -r mode wl sc r; do
    case $sc in "eprz$S7SUF") spec="$EPRZ7";; "epr1$S7SUF") spec="$EPR17";; "epr12$S7SUF") spec="$EPR127";; *) continue;; esac
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof7 $wl)"
    extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra7 $wl) $(bt7 $wl) $extra < /dev/null > /dev/null
  done < $D/logs/refine_q1104_s7$st.txt
  cp -p $D/logs/refine_q1104_s7$st.txt $A/ 2>/dev/null; save; }
