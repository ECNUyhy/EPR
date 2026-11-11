# usweep_lib.sh (11/05): the unified sweep of the stages added on 11/05 (S16, E-c, E-d), sourced by their xbody.
# usweep <mode rk|pk> <workload label> <capacity estimate p> <dataset> <genesis> <s img vc evm q> [env...]
#   grid 0.8p, 0.9p, p, 1.1p ascending until the first run that fails the criterion; if the first one fails,
#   0.6p, 0.4p, 0.2p descending until one is sustained; then rounds of usweep_next.py (the rule of S14, S10c and
#   S15): quarter points above the best sustained rate, ascending until the first failing run; below a saturated
#   peak, descending until the first sustained unsaturated run; a sweep that sustained its highest tested rate
#   extended by 10% steps until a run fails. Labels <mode>_<workload>_<s>_r<rate>, as every other sweep.
saturated() { local g; g=$(echo "$1" | grep -oE "whole_goodput=[0-9]+" | head -1 | cut -d= -f2); [ -n "$g" ] && [ $((g * 100)) -lt $(($2 * 95)) ]; }
# a run waits for a quiet host without the 2-hour cap of queue_1104.sh (as from S15a2 on)
quiet_or_wait() { local l=$1 n=0 l1
  while :; do
    read -r l1 _ < /proc/loadavg
    awk -v a=$l1 'BEGIN{exit !(a < 10)}' && break
    [ $((n % 6)) = 0 ] && qlog "PAUSE before $l: load $l1 (waiting, no time limit)"
    n=$((n+1)); sleep 300
  done
  echo "LOAD $l $(cut -d' ' -f1-3 /proc/loadavg) waited=$((n*5))min" >> $out; }
# usw_pause <label>: once the time (epoch seconds) in logs/queue_pause_at has come, the queue exits between two runs
# without marking the stage done (11/05: E-c uses the idle hour before the five-host runs). It is resumable: on the
# next launch, run() replays the runs already in the stage log.
usw_pause() { local t; [ -s $D/logs/queue_pause_at ] || return 0; read -r t < $D/logs/queue_pause_at
  [ "$(date +%s)" -lt "$t" ] && return 0
  qlog "PAUSE_AT reached before $1: queue exits (resumable)"; save; exit 0; }
usweep() { local mode=$1 wl=$2 p=$3 ds=$4 gen=$5; shift 5
  local s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
  local -a ENVS=("$@")
  local lab=${mode}_${wl}_${s} extra="" first=1 down="" r res it dir list
  [ $mode = rk ] && extra="FX_RETRY=50"
  case $wl in dex*) extra="$extra OUTSTANDING=30000";; esac
  for r in $((p*8/10)) $((p*9/10)) $p $((p*11/10)); do
    usw_pause ${lab}_r$r
    res=$(run ${lab}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "${ENVS[@]}" $extra < /dev/null)
    if ! sustained "$res" $mode; then echo "COLLAPSE $lab at r=$r" | tee -a $out; [ $first = 1 ] && down=1; break; fi
    first=0
  done
  if [ -n "$down" ]; then
    for r in $((p*6/10)) $((p*4/10)) $((p*2/10)); do
      usw_pause ${lab}_r$r
    res=$(run ${lab}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "${ENVS[@]}" $extra < /dev/null)
      if sustained "$res" $mode; then echo "SUSTAIN $lab at r=$r" | tee -a $out; break; fi
      echo "COLLAPSE $lab at r=$r" | tee -a $out
    done
  fi
  for it in $(seq 1 20); do
    read -r dir list <<< "$(python3 $D/scripts/usweep_next.py $lab $mode $out)"
    [ -n "$list" ] || break
    echo "REFINE_ROUND $it $lab ($dir): $list" | tee -a $out
    for r in $list; do
      usw_pause ${lab}_r$r
    res=$(run ${lab}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "${ENVS[@]}" $extra < /dev/null)
      if [ $dir = up ]; then
        if ! sustained "$res" $mode; then echo "REFINE_STOP $lab at r=$r" | tee -a $out; break; fi
        echo "REFINE_OK $lab at r=$r" | tee -a $out
      else
        if sustained "$res" $mode && ! saturated "$res" $r; then echo "REFINE_OK $lab at r=$r (below)" | tee -a $out; break; fi
        echo "REFINE_NEXT $lab at r=$r (below)" | tee -a $out
      fi
    done
  done
  echo "SWEEP_DONE $lab best=$(python3 $D/scripts/usweep_next.py --best $lab $mode $out)" | tee -a $out
  save; }
