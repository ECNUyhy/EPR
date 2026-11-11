#!/usr/bin/env python3
"""LaTeX rows of the TR appendix table of the five-server experiment (phase 44): one row per workload and
scheme, one column per round trip, cell = peak throughput tx/s (median latency ms) under the paper's peak
criterion (p50 <= 2 s, >= 95% committed with retries). Repeat runs (mn44rep_*) are listed but not used unless
an override names them.
usage: geo_table.py [p44 dir] [override wl_scheme_rttN=rate ...]"""
import glob
import os
import re
import sys

d = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "..", "..", "..", "results", "raw", "p44")
over = dict(a.split("=") for a in sys.argv[2:])
SCHEMES = [("eprz", "\\sys{}"), ("htf2", "HTFabric"), ("rxo2", "XOX"), ("fs", "FabricSharp"), ("fpp", "Fabric++"),
           ("fifo", "Fabric-X"), ("depg", "Depgraph")]
WORKLOADS = [("usdc", "USDC"), ("usdt", "USDT"), ("sb", "SmallBank $\\theta{=}1$")]
RTTS = (0, 20, 50, 100)
runs = {}
for f in glob.glob(os.path.join(d, "mn44*_*.txt")):
    s = open(f).read()
    m = re.match(r"mn44(rep)?_(\w+?)_(eprz|htf2|rxo2|fs|fpp|fifo|depg)_rtt(\d+)_r(\d+)\.txt$", os.path.basename(f))
    st = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", s)
    gp = re.search(r"GOODPUT .*processed=(\d+)\S* success=(\d+)", s)
    if not (m and st and gp):
        continue
    rep, wl, sc, rtt, r = m[1], m[2], m[3], int(m[4]), int(m[5])
    ok = float(st[2]) <= 2000 and int(gp[1]) > 0 and int(gp[2]) * 100 >= int(gp[1]) * 95
    runs.setdefault((wl, sc, rtt), []).append((r, ok, float(st[1]), float(st[2]), bool(rep)))


def peak(wl, sc, rtt):
    rs = runs.get((wl, sc, rtt), [])
    key = f"{wl}_{sc}_rtt{rtt}"
    cand = [x for x in rs if x[0] == int(over[key]) and x[1]] if key in over else [x for x in rs if x[1] and not x[4]]
    return max(cand, key=lambda x: x[2]) if cand else None


def fmt(x):
    if x is None:
        return "--"
    return (f"{x[2]:,.0f}" if x[2] >= 100 else f"{x[2]:.0f}").replace(",", "{,}") + f" ({x[3]:,.0f})".replace(",", "{,}")


for wl, wname in WORKLOADS:
    if not any(runs.get((wl, sc, r)) for sc, _ in SCHEMES for r in RTTS):
        continue
    for i, (sc, sname) in enumerate(SCHEMES):
        cells = [fmt(peak(wl, sc, r)) for r in RTTS]
        if all(c == "--" for c in cells):
            continue
        print(f"{wname if i == 0 else ''} & {sname} & " + " & ".join(cells) + " \\\\")
    print("\\midrule")
