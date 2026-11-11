# Stage S12: the coordinator crash/restart test of Section VIII-I with EPR's final image (eprw): USDC at 3,000 tx/s
# with retries, the coordinator killed during the run and restarted three seconds later with empty escrow state;
# completion, supply conservation and the final-state digest against an uninterrupted run (exp_restart4.sh, log
# exp_restart4.txt, copied into this stage's log).
XNAME=s12
xbody() {
  bash $D/scripts/exp_restart4.sh < /dev/null > /dev/null 2>&1
  cat $D/logs/exp_restart4.txt >> $out
  echo "S12 crash/restart done" | tee -a $out
  save
}
