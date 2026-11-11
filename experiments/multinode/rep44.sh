#!/usr/bin/env bash
# Repeat measurements of phase 44 (run on 116 after phase44_mn.sh). Rule, fixed before running: a sweep whose
# collapsed point did worse than a higher rate of the same sweep (non-monotone) gets that point repeated once;
# both runs are kept (labels mn44rep_*, which the peak table ignores) and reported in the notes.
#   SmallBank EPR RTT 100: r3000 collapsed at 1,449 tx/s (p50 23 s) while r4000 reached 2,526 (p50 3.2 s).
. /home/yhy/d16/env.sh
D=/home/yhy/d16; out=$D/logs/phase44.txt
S="data/smallbank/sb_t1.0.json.gz data/smallbank/genesis_t1.0.json"
export FX_NO_READ_PRIME=1 FX_OL_SECONDS=60 FX_RETRY=50 RUN_TIMEOUT=400 OUTSTANDING=60000
for r in 3000 3500; do
  l=mn44rep_sb_eprz_rtt100_r$r
  [ -s $D/logs/p44/$l.txt ] && continue
  env FX_RATE=$r NETEM_MS=100 FX_EPR_PROG=1 EPR_RO=1 bash $D/scripts/mn_run.sh $l epr103 3 1 $S 2>&1 | grep -E "^==|EPR STATS|STACK|GOODPUT|STATE" > $D/logs/p44/$l.txt
  cat $D/logs/p44/$l.txt >> $out
  echo "$(date '+%m-%d %H:%M:%S') REPEAT $l $(grep -oE 'whole_goodput=[0-9.]+ lat_ms_p50=[0-9.]+' $D/logs/p44/$l.txt)" | tee -a $out
done
for h in 112:/data/d2025yhy/mn 115:mn 118:mn; do ssh -F $HOME/.ssh/config_d16mn -o BatchMode=yes n${h%%:*} "bash bin/mn_host.sh ${h#*:} ${h%%:*} down" >/dev/null 2>&1 & done
bash $D/scripts/mn_host.sh $D/multinode/cfg 116 down >/dev/null 2>&1; wait
bash $D/scripts/mn_netem.sh clear >/dev/null
echo "$(date '+%m-%d %H:%M:%S') REP44_DONE" | tee -a $out
