#!/usr/bin/env bash
# E1 (11/09): differential check of the extracted content on the mainnet sample of Table III
# (the same 200 blocks, offline from the RPC cache of cov1105), with the fix_1109c tracer (deterministic memory
# iteration) through go -overlay. Four processes on cores 50-55, low priority. Per repairable transaction: a self
# check, 3 perturbations at +-3% and 2 at +-50% (by value type), and for deltas the guard bounds; 3 repeated
# extractions (determinism); the write classes under the endorser's write set (Table III recomputed).
# The coverage lines (-out) must equal cov_1109b.
. /home/yhy/d16/env.sh
E=/home/yhy/d16/fix_1109e1; B=$E/d16coverage_e1; C=/data/yhy/cov1105/cache
W=/data/yhy/e1; mkdir -p $W; cd $W || exit 1
rm -f pert_p*.jsonl cov_p*.jsonl faith_p*.jsonl part_p*.log
st() { echo "$1 $(date '+%F %T')" >> $E/e1_status.txt; }
: > $E/e1_status.txt
st start
export GOMAXPROCS=2
part() { local k=$1 s=$((25100000 + $1 * 250000)); nice -n 19 ionice -c3 taskset -c 50-55 $B -start $s -runs 5 -run-len 10 \
  -stride 50000 -cache $C -out cov_p$k.jsonl -perturb-out pert_p$k.jsonl -faithful-out faith_p$k.jsonl > part_p$k.log 2>&1; }
for k in 0 1 2 3; do part $k & done
wait
for k in 0 1 2 3; do echo "part $k: $(grep -c 'replayed ok' part_p$k.log) blocks" >> $E/e1_status.txt; done
st parts_done
cat faith_p0.jsonl faith_p1.jsonl faith_p2.jsonl faith_p3.jsonl > faith_all.jsonl
python3 $E/e1_summary.py $W /data/yhy/cov1109b/cov_1109b.jsonl > $E/E1_SUMMARY.txt 2>&1
{ echo; echo "== 3. Table III under the endorser's write set (every kept SSTORE; Headroom column only; Strict is not computed)";
  python3 /home/yhy/d16/fix_1107/table2_1107.py faith_all.jsonl;
  echo "== 3b. split"; python3 /home/yhy/d16/fix_1109/table2_split_1109.py faith_all.jsonl; } >> $E/E1_SUMMARY.txt 2>&1
st E1_DONE
