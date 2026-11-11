# Stage S7a (11/04): first part of the final re-measurement of EPR (see s7lib.inc; released by logs/s7_go):
#   a) phase 38 main sweeps, with retries; b) uniform SmallBank and Zipf (theta = 0); c) phase 46b skews;
#   then one refinement pass for all of them and two repeats of the uniform SmallBank peak.
. $D/scripts/queue_extra_1104/s7lib.inc
XNAME=""; [ "$S7OK" = 1 ] && XNAME=s7a

xbody() {
  local R=$D/scripts/srv_run_evm6w.sh   # superset of srv_run_evm6.sh: images eprv, eprw (and the timer variants)
  echo "S7 image=$S7IMG suffix=$S7SUF" | tee -a $out
  local wl p r i ds gen s img vc evm q
  # a) phase 38 main sweeps, with retries
  while read -r wl p; do sweepx7 rk $wl $p "$EPRZ7"; done <<'TAB'
sb1.2 1859
sb1.0 4000
sb0.8 9562
usdc 6500
z0.99 3125
z0.8 6000
dex1 343
dex16 1593
dex64 2800
TAB
  echo "S7A sweeps done" | tee -a $out
  # b) uniform access (theta = 0, 60k) on the 46c grid
  for wl in sb0 z0; do sweepg7 rk $wl "$GRIDU" "$EPRZ7"; done
  echo "S7B sweeps done" | tee -a $out
  # c) phase 46b skews, with retries
  for wl in z0.6 z0.9 z1.2 sb0.6; do sweepg7 rk $wl "$GRID" "$EPRZ7"; done
  echo "S7C sweeps done" | tee -a $out
  # one refinement pass for every sweep of the stage (as phase 38 / 46b / 46c: each workload refined once)
  refine7 a "eprz$S7SUF"
  python3 $D/scripts/peak_rates.py $out "eprz$S7SUF" > $D/logs/peak_rates_q1104_s7a.txt
  # two repeats of the uniform SmallBank peak
  r=$(awk -v s="eprz$S7SUF" '$1=="rk" && $2=="sb0" && $3==s {print $4}' $D/logs/peak_rates_q1104_s7a.txt | head -1)
  if [ -n "$r" ]; then
    read -r ds gen <<< "$(dsof7 sb0)"
    set -- $EPRZ7; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    for i in 1 2; do run rep${i}_rk_sb0_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null; done
  fi
  echo "PEAKS" | tee -a $out; cat $D/logs/peak_rates_q1104_s7a.txt | tee -a $out
  cp -p $D/logs/peak_rates_q1104_s7a.txt $A/ 2>/dev/null; save; }
