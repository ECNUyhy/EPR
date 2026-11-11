# Stage E-f (11/05 night): where the re-execution time of the HTFabric and XOX ports goes (what the
# 348 µs include). d16reexec_prof is d16reexec_v2 with timers only (built with go -overlay): request decoding, the prefetch query,
# per-transaction decoding, executor setup (NewStateDB + NewExecutor), the EVM call (database reads that miss the
# prefetch and code cache timed separately), collecting the writes and encoding the response. All else is the S14
# configuration. One run per row at the baseline's peak rate, with retries; the D16REEXEC line of each run carries the
# breakdown, and the full service log goes to logs/ef_<label>.log. Control for the timers' own cost: the service time
# per transaction of the same configuration with d16reexec_v2 (REP and S14 logs). Labels ef_<wl>_<scheme>_r<rate>.
XNAME=ef
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local P=$D/bin/d16reexec_prof
  [ -x $P ] || { echo "MISSING $P: stage skipped" | tee -a $out; save; return 0; }
  local HTFP="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$P REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
  local RXOP="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$P FX_EPR_PROG=1 REEXEC_ALL=1"
  local wl s r spec ds gen extra lab
  while read -r wl s r; do
    read -r ds gen <<< "$(dsof $wl)"
    case $s in htf2) spec="$HTFP";; rxo2) spec="$RXOP";; *) continue;; esac
    extra="FX_RETRY=50"; case $wl in dex*) extra="$extra OUTSTANDING=30000";; esac
    lab=ef_${wl}_${s}_r$r
    grep -q "^== $lab exit=" $D/logs/q1103_*.txt 2>/dev/null && continue
    set -- $spec; shift 5
    usw_pause $lab
    run $lab $(echo $spec | cut -d" " -f2-5) $ds $gen FX_RATE=$r "$@" $extra < /dev/null > /dev/null
    docker exec test-committer cat /tmp/d16reexec.log > $D/logs/ef_$lab.log 2>/dev/null
    echo "EF $lab $(grep D16REEXEC $D/logs/ef_$lab.log | tail -1)" | tee -a $out
  done <<'TAB'
usdc htf2 1500
usdc rxo2 1500
z0.8 htf2 2250
sb1.0 htf2 2562
dex1 rxo2 212
TAB
  echo "EF runs done" | tee -a $out
}
