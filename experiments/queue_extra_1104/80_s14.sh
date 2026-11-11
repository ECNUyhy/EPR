# Stage S14 (11/05): refinement of the baseline peaks that bound EPR's leads. The paper says every sweep refines
# around the best sustained rate for all schemes alike, but some baseline sweeps were never refined: between the best
# sustained rate and the next tested one lies a gap of 7-100% (HTFabric DEX 16 pairs: 500 sustained, 625 not; with two
# organizations it sustains 550 in S10b). Listed here is every baseline whose gap could change a lead: the strongest
# baseline of a workload, or one whose next tested rate exceeds the strongest's peak (Fig. 8 with retries, Fig. 11a
# without, Fig. 10 skews), plus HTFabric and XOX on the 1M uniform transfers (quoted in the text, phase 33 settings
# FX_OL_SECONDS=30 OUTSTANDING=200000). Each gets the quarter points of [best sustained rate, next tested rate], ascending, until one
# fails the criterion (same rule as refine_points.py). Normal sweep labels, so the figures read them as sweep points.
XNAME=s14
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local mode sc wl rates rep x s img vc evm q ds gen r res extra rok gok
  while read -r mode sc wl rates rep; do
    case $sc in htf2) x="$HTF2";; rxo2) x="$RXO2";; fifo) x="$FIFO";; depg) x="$DEPG";; esac
    set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    case $wl in *0M) extra="$extra FX_OL_SECONDS=30 OUTSTANDING=200000";; esac
    rok=""; gok=0
    for r in ${rates//,/ }; do
      res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null)
      if ! sustained "$res" $mode; then echo "REFINE_STOP ${mode}_${wl}_${s} at r=$r" | tee -a $out; break; fi
      echo "REFINE_OK ${mode}_${wl}_${s} at r=$r" | tee -a $out; rok=$r
      gok=$(echo "$res" | grep -oE "whole_goodput=[0-9]+" | head -1 | cut -d= -f2); gok=${gok:-0}
    done
    # Fig. 8 workloads (column rep = the strongest baseline's current peak): if a refined rate sustained above it,
    # this scheme's peak moved there and is the strongest, so it is measured twice more, as S11 did at the old peaks
    # (labels <scheme>rp1/rp2, kept out of the sweeps)
    if [ -n "$rep" ] && [ "$rep" != - ] && [ -n "$rok" ] && [ "$gok" -gt "$rep" ]; then
      for k in 1 2; do
        run ${mode}_${wl}_${s}rp${k}_r$rok $img $vc $evm $q $ds $gen FX_RATE=$rok "$@" $(xtra $wl) $extra < /dev/null > /dev/null
      done
    fi
    save
  done <<'TAB'
rk htf2 dex16 531,562,593 499
rk htf2 dex64 906,937,968 878
rk htf2 usdt 1812,1875,1937 1751
rk rxo2 dex1 221,231,240 212
rk rxo2 usdc 1250,1500,1750 1479
rk rxo2 sb0.8 4500,5000,5500 5464
rk rxo2 sb1.0 2250,2500,2750 2528
rk rxo2 sb1.2 1250,1500,1750 1244
rk rxo2 dex16 452,468,484 499
rk fifo dex64 625,750,875 878
rk htf2 z0.6 3562,3625,3687
rk htf2 z0.9 1562,1625,1687
rk htf2 z1.2 656,687,718
rk htf2 sb0.6 8250,8500,8750
rk rxo2 sb0.6 9250,9500,9750
pk htf2 dex16 531,562,593
pk htf2 dex64 906,937,968
pk htf2 usdt 1812,1875,1937
pk rxo2 dex1 221,231,240
pk rxo2 usdc 1250,1500,1750
pk rxo2 sb0.8 4500,5000,5500
pk rxo2 sb1.0 2250,2500,2750
pk rxo2 sb1.2 1250,1500,1750
pk rxo2 dex16 452,468,484
pk depg sb0.6 9250,9500,9750
pk htf2 z0M 5250,5500,5750
pk rxo2 z0M 5000,6000,7000
TAB
  echo "S14 refinements done" | tee -a $out
}
