"""Refinement points around the measured peaks (phase 33, step 0).

Phase 32 refined between the last rate that met the criterion and the first that did not, but a rate can meet
p50 <= 2 s while already overloaded (EPR, SmallBank 1.2: 912 tx/s at 1000, 748 at 2000 with p50 1.99 s and
p90 40 s), so its refinement went above the peak. Here the interval is [peak rate, next tested rate above it],
where the peak rate is the sustained point with the highest goodput (the same rule as the analysis), and the
quarter points not yet measured are listed, for EPR and HTFabric alike.
10/11: if the peak rate is saturated (goodput < 90% of the offered rate), the peak lies below it, so the interval
[previous tested rate, peak rate] is refined as well (EPR, SmallBank 1.2: 1072 tx/s at 2000 with p50 1.1 s,
1458 tx/s at 1500). The rule is the same for every scheme.

usage: python3 refine_points.py [--below-only] log.txt [log2.txt ...] [schemes...]  ->  lines "<mode> <workload> <scheme> <rate>"
"""
import re
import sys
from collections import defaultdict

below_only = "--below-only" in sys.argv  # a second pass after a first refinement: only the saturated case
files = [a for a in sys.argv[1:] if a.endswith(".txt")]
schemes = set([a for a in sys.argv[1:] if not a.endswith(".txt") and not a.startswith("--")] or ["epr", "htf2"])
pts = defaultdict(dict)  # (mode, workload, scheme) -> rate -> [goodput, p50, commit]
cur = None
lines = [l for f in files for l in open(f, encoding="utf-8", errors="replace")]
for line in lines:
    if line.startswith("== "):  # any run header ends the previous run (smoke_, prof_ labels too)
        cur = None
    m = re.match(r"== ([pr]k)_(\S+?)_([a-z0-9]+)_r(\d+) exit=", line)
    if m:
        cur = (m[1], m[2], m[3]) if m[3] in schemes else None
        if cur:
            rate = int(m[4])
            pts[cur].setdefault(rate, [None, None, None])
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


def ok(mode, p):
    g, p50, c = p
    if g is None or p50 is None or p50 > 2000:
        return False
    return mode == "pk" or (c is not None and c >= 0.95)


for (mode, wl, sc), rates in sorted(pts.items()):
    good = [(p[0], r) for r, p in rates.items() if ok(mode, p)]
    if not good:
        continue
    g, peak = max(good)
    out = set()
    above = sorted(r for r in rates if r > peak)
    below = sorted(r for r in rates if r < peak)
    intervals = []
    if above and not below_only:
        intervals.append((peak, above[0]))
    if below and g < 0.9 * peak:  # saturated at the peak rate: the peak lies below it
        intervals.append((below[-1], peak))
    for lo, hi in intervals:
        for k in (1, 2, 3):
            r = lo + (hi - lo) * k // 4
            if r not in rates and lo < r < hi:
                out.add(r)
    for r in sorted(out):
        print(mode, wl, sc, r)
