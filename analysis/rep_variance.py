"""REP (11/05): run-to-run variance of the peaks of Fig. 7, for the Setup sentence.

For every workload, the EPR peak rate and the strongest baseline's rate are those of stage REP (logged as
"REP <wl> eprzw@<rate> <scheme>@<rate>"). Each side has up to five runs at its rate: the sweep run
(rk_<wl>_<scheme>_r<rate>, any stage log) and the repeats rk_<wl>_<scheme>rp<k>_r<rate>, k = 1..4. Goodput and
the criterion (p50 <= 2 s, >= 95% committed) are parsed as in eprstyle.py. A label that occurs in several logs is
counted once (the last occurrence).

usage: python rep_variance.py [q1103 log dir]   (default results/raw/q1103)
"""
import glob
import os
import re
import statistics
import sys

D = sys.argv[1] if len(sys.argv) > 1 else os.path.join(os.path.dirname(__file__), "..", "results", "raw", "q1103")


def parse(paths):
    runs, cur = {}, None
    for path in paths:
        for line in open(path, encoding="utf-8", errors="replace"):
            if line.startswith("== "):
                m = re.match(r"== (rk_\S+) exit=", line)
                cur = m[1] if m else None
                if cur:
                    runs[cur] = {"goodput": None, "p50": None, "commit": None}
                continue
            if not cur:
                continue
            m = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
            if m:
                runs[cur]["goodput"], runs[cur]["p50"] = float(m[1]), float(m[2])
            m = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
            if m and int(m[1]) > 0:
                runs[cur]["commit"] = int(m[2]) / int(m[1])
    return runs


def ok(p):
    return p["goodput"] is not None and p["p50"] is not None and p["p50"] <= 2000 and (p["commit"] or 0) >= 0.95


def main():
    logs = sorted(glob.glob(os.path.join(D, "q1103_*.txt")))
    runs = parse(logs)
    rep = [l.split() for l in open(os.path.join(D, "q1103_rep.txt"), encoding="utf-8") if l.startswith("REP ") and "@" in l]
    worst_cv, worst_lead = 0.0, None
    print(f"{'wl':6} {'side':10} {'n':>2} {'mean':>8} {'cv%':>5} {'min':>8} {'max':>8} fails")
    for _, wl, e, b in rep:
        side = {}
        for spec in (e, b):
            s, r = spec.split("@")
            labs = [f"rk_{wl}_{s}_r{r}"] + [f"rk_{wl}_{s}rp{k}_r{r}" for k in range(1, 5)]
            pts = [runs[l] for l in labs if l in runs and runs[l]["goodput"] is not None]
            g = [p["goodput"] for p in pts]
            fails = sum(not ok(p) for p in pts)
            side[s] = g
            if len(g) >= 2:
                cv = 100 * statistics.pstdev(g) / statistics.mean(g)
                worst_cv = max(worst_cv, cv)
                print(f"{wl:6} {s+'@'+r:10} {len(g):2} {statistics.mean(g):8.0f} {cv:5.1f} {min(g):8.0f} {max(g):8.0f} {fails}")
            else:
                print(f"{wl:6} {s+'@'+r:10} {len(g):2} (too few runs)")
        es, bs = side[e.split("@")[0]], side[b.split("@")[0]]
        if es and bs:
            lead = min(es) / max(bs)
            worst_lead = lead if worst_lead is None else min(worst_lead, lead)
            print(f"{wl:6} lead: worst-case min(EPR)/max(base) = {lead:.2f}x, mean/mean = {statistics.mean(es)/statistics.mean(bs):.2f}x")
    print(f"\nmax CV = {worst_cv:.1f}%   min worst-case lead = {worst_lead:.2f}x")


if __name__ == "__main__":
    main()
