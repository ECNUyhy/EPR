# Stage E-c (11/05): HTFabric with EPR's snapshot-query rule (read-only queries commit at their
# snapshot). htf2ro = htf2 (same image epr103, same settings) plus FX_EPR_RO=1: a stale transaction that writes no
# contract storage is committed at its snapshot instead of being re-executed (the check precedes HTFabric's
# re-execution in the VC). SmallBank 0.8, 1.0, 1.2 with retries: the rule fires only on SmallBank's balance queries
# (snapshot_reads=0 in every eprzw run on USDC, USDT, Zipf and DEX). Unified sweep of usweep_lib.sh, starting from
# 1.3x htf2's peak. A smoke run first (SmallBank 1.0 at half htf2's peak): it must sustain with snapshot_reads > 0.
XNAME=ec
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local HTF2RO="htf2ro epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1 EPR_RO=1"
  local ds gen res wl p
  read -r ds gen <<< "$(dsof sb1.0)"
  set -- $HTF2RO; shift 5
  usw_pause sm_sb1.0_htf2ro_r1250
  res=$(run sm_sb1.0_htf2ro_r1250 epr 1 1 oldqueue $ds $gen FX_RATE=1250 "$@" FX_RETRY=50 < /dev/null)
  if ! sustained "$res" rk || ! echo "$res" | grep -qE "PILOT_EPR .*snapshot_reads=[1-9]"; then
    echo "SMOKE_FAILED ec (rule inactive or run failed): stage skipped" | tee -a $out; save; return 0
  fi
  echo "SMOKE_OK ec ($(echo "$res" | grep -oE 'snapshot_reads=[0-9]+' | tail -1))" | tee -a $out
  while read -r wl p; do
    read -r ds gen <<< "$(dsof $wl)"
    usweep rk $wl $p $ds $gen $HTF2RO
  done <<'TAB'
sb1.0 3300
sb0.8 7100
sb1.2 1600
TAB
  echo "EC sweeps done" | tee -a $out
}
