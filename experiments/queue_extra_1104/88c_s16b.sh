# Stage S16b (11/05 evening): tight funding, redesigned after S16's smoke (11/05 17:36). With each sender funded with
# exactly the least balance that lets its transfers succeed in ledger order (data/USDC_genesis_tight.json), a transfer
# whose funding credit has not committed when it is endorsed reverts in the EVM, under every scheme alike: at half of
# EPR's peak only 47% of the 151k transfers committed even with retries, so the retry criterion (95% committed) can
# never hold and S16 skipped its sweeps. Here both schemes run without retries (mode pk: median latency within 2 s,
# throughput of the transactions that commit), which compares how fast each commits the same feasible transfers.
# Smoke (EPR, pk, 2000 tx/s): supply equals the genesis total and no encoded content remains. Zero balances are
# stored as empty words, so the "empty" count of STATECHECK may be positive here. Unified sweep of usweep_lib.sh;
# labels pk_usdcT_<scheme>_r<rate>; PILOT_ESCROW reports the fast and ordered debits.
XNAME=s16b
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local GT=$D/data/USDC_genesis_tight.json res st
  [ -s $GT ] || { echo "MISSING $GT: stage skipped" | tee -a $out; save; return 0; }
  usw_pause sm_pk_usdcT_eprzw_r2000
  res=$(run sm_pk_usdcT_eprzw_r2000 eprw 3 1 oldqueue $U $GT FX_RATE=2000 FX_EPR_PROG=1 EPR_RO=1 < /dev/null)
  st=$(echo "$res" | grep -oE "STATECHECK.* sm_pk_usdcT_eprzw_r2000 [0-9]+ [0-9]+ [0-9]+ [0-9]+ [0-9]+ [0-9]+" | head -1)
  if [ -z "$st" ] || [ "$(echo "$st" | awk '{print $(NF-4)}')" != "5000286268567064448" ] || [ "$(echo "$st" | awk '{print $(NF-2)}')" != 0 ]; then
    echo "SMOKE_FAILED s16b (supply or encoded content): stage skipped" | tee -a $out; save; return 0
  fi
  echo "SMOKE_OK s16b ($st)" | tee -a $out
  usweep pk usdcT 5000 $U $GT $EPRZW
  usweep pk usdcT 1200 $U $GT $HTF2
  echo "S16B sweeps done" | tee -a $out
}
