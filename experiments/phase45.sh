#!/usr/bin/env bash
# Phase 45 (10/18): depgraph on the real USDT trace, single host -- the one cell of the main table that was
# never measured. Same harness, images, block cutting and criterion as phase 23 (depgraph never aborts, so the
# retry and no-retry modes coincide; dk_ labels, p50 <= 2 s). Grid stops at the first unsustained rate.
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase45.txt; : > $out
. $D/scripts/sweep_lib.sh
for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
grep -qv performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor && { echo "GOVERNOR not performance, abort" | tee -a $out; exit 1; }
T=$D/data/usdt
[ -f $T/usdt.json.gz ] && [ -f $T/genesis.json ] || { echo "DATASET usdt missing, abort" | tee -a $out; exit 1; }
cd $D
R=$D/scripts/srv_run_evm6.sh
export FX_OL_SECONDS=120 FX_NO_READ_PRIME=1 RUN_TIMEOUT=300 MOCK_BS=100 MOCK_BT=200ms OUTSTANDING=60000
G="==|EPR STATS|Replay complete|GOODPUT|STATECHECK"
run() { local l=$1; shift
  [ "$(free_kb)" -lt "$(disk_need "$@")" ] && { echo "DISK_LOW abort before $l" | tee -a $out; exit 1; }
  env FX_PRIME_JSON=$T/genesis.json "$@" bash $R $l base 0 0 depgraph $T/usdt.json.gz 2>&1 | grep -E "$G" | tee -a $out; }
for r in 25 50 100 150 200 300 400; do
  res=$(run dk_usdt_depg_r$r FX_RATE=$r < /dev/null)
  if ! sustained "$res" pk; then echo "COLLAPSE dk_usdt_depg at r=$r" | tee -a $out; break; fi
done
echo PHASE45_DONE | tee -a $out
