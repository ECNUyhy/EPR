"""Fig. overall: peak sustainable throughput (with retries) of every scheme on every contended workload.
Grouped bars on a log axis, Loom-style (hue + hatch + dark edge; EPR solid). 11/03: depgraph added (it was the
seventh column of the former Table II; it never aborts, so its no-retry sweeps are its peaks), and Table II
removed. A scheme that sustains no tested rate gets a short open stub labelled "<r" (r = lowest rate tried);
phase 47 extends the downward search so that every such cell gets a measured bar.
Data: the final sweeps (eprstyle.SOURCES) plus phase 47 and queue stage S2 (11/04)."""
import numpy as np
from matplotlib.patches import Patch

import eprstyle as S

WORKLOADS = [("usdc", "USDC"), ("usdt", "USDT"), ("z0.8", "Zipf\n0.8"), ("z0.99", "Zipf\n0.99"),
             ("sb0.8", "SmallBank\n0.8"), ("sb1.0", "SmallBank\n1.0"), ("sb1.2", "SmallBank\n1.2"),
             ("dex1", "DEX\n1 pair"), ("dex16", "DEX\n16 pairs"), ("dex64", "DEX\n64 pairs")]
SCHEMES = ["eprz", "htf2", "rxo2", "fs", "fpp", "fifo", "depg"]
YMIN, STUB = 1, 1.8


def points(s, wl, p47):
    mode = "pk" if s == "depg" else "rk"  # depgraph never aborts: both modes coincide
    pts = dict(S.scheme_points(s, mode, wl))
    pts.update(p47.get((mode, wl, s), {}))
    return pts, mode


def main():
    S.setup()
    S.plt.rcParams["hatch.linewidth"] = 0.5
    p47 = S.load_points(["phase47", "q1103/q1103_s2"])  # 11/04: stage S2 continues the remaining "<r" cells
    fig, ax = S.plt.subplots(figsize=(S.PAGE_W, 1.4))  # 11/05 trim: 1.5 -> 1.4
    n = len(SCHEMES)
    width = 0.86 / n
    x = np.arange(len(WORKLOADS))
    peaks = {}
    for i, s in enumerate(SCHEMES):
        xs = x - 0.43 + width * (i + 0.5)
        for j, (wl, _) in enumerate(WORKLOADS):
            pts, mode = points(s, wl, p47)
            v = S.peak(pts, mode) if pts else None
            peaks[(s, wl)] = v
            if v is not None:
                ax.bar(xs[j], v, width * 0.9, zorder=2, **S.bar_style(s))
            elif pts:  # below the lowest rate tried
                st = S.bar_style(s)
                ax.bar(xs[j], STUB, width * 0.9, bottom=YMIN, facecolor="white", edgecolor=st["edgecolor"],
                       hatch=st["hatch"] or "//", linewidth=0.5, zorder=2)
                ax.text(xs[j], STUB * 1.15, f"<{min(pts)}", rotation=90, ha="center", va="bottom", fontsize=5,
                        color=S.MUTED)
    ax.set_yscale("log")
    ax.set_xlim(-0.6, len(WORKLOADS) - 0.5)  # 11/06: no empty margins; -0.6 keeps USDC's lead label off the y axis
    ax.set_ylim(YMIN, 32000)  # 11/05: 25000 -> 32000, room between the lead labels and the legend at 1.4in
    ax.set_ylabel("Peak throughput (tx/s)")
    ax.set_xticks(x)
    ax.set_xticklabels([l for _, l in WORKLOADS])
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="x", visible=False)
    handles = [Patch(label=S.SCHEMES[s][0], **S.bar_style(s)) for s in SCHEMES]
    ax.legend(handles=handles, ncol=7, loc="upper center", bbox_to_anchor=(0.5, 1.17), handlelength=1.6,
              handleheight=0.9)
    for j, (wl, _) in enumerate(WORKLOADS):  # EPR's lead over the strongest baseline
        e = peaks[("eprz", wl)]
        base = [peaks[(s, wl)] for s in SCHEMES[1:] if peaks[(s, wl)]]
        if e and base:
            ax.text(x[j] - 0.43 + width * 0.5, e * 1.25, f"{e / max(base):.1f}$\\times$", ha="center", va="bottom",
                    fontsize=6.5, color=S.INK, fontweight="bold")
    print(S.save(fig, "fig_overall"))
    for (s, wl), v in sorted(peaks.items()):
        if v is None:
            print("no sustained rate:", s, wl)


if __name__ == "__main__":
    main()
