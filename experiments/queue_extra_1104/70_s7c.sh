# Stage S7c (11/05): completes S7b's duration check (h) for Zipf 0.8 with retries. S7b tested 100/95/90/85% of the
# new peak and stopped at the first rate that passed: 6056 tx/s (95%) passed with p50 765 ms, but it was past capacity
# (p90 15.4 s, whole goodput 5,327 tx/s), so the long-run point is lower than at 90% of the peak (phase 38 with the
# previous image: 5,737 tx/s at 90%, p50 47 ms). This runs the remaining grid rates, 90% then 85% of 6375, and stops at
# the first that sustains with whole goodput >= 97% of the offered rate. The figure keeps its rule (best goodput with
# p50 <= 2 s over all tested rates), so the 95% run stays in the data.
. $D/scripts/queue_extra_1104/s7lib.inc
XNAME=""; [ "$S7OK" = 1 ] && XNAME=s7c

xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  echo "S7 image=$S7IMG suffix=$S7SUF" | tee -a $out
  local r0=6375 pct r s img vc evm q ds=$S/zipf_0.8L.json.gz gen=$S/genesis_0.8L.json res g
  set -- $EPRZ7; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  for pct in 90 85; do
    r=$((r0 * pct / 100))
    res=$(run rk_z0.8L_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null)
    g=$(echo "$res" | grep -oE "whole_goodput=[0-9]+" | head -1 | cut -d= -f2)
    if sustained "$res" rk && [ -n "$g" ] && [ $((g * 100)) -ge $((r * 97)) ]; then
      echo "DURATION ok rk_z0.8L_${s} at r=$r (${pct}% of $r0, whole goodput $g)" | tee -a $out; break
    fi
    echo "DURATION fail rk_z0.8L_${s} at r=$r (${pct}% of $r0, whole goodput ${g:-none})" | tee -a $out
  done
  save
  echo "S7C done" | tee -a $out; save; }
