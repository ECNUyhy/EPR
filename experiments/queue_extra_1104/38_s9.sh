# Stage S9 (11/04): low-load points for the load-sweep figure (Fig. 9: committed throughput and median latency against
# the offered rate, linear axes). The paper's sweeps start at half a capacity estimate (EPR, phase 38) or at 1,000 tx/s
# (HTFabric, XOX), so their curves began mid-axis and read as missing data; below capacity a scheme commits what is
# offered, so these runs only extend each sweep downwards (as S2 did for the cells without a sustained rate). EPR with
# the final image (eprzw, as S7), HTFabric and XOX with the paper's configuration; with retries; labels rk_ (they are
# ordinary sweep points of the same schemes).
XNAME=s9
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local sc wl r x s img vc evm q ds gen
  while read -r sc wl r; do
    case $sc in eprzw) x="$EPRZW";; htf2) x="$HTF2";; rxo2) x="$RXO2";; esac
    set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    run rk_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) FX_RETRY=50 < /dev/null > /dev/null
    save
  done <<'TAB'
eprzw usdc 500
eprzw usdc 1000
eprzw usdc 2000
eprzw sb1.0 500
eprzw sb1.0 1000
eprzw dex16 250
eprzw dex16 500
htf2 usdc 250
htf2 usdc 500
htf2 sb1.0 250
htf2 sb1.0 500
rxo2 usdc 250
rxo2 usdc 500
rxo2 usdc 750
rxo2 sb1.0 250
rxo2 sb1.0 500
TAB
}
