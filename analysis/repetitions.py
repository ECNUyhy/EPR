"""Five runs of each peak of Fig. 7 (Sec. VIII-B): EPR and the strongest baseline at their peak rates, the sweep
run plus four repeats. Prints, per workload, the coefficient of variation of each, and EPR's lead in mean
throughput over the baseline runs that sustain the rate (median latency within 2 s, at least 95% committed),
as the paper defines the peak. usage: python repetitions.py [results/raw]"""
import glob
import os
import statistics
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
sys.path.insert(0, HERE)
import rep_variance as R  # noqa: E402

RAW = sys.argv[1] if len(sys.argv) > 1 else os.path.join(HERE, "..", "results", "raw")
D = os.path.join(RAW, "q1103")
runs = R.parse(sorted(glob.glob(os.path.join(D, "q1103_*.txt"))))
runs_old = R.parse(sorted(glob.glob(os.path.join(RAW, "*.txt"))))
rep = [l.split() for l in open(os.path.join(D, "q1103_rep.txt"), encoding="utf-8") if l.startswith("REP ") and "@" in l]


def ok(v):
    return v["goodput"] is not None and (v["p50"] or 1e9) <= 2000 and (v["commit"] or 0) >= 0.95


def cv(g):
    return 100 * statistics.pstdev(g) / statistics.mean(g) if len(g) > 1 else 0.0


cvs, leads = [], []
print("workload  EPR runs  CV     | baseline       runs sustaining CV(sust.) | lead in mean")
for _, wl, e, b in rep:
    stats = {}
    for spec in (e, b):
        s, r = spec.split("@")
        labs = [f"rk_{wl}_{s}_r{r}"] + [f"rk_{wl}_{s}rp{k}_r{r}" for k in range(1, 5)]
        vals = [(runs if l in runs else runs_old).get(l) for l in labs]
        vals = [v for v in vals if v and v["goodput"] is not None]
        good = [v["goodput"] for v in vals if ok(v)]
        stats[s] = (len(vals), cv([v["goodput"] for v in vals]), len(good), cv(good), statistics.mean(good))
    se, sb = stats[e.split("@")[0]], stats[b.split("@")[0]]
    lead = se[4] / sb[4]
    cvs.append(se[1])
    leads.append(lead)
    print(f"{wl:8s}  {se[0]:4d}  {se[1]:5.2f}% | {b:14s} {sb[0]:4d} {sb[2]:4d}  {sb[3]:5.2f}%   | {lead:.2f}x")
print(f"\nEPR's coefficient of variation: at most {max(cvs):.1f}%; lead in mean throughput: "
      f"{min(leads):.2f}-{max(leads):.2f}x")
