#!/usr/bin/env bash
# Stage S8 (Fig. 12a): count the client resubmissions of every m_rk_* run, i.e. the transactions the gateway reports
# back as aborted (one 'TxQueue.Handle() ... fabric-invalid status=MVCC_CONFLICT' line per rejection). The output
# is results/raw/q1103/s8_rejections.txt; the commits come from the GOODPUT lines of q1103_s8.txt.
# usage: s8_rejections.sh [logdir]   (default: ~/d16/logs)
set -u
cd "${1:-$HOME/d16/logs}" || exit 1
echo "# S8 (q1103_s8.txt runs): client resubmissions = gateway rejections, i.e. lines 'TxQueue.Handle() ... fabric-invalid status=MVCC_CONFLICT'"
echo "# counted per run in logs/evm_<label>.log.gz by scripts/server/s8_rejections.sh; label rejections"
for f in evm_m_rk_*_r1000.log.gz; do
  label=${f#evm_}; label=${label%.log.gz}
  n=$(ionice -c3 nice -n 19 zcat "$f" | grep -a -c 'TxQueue.Handle().*fabric-invalid status=MVCC_CONFLICT')
  echo "$label $n"
done | sort
