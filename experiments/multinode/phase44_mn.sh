#!/usr/bin/env bash
# Phase 44 (multi-node, night of 10/16): peak goodput vs. emulated WAN round-trip time on the five-host
# deployment (Arma parties on 112/115/116/118, org1 committer on 118 next to party 4, client + gateway on 124).
# EPR (final), HTFabric and Fabric-X; USDC (real 151k) and SmallBank theta=1.0; RTT 0/20/50/100 ms (in this
# order: each RTT starts from the previous peak and searches downwards); client retries as in phase 42. Same peak criterion
# as the paper (sweep_lib.sh: p50 <= 2 s and >= 95% committed).
# Adaptive sweep per (workload, scheme, RTT): start at the grid point of the previous RTT's peak (single-host
# full-stack peaks for RTT 0), climb while sustained / descend while collapsing, stop at the first boundary,
# then one midpoint between the last sustained and the first collapsing point. At most 6 grid runs per sweep.
# Resumable: every run's output is kept in logs/p44/<label>.txt and reused when the phase is restarted.
#   usage (on 116): nohup bash scripts/phase44_mn.sh [deadline, e.g. "tomorrow 08:30"] > /dev/null 2>&1 &
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase44${P44_TAG:-}.txt; P=$D/logs/p44${P44_TAG:-}; mkdir -p $P
C="ssh -F $HOME/.ssh/config_d16mn -o BatchMode=yes"
. $D/scripts/sweep_lib.sh
DEADLINE=$(date -d "${1:-tomorrow 08:30}" +%s)
R=${MN_RUN:-$D/scripts/mn_run.sh}
U="integration/perf/testdata/USDC_dataset.json.gz data/USDC_genesis_151k.json"
S="data/smallbank/sb_t1.0.json.gz data/smallbank/genesis_t1.0.json"
X="data/dex/swaps_p16.json.gz data/dex/genesis_p16.json"
T="data/usdt/usdt.json.gz data/usdt/genesis.json"
export FX_NO_READ_PRIME=1 FX_OL_SECONDS=60 FX_RETRY=50 RUN_TIMEOUT=400 OUTSTANDING=60000
# scheme specs as in phase 40/42 (XOX = sequential re-execution of every invalid transaction, no WARD/gossip;
# FabricSharp and Fabric++ = the ported schedulers in the coordinator of the same image with EPR off;
# depgraph = IBM's gateway that serializes conflicting transactions through the committer's dependency manager)
EPRZ="eprz epr103 3 1 FX_EPR_PROG=1 EPR_RO=1"
HTF2="htf2 epr103 1 1 WARD=1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 REEXEC_ARGS=-parallel=-1 FX_EPR_PROG=1 EARLY_GOSSIP=1 REEXEC_ALL=1"
RXO2="rxo2 epr103 1 1 REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
FS="fs epr103 0 0 FABRICSHARP=1"
FPP="fpp epr103 0 0 REORDER=1"
FIFO="fifo base103 0 0"
DEPG="depg base103 0 0 QUEUE=depgraph"
log() { echo "$(date '+%m-%d %H:%M:%S') $*" | tee -a $out; }

# load of the hosts at the start (other users' jobs would distort the numbers; logged, not enforced)
log "PHASE44 start, deadline $(date -d @$DEADLINE '+%m-%d %H:%M')"
for h in n112 n115 n118 n124; do log "LOAD $h $($C $h "cut -d' ' -f1-3 /proc/loadavg; docker ps --format '{{.Names}}' 2>/dev/null | grep -vcE '^(orderer-party|committer-)'" | tr '\n' ' ')"; done
log "LOAD n116 $(cut -d' ' -f1-3 /proc/loadavg) $(docker ps --format '{{.Names}}' | grep -vcE '^(orderer-party|committer-)')"

# CPU frequency governor: performance on every host, as in the single-server runs (phase 40/42). The hosts'
# default governors (schedutil/powersave) kept the client's commit-notification path at a low clock and capped
# every scheme at ~2.8k tx/s (10/17: EPR at 4000 offered went from 2.6k to 4.0k tx/s with the change).
for h in n112 n115 n118 n124; do bad=$($C $h "bash bin/d16host.sh gov_perf"); [ "$bad" = 0 ] || { log "GOVERNOR not performance on $h ($bad cpus), abort"; exit 1; }; done
bad=$(bash $D/scripts/d16host.sh gov_perf); [ "$bad" = 0 ] || { log "GOVERNOR not performance on n116 ($bad cpus), abort"; exit 1; }
log "GOVERNOR performance on all hosts"

# other people's jobs: before each run, wait while any host has more than 8 cores of other users' processes
# (112's vLLM workers idle at ~3 cores); checked every minute, logged every 10 minutes, bounded by the deadline
wait_idle() { local n=0 busy c h
  while :; do
    busy=""
    for h in n112 n115 n118 n124; do c=$($C $h "bash bin/d16host.sh others_cpu"); [ "${c:-0}" -gt 800 ] && busy="$busy $h:${c}%"; done
    c=$(bash $D/scripts/d16host.sh others_cpu); [ "${c:-0}" -gt 800 ] && busy="$busy n116:${c}%"
    [ -z "$busy" ] && { [ $n -gt 0 ] && log "IDLE again after $n min" > /dev/null; return 0; }
    [ $(( $(date +%s) + 300 )) -gt $DEADLINE ] && return 0
    [ $((n % 10)) = 0 ] && log "BUSY other users' CPU:$busy, waiting" > /dev/null
    n=$((n + 1)); sleep 60
  done; }

# free space (KB) on the smallest of the hosts' experiment volumes
disk_min() { { $C n112 "df --output=avail /data | tail -1"; $C n115 "df --output=avail ~ | tail -1"; $C n118 "df --output=avail ~ | tail -1"
  df --output=avail /home/yhy | tail -1; } | sort -n | head -1; }

run() { local l=$1 img=$2 vc=$3 evm=$4 ds=$5 gen=$6; shift 6
  # a recorded exit code of any value is a finished run (124 = client timeout = collapse); infra failures were
  # moved to *.infra and are retried
  if [ -s $P/$l.txt ] && grep -qE "^== $l exit=(0|124)( |$)" $P/$l.txt; then cat $P/$l.txt; return; fi
  # run() executes in a command substitution: stop the phase through the main shell's trap
  [ $(( $(date +%s) + 300 )) -gt $DEADLINE ] && { log "DEADLINE reached before $l"; kill -TERM $$; exit 1; }
  [ "$(disk_min)" -lt 12000000 ] && { log "DISK_LOW abort before $l"; kill -TERM $$; exit 1; }
  wait_idle
  local a
  for a in 1 2; do
    env "$@" bash $R $l $img $vc $evm $ds $gen 2>&1 | grep -E "^==|EPR STATS|STACK|GOODPUT|STATE" > $P/$l.txt
    cat $P/$l.txt >> $out
    # only exit 0 (run finished) and 124 (client timed out = collapse) are results of the offered load; mn_run's
    # own codes (92/93/96/97: stack, EPR env, namespace) and client errors (e.g. 2 = test panic) are not:
    # retry once, then stop the phase rather than record a false collapse
    grep -qE "^== $l exit=(0|124)( |$)" $P/$l.txt && break
    [ $a = 2 ] && { log "INFRA_FAIL $l twice ($(grep -oE 'exit=[0-9]+ \S+' $P/$l.txt)), abort"; mv $P/$l.txt $P/$l.infra; kill -TERM $$; exit 1; }
    log "INFRA_RETRY $l ($(grep -oE 'exit=[0-9]+ \S+' $P/$l.txt))"
  done
  grep -q "STATE str_keys=[1-9]" $P/$l.txt || { log "INVALID $l (genesis state missing), abort"; kill -TERM $$; exit 1; }
  cat $P/$l.txt; }

finish() {
  for h in 112:/data/d2025yhy/mn 115:mn 118:mn; do $C n${h%%:*} "bash bin/mn_host.sh ${h#*:} ${h%%:*} down" >/dev/null 2>&1 & done
  bash $D/scripts/mn_host.sh $D/multinode/cfg 116 down >/dev/null 2>&1; wait
  bash $D/scripts/mn_netem.sh clear >/dev/null
  log PHASE44_DONE; exit 0; }
trap finish TERM INT

grid() { case $1 in
  eprz) echo "500 1000 1500 2000 3000 4000 5000 6000 8000";;
  htf2|rxo2) echo "250 500 750 1000 1500 2000 2500 3000 4000 5000 6000";;
  fs) echo "25 50 75 100 150 200 300 400 600 800";;
  fpp|fifo) echo "25 50 75 100 150 200 300 400";;
  depg) echo "25 50 75 100 150 200 300 400 600 800";; esac; }
# start points for RTT 0: the single-host full-stack peaks of phase 42 (USDC 4945/2220/126, SB 3178/2151/211);
# the other schemes and workloads: the single-host peaks of the main table (XOX USDC 1000, SB 1962; FabricSharp
# 233/450; Fabric++ <100; depgraph 44/50; USDT 6284/1751/1001/350/<100/117; DEX-16 1674/499/428/97)
declare -A START=([usdc_eprz]=4000 [usdc_htf2]=2000 [usdc_rxo2]=1000 [usdc_fs]=200 [usdc_fpp]=50 [usdc_fifo]=100 [usdc_depg]=100
                  [sb_eprz]=3000 [sb_htf2]=2000 [sb_rxo2]=2000 [sb_fs]=400 [sb_fpp]=50 [sb_fifo]=200 [sb_depg]=100
                  [usdt_eprz]=5000 [usdt_htf2]=2500 [usdt_rxo2]=2500 [usdt_fs]=300 [usdt_fpp]=50 [usdt_fifo]=100 [usdt_depg]=100
                  [dex_eprz]=1500 [dex_htf2]=500 [dex_rxo2]=500 [dex_fifo]=100)

sweep() { local wl=$1 rtt=$2 spec=$3; local ds gen
  case $wl in usdc) set -- $U;; sb) set -- $S;; dex) set -- $X;; usdt) set -- $T;; esac; ds=$1 gen=$2
  # the client's outstanding window cannot exceed the dataset: DEX has 30k swaps (as in phase 38)
  local extra=""; [ $wl = dex ] && extra="OUTSTANDING=30000"
  set -- $spec; local s=$1 img=$2 vc=$3 evm=$4; shift 4
  set -- "$@" $extra
  local -a G=($(grid $s)); local n=${#G[@]} i=0 lo=-1 hi=-1 k=0 res r
  while [ $i -lt $((n - 1)) ] && [ ${G[$i]} -lt ${START[${wl}_$s]} ]; do i=$((i + 1)); done
  while [ $k -lt 6 ]; do
    r=${G[$i]}; k=$((k + 1))
    res=$(run mn44_${wl}_${s}_rtt${rtt}_r$r $img $vc $evm $ds $gen FX_RATE=$r NETEM_MS=$rtt "$@" < /dev/null)
    if sustained "$res" rk; then
      log "SUSTAIN ${wl}_${s} rtt=$rtt r=$r"; lo=$i; [ $hi -ge 0 ] && break; i=$((i + 1)); [ $i -ge $n ] && break
    else
      log "COLLAPSE ${wl}_${s} rtt=$rtt r=$r"; hi=$i; [ $lo -ge 0 ] && break; i=$((i - 1)); [ $i -lt 0 ] && break
    fi
  done
  if [ $lo -ge 0 ] && [ $hi -eq $((lo + 1)) ]; then
    local step; case $s in fifo|fs|fpp|depg) step=25;; *) step=250;; esac
    r=$(( (G[lo] + G[hi]) / 2 / step * step ))
    if [ $r -gt ${G[$lo]} ] && [ $r -lt ${G[$hi]} ]; then
      res=$(run mn44_${wl}_${s}_rtt${rtt}_r$r $img $vc $evm $ds $gen FX_RATE=$r NETEM_MS=$rtt "$@" < /dev/null)
      sustained "$res" rk && log "SUSTAIN ${wl}_${s} rtt=$rtt r=$r (refine)" || log "COLLAPSE ${wl}_${s} rtt=$rtt r=$r (refine)"
    fi
  fi
  [ $lo -ge 0 ] && START[${wl}_$s]=${G[$lo]} || START[${wl}_$s]=${G[0]}; }

# completed points are reused (resume), so re-running the phase only fills in combinations added later.
# DEX-16/64 (10/17 afternoon) is not swept: on this network EPR and HTFabric both stop at ~1.1-1.3k tx/s, the
# throughput at which the Arma router and primary batcher saturate one core on ~6 KB requests (programs), so
# the comparison says nothing about the schemes; the single-host main table keeps DEX.
for rtt in 0 20 50 100; do
  for wl in ${P44_WORKLOADS:-usdc sb usdt}; do
    for spec in "$EPRZ" "$HTF2" "$RXO2" "$FS" "$FPP" "$FIFO" "$DEPG"; do sweep $wl $rtt "$spec"; done
  done
done
finish
