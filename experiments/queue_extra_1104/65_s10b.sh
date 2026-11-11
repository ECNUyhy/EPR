# Stage S10b (11/05): the two-organization sweeps of S10 with clients that resubmit a transaction whose endorsements
# disagree. In S10 every failure was such a disagreement: the two endorsers were at different block heights, so they read
# different versions, and a program also carries the values its endorser read (the endorsed fast path uses them); not one
# disagreement had the same read versions. The harness retries only MVCC conflicts, so these transactions counted as
# failed, and their share grows with the rate: EPR's DEX sweep stopped at 836 tx/s (5.5% failed at 1,254) while HTFabric
# ran at 500. A disagreement never reaches ordering; a client re-endorses it as it would an aborted transaction.
# Overlay patch/twoorg_r (= patch/twoorg plus gateway/core/batch_submitter.go: SubmitFailedErrHook) and flag
# -retry-mismatch: only errors "do not match" are resubmitted (up to FX_RETRY times, as MVCC conflicts); reverts and
# other submission failures stay failures. Schemes, workloads and grid as S10 (EPR and HTFabric; USDC, SmallBank 1.0,
# DEX 16; 0.8p..1.1p up, 0.6p..0.2p down), labels ..2r..; the one-organization references are S10's (..1o..).
# A smoke run first (EPR, USDC, 0.5p): it must sustain, log resubmitted disagreements and keep the USDC supply.
XNAME=s10b
xbody() {
  local R=$D/scripts/srv_run_evm6w2o.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local TWO="NS=basic2of2 EXTRA_ENDORSERS=fabx-2of2-org2.yaml OVERLAY=twoorg_r RETRY_MISMATCH=1"
  local ds gen res n
  read -r ds gen <<< "$(dsof usdc)"
  res=$(run sm_usdc_eprzw2r_r3453 eprw 3 1 oldqueue $ds $gen FX_RATE=3453 FX_EPR_PROG=1 EPR_RO=1 $TWO FX_RETRY=50 < /dev/null)
  n=$(zcat $D/logs/evm_sm_usdc_eprzw2r_r3453.log.gz 2>/dev/null | grep -c "EPR MISMATCH RETRY")
  if ! sustained "$res" rk || [ "${n:-0}" -lt 1 ] || \
     ! echo "$res" | grep -qE "STATECHECK.* sm_usdc_eprzw2r_r3453 [0-9]+ 48108003660031865732317 [0-9]+ 0 0 [0-9]+"; then
    echo "SMOKE_FAILED s10b (mismatch-retry lines: ${n:-0}): stage skipped" | tee -a $out; save; return 0
  fi
  echo "SMOKE_OK s10b (mismatch-retry lines: $n)" | tee -a $out
  sw2r() { local wl=$1 spec=$2 p=$3; shift 3
    set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    local ds gen r res first=1 down="" sb=""; read -r ds gen <<< "$(dsof $wl)"
    case $wl in sb*) sb="SBCHECK=1";; esac
    for r in $((p*8/10)) $((p*9/10)) $p $((p*11/10)); do
      res=$(run rk_${wl}_${s}2r_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $sb FX_RETRY=50 < /dev/null)
      if ! sustained "$res" rk; then echo "COLLAPSE rk_${wl}_${s}2r at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
      first=0
    done
    if [ -n "$down" ]; then
      for r in $((p*6/10)) $((p*4/10)) $((p*2/10)); do
        res=$(run rk_${wl}_${s}2r_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $sb FX_RETRY=50 < /dev/null)
        if sustained "$res" rk; then echo "SUSTAIN rk_${wl}_${s}2r at r=$r" | tee -a $out; break; fi
        echo "COLLAPSE rk_${wl}_${s}2r at r=$r" | tee -a $out
      done
    fi
    save; }
  local sc wl p spec
  while read -r sc wl p; do
    case $sc in eprzw) spec="$EPRZW";; htf2) spec="$HTF2";; esac
    sw2r $wl "$spec" $p $TWO
  done <<'TAB'
eprzw dex16 2090
htf2 dex16 500
eprzw usdc 6906
htf2 usdc 1500
eprzw sb1.0 4000
htf2 sb1.0 2562
TAB
  echo "S10B sweeps done" | tee -a $out
}
