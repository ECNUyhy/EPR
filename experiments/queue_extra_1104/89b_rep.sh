# Stage REP (11/05 night): run-to-run variance of the peaks of Fig. 8.
# Every peak of Fig. 8 (EPR and the strongest baseline, with retries) has three runs so far: the sweep run at the peak
# rate and the repeats rp1, rp2 (S14r, S15e). This stage adds rp3 and rp4 at the same rate, so each peak has five
# runs; rp1/rp2 are run too where they are missing (Zipf). EPR's rate is its final peak over all stage logs (S15e
# included); the baselines' rates are their S14-refined peaks. EPR and the baseline alternate within each workload,
# so both see the same host conditions. Labels rk_<wl>_<scheme>rp<k>_r<rate>; a label present in any stage log is
# not run again.
XNAME=rep
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local wl bs br er k pair s r spec ds gen extra lab
  while read -r wl bs br; do
    read -r ds gen <<< "$(dsof $wl)"
    er=$(python3 $D/scripts/usweep_next.py --best rk_${wl}_eprzw rk $D/logs/q1103_*.txt | cut -d@ -f2)
    if [ -z "$er" ] || [ "$er" = none ]; then echo "REP no EPR peak for $wl" | tee -a $out; continue; fi
    echo "REP $wl eprzw@$er $bs@$br" | tee -a $out
    extra="FX_RETRY=50"; case $wl in dex*) extra="$extra OUTSTANDING=30000";; esac
    for k in 1 2 3 4; do
      for pair in "eprzw $er" "$bs $br"; do
        set -- $pair; s=$1; r=$2
        case $s in eprzw) spec="$EPRZW";; htf2) spec="$HTF2";; rxo2) spec="$RXO2";; *) continue;; esac
        lab=rk_${wl}_${s}rp${k}_r$r
        grep -q "^== $lab exit=" $D/logs/q1103_*.txt 2>/dev/null && continue
        set -- $spec; shift 5
        usw_pause $lab
        run $lab $(echo $spec | cut -d" " -f2-5) $ds $gen FX_RATE=$r "$@" $extra < /dev/null > /dev/null
      done
    done
    save
  done <<'TAB'
usdc htf2 1500
usdt htf2 1875
z0.8 htf2 2250
z0.99 htf2 1187
sb0.8 htf2 5625
sb1.0 htf2 2562
sb1.2 htf2 1250
dex1 rxo2 212
dex16 htf2 593
dex64 htf2 906
TAB
  echo "REP runs done" | tee -a $out
}
