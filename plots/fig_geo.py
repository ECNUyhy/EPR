"""Fig. geo: peak throughput (with retries) on the five-host deployment vs. emulated WAN round-trip time,
USDC and SmallBank (theta = 1.0). Data: results/raw/phase44.txt (multi-node phase 44); same peak criterion as
every other figure (eprstyle.peak)."""
import os
import re
from collections import defaultdict

import numpy as np

import eprstyle as S

RTTS = [0, 20, 50, 100]
PANELS = [("usdc", "USDC"), ("sb", "SmallBank ($\\theta$=1.0)"), ("dex", "DEX-16")]


def load():
    """(workload, scheme, rtt) -> rate -> point, from the phase-44 log (labels mn44_<wl>_<scheme>_rtt<r>_r<rate>)."""
    pts = defaultdict(dict)
    cur = None
    for line in open(os.path.join(S.RAW, "phase44.txt"), encoding="utf-8", errors="replace"):
        if "RESUME:" in line:  # the runs before the committer moved to 118 (slow-fsync 112) are void
            pts, cur = defaultdict(dict), None
            continue
        if line.startswith("== "):
            m = re.match(r"== mn44_(\w+?)_(eprz|htf2|rxo2|fs|fpp|fifo|depg)_rtt(\d+)_r(\d+) exit=", line)
            cur = ((m[1], m[2], int(m[3])), int(m[4])) if m else None
            if cur:
                pts[cur[0]][cur[1]] = {"goodput": None, "p50": None, "commit": None}
            continue
        if not cur:
            continue
        p = pts[cur[0]][cur[1]]
        m = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
        if m:
            p["goodput"], p["p50"] = float(m[1]), float(m[2])
        m = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
        if m and int(m[1]) > 0:
            p["commit"] = int(m[2]) / int(m[1])
    # 11/05: EPR's points come from phase44_w.txt (labels mn44_<wl>_eprzw_rtt<r>_r<rate>), the baselines'
    # points from phase44.txt.
    new, cur = defaultdict(dict), None
    for line in open(os.path.join(S.RAW, "phase44_w.txt"), encoding="utf-8", errors="replace"):
        if line.startswith("== "):
            m = re.match(r"== mn44_(\w+?)_eprzw_rtt(\d+)_r(\d+) exit=", line)
            cur = ((m[1], "eprz", int(m[2])), int(m[3])) if m else None
            if cur:
                new[cur[0]][cur[1]] = {"goodput": None, "p50": None, "commit": None}
            continue
        if not cur:
            continue
        p = new[cur[0]][cur[1]]
        m = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
        if m:
            p["goodput"], p["p50"] = float(m[1]), float(m[2])
        m = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
        if m and int(m[1]) > 0:
            p["commit"] = int(m[2]) / int(m[1])
    pts.update(new)
    return pts


def geo():
    pts = load()
    fig, axes = S.plt.subplots(1, 2, figsize=(S.COL_W, 1.5), sharey=True)
    x = np.arange(len(RTTS))
    for ax, (wl, title) in zip(axes, PANELS):
        vals = {}
        for s in ["eprz", "htf2", "fifo"]:
            ys = [S.peak(pts.get((wl, s, r), {}), "rk") for r in RTTS]
            vals[s] = ys
            ax.plot(x, [np.nan if y is None else y for y in ys], **S.style(s))
        for j in range(len(RTTS)):  # EPR over Fabric-X at every RTT
            e, f = vals["eprz"][j], vals["fifo"][j]
            if e and f:
                ax.text(x[j] + 0.08, np.sqrt(e * f) / 2.0, f"{e / f:.0f}$\\times$", ha="left", va="center",
                        fontsize=6, color=S.INK, fontweight="bold")
        ax.set_yscale("log")
        ax.set_ylim(10, 20000)
        ax.set_xlim(-0.3, len(RTTS) - 0.45)
        ax.set_xticks(x)
        ax.set_xticklabels([str(r) for r in RTTS])
        ax.set_title(title, fontsize=7, pad=2)
        ax.set_xlabel("WAN round trip (ms)")
    axes[0].set_ylabel("Peak (tx/s)")
    axes[0].legend(ncol=3, loc="upper center", bbox_to_anchor=(1.03, 1.36))
    print(S.save(fig, "fig_geo"))


if __name__ == "__main__":
    S.setup()
    geo()
