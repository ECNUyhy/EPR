# Stage S7b (11/04): second part of the final re-measurement of EPR (see s7lib.inc; released by logs/s7_go):
#   d) phase 38 main sweeps without retries (incl. the 1M uniform sets); e) phase 40 USDT, both modes;
#   f) phase 38 ablation; g) phase 39 block cutting; one refinement pass for all of them;
#   h) phase 38 duration check at the new peaks.
. $D/scripts/queue_extra_1104/s7lib.inc
XNAME=""; [ "$S7OK" = 1 ] && XNAME=s7b

xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  echo "S7 image=$S7IMG suffix=$S7SUF" | tee -a $out
  local wl p mode spec
  # d) phase 38 main sweeps without retries
  while read -r wl p; do sweepx7 pk $wl $p "$EPRZ7"; done <<'TAB'
sb1.2 1859
sb1.0 4000
sb0.8 9562
usdc 6500
z0.99 3125
z0.8 6000
dex1 343
dex16 1593
dex64 2800
sb0M 16875
z0M 17000
TAB
  echo "S7D sweeps done" | tee -a $out
  # e) phase 40 USDT, both modes
  for mode in rk pk; do sweepg7 $mode usdt "$GRID" "$EPRZ7"; done
  echo "S7E sweeps done" | tee -a $out
  # f) ablation on the final image (phase 38's capacity estimates)
  while read -r wl p; do
    for spec in "$EPR17" "$EPR127"; do for mode in rk pk; do sweepx7 $mode $wl $p "$spec"; done; done
  done <<'TAB'
sb1.0 3000
usdc 3000
z0.99 2000
z0.8 4000
TAB
  echo "S7F sweeps done" | tee -a $out
  # g) block cutting (phase 39's capacity estimate for EPR: 6400)
  for wl in usdcB25 usdcB250 usdcB500; do sweepx7 rk $wl 6400 "$EPRZ7"; done
  echo "S7G sweeps done" | tee -a $out
  # one refinement pass for every sweep of the stage
  refine7 b "eprz$S7SUF epr1$S7SUF epr12$S7SUF"; echo "S7 refinement done" | tee -a $out
  # h) duration check at the new peaks (600k transactions, 120 s)
  python3 $D/scripts/peak_rates.py $D/logs/q1103_s7a.txt $out "eprz$S7SUF" > $D/logs/peak_rates_q1104_s7b.txt
  cp -p $D/logs/peak_rates_q1104_s7b.txt $A/ 2>/dev/null
  echo "PEAKS" | tee -a $out; cat $D/logs/peak_rates_q1104_s7b.txt | tee -a $out
  local line r0 pct r s img vc evm q ds gen extra res
  for wl in sb1.2 sb1.0 sb0.8 z0.99 z0.8; do
    for mode in rk pk; do
      line=$(grep -E "^$mode $wl eprz$S7SUF " $D/logs/peak_rates_q1104_s7b.txt)
      [ -n "$line" ] || { echo "NOPEAK $mode $wl eprz$S7SUF" | tee -a $out; continue; }
      r0=$(echo "$line" | awk '{print $4}')
      case $wl in sb*) ds=$X/sb_t${wl#sb}L.json.gz gen=$X/genesis_t${wl#sb}L.json;; z*) ds=$S/zipf_${wl#z}L.json.gz gen=$S/genesis_${wl#z}L.json;; esac
      set -- $EPRZ7; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
      extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
      for pct in 100 95 90 85; do
        r=$((r0 * pct / 100))
        res=$(run ${mode}_${wl}L_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $extra < /dev/null)
        if sustained "$res" $mode; then echo "DURATION ok ${mode}_${wl}L_${s} at r=$r (${pct}% of $r0)" | tee -a $out; break; fi
        echo "DURATION fail ${mode}_${wl}L_${s} at r=$r (${pct}% of $r0)" | tee -a $out
      done
      save
    done
  done
  echo "S7H done" | tee -a $out; save; }
