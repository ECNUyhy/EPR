# Stage S10c (11/05): completes the two-organization sweeps of S10b (clients resubmit disagreeing endorsements).
# HTFabric on DEX 16 pairs sustained every rate of the grid (550 = 1.1p, 550 tx/s), so its sweep goes on upwards in
# steps of 50 until a rate fails; then every S10b sweep is refined like the single-organization ones: the quarter
# points of [best sustained rate, next tested rate], ascending until one fails (refine_points.py's rule).
XNAME=s10c
xbody() {
  local R=$D/scripts/srv_run_evm6w2o.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local TWO="NS=basic2of2 EXTRA_ENDORSERS=fabx-2of2-org2.yaml OVERLAY=twoorg_r RETRY_MISMATCH=1"
  local sc wl lo hi x s img vc evm q ds gen sb r res k last
  r2() { local r=$1; shift; res=$(run rk_${wl}_${s}2r_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $TWO $(xtra $wl) $sb FX_RETRY=50 < /dev/null); }
  while read -r sc wl lo hi; do
    case $sc in eprzw) x="$EPRZW";; htf2) x="$HTF2";; esac
    set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"; sb=""; case $wl in sb*) sb="SBCHECK=1";; esac
    if [ "$hi" = up ]; then   # continue the sweep upwards from lo (last sustained rate of S10b)
      last=$lo; hi=""
      for r in $((lo + 50)) $((lo + 100)) $((lo + 150)) $((lo + 200)) $((lo + 250)); do
        r2 $r "$@"
        if sustained "$res" rk; then last=$r; echo "SUSTAIN rk_${wl}_${s}2r at r=$r" | tee -a $out
        else hi=$r; echo "COLLAPSE rk_${wl}_${s}2r at r=$r" | tee -a $out; break; fi
      done
      lo=$last; [ -n "$hi" ] || { save; continue; }
    fi
    for k in 1 2 3; do
      r=$((lo + (hi - lo) * k / 4))
      r2 $r "$@"
      if ! sustained "$res" rk; then echo "REFINE_STOP rk_${wl}_${s}2r at r=$r" | tee -a $out; break; fi
      echo "REFINE_OK rk_${wl}_${s}2r at r=$r" | tee -a $out
    done
    save
  done <<'TAB'
htf2 dex16 550 up
eprzw dex16 1881 2090
eprzw usdc 6215 6906
htf2 usdc 1350 1500
eprzw sb1.0 4000 4400
htf2 sb1.0 2305 2562
TAB
  echo "S10C sweeps done" | tee -a $out
}
