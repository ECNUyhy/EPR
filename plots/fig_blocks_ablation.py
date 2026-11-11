"""Fig. blocks: peak throughput (USDC, retries) vs. block cutting; Fig. ablation: components added one at a time.
Data from the raw logs (phase 39 for block cutting, phase 38 for the ablation, final sweeps for B=100)."""
import numpy as np

import eprstyle as S

BLOCKS = [("usdcB25", "25 / 50 ms"), ("usdc", "100 / 200 ms"), ("usdcB250", "250 / 500 ms"), ("usdcB500", "500 / 1 s")]


def blocks():
    pts39 = S.load_points(["phase39"])
    fig, ax = S.plt.subplots(figsize=(S.COL_W, 1.45))
    x = np.arange(len(BLOCKS))
    vals = {}
    for s in ["eprz", "htf2", "fifo"]:
        ys = []
        for wl, _ in BLOCKS:
            pts = S.scheme_points(s, "rk", wl) if wl == "usdc" else dict(pts39.get(("rk", wl, s), {}))
            ys.append(S.peak(pts, "rk"))
        vals[s] = ys
        ax.plot(x, ys, **S.style(s))
    for j in range(len(BLOCKS)):  # EPR / Fabric-X at every setting, just right of the gap
        r = vals["eprz"][j] / vals["fifo"][j]
        ax.text(x[j] + 0.07, np.sqrt(vals["eprz"][j] * vals["fifo"][j]) / 2.2, f"{r:.0f}$\\times$", ha="left",
                va="center", fontsize=6.5, color=S.INK, fontweight="bold")
    ax.set_yscale("log")
    ax.set_ylim(8, 20000)
    ax.set_xticks(x)
    ax.set_xticklabels([l for _, l in BLOCKS])
    ax.set_xlabel("Block cutting ($B$ transactions / timeout $t$)")
    ax.set_ylabel("Peak (tx/s)")
    ax.legend(ncol=3, loc="upper center", bbox_to_anchor=(0.5, 1.22))
    print(S.save(fig, "fig_blocks"))


# 11/06: Zipf 0.8 before 0.99 as in Fig. 8 and 11; the third step named by what it adds
ABL = [("usdc", "USDC"), ("z0.8", "Zipf 0.8"), ("z0.99", "Zipf 0.99"), ("sb1.0", "SmallBank 1.0")]
# 11/06 (stage E-d): the DEX in a panel of its own, on its own linear axis. Every variant runs guarded programs:
# epr1 = ordered repair with guarded deltas and programs, epr12 = + commutative credits, eprz = + escrow debits and
# queries at their snapshot. The snapshot-query rule fires only on SmallBank, so elsewhere EPR without it (eprzn) runs
# eprz's code path and the third bar is full EPR; on SmallBank it stops at eprzn and a lighter segment adds the queries.
ABL_DEX = [("dex1", "1 pair"), ("dex16", "16 pairs")]
VARIANTS = [("epr1", "Ordered repair", "#9ec5f4"), ("epr12", "+ commutative credits", "#5598e7"),
            ("eprz", "+ escrow debits", "#2a78d6")]
QUERIES = ("+ queries at their snapshot", S.light("#2a78d6", 0.72))
REEXEC = ("htf2", "rxo2")  # the reference line: the stronger re-execution design per workload
REF = dict(color=S.INK, linewidth=1.1, path_effects=[S.patheffects.withStroke(linewidth=2.2, foreground="white")])


def ablation_peaks(wls):
    """workload -> peak of epr1, epr12, eprz, eprzn and of the best re-execution design (with retries)."""
    pts38 = S.load_points(["phase38"])
    out = {}
    for wl, _ in wls:
        v = {k: S.peak(dict(pts38.get(("rk", wl, k), {})), "rk") for k in ("epr1", "epr12", "eprz", "eprzn")}
        v["best"] = max(S.peak(S.scheme_points(s, "rk", wl), "rk") or 0 for s in REEXEC) or None
        out[wl] = v
    return out


def ablation():
    """The third bar is full EPR on every workload (the snapshot-query rule, which
    fires only on SmallBank, is counted in it, not singled out), labelled "+ escrow debits (full EPR)"; the best
    re-execution is a thin dashed line without a halo; no ratio labels.
    The third entry reads "Full EPR", which is what the bar is on every workload
    (escrow debits, and on SmallBank also queries at their snapshot); the text says so, not split."""
    from matplotlib.lines import Line2D
    from matplotlib.patches import Patch
    S.plt.rcParams["hatch.linewidth"] = 0.5
    ref = dict(color=S.INK, linewidth=0.95, linestyle=(0, (3.2, 1.6)))
    peaks = ablation_peaks(ABL + ABL_DEX)
    for wl, v in peaks.items():
        print("  ", wl, {k: x and round(x) for k, x in v.items()})
    leg_in = 0.24
    fig, axes = S.plt.subplots(1, 2, figsize=(S.COL_W, 1.295), gridspec_kw={"width_ratios": [2.9, 1]})
    w, edge = 0.25, S.dark(S.SCHEMES["eprz"][1])
    labels = ["Ordered repair", "+ commutative credits", "Full EPR"]
    panels = [(axes[0], ABL, "(a) Transfers and SmallBank", 7000, [0, 2000, 4000, 6000]),
              (axes[1], ABL_DEX, "(b) DEX", 2300, [0, 1000, 2000])]
    for ax, wls, cap, ymax, ticks in panels:
        x = np.arange(len(wls))
        for i, (v, _, color) in enumerate(VARIANTS):
            st = dict(edgecolor=edge, hatch=S.HATCH.get(v, ""), linewidth=0.5, zorder=2)
            for j, (wl, _) in enumerate(wls):
                p = peaks[wl]
                if p[v] is not None:
                    ax.bar(x[j] + (i - 1) * w, p[v], w * 0.9, facecolor=color, **st)
        for j, (wl, _) in enumerate(wls):
            b = peaks[wl]["best"]
            ax.plot([x[j] - 1.6 * w, x[j] + 1.6 * w], [b, b], zorder=3, **ref)
        ax.set_xlim(-0.5, len(wls) - 0.5)
        ax.set_xticks(x)
        ax.set_xticklabels([l for _, l in wls], fontsize=6.5)
        ax.tick_params(axis="x", length=0, pad=2)
        ax.tick_params(axis="y", labelsize=6.5)
        ax.grid(axis="x", visible=False)
        ax.set_ylim(0, ymax)
        ax.set_yticks(ticks)
        ax.yaxis.set_major_formatter(S.plt.FuncFormatter(lambda v, _: "0" if v == 0 else f"{v / 1000:g}k"))
        ax.annotate(cap, xy=(0.5, 0), xycoords="axes fraction", xytext=(0, -11), textcoords="offset points",
                    ha="center", va="top", fontsize=7.5, color=S.INK)  # as fig_layout1104.subcap
    axes[0].set_ylabel("Peak (tx/s)", fontsize=7)
    patch = lambda c, h="": Patch(facecolor=c, edgecolor=edge, hatch=h, linewidth=0.5)  # noqa: E731
    hs = [patch(c, S.HATCH.get(v, "")) for v, _, c in VARIANTS] + [Line2D([], [], **ref)]
    ls = labels + ["Best re-execution"]
    order = [0, 2, 1, 3]  # two columns, filled top-down: the steps read left to right, then down
    t = 1 - leg_in / fig.get_figheight()
    fig.legend([hs[i] for i in order], [ls[i] for i in order], ncol=2, loc="lower center", bbox_to_anchor=(0.5, t),
               fontsize=6.5, handlelength=1.6, handleheight=0.8, handletextpad=0.4, columnspacing=1.6,
               labelspacing=0.25, borderaxespad=0, borderpad=0.1)
    fig.tight_layout(w_pad=0.5, rect=(0, 0, 1, t))
    print(S.save(fig, "fig_ablation"))


if __name__ == "__main__":
    S.setup()
    blocks()
    ablation()
