# Stage S11u (11/05): the USDT part of S11 (repeatability of the peaks of Fig. 8). EPR's USDT peak comes from S7b, so its
# offered rate is read from S7b's log here (peak_rates.py, the rule of the analysis); HTFabric's is the paper's
# (1,750 tx/s). Two repeats each, with retries, labels <scheme>rp1 / <scheme>rp2 as in S11.
XNAME=s11u
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local pe k spec sc r x s img vc evm q ds gen
  pe=$(python3 $D/scripts/peak_rates.py $D/logs/q1103_s7b.txt eprzw | awk '$1=="rk" && $2=="usdt" {print $4}' | head -1)
  if [ -z "$pe" ]; then echo "S11U: no EPR USDT peak with retries in S7b, stage skipped" | tee -a $out; save; return 0; fi
  for k in 1 2; do
    for spec in "eprzw $pe" "htf2 1750"; do
      set -- $spec; sc=$1 r=$2
      case $sc in eprzw) x="$EPRZW";; htf2) x="$HTF2";; esac
      set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
      read -r ds gen <<< "$(dsof usdt)"
      run rk_usdt_${s}rp${k}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null
      save
    done
  done
  echo "S11U repeats done (EPR USDT peak rate $pe)" | tee -a $out
}
