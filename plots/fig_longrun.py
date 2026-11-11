"""Fig. longrun: peak of the regular sweeps (60k transactions) vs. sustained goodput over 600k-transaction,
120 s runs (phase 38, latency criterion as in duration_table.py), with retries, for EPR and HTFabric."""
import os
import re
from collections import defaultdict

import numpy as np
from matplotlib.patches import Patch

import eprstyle as S

RAW = os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "results", "raw", "phase38.txt")
WLS = [("z0.8", "Zipf 0.8"), ("z0.99", "Zipf 0.99"), ("sb0.8", "SB 0.8"), ("sb1.0", "SB 1.0"), ("sb1.2", "SB 1.2")]


def long_runs():
    """(workload, scheme) -> best goodput with p50 <= 2 s over the 600k runs with retries; with eprstyle.EPR_FINAL,
    EPR's runs come from the final logs (label eprz<suffix>) and phase 38's EPR runs are ignored."""
    pts = defaultdict(dict)
    final = S.epr_final()
    files = [(RAW, {})]
    if final:
        files += [(os.path.join(S.RAW, x + ".txt"), {"eprz" + final[0]: "eprz"}) for x in final[1]]
    # EPR_FINAL_FILL=1: keep other EPR runs for the workloads these logs lack (not used)
    have = set()
    if final and os.environ.get("EPR_FINAL_FILL") == "1":
        for path, rename in files[1:]:
            if os.path.exists(path):
                have |= {m[1] for m in re.finditer(r"^== rk_(\S+?)L_eprz" + final[0] + r"_r\d+ exit=",
                                                    open(path, encoding="utf-8", errors="replace").read(), re.M)}
    for path, rename in files:
        if not os.path.exists(path):
            continue
        cur = None
        for line in open(path, encoding="utf-8", errors="replace"):
            if line.startswith("== "):
                cur = None
                m = re.match(r"== (rk)_(\S+?)L_([a-z0-9]+)_r(\d+) exit=", line)
                if m:
                    sc = rename.get(m[3], m[3])
                    fill = os.environ.get("EPR_FINAL_FILL") == "1"
                    old_epr = not rename and sc in S.EPR_KEYS and (m[2] in have if fill else True)
                    if final and (old_epr or (rename and m[3] not in rename)):
                        continue
                    cur = (m[2], sc), int(m[4])
                    pts[cur[0]][cur[1]] = [None, None]
                continue
            m = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line) if cur else None
            if m:
                pts[cur[0]][cur[1]] = [float(m[1]), float(m[2])]
    return {k: max((g for g, p in v.values() if g is not None and p <= 2000), default=None) for k, v in pts.items()}


def main():
    S.setup()
    lr = long_runs()
    fig, ax = S.plt.subplots(figsize=(S.COL_W, 1.15))
    x = np.arange(len(WLS))
    w = 0.2
    for i, s in enumerate(["eprz", "htf2"]):
        color = S.SCHEMES[s][1]
        short = [S.peak(S.scheme_points(s, "rk", wl), "rk") for wl, _ in WLS]
        longv = [lr.get((wl, s)) for wl, _ in WLS]
        ax.bar(x + (2 * i - 1.5) * w, short, w * 0.92, color=color, linewidth=0, zorder=2)
        ax.bar(x + (2 * i - 0.5) * w, longv, w * 0.92, facecolor="white", edgecolor=color, hatch="//////",
               linewidth=0.6, zorder=2)
        if s == "eprz":
            epr_long, epr_short = longv, short
        else:
            for j, (e, h) in enumerate(zip(epr_long, longv)):
                ax.text(x[j] - w, max(e, epr_short[j]) + 250, f"{e / h:.1f}$\\times$", ha="center", va="bottom", fontsize=6.5,
                        fontweight="bold", color=S.INK)
    ax.set_xticks(x)
    ax.set_xticklabels([l for _, l in WLS])
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="x", visible=False)
    ax.set_ylabel("Goodput (tx/s)")
    ax.set_ylim(0, 10500)
    hs = [Patch(facecolor=S.SCHEMES["eprz"][1], label="EPR, 60k"),
          Patch(facecolor="white", edgecolor=S.SCHEMES["eprz"][1], hatch="//////", label="EPR, 600k / 120 s"),
          Patch(facecolor=S.SCHEMES["htf2"][1], label="HTFabric, 60k"),
          Patch(facecolor="white", edgecolor=S.SCHEMES["htf2"][1], hatch="//////", label="HTFabric, 600k / 120 s")]
    ax.legend(handles=hs, ncol=2, loc="upper center", bbox_to_anchor=(0.5, 1.33), fontsize=6.5)
    print(S.save(fig, "fig_longrun"))


if __name__ == "__main__":
    main()
