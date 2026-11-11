"""
# 11/04 trim plan: sweep_lin, skew, mechanism, modes, ordering each 0.1in lower (fonts unchanged).
Evaluation figures : single-column figures,
sub-captions centred below every sub-plot, one shared legend per figure, all seven schemes in every comparison.
  sweep     Fig. load sweep: committed throughput (a-c) and median latency (d-f) vs. offered rate, with retries
  skew      Fig. contention: peak with retries as the skew grows, Zipf transfers (a) and SmallBank (b)
  mechanism Fig. why repair wins: (a) resubmissions per commit vs. skew (stage S8), (b) commit-path cost of one
            stale transaction (EPR evaluation vs. re-execution)
  modes     Fig. retry modes and run length: (a) lead with and without retries, (b) 60k vs. 600k runs
  ordering  Fig. ordering latency: (a) block cutting (USDC), (b-d) WAN round trip (USDC, USDT, SmallBank)
usage: python fig_layout1104.py [names...]   (EPR_FIG_OUT selects the output folder; EPR_FINAL the EPR data)"""
import os
import re
import sys

import numpy as np
from matplotlib.lines import Line2D
from matplotlib.patches import Patch

import eprstyle as S
import fig_geo as G
import fig_longrun as LR
from fig_blocks_ablation import BLOCKS

ALL = ["eprz", "htf2", "rxo2", "fs", "fpp", "fifo", "depg"]
FILLS = ["phase47", "q1103/q1103_s2", "q1103/q1103_s2b", "q1103/q1103_s2c", "q1103/q1103_s9"] + S.REFINE


def mode_of(s, mode="rk"):
    return "pk" if s == "depg" else mode  # depgraph never aborts: its no-retry sweeps are its peaks


def subcap(ax, text, pad):
    """IEEE-style sub-caption centred below the sub-plot, `pad` points below its bottom edge."""
    ax.annotate(text, xy=(0.5, 0), xycoords="axes fraction", xytext=(0, -pad), textcoords="offset points",
                ha="center", va="top", fontsize=7.5, color=S.INK)


LEG_IN = 0.3  # height of the two-row legend (in)


def legend7(fig, top):
    """The seven schemes in two rows, just above the plots (top = upper edge of the tight_layout rect)."""
    h = [Line2D([], [], markersize=3.5, **S.style(s)) for s in ALL]
    l = [S.SCHEMES[s][0] for s in ALL]
    fig.legend(h, l, ncol=4, loc="lower center", bbox_to_anchor=(0.5, top), fontsize=6.5, columnspacing=0.9,
               handlelength=1.8, handletextpad=0.4, borderaxespad=0, borderpad=0.1)


def top_of(fig):
    return 1 - LEG_IN / fig.get_figheight()


# ---------------------------------------------------------------------------------------------------------------
def sweep():
    panels = [("usdc", "USDC"), ("sb1.0", "SmallBank, $\\theta=1.0$"), ("dex16", "DEX, 16 pairs")]
    fills = S.load_points(FILLS)
    fig, axes = S.plt.subplots(2, 3, figsize=(S.COL_W, 2.75), sharex="col")
    for c, (wl, title) in enumerate(panels):
        top, bot = axes[0][c], axes[1][c]
        for s in ALL:
            m = mode_of(s)
            pts = dict(S.scheme_points(s, m, wl))
            pts.update(fills.get((m, wl, s), {}))
            pts = {r: p for r, p in pts.items() if p["goodput"] is not None and p["p50"] is not None}
            if not pts:
                continue
            rs = sorted(pts)
            st = S.style(s)
            top.plot(rs, [max(pts[r]["goodput"], 1) for r in rs], markersize=2.5, linewidth=1, **st)
            bot.plot(rs, [max(pts[r]["p50"], 10) / 1000 for r in rs], markersize=2.5, linewidth=1, **st)
        for ax in (top, bot):
            ax.set_xscale("log")
            ax.set_yscale("log")
            ax.tick_params(labelsize=6)
        top.set_ylim(1, 2e4)
        bot.set_ylim(0.01, 300)
        bot.axhline(2.0, color=S.MUTED, linewidth=0.6, linestyle=(0, (3, 2)))
        top.tick_params(axis="x", labelbottom=False)
        if c == 0:
            top.set_ylabel("Committed (tx/s)", fontsize=7)
            bot.set_ylabel("Median latency (s)", fontsize=7)
        else:
            top.tick_params(axis="y", labelleft=False)
            bot.tick_params(axis="y", labelleft=False)
        bot.set_xlabel("Offered (tx/s)", fontsize=6.5, labelpad=1)
        subcap(top, f"({'abc'[c]}) {title}", 8)  # 11/05: 3 -> 8, centred in the gap between the rows
        subcap(bot, f"({'def'[c]}) {title}", 22)
    t = top_of(fig)
    legend7(fig, t)
    fig.tight_layout(h_pad=1.4, w_pad=0.3, rect=(0, 0, 1, t))
    return S.save(fig, "fig_sweep2")


def sweep_lt(linear=False):
    """Latency-throughput curves (as Porygon, ICDE'24, Fig. 8c; Pilotfish, Fig. 8): one panel per workload, x =
    committed throughput, y = median latency, each scheme's runs in order of offered rate; the curve turns upward
    where the scheme saturates. Uses the adaptive sweeps as measured (no common grid needed)."""
    panels = [("usdc", "USDC"), ("sb1.0", "SmallBank, $\\theta=1.0$"), ("dex16", "DEX, 16 pairs")]
    fills = S.load_points(FILLS)
    fig, axes = S.plt.subplots(1, 3, figsize=(S.COL_W, 1.75), sharey=True)
    ymax = 4.0
    for c, (wl, title) in enumerate(panels):
        ax = axes[c]
        for s in ALL:
            m = mode_of(s)
            pts = dict(S.scheme_points(s, m, wl))
            pts.update(fills.get((m, wl, s), {}))
            pts = {r: p for r, p in pts.items() if p["goodput"] is not None and p["p50"] is not None}
            if not pts:
                continue
            rs = sorted(pts)
            xs = [max(pts[r]["goodput"], 1) for r in rs]
            ys = [pts[r]["p50"] / 1000 for r in rs]
            if linear:
                ys = [min(y, ymax) for y in ys]  # runs beyond the axis drawn at its top
            ax.plot(xs, ys, markersize=2.5, linewidth=1, clip_on=False, **S.style(s))
        ax.set_xscale("log")
        if linear:
            ax.set_ylim(0, ymax)
        else:
            ax.set_yscale("log")
            ax.set_ylim(0.01, 100)
        ax.axhline(2.0, color=S.MUTED, linewidth=0.6, linestyle=(0, (3, 2)))
        ax.tick_params(labelsize=6)
        ax.set_xlabel("Committed (tx/s)", fontsize=6.5, labelpad=1)
        subcap(ax, f"({'abc'[c]}) {title}", 22)
    axes[0].set_ylabel("Median latency (s)", fontsize=7)
    t = top_of(fig)
    legend7(fig, t)
    fig.tight_layout(w_pad=0.3, rect=(0, 0, 1, t))
    return S.save(fig, "fig_sweep_lt_lin" if linear else "fig_sweep_lt_log")


def sweep_lt_lin():
    return sweep_lt(True)


def sweep_lin():
    """Offered-load sweep on linear axes (as NeuChain, VLDB'22, Fig. 10): committed throughput (a-c) against the
    offered rate with the y = x line a scheme follows while it keeps up, and median latency (d-f) against the offered
    rate; runs beyond 4 s are drawn at the top edge."""
    panels = [("usdc", "USDC", 8000), ("sb1.0", "SmallBank, $\\theta=1.0$", 5000), ("dex16", "DEX, 16 pairs", 2500)]
    fills = S.load_points(FILLS)
    fig, axes = S.plt.subplots(2, 3, figsize=(S.COL_W, 2.34))  # 11/05 trim: 2.5 -> 2.4; 11/06: rows closer, 2.34
    ymax = 4.0
    for c, (wl, title, xmax) in enumerate(panels):
        top, bot = axes[0][c], axes[1][c]
        top.plot([0, xmax], [0, xmax], color=S.MUTED, linewidth=0.6, linestyle=(0, (1, 1.5)), zorder=1)
        for s in ALL:
            m = mode_of(s)
            pts = dict(S.scheme_points(s, m, wl))
            pts.update(fills.get((m, wl, s), {}))
            pts = {r: p for r, p in pts.items() if p["goodput"] is not None and p["p50"] is not None}
            if not pts:
                continue
            rs = sorted(pts)
            st = S.style(s)
            lw = 1.4 if s == "eprz" else 1.0
            top.plot(rs, [pts[r]["goodput"] for r in rs], markersize=2.5, linewidth=lw, clip_on=False, **st)
            bot.plot(rs, [min(pts[r]["p50"] / 1000, ymax) for r in rs], markersize=2.5, linewidth=lw, clip_on=False,
                     **st)
        for ax in (top, bot):
            ax.set_xlim(0, xmax)
            ax.tick_params(labelsize=6)
            ax.xaxis.set_major_formatter(S.plt.FuncFormatter(lambda v, _: "0" if v == 0 else f"{v / 1000:g}k"))
            ax.xaxis.set_major_locator(S.plt.MaxNLocator(3))
        top.set_ylim(0, xmax)
        top.yaxis.set_major_formatter(S.plt.FuncFormatter(lambda v, _: "0" if v == 0 else f"{v / 1000:g}k"))
        top.yaxis.set_major_locator(S.plt.MaxNLocator(3))
        bot.set_ylim(0, ymax)
        bot.set_yticks([0, 1, 2, 3, 4])
        bot.set_yticklabels(["0", "1", "2", "3", "$\\geq$4"])  # runs beyond 4 s sit on the top edge
        bot.axhline(2.0, color=S.MUTED, linewidth=0.6, linestyle=(0, (3, 2)))
        # 11/06: both rows show the offered rate (they share it), closer to the axis
        top.tick_params(axis="x", pad=1.5)
        bot.tick_params(axis="x", pad=1.5)
        if c == 0:
            top.set_ylabel("Committed (tx/s)", fontsize=7)
            bot.set_ylabel("Median latency (s)", fontsize=7)
        else:
            bot.tick_params(axis="y", labelleft=False)
        bot.set_xlabel("Offered rate (tx/s)", fontsize=6.5, labelpad=1)  # 11/06: as Fig. 1(a)
        subcap(top, f"({'abc'[c]}) {title}", 12.5)  # 11/06: below the top row's tick labels
        subcap(bot, f"({'def'[c]}) {title}", 20)
    t = top_of(fig)
    legend7(fig, t)
    fig.tight_layout(h_pad=0.5, w_pad=0.5, rect=(0, 0, 1, t))
    return S.save(fig, "fig_sweep_lin")


# ---------------------------------------------------------------------------------------------------------------
ZIPF = [(0.0, "z0"), (0.6, "z0.6"), (0.8, "z0.8"), (0.9, "z0.9"), (0.99, "z0.99"), (1.2, "z1.2")]
SB = [(0.0, "sb0"), (0.6, "sb0.6"), (0.8, "sb0.8"), (1.0, "sb1.0"), (1.2, "sb1.2")]
NEW = {"z0", "z0.6", "z0.9", "z1.2", "sb0", "sb0.6"}  # skews measured in 46b / 46c / S1 (60k datasets)


def skew_points(extra, s, m, wl):
    pts = {} if wl in NEW else dict(S.scheme_points(s, m, wl))
    pts.update(extra.get((m, wl, s), {}))
    return pts


def skew():
    extra = S.load_points(["phase46b", "phase46c", "q1103/q1103_s1"] + FILLS)
    fig, axes = S.plt.subplots(1, 2, figsize=(S.COL_W, 1.65), sharey=True)  # 11/05 trim: 1.75 -> 1.65
    for ax, wls, title in [(axes[0], ZIPF, "(a) Zipf transfers"), (axes[1], SB, "(b) SmallBank")]:
        x = np.arange(len(wls))
        for s in ALL:
            m = mode_of(s)
            ys = [S.peak(skew_points(extra, s, m, wl), m) for _, wl in wls]
            ax.plot(x, [np.nan if y is None else y for y in ys], markersize=3, linewidth=1.4 if s == "eprz" else 1.1, **S.style(s))
        ax.set_yscale("log")
        ax.set_xticks(x)
        ax.set_xticklabels(["1.0" if t == 1.0 else f"{t:g}" for t, _ in wls], fontsize=6.5)  # 11/06: "1.0" as Fig. 8
        ax.set_xlabel(r"Skew $\theta$", fontsize=7, labelpad=1)
        subcap(ax, title, 23)  # 11/05: 19 -> 23, clear of the axis title
    axes[0].set_ylabel("Peak (tx/s)")
    t = top_of(fig)
    legend7(fig, t)
    fig.tight_layout(w_pad=0.6, rect=(0, 0, 1, t))
    return S.save(fig, "fig_skew")


# ---------------------------------------------------------------------------------------------------------------
def s8_runs():
    """Stage S8 at an offered 1,000 tx/s: ((scheme, wl) -> endorsements per committed transaction, stalled runs).

    Every transaction is endorsed once when submitted and again on every resubmission, so endorsements per commit are
    (finished transactions + resubmissions) / committed ones; resubmissions are the gateway's rejections of the run
    (s8_rejections.txt, counted in the run's log by scripts/server/s8_rejections.sh), the counts come from the run's
    GOODPUT line. A run that timed out after it stopped committing (nothing committed, or no commit after its first
    90 s) has no meaningful ratio: it is returned as stalled (Fabric++ from theta = 0.6 on)."""
    path = os.path.join(S.RAW, "q1103", "q1103_s8.txt")
    rpath = os.path.join(S.RAW, "q1103", "s8_rejections.txt")
    out, stalled, cur, rec = {}, set(), None, {}
    if not (os.path.exists(path) and os.path.exists(rpath)):
        return out, stalled
    rej = {}
    for line in open(rpath, encoding="utf-8"):
        p = line.split()
        if p and not p[0].startswith("#"):
            rej[p[0]] = int(p[1])
    final = S.epr_final()
    rename = {("eprz" + final[0]) if final else "eprzw": "eprz"}
    for line in open(path, encoding="utf-8", errors="replace"):
        if line.startswith("== "):
            m = re.match(r"== (m_rk_(\S+?)_([a-z0-9]+)_r1000) exit=(\d+)", line)
            cur = (rename.get(m[3], m[3]), m[2]) if m else None
            if cur:
                rec[cur] = {"label": m[1], "exit": int(m[4])}
            continue
        m = re.search(r"GOODPUT label=(\S+) processed=(\d+)/\d+ success=(\d+) .*?elapsed_s=([\d.]*)", line)
        if cur and m and m[1] == rec[cur]["label"]:
            rec[cur]["done"], rec[cur]["ok"] = int(m[2]), int(m[3])
            rec[cur]["last"] = float(m[4]) if m[4] else 0.0
    for k, v in rec.items():
        if v["exit"] != 0 and (not v.get("ok") or v.get("last", 0) < 90):
            stalled.add(k)
        elif v.get("ok") and v["label"] in rej:
            # 11/08: a transaction dropped after 50 retries is rejected 51 times and endorsed 51 times, so the
            # endorsements are the committed transactions plus the rejections ((done + rej) counted it as 52)
            out[k] = (v["ok"] + rej[v["label"]]) / v["ok"]
    return out, stalled


def s8_points():
    """(scheme, wl) -> endorsements per committed transaction at 1,000 tx/s (stage S8), when measured."""
    return s8_runs()[0]


# commit-path cost of one stale transaction (us): EPR's evaluation (delta: S8 micro-benchmark BenchmarkEPRDeltaEval,
# median of 5 x 2M iterations = 18.39 ns, q1103_s8.txt; router program: phase A3 micro-benchmark) vs. re-execution
# (median avg_us_per_tx of the re-execution baselines' runs, phases 28-46: USDC transfers, DEX 1 swaps, the single
# hot pair the text's 1.6--1.7 ms refers to)
# The transfer bar is a whole stale transfer, its two deltas
# (BenchmarkEPRDeltaEval run on both deltas of a transfer, median of 5 = 40.60 ns), like the re-execution bars
COST = {"delta_us": float(os.environ.get("EPR_DELTA_US", "0.01839")), "prog_us": 38.0,
        "transfer_us": float(os.environ.get("EPR_TRANSFER_US", "0.0406")),
        "htf2": {"transfer": 348.0, "swap": 1675.0}, "rxo2": {"transfer": 689.0, "swap": 1623.0}}


def mechanism():
    """Fig. 11 (11/05): (a) endorsements per commit of every scheme at an offered 1,000 tx/s; the four
    schemes at exactly 1 are drawn side by side; Fabric++ commits almost nothing at this rate from theta = 0.6 on and
    has its theta = 0 point only (caption, text), as HTFabric's paper leaves out its unmeasurable points; (b)
    commit-path cost of one stale transaction. Each panel has its own legend."""
    # 11/05 trim: 2.15 -> 2.05; 11/06: both panels of equal size
    fig, axes = S.plt.subplots(1, 2, figsize=(S.COL_W, 2.05), gridspec_kw={"width_ratios": [1, 1]})
    leg = dict(fontsize=5.8, columnspacing=0.7, handlelength=1.5, handletextpad=0.35, borderaxespad=0.1,
               borderpad=0.1, frameon=False, loc="lower center", bbox_to_anchor=(0.5, 1.01))
    grey = dict(color="#9a9a9a", markeredgecolor="#6f6f6f")
    # (a) endorsements per commit vs. skew
    ax = axes[0]
    r, stalled = s8_runs()
    x = np.arange(len(ZIPF))
    for i in x:   # one light band per skew: the side-by-side markers inside a band share that skew
        ax.axvspan(i - 0.4, i + 0.4, color="#f1f1f1", linewidth=0, zorder=0)
    ax.grid(axis="x", visible=False)
    dodge = {"eprz": -0.18, "htf2": -0.06, "rxo2": 0.06, "depg": 0.18, "fpp": 0.3}
    for s in ["fifo", "fs", "fpp", "eprz", "htf2", "rxo2", "depg"]:   # Fabric++: theta = 0 only (caption, text)
        ys = np.array([np.nan if r.get((s, wl)) is None else r[(s, wl)] for _, wl in ZIPF])
        st = dict(S.style(s))
        if s == "eprz":
            st.update(linewidth=1.6, markersize=3.4, zorder=10)
        elif s in dodge:
            st.update(linestyle="none", markersize=3.4, zorder=6, markeredgewidth=0.6)
            if s == "depg":
                st.update(grey)
        else:
            st.update(linewidth=1.1, markersize=3.0, zorder=4)
        ax.plot(x + dodge.get(s, 0.0), ys, **st)
    for s in ("fs", "fifo"):
        v = r.get((s, "z1.2"))
        if v:
            ax.text(x[-1] - 0.05, v * 1.4, f"{v:.0f}", ha="center", va="bottom", fontsize=6,
                    color=S.SCHEMES[s][1])
    ax.set_yscale("log")
    ax.set_ylim(0.7, 3000)
    ax.set_yticks([1, 10, 100, 1000])
    ax.set_yticklabels(["1", "10", "100", "1k"], fontsize=6.5)
    ax.yaxis.set_minor_locator(S.plt.NullLocator())
    ax.set_xticks(x)
    ax.set_xticklabels([f"{t:g}" for t, _ in ZIPF], fontsize=6.2)
    ax.set_xlim(-0.4, len(ZIPF) - 0.6)
    ax.set_xlabel(r"Skew $\theta$", fontsize=7, labelpad=1)
    ax.set_ylabel("Endorsements / commit", fontsize=7)
    subcap(ax, "(a) Zipf, 1,000 tx/s offered", 24)   # same pad as (b): one baseline
    order = ["eprz", "htf2", "rxo2", "depg", "fs", "fifo", "fpp"]
    h = []
    for s in order:
        st = dict(S.style(s))
        if s in dodge and s != "eprz":
            st["linestyle"] = "none"
        if s == "depg":
            st.update(grey)
        h.append(Line2D([], [], markersize=3.2, **st))
    # 11/06: compact columns and handles; the legend starts at the figure's left edge (figure coordinates), so the
    # wider panel does not push it out of the figure
    ax.legend(h, [S.SCHEMES[s][0] for s in order], ncol=4,
              **dict(leg, loc="lower left", bbox_to_anchor=(0.01, 1.01),
                     bbox_transform=S.mtrans.blended_transform_factory(fig.transFigure, ax.transAxes), fontsize=5.5,
                     columnspacing=0.4, handlelength=1.0, handletextpad=0.25))
    # (b) cost of one stale transaction on the commit path
    ax = axes[1]
    groups = [("transfer", "Transfer\n(USDC)", COST["transfer_us"]), ("swap", "Swap\n(DEX 1)", COST["prog_us"])]
    w = 0.26
    for j, (k, lab, epr) in enumerate(groups):
        for i, (s, v) in enumerate([("eprz", epr), ("htf2", COST["htf2"][k]), ("rxo2", COST["rxo2"][k])]):
            ax.bar(j + (i - 1) * w, v, w * 0.92, zorder=2, **S.bar_style(s))
        # 11/05 (1b, C23): no ratio labels; the text states what each side includes instead
    ax.set_yscale("log")
    ax.set_ylim(1e-3, 1e5)   # the delta bar (0.018 us) stays visible
    ax.set_yticks([1e-3, 1e-1, 1e1, 1e3])
    ax.set_xticks([0, 1])
    ax.set_xticklabels([g[1] for g in groups], fontsize=6.5)
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="x", visible=False)
    ax.set_ylabel(r"Commit-path cost ($\mu$s)", fontsize=7)
    subcap(ax, "(b) Commit-path cost", 24)
    ax.legend([Patch(**S.bar_style(s)) for s in ("eprz", "htf2", "rxo2")], ["EPR", "HTFabric", "XOX"], ncol=3,
              **dict(leg, handlelength=1.2))
    fig.tight_layout(w_pad=0.2, rect=(0, 0, 1, 0.9))  # 11/06: 0.8 -> 0.2, wider panels
    # 11/06: no Ghostscript pass: it wraps the hatched bars in form XObjects that inherit the dash of (a)'s dotted
    # lines, so (b)'s hatch broke into dashes; matplotlib subsets the fonts itself
    return S.save(fig, "fig_mech", subset=False)


# ---------------------------------------------------------------------------------------------------------------
WLS = [("usdc", "USDC"), ("usdt", "USDT"), ("z0.8", "Zipf 0.8"), ("z0.99", "Zipf 0.99"), ("sb0.8", "SB 0.8"),
       ("sb1.0", "SB 1.0"), ("sb1.2", "SB 1.2"), ("dex1", "DEX 1"), ("dex16", "DEX 16"), ("dex64", "DEX 64")]
BASE = ["htf2", "rxo2", "fs", "fpp", "fifo", "depg"]


# 11/06: the per-panel legends of Fig. 11
LEG11 = dict(fontsize=6, handlelength=1.2, handleheight=0.8, handletextpad=0.35, columnspacing=0.6, borderpad=0.1,
             borderaxespad=0.15, labelspacing=0.2, frameon=False)


def modes():
    extra = S.load_points(["phase46b", "phase46c", "q1103/q1103_s1"] + FILLS)

    def lead(mode, wl):
        e = S.peak95(skew_points(extra, "eprz", mode, wl))
        best = [v for s in BASE for v in [S.peak95(skew_points(extra, s, mode_of(s, mode), wl))] if v]
        return e / max(best) if e and best else None

    # 11/05 trim: 1.5 -> 1.4; 11/06: panels of equal size, each with its own legend above it (no shared legend):
    # 1.46 in keeps the plots 39 pt tall
    fig, axes = S.plt.subplots(1, 2, figsize=(S.COL_W, 1.46), gridspec_kw={"width_ratios": [1, 1]})
    ax = axes[0]
    x = np.arange(len(WLS))
    rk = [lead("rk", wl) for wl, _ in WLS]
    pk = [lead("pk", wl) for wl, _ in WLS]
    col = S.SCHEMES["eprz"][1]
    ax.bar(x - 0.2, rk, 0.38, facecolor=col, edgecolor=S.dark(col), linewidth=0.5, zorder=2)
    ax.bar(x + 0.2, pk, 0.38, facecolor="white", edgecolor=S.dark(col), hatch="////", linewidth=0.5, zorder=2)
    ax.axhline(1, color=S.MUTED, linewidth=0.6, zorder=1)
    ax.set_xticks(x)
    ax.set_xticklabels([l for _, l in WLS], fontsize=6, rotation=45, ha="right", rotation_mode="anchor")
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="x", visible=False)
    ax.set_ylim(0, 5)
    ax.set_yticks([0, 1, 2, 3, 4])
    ax.legend([Patch(facecolor=col, edgecolor=S.dark(col), linewidth=0.5),
               Patch(facecolor="white", edgecolor=S.dark(col), hatch="////", linewidth=0.5)],
              ["with retries", "without retries"], ncol=2, loc="lower center", bbox_to_anchor=(0.5, 1.0), **LEG11)
    ax.set_ylabel("EPR / best baseline", fontsize=7)
    subcap(ax, "(a) With and without retries", 27)
    ax = axes[1]
    lr = LR.long_runs()
    xs = np.arange(len(LR.WLS))
    w = 0.2
    for i, s in enumerate(["eprz", "htf2"]):
        c = S.SCHEMES[s][1]
        short = [S.peak(S.scheme_points(s, "rk", wl), "rk") for wl, _ in LR.WLS]
        longv = [lr.get((wl, s)) for wl, _ in LR.WLS]
        ax.bar(xs + (2 * i - 1.5) * w, short, w * 0.92, facecolor=c, edgecolor=S.dark(c), linewidth=0.5, zorder=2)
        ax.bar(xs + (2 * i - 0.5) * w, longv, w * 0.92, facecolor="white", edgecolor=S.dark(c), hatch="////",
               linewidth=0.5, zorder=2)
    ax.set_xticks(xs)
    ax.set_xticklabels([l for _, l in LR.WLS], fontsize=6, rotation=45, ha="right", rotation_mode="anchor")
    ax.tick_params(axis="x", length=0)
    ax.grid(axis="x", visible=False)
    ce, ch = S.SCHEMES["eprz"][1], S.SCHEMES["htf2"][1]
    ax.legend([Patch(facecolor=ce, edgecolor=S.dark(ce), linewidth=0.5),
               Patch(facecolor="white", edgecolor=S.dark(ce), hatch="////", linewidth=0.5),
               Patch(facecolor=ch, edgecolor=S.dark(ch), linewidth=0.5),
               Patch(facecolor="white", edgecolor=S.dark(ch), hatch="////", linewidth=0.5)],
              ["EPR, 60k", "EPR, 600k", "HTFabric, 60k", "HTFabric, 600k"], ncol=2, loc="lower center",
              bbox_to_anchor=(0.5, 1.0), **LEG11)
    ax.set_ylabel("Throughput (tx/s)", fontsize=7)  # 11/06: the metric's name elsewhere; ticks in k as Fig. 9
    ax.set_yticks([0, 5000, 10000])
    ax.yaxis.set_major_formatter(S.plt.FuncFormatter(lambda v, _: "0" if v == 0 else f"{v / 1000:g}k"))
    subcap(ax, "(b) 60k vs. 600k datasets", 27)  # 11/09: datasets (runs stop at 120 s, Sec. VIII-B)
    fig.tight_layout(w_pad=0.6)
    return S.save(fig, "fig_modes")


# ---------------------------------------------------------------------------------------------------------------
def ordering():
    fig, axes = S.plt.subplots(2, 2, figsize=(S.COL_W, 2.5), sharey=True)  # 11/05 trim: 2.7 -> 2.6; 11/06: 2.5
    axes = axes.ravel()
    blk = S.load_points(["phase39", "phase39x", "q1103/q1103_s2c"])
    ax = axes[0]
    x = np.arange(len(BLOCKS))
    for s in ALL:
        m = mode_of(s)
        ys = []
        for wl, _ in BLOCKS:
            if wl == "usdc":
                pts = dict(S.scheme_points(s, m, wl))
                pts.update(S.load_points(FILLS).get((m, wl, s), {}))
            else:
                pts = dict(blk.get((m, wl, s), {}))
            ys.append(S.peak(pts, m) if pts else None)
        if any(y is not None for y in ys):
            ax.plot(x, [np.nan if y is None else y for y in ys], markersize=3, linewidth=1.4 if s == "eprz" else 1.1, **S.style(s))
    ax.set_xticks(x)
    ax.set_xticklabels(["25", "100", "250", "500"], fontsize=6.5)
    ax.set_xlabel("Block size $B$ (tx)", fontsize=7, labelpad=1)  # 11/06: (a)'s axis differs from (b)-(d)
    subcap(ax, "(a) Block cutting, USDC", 22)
    pts = G.load()
    for k, (ax, (wl, title)) in enumerate(zip(axes[1:], [("usdc", "USDC"), ("usdt", "USDT"),
                                                         ("sb", "SmallBank")])):
        x = np.arange(len(G.RTTS))
        for s in ALL:
            ys = [S.peak(pts.get((wl, s, r), {}), "rk") for r in G.RTTS]
            if any(y is not None for y in ys):
                ax.plot(x, [np.nan if y is None else y for y in ys], markersize=3, linewidth=1.4 if s == "eprz" else 1.1, **S.style(s))
        ax.set_xticks(x)
        ax.set_xticklabels([str(r) for r in G.RTTS], fontsize=6.5)
        ax.set_xlabel("Round trip (ms)", fontsize=7, labelpad=1)
        subcap(ax, f"({'bcd'[k]}) WAN, {title}", 22)
    for ax in axes:
        ax.set_yscale("log")
        ax.set_ylim(5, 20000)
        ax.set_xlim(-0.35, 3.35)
    axes[0].set_ylabel("Peak (tx/s)", fontsize=7)
    axes[2].set_ylabel("Peak (tx/s)", fontsize=7)
    t = top_of(fig)
    legend7(fig, t)
    fig.tight_layout(h_pad=0.8, w_pad=0.6, rect=(0, 0, 1, t))  # 11/06: rows closer (1.8 -> 0.8)
    return S.save(fig, "fig_order")


if __name__ == "__main__":
    S.setup()
    S.plt.rcParams["hatch.linewidth"] = 0.5
    names = sys.argv[1:] or ["sweep", "skew", "mechanism", "modes", "ordering"]
    for n in names:
        print(globals()[n]())
