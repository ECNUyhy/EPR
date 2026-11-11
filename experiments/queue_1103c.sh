#!/usr/bin/env bash
# Overnight queue (11/03): every remaining experiment in one unattended, resumable run.
# queue_1103c (12:41 UTC): the control waits for a quiet host on two checks 5 min apart (1- and 5-min load
# below 8), at most 3 h, and a stage gets 12 tries; HTFabric's control is very sensitive to other users' load.
# queue_1103b: same as queue_1103 plus a quiet-host wait before each stage's control (11/03 12:22 UTC: the first
# control ran at load 18 from another user's jobs and failed); a relaunch resumes from the existing logs.
# Launched once by hand:  (setsid nohup bash scripts/queue_1103c.sh > logs/queue_1103c.nohup 2>&1 < /dev/null &)
#
# Stages, in this order: S4a, S3, S1, S2, S4b, then scripts/queue_extra/*.sh (each has its own log logs/q1103_<stage>.txt, ends with "STAGE_DONE <stage>"):
#   S1 theta0  (paper)     every baseline not yet measured at the skews of Fig. 10a-b: FabricSharp and Fabric++
#                          with retries, depgraph without retries (it never aborts), on Zipf 0, 0.6, 0.9, 1.2 and
#                          SmallBank 0, 0.6 (60k datasets); same grid, downward search, refinement and criterion as 46c.
#   S2 fill47  (paper)     phase 47: continue the downward search of the 16 Fig. 8 / Fig. 10 cells with "<r" stubs.
#   S3 diag    (diagnosis) EPR's ~16k tx/s ceiling on uniform workloads: EPR-1, EPR-1+2, unguarded deltas at fixed
#                          rates on SmallBank 0 (60k, retries); 1M SmallBank 0 without retries at 18k tx/s for fifo,
#                          epr1, epr12, ng, eprz with a 10 s CPU profile of every committer process.
#   S4 fair    (fairness)  HTFabric and XOX without EPR's endorser-side extraction (htf2c, rxo2c: evm=0, no program
#                          tracer); the paper's htf2/rxo2 run on EPR's plumbing. Smoke run first; then uniform
#                          sweeps (Zipf 0, SmallBank 0) and contended sweeps (USDC, SmallBank 1.0, Zipf 0.8).
# Safety: every run is appended to the stage log as it finishes; the stage log is copied to
# /data/yhy/d16_archive/queue_1103/ after every sweep and at the end of each stage; a re-launch skips every run
# already in the log and every stage already marked done, so an interruption loses at most the run in progress.
# Control before each stage: EPR Zipf 0.8 r4500 (October 4482) and HTFabric Zipf 0.8 r2000 (October 1993) must reach
# 95%; HTFabric SmallBank 1.0 r2000 is still run and logged but no longer gates (it alternates between ~1855 and
# ~1974, most likely from other users' load). A failed control waits 30 min and retries, at most 12 times per stage.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; Q=$D/logs/queue_1103.txt; A=/data/yhy/d16_archive/queue_1103; mkdir -p $A
. $D/scripts/sweep_lib.sh
X=$D/data/smallbank; S=$D/data/synth; XD=$D/data/dex; T=$D/data/usdt
U=$D/src/fabric-x-evm/integration/perf/testdata/USDC_dataset.json.gz; UG=$D/data/USDC_genesis_151k.json
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|SBCHECK|PILOT_REEXEC|PILOT_EPR |PILOT_ESCROW|PILOT_FABRICSHARP|PILOT_REORDER|D16REEXEC|CPUPROF"
EPRZ="eprz epr 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
EPR1="epr1 epr 1 1 oldqueue FX_EPR_PROG=1"
EPR12="epr12 epr 2 1 oldqueue FX_EPR_PROG=1"
NG="ng epr 2 1 oldqueue NOGUARD=1"
HTF2="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
HTF2C="htf2c epr 1 0 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 EARLY_GOSSIP=1 REEXEC_ALL=1"
RXO2C="rxo2c epr 1 0 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ALL=1"
FS="fs epr 0 0 oldqueue FABRICSHARP=1"
FPP="fpp epr 0 0 oldqueue REORDER=1"
FIFO="fifo base 0 0 oldqueue"
DEPG="depg base 0 0 depgraph"
dsof() { case $1 in sb0M) echo "$X/sb_t0M.json.gz $X/genesis_t0M.json";; sb*) echo "$X/sb_t${1#sb}.json.gz $X/genesis_t${1#sb}.json";;
  usdc) echo "$U $UG";; usdt) echo "$T/usdt.json.gz $T/genesis.json";;
  z*) echo "$S/zipf_${1#z}.json.gz $S/genesis_${1#z}.json";; dex*) echo "$XD/swaps_p${1#dex}.json.gz $XD/genesis_p${1#dex}.json";; esac; }
xtra() { case $1 in dex*) echo "OUTSTANDING=30000";; esac; }
goodput() { echo "$1" | grep -oE "GOODPUT .*" | grep -oE "goodput_tps=[0-9.]+" | head -1 | cut -d= -f2; }
qlog() { echo "$(date '+%F %T') $*" | tee -a $Q; }
save() { cp -p $out $A/ 2>/dev/null; cp -p $Q $A/ 2>/dev/null; }

# quiet_or_wait <label>: before every run, wait while other users' jobs load the host (1-min load above 10; our own
# runs add about 4-6 and decay within a minute), at most 2 hours; the load at the start of each run is logged
# ("LOAD <label> l1 l5 l15") so contaminated points can be screened afterwards
quiet_or_wait() { local l=$1 n=0 l1
  while :; do
    read -r l1 _ < /proc/loadavg
    awk -v a=$l1 'BEGIN{exit !(a < 10)}' && break
    [ $n -ge 24 ] && { qlog "LOAD still $l1 after 2 h before $l, running anyway (flagged)"; break; }
    [ $n = 0 ] && qlog "PAUSE before $l: load $l1"
    n=$((n+1)); sleep 300
  done
  echo "LOAD $l $(cut -d' ' -f1-3 /proc/loadavg) waited=$((n*5))min" >> $out; }

# quiet_strict <label>: two consecutive checks 5 min apart with 1- and 5-min load below 8 (before a control), at most 3 h
quiet_strict() { local l=$1 n=0 ok=0 l1 l5
  while [ $ok -lt 2 ]; do
    read -r l1 l5 _ < /proc/loadavg
    if awk -v a=$l1 -v b=$l5 'BEGIN{exit !(a < 8 && b < 8)}'; then ok=$((ok+1)); else ok=0; fi
    [ $ok -ge 2 ] && break
    [ $n -ge 36 ] && { qlog "LOAD still $l1 $l5 after 3 h before $l, running anyway (flagged)"; break; }
    [ $n = 0 ] && qlog "PAUSE (strict) before $l: load $l1 $l5"
    n=$((n+1)); sleep 300
  done
  echo "LOAD $l $(cut -d' ' -f1-3 /proc/loadavg) waited=$((n*5))min" >> $out; }

# run <label> <img> <vc> <evm> <q> <ds> <gen> [env...]: skipped (stored output replayed) if already in the stage log
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  if grep -q "^== $l exit=" $out 2>/dev/null; then
    awk -v L="== $l exit=" 'index($0,L)==1{f=1;print;next} f&&/^== /{exit} f{print}' $out; return; fi
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { qlog "DISK_LOW before $l, queue stops"; save; kill -TERM $$; exit 1; }
  quiet_or_wait $l
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }

# sweep <mode rk|pk> <workload> <rates> <spec> [env...]: grid ascending until the criterion fails; if the first
# rate fails, 500/250/100 downward (as phases 38/40/46c)
sweep() { local mode=$1 wl=$2 rates=$3 spec=$4; shift 4
  set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local ds gen first=1 down="" r res; read -r ds gen <<< "$(dsof $wl)"
  local extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
  for r in $rates; do
    res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in 500 250 100; do
      res=$(run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${wl}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out
    done
  fi
  save; }

# refine <schemes...>: two-sided refinement around each peak of this stage's sweeps, then the peak table
refine() { local st=$1; shift; local schemes="$*"  # the loop below reuses the positional parameters
  python3 $D/scripts/refine_points.py $out "$@" > $D/logs/refine_q1103_$st.txt
  echo "REFINE points: $(wc -l < $D/logs/refine_q1103_$st.txt)" | tee -a $out
  local mode wl sc r spec s img vc evm q ds gen extra
  while read -r mode wl sc r; do
    case $sc in fs) spec="$FS";; fpp) spec="$FPP";; depg) spec="$DEPG";; htf2c) spec="$HTF2C";; rxo2c) spec="$RXO2C";; *) continue;; esac
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null > /dev/null
  done < $D/logs/refine_q1103_$st.txt
  python3 $D/scripts/peak_rates.py $out $schemes > $D/logs/peak_rates_q1103_$st.txt
  echo "PEAKS" | tee -a $out; cat $D/logs/peak_rates_q1103_$st.txt | tee -a $out
  cp -p $D/logs/refine_q1103_$st.txt $D/logs/peak_rates_q1103_$st.txt $A/ 2>/dev/null; save; }

# control <stage> <try>: returns 0 if the gating points reach 95% of October
control() { local st=$1 try=$2 fail=0 wl sc r ref gate spec s img vc evm q ds gen res g
  while read -r wl sc r ref gate; do
    case $sc in eprz) spec="$EPRZ";; htf2) spec="$HTF2";; esac
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    res=$(env FX_PRIME_JSON=$gen FX_RATE=$r "$@" FX_RETRY=50 bash $R ctl${try}_${st}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen < /dev/null 2>&1 | grep -E "$G")
    echo "$res" >> $out
    g=$(goodput "$res"); g=${g%.*}; [ -n "$g" ] || g=0
    echo "CONTROL $st try=$try $wl $s r=$r goodput=$g october=$ref ratio=$(awk -v a=$g -v b=$ref 'BEGIN{printf "%.3f", a/b}') gate=$gate" | tee -a $out $Q
    [ $gate = 1 ] && [ $((g * 100)) -lt $((ref * 95)) ] && fail=1
  done <<'TAB'
z0.8 eprz 4500 4482 1
z0.8 htf2 2000 1993 1
sb1.0 htf2 2000 1974 0
TAB
  return $fail; }

# stage <name> <function>: control (with retries), body, done marker; skipped if already done
stage() { local st=$1 fn=$2 try
  out=$D/logs/q1103_$st.txt; touch $out
  grep -q "^STAGE_DONE $st" $out && { qlog "SKIP $st (already done)"; return 0; }
  for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
  for try in 1 2 3 4 5 6 7 8 9 10 11 12; do
    qlog "STAGE $st try=$try start load=$(cut -d' ' -f1-3 /proc/loadavg) free_kb=$(free_kb)"
    quiet_strict control_${st}_$try
    if control $st $try; then
      $fn
      echo "STAGE_DONE $st $(date '+%F %T')" | tee -a $out; save; qlog "DONE $st"; return 0
    fi
    qlog "CONTROL failed for $st (try $try), retry in 30 min"; save; sleep 1800
  done
  qlog "GAVE_UP $st after 12 control failures"; save; return 1; }

GRIDU="1000 2000 3000 4000 6000 8000 12000 16000 20000 24000 32000"
GRID="1000 2000 3000 4000 6000 8000 12000 16000"

s1() { local wl
  for wl in z0 sb0 z0.6 sb0.6 z0.9 z1.2; do
    g="$GRID"; case $wl in z0|sb0) g="$GRIDU";; esac
    sweep rk $wl "$g" "$FS"; sweep rk $wl "$g" "$FPP"; sweep pk $wl "$g" "$DEPG"
    echo "SWEPT $wl $(date '+%F %T')" | tee -a $out
  done
  refine s1 fs fpp depg
  # repeats of the theta = 0 peak and parity points of EPR and Fabric-X (46c), two more runs each, so the paper's
  # uniform-workload ratios come with a spread; labels rep<i>_... are not picked up as sweep points
  local i spec s img vc evm q ds gen r
  while read -r wl sc r; do
    case $sc in eprz) spec="$EPRZ";; fifo) spec="$FIFO";; esac
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    for i in 1 2; do run rep${i}_rk_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null; done
    save
  done <<'TAB'
sb0 eprz 24000
sb0 fifo 21000
sb0 eprz 16000
sb0 fifo 16000
z0 eprz 22000
z0 fifo 13000
TAB
}

s2() { local mode wl sc rates spec s img vc evm q ds gen extra lab r res
  while read -r mode wl sc rates; do
    case $sc in fs) spec="$FS";; fpp) spec="$FPP";; fifo) spec="$FIFO";; depg) spec="$DEPG";; esac
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    extra=""; [ $mode = rk ] && extra="FX_RETRY=50"
    lab=$mode; [ $sc = depg ] && lab=dk
    for r in $rates; do
      res=$(run ${lab}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) $extra < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${wl}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${wl}_${s} at r=$r" | tee -a $out
    done
    save
  done <<'TAB'
rk usdc fpp 50 25 10 5
rk usdt fpp 50 25 10 5
rk z0.8 fpp 50 25 10 5
rk sb1.0 fpp 50 25 10 5
rk z0.99 fs 50 25 10 5
rk z0.99 fpp 50 25 10 5
rk z0.99 fifo 50 25 10 5
rk sb1.2 fs 50 25 10 5
rk sb1.2 fpp 50 25 10 5
rk sb1.2 fifo 50 25 10 5
rk dex1 fs 5 2 1
rk dex1 fpp 5 2 1
rk dex1 fifo 5 2 1
pk dex1 depg 10 5 2
rk z0.9 fifo 50 25 10 5
rk z1.2 fifo 50 25 10 5
TAB
}

s3() { local spec s img vc evm q ds gen r
  read -r ds gen <<< "$(dsof sb0)"
  for spec in "$EPR1" "$EPR12" "$NG"; do
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    for r in 16000 20000 24000; do
      run diag_rk_sb0_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null > /dev/null
    done
    save
  done
  read -r ds gen <<< "$(dsof sb0M)"
  for spec in "$FIFO" "$EPR1" "$EPR12" "$NG" "$EPRZ"; do
    set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    run diag_pk_sb0M_${s}_r18000 $img $vc $evm $q $ds $gen FX_RATE=18000 FX_OL_SECONDS=30 OUTSTANDING=200000 \
      CPUPROF=1 CPUPROF_S=10 "$@" < /dev/null > /dev/null
    cp -rp $D/logs/cpu_diag_pk_sb0M_${s}_r18000 $A/ 2>/dev/null; save
  done; }

# S4 split (the fairness check runs first): s4a decides (uniform + the three contended workloads),
# s4b completes the Fig. 10 skews with the clean configuration so that a figure never mixes configurations.
s4smoke() { local spec=$1 s img vc evm q ds gen res
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  read -r ds gen <<< "$(dsof sb1.0)"
  res=$(run smoke_rk_sb1.0_${s}_r1000 $img $vc $evm $q $ds $gen FX_RATE=1000 "$@" FX_RETRY=50 < /dev/null)
  # pass only if the run committed and the colocated EVM re-executed something (a PILOT_REEXEC line is printed only
  # with calls > 0): a configuration that silently aborted stale transactions instead would otherwise look fine
  if echo "$res" | grep -q "GOODPUT .*goodput_tps=[1-9]" && echo "$res" | grep -q "PILOT_REEXEC"; then
    echo "SMOKE_OK $s" | tee -a $out; else echo "SMOKE_FAILED $s" | tee -a $out; fi; }
s4a() { local wl spec ok=""
  for spec in "$HTF2C" "$RXO2C"; do s4smoke "$spec"; done
  grep -q "^SMOKE_OK htf2c" $out && ok="$ok htf2c"; grep -q "^SMOKE_OK rxo2c" $out && ok="$ok rxo2c"
  [ -n "$ok" ] || { echo "SMOKE_FAILED all: stage skipped" | tee -a $out; return 0; }
  for wl in z0 sb0 usdc sb1.0 z0.8; do
    g="$GRID"; case $wl in z0|sb0) g="$GRIDU";; esac
    case "$ok" in *htf2c*) sweep rk $wl "$g" "$HTF2C";; esac
    case "$ok" in *rxo2c*) sweep rk $wl "$g" "$RXO2C";; esac
  done
  refine s4a htf2c rxo2c; }
s4b() { local wl ok="" L=$D/logs/q1103_s4a.txt
  grep -q "^SMOKE_OK htf2c" $L && ok="$ok htf2c"; grep -q "^SMOKE_OK rxo2c" $L && ok="$ok rxo2c"
  [ -n "$ok" ] || { echo "no clean configuration passed the smoke run: stage skipped" | tee -a $out; return 0; }
  for wl in z0.6 z0.8 z0.9 z0.99 z1.2 sb0.6 sb0.8 sb1.2; do
    case "$ok" in *htf2c*) [ $wl = z0.8 ] || sweep rk $wl "$GRID" "$HTF2C";; esac
    case "$ok" in *rxo2c*) [ $wl = z0.8 ] || sweep rk $wl "$GRID" "$RXO2C";; esac
  done
  refine s4b htf2c rxo2c; }

qlog "QUEUE start"
for wl in z0 z0.6 z0.8 z0.9 z0.99 z1.2 sb0 sb0.6 sb1.0 sb1.2 sb0M usdc usdt dex1; do
  for f in $(dsof $wl); do [ -s $f ] || { qlog "MISSING $f, queue stops"; exit 1; }; done
done
stage s4a s4a && stage s3 s3 && stage s1 s1 && stage s2 s2 && stage s4b s4b || { qlog "QUEUE stopped early"; exit 1; }
# extra stages appended while the queue runs (e.g. the optimization stage written after S3's diagnosis): every
# scripts/queue_extra/NN_<name>.sh defines XNAME and a function xbody; each runs once through stage() (control,
# resume, archive), in file-name order; the directory is re-scanned until no new file appears
mkdir -p $D/scripts/queue_extra
while :; do
  ran=0
  for f in $(ls $D/scripts/queue_extra/*.sh 2>/dev/null | sort); do
    unset XNAME; unset -f xbody; . $f
    [ -n "$XNAME" ] || continue
    grep -q "^STAGE_DONE $XNAME" $D/logs/q1103_$XNAME.txt 2>/dev/null && continue
    stage $XNAME xbody || { qlog "QUEUE stopped in extra stage $XNAME"; exit 1; }
    ran=1
  done
  [ $ran = 0 ] && break
done
qlog "QUEUE_DONE"; cp -p $Q $A/ 2>/dev/null
