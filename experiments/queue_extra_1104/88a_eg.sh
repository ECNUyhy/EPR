# Stage E-g (11/05 night): how much faster the HTFabric and XOX ports get when the re-execution service also prefetches
# the account fields its EVM reads. E-f showed that, besides the prefetched read sets, each re-executed transfer reads
# 4-5 account fields (balance, nonce of sender and callee) one database query at a time, about 100 us each, and a
# router swap 14. d16reexec_v3 (static build) = the E-f timers + those fields of each
# transaction's sender and callee in the same prefetch query (-acctprefetch) + a sample of keys that still miss.
# Runs at the baselines' peak rates (REP) and above, with retries; labels eg_<wl>_<scheme>_r<rate>, schemes htf3/rxo3
# (= htf2/rxo2 with d16reexec_v3). Compare with REP (same rates, d16reexec_v2).
XNAME=eg
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local P=$D/bin/d16reexec_v3
  [ -x $P ] || { echo "MISSING $P: stage skipped" | tee -a $out; save; return 0; }
  local HTFP="htf3 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$P REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
  local RXOP="rxo3 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$P FX_EPR_PROG=1 REEXEC_ALL=1"
  local wl s r spec ds gen extra lab
  while read -r wl s r; do
    read -r ds gen <<< "$(dsof $wl)"
    case $s in htf3) spec="$HTFP";; rxo3) spec="$RXOP";; *) continue;; esac
    extra="FX_RETRY=50"; case $wl in dex*) extra="$extra OUTSTANDING=30000";; esac
    lab=eg_${wl}_${s}_r$r
    grep -q "^== $lab exit=" $D/logs/q1103_*.txt 2>/dev/null && continue
    set -- $spec; shift 5
    usw_pause $lab
    run $lab $(echo $spec | cut -d" " -f2-5) $ds $gen FX_RATE=$r "$@" $extra < /dev/null > /dev/null
    docker exec test-committer cat /tmp/d16reexec.log > $D/logs/eg_$lab.log 2>/dev/null
    echo "EG $lab $(grep D16REEXEC $D/logs/eg_$lab.log | tail -1)" | tee -a $out
  done <<'TAB'
sb1.0 htf3 2562
z0.8 htf3 2250
usdc htf3 1500
dex1 rxo3 212
dex16 htf3 593
sb1.0 htf3 3200
z0.8 htf3 2810
usdc htf3 1875
dex1 rxo3 265
dex16 htf3 740
sb1.0 htf3 3840
z0.8 htf3 3375
usdc htf3 2250
TAB
  echo "EG runs done" | tee -a $out
}
