#!/usr/bin/env bash
# Phase 46b (11/03): phase 46 with a sounder control (phase 46 aborted on its control: its points sat at the
# Table II peak rates, on the cliff where throughput with retries drops sharply, so single re-runs varied 20-30%;
# the October logs show the same cliff, e.g. HTFabric Zipf 0.8: 2250 -> 2238 tx/s, 2312 -> 1794).
# The control now re-runs October grid points below the knee and requires >= 95% of the October goodput at the
# same rate.
# Phase 46 (11/03): contention sweep for a throughput-vs-skew figure.
# Same image, harness, grid, downward search, two-sided refinement and peak criterion as phases 38/40 (Table II);
# mode with retries (rk, the paper's main mode) only; the four schemes of Fig. 11 (EPR eprz, HTFabric htf2,
# XOX rxo2, Fabric-X fifo).
#   new skews: Zipf transfers theta = 0 (the 1M uniform dataset, 30 s runs, as Table II's "without hotspots"),
#              0.6, 0.9, 1.2 (60k transfers over 10k accounts, same generator as 0.8/0.99);
#              SmallBank theta = 0 (1M uniform, 30 s), 0.6 (60k);
#   existing skews come from Table II (Zipf 0.8/0.99, SmallBank 0.8/1.0/1.2).
# Step 0 is a control: EPR and HTFabric re-measured at their Table II peak rates on Zipf 0.8 and SmallBank 1.0;
# if a goodput falls below 90% of its Table II value the environment has drifted and the phase aborts before the
# sweeps (other users' jobs run on this host). Controls are reported, never used to replace Table II.
# Ends with a copy of the logs to /data (copy only).
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase46b.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
X=$D/data/smallbank; S=$D/data/synth
for f in $S/zipf_0.6.json.gz $S/zipf_0.9.json.gz $S/zipf_1.2.json.gz $S/zipf_0M.json.gz $X/sb_t0.6.json.gz $X/sb_t0M.json.gz \
         $S/genesis_0.6.json $S/genesis_0.9.json $S/genesis_1.2.json $S/genesis_0M.json $X/genesis_t0.6.json $X/genesis_t0M.json \
         $S/zipf_0.8.json.gz $S/genesis_0.8.json $X/sb_t1.0.json.gz $X/genesis_t1.0.json; do
  [ -s $f ] || { echo "MISSING $f, abort" | tee -a $out; exit 1; }
done
echo "START $(date '+%F %T') free_kb=$(df --output=avail / | tail -1) load=$(cut -d' ' -f1-3 /proc/loadavg)" | tee -a $out
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK|SBCHECK|PILOT_REEXEC|PILOT_EPR |PILOT_ESCROW|D16REEXEC"
run() { local l=$1 img=$2 vc=$3 evm=$4 q=$5 ds=$6 gen=$7; shift 7
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; kill -TERM $$; exit 1; }
  env FX_PRIME_JSON=$gen "$@" bash $R $l $img $vc $evm $q $ds 2>&1 | grep -E "$G" | tee -a $out; }
EPRZ="eprz epr 3 1 oldqueue FX_EPR_PROG=1 EPR_RO=1"
HTF2="htf2 epr 1 1 oldqueue WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
FIFO="fifo base 0 0 oldqueue"
dsof() { case $1 in sb0M) echo "$X/sb_t0M.json.gz $X/genesis_t0M.json";; sb*) echo "$X/sb_t${1#sb}.json.gz $X/genesis_t${1#sb}.json";;
  z0M) echo "$S/zipf_0M.json.gz $S/genesis_0M.json";; z*) echo "$S/zipf_${1#z}.json.gz $S/genesis_${1#z}.json";; esac; }
xtra() { case $1 in *0M) echo "FX_OL_SECONDS=30 OUTSTANDING=200000";; esac; }
goodput() { echo "$1" | grep -oE "GOODPUT .*" | grep -oE "goodput_tps=[0-9.]+" | head -1 | cut -d= -f2; }

# 0) control below the knee: October grid points (wl scheme rate october_goodput), same-rate comparison
ctl_fail=0
while read -r wl sc r ref; do
  case $sc in eprz) spec="$EPRZ";; htf2) spec="$HTF2";; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  read -r ds gen <<< "$(dsof $wl)"
  res=$(run ctl_rk_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" FX_RETRY=50 < /dev/null)
  g=$(goodput "$res"); g=${g%.*}; [ -n "$g" ] || g=0
  echo "CONTROL rk $wl $s r=$r goodput=$g october=$ref ratio=$(awk -v a=$g -v b=$ref 'BEGIN{printf "%.3f", a/b}')" | tee -a $out
  [ $((g * 100)) -ge $((ref * 95)) ] || ctl_fail=1
done <<'TAB'
z0.8 eprz 4500 4482
z0.8 eprz 6000 5979
z0.8 htf2 2000 1993
sb1.0 eprz 3000 2959
sb1.0 eprz 4000 3951
sb1.0 htf2 2000 1974
TAB
[ $ctl_fail = 0 ] || { echo "CONTROL drift > 5% below the knee, abort before the sweeps" | tee -a $out; exit 1; }
echo "CONTROL ok" | tee -a $out

# 1) sweeps: grid ascending until the criterion fails; if the first rate fails, 500/250/100 downward
sweep() { local mode=$1 tag=$2 ds=$3 gen=$4 rates=$5 spec=$6; shift 6
  set -- $spec "$@"; local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local first=1 down=""
  for r in $rates; do
    res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in 500 250 100; do
      res=$(run ${mode}_${tag}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN ${mode}_${tag}_${s} at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE ${mode}_${tag}_${s} at r=$r" | tee -a $out
    done
  fi; }
GRID="1000 2000 3000 4000 6000 8000 12000 16000"
GRIDU="1000 2000 3000 4000 6000 8000 12000 16000 20000 24000"
for wl in z0.6 z0.9 z1.2 sb0.6 z0M sb0M; do
  read -r ds gen <<< "$(dsof $wl)"
  g="$GRID"; case $wl in *0M) g="$GRIDU";; esac
  for spec in "$EPRZ" "$HTF2" "$RXO2" "$FIFO"; do
    sweep rk $wl $ds $gen "$g" "$spec" $(xtra $wl) FX_RETRY=50
  done
  echo "SWEPT $wl $(date '+%F %T')" | tee -a $out
done

# 2) two-sided refinement around each peak (refine_points.py, same rule as Table II)
python3 $D/scripts/refine_points.py $out eprz htf2 rxo2 fifo > $D/logs/refine_points_p46b.txt
echo "REFINE points: $(wc -l < $D/logs/refine_points_p46b.txt)" | tee -a $out
while read -r mode wl sc r; do
  case $wl in ctl*) continue;; esac
  case $sc in eprz) spec="$EPRZ";; htf2) spec="$HTF2";; rxo2) spec="$RXO2";; fifo) spec="$FIFO";; *) continue;; esac
  set -- $spec; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  read -r ds gen <<< "$(dsof $wl)"
  run ${mode}_${wl}_${s}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) FX_RETRY=50 < /dev/null > /dev/null
done < $D/logs/refine_points_p46b.txt
python3 $D/scripts/peak_rates.py $out eprz htf2 rxo2 fifo > $D/logs/peak_rates_p46b.txt
echo "PEAKS" | tee -a $out; cat $D/logs/peak_rates_p46b.txt | tee -a $out

# 3) archive (copy only)
A=/data/yhy/d16_archive/logs_$(date +%Y%m%d)
mkdir -p $A && cp -p $D/logs/phase46b.txt $D/logs/refine_points_p46b.txt $D/logs/peak_rates_p46b.txt $A/ 2>/dev/null
echo "ARCHIVE $A" | tee -a $out
echo "PHASE46B_DONE $(date '+%F %T')" | tee -a $out
