# Stage E-d (11/05): a finer ablation, final image, retries, unified sweep of usweep_lib.sh.
# (1) eprzwn = eprzw without the snapshot-query rule (FX_EPR_RO unset; escrow, programs, fast path and prefetch on)
#     on SmallBank 0.8, 1.0, 1.2: splits Fig. 12's last step into escrow and queries, and gives EPR's SmallBank lead
#     when neither EPR nor the baselines commit queries at their snapshot. The rule fires only on SmallBank
#     (snapshot_reads=0 in every eprzw run on USDC, USDT, Zipf and DEX), so elsewhere eprzwn runs eprzw's code path.
# (2) the ablation variants of S7b (epr1w: ordered repair; epr12w: plus commutative credits) on DEX 1 and DEX 16,
#     where guarded programs carry every swap.
XNAME=ed
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  . $D/scripts/usweep_lib.sh
  local EPRZWN="eprzwn eprw 3 1 oldqueue FX_EPR_PROG=1"
  local EPR1W="epr1w eprw 1 1 oldqueue FX_EPR_PROG=1"
  local EPR12W="epr12w eprw 2 1 oldqueue FX_EPR_PROG=1"
  local ds gen wl p v spec
  while read -r wl p v; do
    read -r ds gen <<< "$(dsof $wl)"
    case $v in zn) spec=$EPRZWN;; 1) spec=$EPR1W;; 12) spec=$EPR12W;; *) continue;; esac
    usweep rk $wl $p $ds $gen $spec
  done <<'TAB'
sb1.0 3200 zn
dex16 2000 1
dex16 2000 12
dex1 360 1
dex1 360 12
sb0.8 8000 zn
sb1.2 1500 zn
TAB
  echo "ED sweeps done" | tee -a $out
}
