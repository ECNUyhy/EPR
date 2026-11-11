#!/usr/bin/env python3
"""Peak table of phase 44 (multi-node, goodput vs. RTT) from logs/p44/<label>.txt.
A run is sustained if p50 <= 2 s and >= 95% of the processed transactions committed (sweep_lib.sh, mode rk);
the peak of (workload, scheme, RTT) is the highest goodput (whole_goodput, as in the paper figures) among its sustained runs.
usage: peaks_p44.py [dir]   (default /home/yhy/d16/logs/p44)"""
import glob
import os
import re
import sys

d = sys.argv[1] if len(sys.argv) > 1 else "/home/yhy/d16/logs/p44"
runs = {}
for f in glob.glob(os.path.join(d, "mn44_*.txt")):
    s = open(f).read()
    m = re.match(r"mn44_(\w+?)_(eprz|htf2|rxo2|fs|fpp|fifo|depg)_rtt(\d+)_r(\d+)\.txt$", os.path.basename(f))
    st = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)(?:.*?lat_ms_p99=([0-9.]+))?", s)
    gp = re.search(r"GOODPUT .*processed=(\d+)\S* success=(\d+)", s)
    if not (m and st and gp):
        continue
    wl, sc, rtt, r = m[1], m[2], int(m[3]), int(m[4])
    proc, succ = int(gp[1]), int(gp[2])
    ok = float(st[2]) <= 2000 and proc > 0 and succ * 100 >= proc * 95
    runs.setdefault((wl, sc, rtt), []).append((r, ok, float(st[1]), float(st[2]), float(st[3] or 0)))

names = {"eprz": "EPR", "htf2": "HTFabric", "rxo2": "XOX", "fs": "FabricSharp", "fpp": "Fabric++", "fifo": "Fabric-X", "depg": "Depgraph"}
print("workload scheme   rtt  peak_tps  at_rate  p50_ms  p99_ms  runs")
for wl in ("usdc", "sb", "usdt", "dex"):
    for sc in ("eprz", "htf2", "rxo2", "fs", "fpp", "fifo", "depg"):
        for rtt in (0, 20, 50, 100):
            rs = sorted(runs.get((wl, sc, rtt), []))
            if not rs:
                continue
            good = [x for x in rs if x[1]]
            if good:
                b = max(good, key=lambda x: x[2])
                print(f"{wl:8} {names[sc]:8} {rtt:4} {b[2]:9.0f} {b[0]:8} {b[3]:7.0f} {b[4]:7.0f}  " +
                      " ".join(f"{x[0]}{'+' if x[1] else '-'}" for x in rs))
            else:
                print(f"{wl:8} {names[sc]:8} {rtt:4}  no sustained point  " + " ".join(f"{x[0]}-" for x in rs))
