#!/usr/bin/env bash
# Event stream of the phase-44 night run (run on 116; read by the operator's monitor): launcher lines, every
# sustained/collapsed point, run failures, aborts and the end; STALL if nothing changed for 25 min while running.
D=/home/yhy/d16; touch $D/logs/phase44.txt $D/logs/phase44_launch.log
( while sleep 300; do
    if pgrep -f "[p]hase44_mn.sh" >/dev/null; then
      new=$(find $D/logs/phase44.txt $D/logs/p44 -newermt "-25 minutes" 2>/dev/null | head -1)
      [ -z "$new" ] && echo "STALL no progress for 25 min; last: $(tail -1 $D/logs/phase44.txt | cut -c1-150)"
    fi
  done ) &
tail -n 0 -F $D/logs/phase44.txt $D/logs/phase44_launch.log 2>/dev/null | \
  grep --line-buffered -E "launcher|load|PHASE44|SUSTAIN|COLLAPSE|INVALID|INFRA|DEADLINE|DISK_LOW|GOVERNOR|BUSY|IDLE|REPEAT|REP44|exited|exit=[1-9]|LOAD n1" | \
  grep --line-buffered -vE "^==> "
