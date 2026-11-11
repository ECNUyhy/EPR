"""Compute the numbers of the abstract, Section I and Section VIII from the logs in results/raw, and print each one
next to the phrase of the paper that states it and the data behind it.
  EPR_FINAL defaults to "w:q1103/q1103_s7a,q1103/q1103_s7b,q1103/q1103_s9,q1103/q1103_s7c,q1103/q1103_s15e",
  the runs of EPR's final image (labels eprzw, epr1w, epr12w).
usage:
  python analysis/paper_numbers.py          prints every number with the data behind it
  python analysis/paper_numbers.py --figs   regenerates all evaluation figures into results/figures
A value that cannot be computed (missing data) is reported. The five runs of each peak (Section VIII-B) are
computed by repetitions.py, Table III by table2.py; the commit-path costs of Fig. 11b are the constants COST of
plots/fig_layout1104.py, whose sources are listed in the README.
"""
import math, os, re, subprocess, sys
from decimal import Decimal, ROUND_HALF_UP

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.join(HERE, "..")
FIGS = os.path.join(ROOT, "plots")
SW = os.path.join(ROOT, "results")
os.environ.pop("EPR_FINAL_FILL", None)
os.environ.setdefault("EPR_FINAL", "w:q1103/q1103_s7a,q1103/q1103_s7b,q1103/q1103_s9,q1103/q1103_s7c,"
                                   "q1103/q1103_s15e")
sys.path.insert(0, FIGS)
import eprstyle as S  # noqa: E402
import fig_overall as O  # noqa: E402
import fig_longrun as LR  # noqa: E402
import fig_layout1104 as L  # noqa: E402
from fig_blocks_ablation import ABL, ABL_DEX, BLOCKS, ablation_peaks  # noqa: E402

BASE = ["htf2", "rxo2", "fs", "fpp", "fifo", "depg"]
SUF, FINAL_LOGS = S.epr_final()


def r1(x):
    return str(Decimal(str(x)).quantize(Decimal("0.1"), rounding=ROUND_HALF_UP))


def r0(x):
    return str(int(Decimal(str(x)).quantize(Decimal("1"), rounding=ROUND_HALF_UP)))


def rng(lo, hi, f=r1):
    a, b = f(lo), f(hi)
    return a if a == b else f"{a}--{b}"


def kfmt(v):  # 6,284 -> 6.3k
    return r1(v / 1000) + "k"


def thousands(v):  # 2746 -> 2{,}746
    s = f"{int(round(v)):,}"
    return s.replace(",", "{,}")


V, WHY = {}, {}


def put(key, val, why):
    V[key], WHY[key] = val, why


def run_lines(path, label):
    """The output lines of the run `label` in a log (from its '== label exit=' line to the next run's)."""
    out, cur = [], False
    for line in open(path, encoding="utf-8", errors="replace"):
        if line.startswith("== "):
            cur = line.startswith(f"== {label} exit=")
            continue
        if cur:
            out.append(line)
    return out


def peak_rate(points, best):
    return next(r for r, p in points.items() if p["goodput"] == best)


# ---- 1. headline: EPR / strongest baseline, with retries, Fig. 7's ten workloads --------------------------------
p47 = S.load_points(["phase47", "q1103/q1103_s2"])


def f7(s, wl):  # Fig. 7: peak as fig_overall draws it
    pts, m = O.points(s, wl, p47)
    return S.peak(pts, m) if pts else None


lead = {}
for wl, _ in O.WORKLOADS:
    e = f7("eprz", wl)
    best = max((f7(s, wl) or 0 for s in BASE), default=None)
    lead[wl] = (e / best) if e and best else None
if all(lead.values()):
    put("headline", rng(min(lead.values()), max(lead.values())), {k: round(v, 2) for k, v in lead.items()})
    put("headline_max", r1(max(lead.values())), "")

# ---- 2. Fig. 1: Fabric-X's aborts without retries (a); USDC peaks of Fabric-X, the reordering schedulers and
# Depgraph, EPR, and EPR's lead over the strongest of the five baselines the panel shows (b) -----------------------
f1a = {r: 1 - p["commit"] for r, p in S.scheme_points("fifo", "pk", "usdc").items() if p["commit"] is not None}
put("f1a_abort", rng(min(f1a.values()) * 100, max(f1a.values()) * 100, r0),
    {r: round(v * 100, 1) for r, v in sorted(f1a.items())})
put("f1a_max", r0(max(f1a.values()) * 100), "")
f1 = {s: S.peak(S.scheme_points(s, L.mode_of(s), "usdc"), L.mode_of(s)) for s in ("fifo", "depg", "fpp", "fs", "htf2", "eprz")}
put("f1_fifo", r0(f1["fifo"]), f"Fabric-X {f1['fifo']:.1f}")
dx = max(p["goodput"] or 0 for p in S.scheme_points("fifo", "rk", "dex1").values())
put("dex1_fx", "10", f"under 10 holds: Fabric-X commits at most {dx:.1f} swaps/s on one pair")
put("f1_reord", r0(max(f1[s] for s in ("fs", "fpp", "depg"))), {s: round(f1[s], 1) for s in ("fs", "fpp", "depg")})
put("f1_epr", kfmt(f1["eprz"]), f"EPR {f1['eprz']:.0f}")
put("f1_lead", r1(f1["eprz"] / max(f1[s] for s in f1 if s != "eprz")), f"/HTFabric {f1['htf2']:.0f}")

# ---- 3. Fig. 8: the highest peak of the schedulers, Fabric-X and Depgraph (own sweeps + FILLS); EPR's median
# latency on its sustained points below its peak rate; tail latency of EPR at its lowest sweep rate >= HTFabric's
# peak rate, vs HTFabric at its own peak ----------------------------------------------------------------------------
fills = S.load_points(L.FILLS)
sw = {}
for wl in ("usdc", "sb1.0", "dex16"):
    for s in ("fs", "fpp", "fifo", "depg"):
        m = L.mode_of(s)
        pts = dict(S.scheme_points(s, m, wl))
        pts.update(fills.get((m, wl, s), {}))
        sw[(s, wl)] = S.peak(pts, m)
wmax = max((v, k) for k, v in sw.items() if v)
put("sweep_weak", r0(wmax[0]), f"max {wmax[0]:.1f} ({wmax[1][0]} {wmax[1][1]})")

lat = []
for wl in ("usdc", "sb1.0", "dex16"):
    pts, m = O.points("eprz", wl, p47)
    best = S.peak(pts, m)
    rpk = max(r for r, p in pts.items() if p["goodput"] == best)
    lat += [p["p50"] for r, p in pts.items() if r < rpk and p["p50"] is not None and p["p50"] <= 2000
            and (p["commit"] or 0) >= 0.95]
if lat:
    put("lat50", f"{int(round(min(lat), -1))}--{int(round(max(lat), -1))}", [round(x) for x in sorted(lat)])


def stats_of(scheme_label, wl, rate, paths):
    pat = re.compile(rf"^== rk_{re.escape(wl)}_{scheme_label}_r{rate} exit=")
    for path in paths:
        if not os.path.exists(path):
            continue
        cur = False
        for line in open(path, encoding="utf-8", errors="replace"):
            if line.startswith("== "):
                cur = bool(pat.match(line))
                continue
            if cur:
                mm = re.search(r"lat_ms_p50=([0-9.]+) lat_ms_p90=([0-9.]+) lat_ms_p99=([0-9.]+)", line)
                if mm:
                    return float(mm[3])
    return None


tail = {}
for wl, _ in O.WORKLOADS:
    hp = S.scheme_points("htf2", "rk", wl)
    hpk = S.peak(hp, "rk")
    if not hpk:
        continue
    hrate = peak_rate(hp, hpk)
    h99 = stats_of("htf2", wl, hrate, [os.path.join(S.RAW, ph + ".txt") for ph in S.SOURCES["htf2"]])
    ep = S.scheme_points("eprz", "rk", wl)
    cand = sorted(r for r, p in ep.items() if r >= hrate and p["p50"] is not None and p["p50"] <= 2000
                  and (p["commit"] or 0) >= 0.95)
    if not cand:
        continue
    er = cand[0]
    e99 = stats_of("eprz" + SUF, wl, er, [os.path.join(S.RAW, x + ".txt") for x in FINAL_LOGS])
    if e99 and h99:
        tail[wl] = (er / hrate, e99, h99)
if len(tail) == len(O.WORKLOADS):
    put("tail_p99", f"{r0(min(t[1] for t in tail.values()))}--{r0(max(t[1] for t in tail.values()))}",
        {k: (round(a, 2), round(b), round(c)) for k, (a, b, c) in tail.items()})
    put("tail_h99", f"{r0(min(t[2] for t in tail.values()))}--{thousands(max(t[2] for t in tail.values()))}", "")

# ---- 4. Fig. 7, per workload (Section VIII-C) -------------------------------------------------------------------
put("usdc_peak", kfmt(f7("eprz", "usdc")), f"EPR {f7('eprz', 'usdc'):.0f}")
put("usdc_htf", r1(f7("eprz", "usdc") / f7("htf2", "usdc")), round(f7("eprz", "usdc") / f7("htf2", "usdc"), 3))
put("usdc_fx", r0(f7("eprz", "usdc") / f7("fifo", "usdc")), f"/{f7('fifo', 'usdc'):.1f}")
put("usdc_dg", r0(round(f7("eprz", "usdc")) / round(f7("depg", "usdc"))),
    f"{f7('eprz', 'usdc'):.0f} / {f7('depg', 'usdc'):.0f}, the values Fig. 1b prints (exact ratio {f7('eprz', 'usdc') / f7('depg', 'usdc'):.1f})")
put("usdt_peak", kfmt(f7("eprz", "usdt")), f"EPR {f7('eprz', 'usdt'):.0f}")
put("usdt_htf", r1(f7("eprz", "usdt") / f7("htf2", "usdt")), f"/{f7('htf2', 'usdt'):.0f}")
put("usdt_fx", r0(f7("eprz", "usdt") / f7("fifo", "usdt")), f"/{f7('fifo', 'usdt'):.0f}")
zh = [f7("eprz", w) / f7("htf2", w) for w in ("z0.8", "z0.99")]
put("zipf_htf", rng(min(zh), max(zh)), [round(x, 3) for x in zh])
# HTFabric with EPR's snapshot rule for queries (label htf2ro) vs HTFabric, SmallBank
ec = S._parse_points(["q1103/q1103_ec"])
chg = {wl: S.peak(ec.get(("rk", wl, "htf2ro"), {}), "rk") / f7("htf2", wl) - 1 for wl in ("sb0.8", "sb1.0", "sb1.2")}
put("ec_change", str(math.ceil(max(abs(v) for v in chg.values()) * 100)), {k: f"{v * 100:+.1f}%" for k, v in chg.items()})
sbl = [lead[w] for w in ("sb0.8", "sb1.0", "sb1.2")]
put("sb_lead", rng(min(sbl), max(sbl)), [round(x, 3) for x in sbl])
trail = {(s, w): f7("eprz", w) / f7(s, w) for s in ("fs", "fpp") for w in ("sb0.8", "sb1.0", "sb1.2")}
put("sched_sb", f"{int(math.floor(min(trail.values())))}--{int(math.floor(max(trail.values())))}",
    {f"{s} {w}": round(v, 1) for (s, w), v in trail.items()})
put("dex1_peak", r0(f7("eprz", "dex1")), f"EPR {f7('eprz', 'dex1'):.1f}")
rex = max(f7(s, "dex1") for s in ("htf2", "rxo2"))
put("dex1_lead", r1(f7("eprz", "dex1") / rex), f"/{rex:.1f} (best re-execution)")
sched = max(f7(s, "dex1") or 0 for s in ("fs", "fpp"))
if sched < 10:
    put("dex1_sched", "10", f"under 10 holds: FabricSharp {f7('fs', 'dex1')}, Fabric++ {f7('fpp', 'dex1')}")
put("dex1664", f"{kfmt(f7('eprz', 'dex16'))} and {kfmt(f7('eprz', 'dex64'))}",
    f"EPR {f7('eprz', 'dex16'):.0f}, {f7('eprz', 'dex64'):.0f}")
dh = [f7("eprz", w) / f7("htf2", w) for w in ("dex16", "dex64")]
put("dex_htf", rng(min(dh), max(dh)), [round(x, 2) for x in dh])

# ---- 5. Fig. 9 (fig_layout1104.skew): lead over the best baseline per skew; Fabric-X and Depgraph from theta = 0 to
# 0.8; the schedulers' highest peak (both panels) ------------------------------------------------------------------
extra = S.load_points(["phase46b", "phase46c", "q1103/q1103_s1"] + L.FILLS)
sk = {}
for t, wl in L.ZIPF + L.SB:
    for s in L.ALL:
        sk[(s, wl)] = S.peak(L.skew_points(extra, s, L.mode_of(s), wl), L.mode_of(s))
skl = {wl: sk[("eprz", wl)] / max(sk[(s, wl)] or 0 for s in L.BASE) for _, wl in L.ZIPF + L.SB}
put("skew_z0", r1(skl["z0"]), round(skl["z0"], 3))
zs = [skl[wl] for t, wl in L.ZIPF if t >= 0.6]
put("skew_z", rng(min(zs), max(zs)), {wl: round(skl[wl], 3) for t, wl in L.ZIPF if t >= 0.6})
put("skew_sb0", "matches", f"EPR {sk[('eprz', 'sb0')]:.0f} / Fabric-X {sk[('fifo', 'sb0')]:.0f} = "
                           f"{sk[('eprz', 'sb0')] / sk[('fifo', 'sb0')]:.3f}")
ss = [skl[wl] for t, wl in L.SB if t >= 0.6]
put("skew_sb", rng(min(ss), max(ss)), {wl: round(skl[wl], 3) for t, wl in L.SB if t >= 0.6})
drop = {(s, a): sk[(s, a)] / sk[(s, b)] for s in ("fifo", "depg") for a, b in (("z0", "z0.8"), ("sb0", "sb0.8"))}
put("skew_drop", f"{r0(min(drop.values()))}--{r0(max(drop.values()))}",
    {f"{s} {a}": round(v, 1) for (s, a), v in drop.items()})
smax = max((sk[(s, wl)] or 0, s, wl) for s in ("fs", "fpp") for _, wl in L.ZIPF + L.SB)
put("skew_sched", f"{int(math.ceil(smax[0] / 1000))}k", f"max {smax[0]:.0f} ({smax[1]} {smax[2]})")

# ---- 6. Fig. 10a: lead without retries (peak95, both modes) --------------------------------------------------------
def lead_mode(mode, wl):
    e = S.peak95(L.skew_points(extra, "eprz", mode, wl))
    best = [v for s in L.BASE for v in [S.peak95(L.skew_points(extra, s, L.mode_of(s, mode), wl))] if v]
    return e / max(best) if e and best else None


nr = {wl: lead_mode("pk", wl) for wl, _ in L.WLS}
if all(nr.values()):
    put("nr_range", rng(min(nr.values()), max(nr.values())), {k: round(v, 2) for k, v in nr.items()})

# ---- 7. Fig. 10b, long runs (600k): EPR from the final logs, HTFabric from phase 38 ------------------------------
LPAT = re.compile(r"== (rk|pk)_(\S+?)L_([a-z0-9]+)_r(\d+) exit=")


def long_points(paths, rename):
    out = {}
    for path in paths:
        if not os.path.exists(path):
            continue
        cur = None
        for line in open(path, encoding="utf-8", errors="replace"):
            if line.startswith("== "):
                mm = LPAT.match(line)
                cur = None
                if mm and mm[3] in rename:
                    cur = (mm[1], mm[2], rename[mm[3]]), int(mm[4])
                    out.setdefault(cur[0], {})[cur[1]] = {"goodput": None, "p50": None, "commit": None}
                continue
            if not cur:
                continue
            p = out[cur[0]][cur[1]]
            mm = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
            if mm:
                p["goodput"], p["p50"] = float(mm[1]), float(mm[2])
            mm = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
            if mm and int(mm[1]) > 0:
                p["commit"] = int(mm[2]) / int(mm[1])
    return out


lp_epr = long_points([os.path.join(S.RAW, x + ".txt") for x in FINAL_LOGS], {"eprz" + SUF: "eprz"})
lp_htf = long_points([LR.RAW], {"htf2": "htf2"})


def peak_p50(points):  # as fig_longrun: best goodput with p50 <= 2 s
    return max((p["goodput"] for p in points.values() if p["goodput"] and p["p50"] is not None and p["p50"] <= 2000),
               default=None)


ratios, retains = {}, {}
for mode in ("rk", "pk"):
    for wl, _ in LR.WLS:
        el = peak_p50(lp_epr.get((mode, wl, "eprz"), {}))
        hl = peak_p50(lp_htf.get((mode, wl, "htf2"), {}))
        es = S.peak(S.scheme_points("eprz", mode, wl), mode)
        if el and hl and mode == "rk":   # the text: offered with retries
            ratios[(mode, wl)] = el / hl
        if el and es and mode == "rk":
            retains[(mode, wl)] = el / es
if len(ratios) == len(LR.WLS):
    put("long_ratio", rng(min(ratios.values()), max(ratios.values())), {k[1]: round(v, 2) for k, v in ratios.items()})
if len(retains) == len(LR.WLS):
    put("long_retain", str(int(math.floor(min(retains.values()) * 100))), {k[1]: round(v, 3) for k, v in retains.items()})

# ---- 8. tight funding (q1103_s16b, without retries) and two organizations (q1103_s10*, 2-of-2, label ..2r..) --------
S16 = os.path.join(S.RAW, "q1103", "q1103_s16b.txt")
tf = S._parse_points(["q1103/q1103_s16b"])
te = S.peak(tf[("pk", "usdcT", "eprzw")], "pk")
th = S.peak(tf[("pk", "usdcT", "htf2")], "pk")
put("tight_htf", r1(te / th), f"EPR {te:.0f} / HTFabric {th:.0f}")
erate = peak_rate(tf[("pk", "usdcT", "eprzw")], te)
esc = [re.search(r"fast_debits=(\d+) slow_debits=(\d+)", x) for x in run_lines(S16, f"pk_usdcT_eprzw_r{erate}")]
esc = [m for m in esc if m][-1]
put("tight_fast", r0(int(esc[1]) / (int(esc[1]) + int(esc[2])) * 100),
    f"at EPR's peak rate {erate}: fast {esc[1]}, ordered {esc[2]}")
to = S._parse_points(["q1103/q1103_s10", "q1103/q1103_s10b", "q1103/q1103_s10c"])
two = {wl: S.peak(to[("rk", wl, "eprzw2r")], "rk") / S.peak(to[("rk", wl, "htf22r")], "rk") for wl in ("usdc", "sb1.0", "dex16")}
put("twoorg", rng(min(two.values()), max(two.values())), {k: round(v, 2) for k, v in two.items()})

# ---- 9. Fig. 11a: endorsements per commit at an offered 1,000 tx/s -------------------------------------------------
s8 = L.s8_points()
if s8:
    put("s8_fifo08", r0(s8[("fifo", "z0.8")]), round(s8[("fifo", "z0.8")], 2))
    put("s8_fifo12", r0(s8[("fifo", "z1.2")]), round(s8[("fifo", "z1.2")], 2))
    put("s8_fs06", r1(s8[("fs", "z0.6")]), round(s8[("fs", "z0.6")], 3))
    z = [s8[("fs", wl)] for t, wl in L.ZIPF if t >= 0.8 and ("fs", wl) in s8]
    put("s8_fs", f"{r0(min(z))}--{r0(max(z))}", [round(x, 1) for x in z])
    w = [v for (s, wl), v in s8.items() if s in ("eprz", "htf2", "rxo2", "depg")]
    put("s8_once", "once", f"max {max(w):.3f} over {len(w)} runs")

# ---- 10. Fig. 11b (constants COST) and the uniform workloads (Section VIII-E) -----------------------------------
C = L.COST
put("cost_reexec", f"{C['htf2']['transfer']:.0f}--{C['rxo2']['transfer']:.0f}", "HTFabric, XOX per USDC transfer (us)")
put("cost_swap", rng(min(C[s]["swap"] for s in ("htf2", "rxo2")) / 1000, max(C[s]["swap"] for s in ("htf2", "rxo2")) / 1000),
    "HTFabric, XOX per swap on one pair (ms)")
put("cost_delta", r0(C["delta_us"] * 1000), "EPR per delta (ns)")
put("cost_transfer", r0(C["transfer_us"] * 1000), "EPR per transfer (ns)")
put("cost_prog", r0(C["prog_us"]), "EPR per router program (us)")
fz = S.scheme_points("fifo", "pk", "z0M")
fmax = max(p["goodput"] for p in fz.values() if p["goodput"])
rmax = peak_rate(fz, fmax)
ab = {r: 1 - p["commit"] for r, p in fz.items() if 14000 < r <= rmax}
put("uni_fx_abort", rng(min(ab.values()) * 100, max(ab.values()) * 100, r0),
    f"offered 14k < r <= {rmax} (where Fabric-X commits the most): " + str({r: round(v * 100, 1) for r, v in sorted(ab.items())}))
ez = S.peak95(S.scheme_points("eprz", "pk", "z0M"))
put("uni_epr", kfmt(ez), f"EPR {ez:.0f}")
put("uni_ratio", r1(ez / fmax), f"Fabric-X commits at most {fmax:.0f}")
ux = [S.peak95(S.scheme_points(s, "pk", "z0M")) for s in ("rxo2", "htf2")]
put("uni_rex", f"{kfmt(min(ux))}--{kfmt(max(ux))}", [round(x) for x in ux])

# ---- 11. Fig. 12, ablation (with retries) --------------------------------------------------------------------
ap = ablation_peaks(ABL + ABL_DEX)
TR, SB1, DX = ("usdc", "z0.8", "z0.99"), "sb1.0", [wl for wl, _ in ABL_DEX]
afx = {wl: ap[wl]["epr1"] / S.peak(S.scheme_points("fifo", "rk", wl), "rk") for wl, _ in ABL}
put("abl_fx", f"{int(math.floor(min(afx.values())))}--{int(math.floor(max(afx.values())))}", {k: round(v, 1) for k, v in afx.items()})
put("abl_rex_tr", rng(min(ap[w]["epr1"] / ap[w]["best"] for w in TR), max(ap[w]["epr1"] / ap[w]["best"] for w in TR)),
    {w: round(ap[w]["epr1"] / ap[w]["best"], 3) for w in TR + (SB1,)})
put("abl_rex_dex", rng(min(ap[w]["epr1"] / ap[w]["best"] for w in DX), max(ap[w]["epr1"] / ap[w]["best"] for w in DX)),
    {w: round(ap[w]["epr1"] / ap[w]["best"], 3) for w in DX})
cg = {wl: ap[wl]["epr12"] / ap[wl]["epr1"] - 1 for wl, _ in ABL}
put("abl_credit", r0(max(cg.values()) * 100), {k: round(v, 3) for k, v in cg.items()})
put("abl_escrow", rng(min(ap[w]["eprz"] / ap[w]["epr12"] for w in TR), max(ap[w]["eprz"] / ap[w]["epr12"] for w in TR)),
    {w: round(ap[w]["eprz"] / ap[w]["epr12"], 3) for w in TR})
put("abl_sb", r1(ap[SB1]["eprz"] / ap[SB1]["epr12"]), round(ap[SB1]["eprz"] / ap[SB1]["epr12"], 3))

# ---- 12. Fig. 13a, block cutting (with retries; USDC at the default size as in Fig. 7) ---------------------------
blk = S.load_points(["phase39", "phase39x", "q1103/q1103_s2c"])
bc = {}
for s in ("eprz", "htf2", "rxo2", "fifo"):
    m = L.mode_of(s)
    bc[s] = []
    for wl, _ in BLOCKS:
        if wl == "usdc":
            pts = dict(S.scheme_points(s, m, wl))
            pts.update(fills.get((m, wl, s), {}))
        else:
            pts = dict(blk.get((m, wl, s), {}))
        bc[s].append(S.peak(pts, m))
e = bc["eprz"]
put("bc_fx", f"{r0(bc['fifo'][0])} down to {r0(bc['fifo'][-1])}", [round(x, 1) for x in bc["fifo"]])
put("bc_epr", f"{kfmt(min(e))[:-1]}k--{kfmt(max(e))}", [round(x) for x in e])
put("bc_htf", rng(min(a / b for a, b in zip(e, bc["htf2"])), max(a / b for a, b in zip(e, bc["htf2"]))),
    [round(a / b, 2) for a, b in zip(e, bc["htf2"])])
put("bc_xox", rng(min(a / b for a, b in zip(e, bc["rxo2"])), max(a / b for a, b in zip(e, bc["rxo2"]))),
    [round(a / b, 2) for a, b in zip(e, bc["rxo2"])])

# ---- 13. Fig. 13b-d, five servers with Arma BFT ordering and WAN round trips (fig_geo.load: phase44 baselines,
# phase44_w EPR's final image) -------------------------------------------------------------------------------
G = L.G
geo = G.load()
gp = {(wl, s, r): S.peak(geo.get((wl, s, r), {}), "rk") for wl in ("usdc", "usdt", "sb") for s in L.ALL for r in G.RTTS}


def glead(wl, r):
    return gp[(wl, "eprz", r)] / max(gp[(wl, s, r)] or 0 for s in BASE)


g050 = {(wl, r): glead(wl, r) for wl in ("usdc", "usdt", "sb") for r in G.RTTS if r <= 50}
put("geo_usdc", rng(min(v for (wl, _), v in g050.items() if wl == "usdc"), max(v for (wl, _), v in g050.items() if wl == "usdc")),
    {f"{wl} {r}": round(v, 2) for (wl, r), v in g050.items()})
put("geo_rest", rng(min(v for (wl, _), v in g050.items() if wl != "usdc"), max(v for (wl, _), v in g050.items() if wl != "usdc")), "")
put("geo_100", f"{r1(glead('usdc', 100))} and {r1(glead('usdt', 100))}",
    f"SmallBank at 100 ms: {glead('sb', 100):.2f} (matches HTFabric)")


def p99_at_peak(log, pat, wl, s, r):
    pts = geo.get((wl, s, r), {})
    best = S.peak(pts, "rk")
    if not best:
        return None
    rate = peak_rate(pts, best)
    lines, cur = [], None
    for line in open(os.path.join(S.RAW, log), encoding="utf-8", errors="replace"):
        if "RESUME:" in line:   # as fig_geo.load: the runs before the committer moved are void
            lines, cur = [], None
            continue
        if line.startswith("== "):
            cur = line.startswith(f"== {pat.format(wl=wl, r=r, rate=rate)} exit=")
            continue
        if cur:
            mm = re.search(r"lat_ms_p99=([0-9.]+)", line)
            if mm:
                lines.append(float(mm[1]))
    return lines[-1] if lines else None


late = [r for r in G.RTTS if r >= 20]
rp99 = {(s, r): p99_at_peak("phase44.txt", "mn44_{wl}_" + s + "_rtt{r}_r{rate}", "sb", s, r) for s in ("htf2", "rxo2") for r in late}
ep99 = {r: p99_at_peak("phase44_w.txt", "mn44_{wl}_eprzw_rtt{r}_r{rate}", "sb", "eprz", r) for r in late}
put("geo_p99_rex", f"{int(math.floor(min(rp99.values()) / 1000))}--{int(math.ceil(max(rp99.values()) / 1000))}",
    {f"{s} {r}": round(v / 1000, 1) for (s, r), v in rp99.items()})
put("geo_p99_epr", rng(min(ep99.values()) / 1000, max(ep99.values()) / 1000), {r: round(v / 1000, 2) for r, v in ep99.items()})
fxd = {wl: gp[(wl, "fifo", 0)] / gp[(wl, "fifo", 100)] for wl in ("usdc", "usdt", "sb")}
put("geo_fx", f"{r0(min(fxd.values()))}--{r0(max(fxd.values()))}", {k: round(v, 2) for k, v in fxd.items()})
st = [gp[(wl, s, 0)] for wl in ("usdc", "usdt", "sb") for s in ("fs", "fpp", "depg")]
en = [gp[(wl, s, 100)] or 0 for wl in ("usdc", "usdt", "sb") for s in ("fs", "fpp", "depg")]
put("geo_weak", f"{r0(min(st))}--{r0(max(st))}", f"at 0 ms; at 100 ms at most {max(en):.1f} (below 50)")

# ---- where the paper states each number: (section, phrase with {} for the number as typeset, key) --------------
PAPER = [
    ("Abstract", "up to {}% of real USDC transfers abort", "f1a_max"),
    ("Abstract", "by up to {}x on real USDC and USDT transfer traces", "headline_max"),
    ("I", "Without retries, {}% of the transfers abort", "f1a_abort"),
    ("I", "Fabric-X sustains only {} tx/s", "f1_fifo"),
    ("I", "it commits fewer than {} swaps per second", "dex1_fx"),
    ("I", "none of these designs exceeds {} tx/s", "f1_reord"),
    ("I", "EPR sustains {} tx/s", "f1_epr"),
    ("I", "{}x the state-of-the-art HTFabric", "f1_lead"),
    ("I", "leads the best baseline by {}x", "headline"),
    ("VIII-C", "{}x the best baseline, HTFabric or XOX", "headline"),
    ("VIII-C", "at no more than {} tx/s here", "sweep_weak"),
    ("VIII-C", "median latency stays at {} ms", "lat50"),
    ("VIII-C", "99th-percentile latency is {} ms", "tail_p99"),
    ("VIII-C", "HTFabric's at its own peak ({} ms)", "tail_h99"),
    ("VIII-C", "commits all 151k transfers at {} tx/s", "usdc_peak"),
    ("VIII-C", "{}x HTFabric (USDC)", "usdc_htf"),
    ("VIII-C", "{}x Fabric-X (USDC)", "usdc_fx"),
    ("VIII-C", "{}x Depgraph", "usdc_dg"),
    ("VIII-C", "it reaches {} tx/s (USDT)", "usdt_peak"),
    ("VIII-C", "{}x HTFabric (USDT)", "usdt_htf"),
    ("VIII-C", "{}x Fabric-X (USDT)", "usdt_fx"),
    ("VIII-C", "On Zipf transfers ... leads HTFabric by {}x", "zipf_htf"),
    ("VIII-C", "changes its SmallBank peaks by less than {}%", "ec_change"),
    ("VIII-C", "leads at all three skews ({}x)", "sb_lead"),
    ("VIII-C", "the classic schedulers trail by {}x", "sched_sb"),
    ("VIII-C", "commits all 30k at {} tx/s", "dex1_peak"),
    ("VIII-C", "{}x the best re-execution", "dex1_lead"),
    ("VIII-C", "the schedulers commit under {} tx/s", "dex1_sched"),
    ("VIII-C", "scales to {} tx/s", "dex1664"),
    ("VIII-C", "{}x HTFabric (16 and 64 pairs)", "dex_htf"),
    ("VIII-D", "it leads by {}x under uniform access", "skew_z0"),
    ("VIII-D", "and by {}x from theta=0.6 to 1.2", "skew_z"),
    ("VIII-D", "On SmallBank, it {} Fabric-X under uniform access", "skew_sb0"),
    ("VIII-D", "leads by {}x as soon as access is skewed", "skew_sb"),
    ("VIII-D", "Fabric-X and Depgraph ... fall {}x by theta=0.8", "skew_drop"),
    ("VIII-D", "the schedulers stay below {} tx/s", "skew_sched"),
    ("VIII-D", "Without retries, EPR leads by {}x", "nr_range"),
    ("VIII-D", "retains over {}% of its short-run peak", "long_retain"),
    ("VIII-D", "delivers {}x HTFabric's throughput", "long_ratio"),
    ("VIII-D", "EPR commits {}x HTFabric's rate without retries", "tight_htf"),
    ("VIII-D", "{}% of its debits still take the escrow fast path", "tight_fast"),
    ("VIII-D", "keeps a {}x lead over HTFabric", "twoorg"),
    ("VIII-E", "Fabric-X endorses each committed transfer {} times at theta=0.8", "s8_fifo08"),
    ("VIII-E", "and {} at theta=1.2", "s8_fifo12"),
    ("VIII-E", "FabricSharp needs {} endorsements per commit at theta=0.6", "s8_fs06"),
    ("VIII-E", "but {} from theta=0.8 on", "s8_fs"),
    ("VIII-E", "Depgraph, HTFabric, XOX and EPR endorse each transfer {}", "s8_once"),
    ("VIII-E", "costs {} us per USDC transfer", "cost_reexec"),
    ("VIII-E", "{} ms per swap on one hot pair", "cost_swap"),
    ("VIII-E", "{} ns per delta", "cost_delta"),
    ("VIII-E", "{} ns per transfer", "cost_transfer"),
    ("VIII-E", "{} us per router program", "cost_prog"),
    ("VIII-E", "Fabric-X aborts {}% of them above 14k tx/s", "uni_fx_abort"),
    ("VIII-E", "EPR commits every one at {} tx/s", "uni_epr"),
    ("VIII-E", "{}x Fabric-X (uniform transfers)", "uni_ratio"),
    ("VIII-E", "HTFabric and XOX ... peak at {} tx/s", "uni_rex"),
    ("VIII-F", "reaches {}x Fabric-X's peak on the transfers and SmallBank", "abl_fx"),
    ("VIII-F", "by {}x on the transfers", "abl_rex_tr"),
    ("VIII-F", "{}x on the DEX", "abl_rex_dex"),
    ("VIII-F", "Commutative credits add up to {}%", "abl_credit"),
    ("VIII-F", "escrow debits another {}x on the transfers", "abl_escrow"),
    ("VIII-F", "On SmallBank, full EPR adds a further {}x", "abl_sb"),
    ("VIII-G", "Fabric-X ... from {} tx/s", "bc_fx"),
    ("VIII-G", "stays at {} tx/s", "bc_epr"),
    ("VIII-G", "keeps {}x HTFabric's throughput", "bc_htf"),
    ("VIII-G", "and {}x XOX's at every block size", "bc_xox"),
    ("VIII-G", "From 0 to 50 ms, EPR commits {}x the rate of the best baseline on USDC", "geo_usdc"),
    ("VIII-G", "and {}x on USDT and on SmallBank", "geo_rest"),
    ("VIII-G", "At 100 ms ... it still leads by {}x on the transfers", "geo_100"),
    ("VIII-G", "the re-execution designs' 99th-percentile latency on SmallBank is {} s", "geo_p99_rex"),
    ("VIII-G", "EPR's {} s", "geo_p99_epr"),
    ("VIII-G", "Fabric-X falls {}x on every workload", "geo_fx"),
    ("VIII-G", "FabricSharp, Fabric++ and Depgraph start at {} and end below 50 tx/s", "geo_weak"),
]


def plain(v):
    return v.replace("{,}", ",").replace("--", "-")


def main():
    print("== numbers of the paper computed from results/raw: section, phrase (number as printed), data behind it")
    shown = set()
    for sec, phrase, key in PAPER:
        shown.add(key)
        if key not in V:
            print(f"   {sec:8} MISSING DATA for {key}")
            continue
        print(f"   {sec:8} {phrase.replace('{}', plain(V[key]))}")
        if WHY.get(key) != "":
            print(f"   {'':8}   data: {WHY.get(key)}")
    for k in V:
        if k not in shown:
            print(f"   (not in the paper) {k}: {V[k]}  {WHY.get(k, '')}")
    print()
    print("== lead of EPR over the strongest baseline per workload (Fig. 7)")
    for wl in ("usdc", "usdt", "dex1", "dex16", "dex64", "sb0.8", "sb1.0", "sb1.2", "z0.8", "z0.99"):
        print(f"   {wl:6} lead {lead.get(wl) and round(lead[wl], 2)}")


def figs():
    out = os.path.join(SW, "figures")
    env = dict(os.environ, EPR_FIG_OUT=out, PYTHONIOENCODING="utf-8")
    env.pop("EPR_FINAL_FILL", None)
    for cmd in (["fig_overall.py"], ["fig_motivation.py"], ["fig_blocks_ablation.py"],
                ["fig_layout1104.py", "sweep_lin", "skew", "mechanism", "modes", "ordering"]):
        r = subprocess.run([sys.executable] + cmd, cwd=FIGS, env=env, capture_output=True, text=True)
        print(" ".join(cmd), "rc", r.returncode, (r.stderr.strip().splitlines() or [""])[-1][:120])


if __name__ == "__main__":
    if "--figs" in sys.argv:
        figs()
    else:
        main()
