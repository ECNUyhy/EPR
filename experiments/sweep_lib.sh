# Shared helpers of the peak sweeps (sourced by phase25b/26/28).
# sustained <run output> <mode>: a point is sustained if its median latency is at most 2 s and, with client
# retries (mode rk), at least 95% of the processed transactions finally committed. Without the commit
# criterion a retrying client that gives up after FX_RETRY attempts keeps a low latency for the transactions
# that do commit while dropping more and more of them (FabricSharp, SmallBank 1.0: 70% at 16000 tx/s).
sustained() { local res=$1 mode=$2 p50 proc succ
  p50=$(echo "$res" | grep -oE "p50=[0-9.]+" | head -1 | cut -d= -f2)
  [ -n "$p50" ] && [ "${p50%.*}" -le 2000 ] || return 1
  [ "$mode" = rk ] || return 0
  proc=$(echo "$res" | grep -oE "GOODPUT .*processed=[0-9]+" | grep -oE "[0-9]+$" | head -1)
  succ=$(echo "$res" | grep -oE "GOODPUT .* success=[0-9]+" | grep -oE "[0-9]+$" | head -1)
  [ -n "$proc" ] && [ -n "$succ" ] && [ "$proc" -gt 0 ] && [ $((succ * 100)) -ge $((proc * 95)) ]; }
# disk_need <args...>: free space a run needs; a collapsing run with retries grows the committer's block
# store by about 8 GB within 120 s (phase 25, rk_usdc_fs_r2000), so retry runs need 12 GB.
# Uncontended runs at >= 16000 tx/s commit ~0.5-1M transactions and need as much.
disk_need() { local r
  case " $* " in *" FX_RETRY="*) echo 12000000; return;; esac
  r=$(echo " $* " | grep -oE " FX_RATE=[0-9]+" | tail -1 | cut -d= -f2)
  if [ -n "$r" ] && [ "$r" -ge 16000 ]; then echo 12000000; else echo 3000000; fi; }
# free_kb: free root-disk space once the previous run's committer container is gone (srv_run_evm6.sh removes
# it only when the next run starts; after a collapsing retry run its block store holds ~8 GB).
free_kb() { (cd /home/yhy/d16/src/fabric-x-evm && docker compose -f compose.d16.yml down -v -t 5 >/dev/null 2>&1); df --output=avail / | tail -1; }
