# Stage S8 (11/04): data for the evaluation's mechanism figure (how each design treats a transaction whose
# endorsement went stale), as the related papers do (FabricSharp: abort rate by cause; Spectrum: aborts per commit;
# Loom: re-execution ratio). Zipf transfers at theta = 0, 0.6, 0.8, 0.9, 0.99, 1.2, one fixed offered rate
# (1,000 tx/s, which EPR sustains at every skew), with retries, all seven schemes; per run the harness's
# resubmission count (EPR RETRY) and the gateway's invalid count are kept next to the usual lines, and the
# re-execution baselines' PILOT_REEXEC / D16REEXEC counters. Labels m_ (not read by the peak scripts).
XNAME=s8
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local EPRZW="eprzw eprw 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local wl x l s img vc evm q ds gen
  # committer-side cost of repairing one stale guarded delta (micro-benchmark, 5 x 2M iterations; the router
  # program's decode + evaluation, 38 us, was measured in phase A3)
  if ! grep -q "^BenchmarkEPRDeltaEval" $out; then
    (cd $D/src/committer-v103 && GOFLAGS=-tags=http2legacy DB_DEPLOYMENT=local go test -run XXX -bench EPRDeltaEval \
       -benchtime 2000000x -count 5 -overlay $D/patch/opt3/overlay.json ./service/vc/ 2>&1 | grep -E "^Benchmark|^ok|FAIL") | tee -a $out
    save
  fi
  for wl in z0 z0.6 z0.8 z0.9 z0.99 z1.2; do
    read -r ds gen <<< "$(dsof $wl)"
    for x in "$EPRZW" "$HTF2" "$RXO2" "$FS" "$FPP" "$FIFO" "$DEPG"; do
      set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
      l=m_rk_${wl}_${s}_r1000
      grep -q "^== $l exit=" $out && continue
      [ "$(free_kb)" -lt 12000000 ] && { qlog "DISK_LOW before $l, stage stops"; save; return 1; }
      quiet_or_wait $l
      env FX_PRIME_JSON=$gen FX_RATE=1000 "$@" FX_RETRY=50 bash $R $l $img $vc $evm $q $ds < /dev/null 2>&1 \
        | grep -E "$G|EPR RETRY|Gateway.Stop" | tee -a $out
      save
    done
  done; }
