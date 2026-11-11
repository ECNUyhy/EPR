# Stage S10 (11/05): two-organization endorsement. Every other result uses one organization's endorsement (namespace
# basic, policy AND(Org1), as in IBM's harness). Here the namespace basic2of2 (policy AND(Org1, Org2), created at
# every start of the test node) gets a second organization's endorser (fabx-2of2-org2.yaml, Org2MSP), built like
# org1's (genesis funding, client nonces, EPR extraction) and syncing every block itself; the gateway collects both
# endorsements and the committer checks both signatures. EPR's signed content must be byte-identical across the two
# organizations, or the endorsements differ and the transaction fails. Harness: srv_run_evm6w2o.sh (overlay
# patch/twoorg, which adds -extra-endorsers; without it the overlay build runs the unchanged code path).
# Schemes: EPR (final image eprw) and the strongest baseline, HTFabric (paper configuration); USDC, SmallBank
# theta=1.0 and DEX 16 pairs; with retries. (Fabric-X left out on 11/05: at about 100 tx/s every run lasts the full
# 120 s or times out, about an hour for no information on the lead over the strongest baseline.) Per scheme and workload: a one-organization reference at the paper's peak offered rate p
# (same harness build, same session; labels ..1o..), then the two-organization sweep (labels ..2o..) 0.8p, 0.9p, p,
# 1.1p ascending until the criterion fails, or 0.6p, 0.4p, 0.2p downward if 0.8p already fails.
# A smoke run comes first (EPR, USDC, two organizations, 0.5p): it must sustain, log org2's endorser, keep the USDC
# supply and leave no encoded content; otherwise the stage is skipped (logged SMOKE_FAILED).
XNAME=s10
xbody() {
  local R=$D/scripts/srv_run_evm6w2o.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local TWO="NS=basic2of2 EXTRA_ENDORSERS=fabx-2of2-org2.yaml"
  local ds gen res n
  read -r ds gen <<< "$(dsof usdc)"
  res=$(run sm_usdc_eprzw2o_r3453 eprw 3 1 oldqueue $ds $gen FX_RATE=3453 FX_EPR_PROG=1 EPR_RO=1 $TWO FX_RETRY=50 < /dev/null)
  n=$(zcat $D/logs/evm_sm_usdc_eprzw2o_r3453.log.gz 2>/dev/null | grep -c "D16 EXTRA ENDORSER org2")
  if ! sustained "$res" rk || [ "${n:-0}" -lt 1 ] || \
     ! echo "$res" | grep -qE "STATECHECK.* sm_usdc_eprzw2o_r3453 [0-9]+ 48108003660031865732317 [0-9]+ 0 0 [0-9]+"; then
    echo "SMOKE_FAILED s10 (org2 endorser lines: ${n:-0}): stage skipped" | tee -a $out; save; return 0
  fi
  echo "SMOKE_OK s10 (org2 endorser lines: $n)" | tee -a $out
  # sw2 <workload> <spec> <p> <label suffix> [env...]: the sweep described above (one organization: reference only)
  sw2() { local wl=$1 spec=$2 p=$3 sfx=$4; shift 4
    set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    local ds gen r res first=1 down="" sb=""; read -r ds gen <<< "$(dsof $wl)"
    case $wl in sb*) sb="SBCHECK=1";; esac
    if [ "$sfx" = 1o ]; then
      run rk_${wl}_${s}1o_r$p $img $vc $evm $q $ds $gen FX_RATE=$p "$@" $(xtra $wl) $sb FX_RETRY=50 < /dev/null > /dev/null
      save; return 0
    fi
    for r in $((p*8/10)) $((p*9/10)) $p $((p*11/10)); do
      res=$(run rk_${wl}_${s}2o_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $sb FX_RETRY=50 < /dev/null)
      if ! sustained "$res" rk; then echo "COLLAPSE rk_${wl}_${s}2o at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
      first=0
    done
    if [ -n "$down" ]; then
      for r in $((p*6/10)) $((p*4/10)) $((p*2/10)); do
        res=$(run rk_${wl}_${s}2o_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $sb FX_RETRY=50 < /dev/null)
        if sustained "$res" rk; then echo "SUSTAIN rk_${wl}_${s}2o at r=$r" | tee -a $out; break; fi
        echo "COLLAPSE rk_${wl}_${s}2o at r=$r" | tee -a $out
      done
    fi
    save; }
  local sc wl p spec
  while read -r sc wl p; do
    case $sc in eprzw) spec="$EPRZW";; htf2) spec="$HTF2";; fifo) spec="$FIFO";; esac
    sw2 $wl "$spec" $p 1o NS=basic
    sw2 $wl "$spec" $p 2o $TWO
  done <<'TAB'
eprzw usdc 6906
htf2 usdc 1500
eprzw sb1.0 4000
htf2 sb1.0 2562
eprzw dex16 2090
htf2 dex16 500
TAB
  echo "S10 sweeps done" | tee -a $out
}
