# Stage S11 (11/05): repeatability of the peaks of Fig. 8. Every point of the paper is one run; this stage repeats the
# peak run (the offered rate at which the paper's peak was measured) of EPR (final image, S7a) and of the strongest
# baselines (HTFabric everywhere, XOX on DEX 1 pair) twice, on the nine single-host workloads of Fig. 8 with retries
# (USDT's EPR peak comes from S7b and is left out). Labels <scheme>rp1 / <scheme>rp2, so the figures' parsers do not
# mix them into the sweeps; the analysis compares them with the original peak runs.
XNAME=s11
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local k sc wl r x s img vc evm q ds gen
  for k in 1 2; do
    while read -r sc wl r; do
      case $sc in eprzw) x="$EPRZW";; htf2) x="$HTF2";; rxo2) x="$RXO2";; esac
      set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
      read -r ds gen <<< "$(dsof $wl)"
      run rk_${wl}_${s}rp${k}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) FX_RETRY=50 < /dev/null > /dev/null
      save
    done <<'TAB'
eprzw usdc 6906
htf2 usdc 1500
eprzw z0.8 6375
htf2 z0.8 2250
eprzw z0.99 3125
htf2 z0.99 1187
eprzw sb0.8 10757
htf2 sb0.8 5625
eprzw sb1.0 4000
htf2 sb1.0 2562
eprzw sb1.2 1859
htf2 sb1.2 1250
eprzw dex1 364
htf2 dex1 175
rxo2 dex1 212
eprzw dex16 2090
htf2 dex16 500
eprzw dex64 4200
htf2 dex64 875
TAB
  done
  echo "S11 repeats done" | tee -a $out
}
