#!/usr/bin/env bash
# Night launcher of phase 44 (run on 116, nohup). Times are Beijing time (+0800); the hosts' clocks are UTC.
# Waits for START, then until 116's 1-min load (other users' jobs; our stack is down) is below LOAD_MAX,
# re-checking every 10 min, and starts anyway at LATEST. phase44_mn.sh stops by itself at DEADLINE.
D=/home/yhy/d16; L=$D/logs/phase44_launch.log
START=$(date -d "${START:-now}" +%s)
LATEST=$(date -d "${LATEST:-now}" +%s)
DEADLINE="${DEADLINE:-$(TZ=Asia/Shanghai date -d "tomorrow 08:30" "+%Y-%m-%d %H:%M +0800")}"
LOAD_MAX=${LOAD_MAX:-24}
log() { echo "$(TZ=Asia/Shanghai date '+%m-%d %H:%M:%S') $*" >> $L; }
log "launcher armed: start $(TZ=Asia/Shanghai date -d @$START '+%H:%M'), latest $(TZ=Asia/Shanghai date -d @$LATEST '+%H:%M'), deadline $DEADLINE"
while [ $(date +%s) -lt $START ]; do sleep 20; done
while :; do
  l=$(cut -d' ' -f1 /proc/loadavg)
  if python3 -c "import sys; sys.exit(0 if $l < $LOAD_MAX else 1)"; then log "load $l < $LOAD_MAX: starting phase44"; break; fi
  [ $(date +%s) -ge $LATEST ] && { log "load $l still high, latest start reached: starting phase44"; break; }
  log "load $l >= $LOAD_MAX ($(ps -eo user,pcpu --sort=-pcpu | awk 'NR>1 && $2>50 {print $1}' | sort | uniq -c | tr -s ' ' | tr '\n' ';')), waiting"
  sleep 600
done
bash $D/scripts/phase44_mn.sh "$DEADLINE"
log "phase44 exited ($?)"
