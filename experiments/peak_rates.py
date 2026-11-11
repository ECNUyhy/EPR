"""Offered rate of each scheme's peak (phase 34): for every (mode, workload, scheme), the sustained point with the
highest goodput, by the rule of the analysis (p50 <= 2 s; with retries also >= 95% committed).

usage: python3 peak_rates.py log.txt [log2.txt ...] scheme [scheme ...]  ->  lines "<mode> <workload> <scheme> <rate> <goodput>"
"""
import re
import sys
from collections import defaultdict

files = [a for a in sys.argv[1:] if a.endswith(".txt")]
schemes = set(a for a in sys.argv[1:] if not a.endswith(".txt"))
pts = defaultdict(dict)
cur = None
for f in files:
    for line in open(f, encoding="utf-8", errors="replace"):
        if line.startswith("== "):  # any run header ends the previous run (smoke_, prof_ labels too)
            cur = None
        m = re.match(r"== ([pr]k)_(\S+?)_([a-z0-9]+)_r(\d+) exit=", line)
        if m:
            cur = (m[1], m[2], m[3]) if m[3] in schemes else None
            if cur:
                rate = int(m[4])
                pts[cur][rate] = [None, None, None]
            continue
        if not cur:
            continue
        p = pts[cur][rate]
        m = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
        if m:
            p[0], p[1] = float(m[1]), float(m[2])
        m = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
        if m and int(m[1]) > 0:
            p[2] = int(m[2]) / int(m[1])
for (mode, wl, sc), rates in sorted(pts.items()):
    good = [(p[0], r) for r, p in rates.items()
            if p[0] is not None and p[1] is not None and p[1] <= 2000 and (mode == "pk" or (p[2] or 0) >= 0.95)]
    if good:
        g, r = max(good)
        print(mode, wl, sc, r, f"{g:.0f}")
