"""Shared style and data access for the paper's figures (IEEE two-column, vector PDF).

Colors: four validated categorical hues (all-pairs CVD and normal-vision checks pass) for EPR and the strongest
baselines; weaker baselines are neutral grays told apart by marker and dash. Every scheme keeps its color and
marker in every figure.
"""
import os
import re
import shutil
import subprocess
from collections import defaultdict

import matplotlib

matplotlib.use("Agg")
import matplotlib.pyplot as plt  # noqa: E402
import matplotlib.transforms as mtrans  # noqa: E402
from matplotlib import patheffects  # noqa: E402
from matplotlib.transforms import Bbox  # noqa: E402

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.normpath(os.path.join(HERE, ".."))
RAW = os.path.join(ROOT, "results", "raw")
OUT = os.environ.get("EPR_FIG_OUT") or os.path.join(ROOT, "results", "figures")

# 11/06: every figure is made at the width LaTeX prints it (IEEEtran: \columnwidth = 252 pt, \textwidth = 516 pt, in
# TeX points), so \includegraphics scales it by exactly 1.0 and fonts keep their designed sizes. The plot areas of all
# figures start ALIGN_L from the left edge and end ALIGN_R before the right edge, so stacked figures line up.
COL_W = 252.0 / 72.27   # IEEE column width (in)
PAGE_W = 516.0 / 72.27  # IEEE text width (in)
ALIGN_L, ALIGN_R = 0.41, 0.09  # plot-area margins (in)

# scheme -> (label, color, marker, linestyle)
SCHEMES = {
    "eprz": ("EPR", "#2a78d6", "o", "-"),
    "htf2": ("HTFabric", "#eb6834", "s", "-"),
    "rxo2": ("XOX", "#1baf7a", "^", "-."),   # 11/06: dash-dot, apart from HTFabric in grayscale
    "fs": ("FabricSharp", "#4a3aa7", "D", "--"),
    "fpp": ("Fabric++", "#8a8981", "v", "--"),
    "fifo": ("Fabric-X", "#55544e", "x", ":"),
    "depg": ("Depgraph", "#b5b4ad", "+", ":"),
}
ORDER = ["eprz", "htf2", "rxo2", "fs", "fpp", "fifo", "depg"]

INK = "#000000"  # 11/10: all text in black
MUTED = "#76756d"
GRID = "#e4e3dc"


def setup():
    plt.rcParams.update({
        "font.family": "serif",
        "font.serif": ["Times New Roman", "Times", "DejaVu Serif"],
        "mathtext.fontset": "stix",
        "font.size": 7.5,
        "axes.titlesize": 7.5,
        "axes.labelsize": 7.5,
        "xtick.labelsize": 7,
        "ytick.labelsize": 7,
        "legend.fontsize": 7,
        "axes.edgecolor": MUTED,
        "axes.labelcolor": INK,
        "xtick.color": MUTED, "xtick.labelcolor": INK,
        "ytick.color": MUTED, "ytick.labelcolor": INK,
        "text.color": INK,
        "axes.linewidth": 0.6,
        "xtick.major.width": 0.6,
        "ytick.major.width": 0.6,
        "xtick.major.size": 2.5,
        "ytick.major.size": 2.5,
        "axes.grid": True,
        "grid.color": GRID,
        "grid.linewidth": 0.5,
        "axes.axisbelow": True,
        "axes.spines.top": False,
        "axes.spines.right": False,
        "lines.linewidth": 1.3,
        "lines.markersize": 4,
        "legend.frameon": False,
        "legend.handlelength": 2.0,
        "legend.columnspacing": 1.0,
        "pdf.fonttype": 42,
        "ps.fonttype": 42,
        "savefig.bbox": "tight",
        "savefig.pad_inches": 0.02,
    })


def style(s):
    label, color, marker, ls = SCHEMES[s]
    d = dict(label=label, color=color, marker=marker, linestyle=ls)
    if s == "eprz":
        d["zorder"] = 10  # 11/06: EPR's line and markers above every other series
    return d


def exact_box(fig, name, pad=0.02):
    """The saved region (in): the full figure width, with the plot areas from ALIGN_L to W - ALIGN_R, and the content's
    height plus `pad`, measured with the PDF renderer as savefig's bbox_inches="tight" does (Agg's text extents differ
    by up to 0.03 in). Replaces the 11/03 fill_width, whose tight crop left PDFs 1-2% narrower than the column, so
    LaTeX scaled them up. Warns if anything sticks out left or right."""
    from matplotlib.backend_bases import _get_renderer
    W = fig.get_figwidth()
    fig.subplots_adjust(left=ALIGN_L / W, right=1 - ALIGN_R / W)
    with fig.canvas._switch_canvas_and_return_print_method("pdf") as print_method:
        bb = fig.get_tightbbox(_get_renderer(fig, print_method))
    if bb.x0 < -0.002 or bb.x1 > W + 0.002:
        print(f"  WARNING {name}: content outside the figure width: left {bb.x0:.3f}, right {bb.x1 - W:.3f} in")
    return Bbox([[0, bb.y0 - pad], [W, bb.y1 + pad]])


def save(fig, name, subset=True):
    os.makedirs(OUT, exist_ok=True)
    path = os.path.join(OUT, name + ".pdf")
    box = exact_box(fig, name)
    fig.savefig(path, bbox_inches=box)
    fig.savefig(os.path.join(OUT, name + ".png"), dpi=200, bbox_inches=box)
    plt.close(fig)
    if subset:  # 11/06: Fig. 12 skips it, see fig_layout1104.mechanism
        subset_fonts(path)
    return path


def subset_fonts(path):
    """matplotlib 3.3 embeds whole TrueType fonts (~0.6 MB each); re-distill with Ghostscript to subsets."""
    gs = shutil.which("mgs") or shutil.which("gswin64c") or shutil.which("gs")
    if not gs:
        return
    tmp = path + ".tmp.pdf"
    r = subprocess.run([gs, "-q", "-dNOPAUSE", "-dBATCH", "-sDEVICE=pdfwrite", "-dSubsetFonts=true",
                        "-dEmbedAllFonts=true", "-dCompatibilityLevel=1.5", "-sOutputFile=" + tmp, path])
    if r.returncode == 0 and os.path.getsize(tmp) > 0:
        os.replace(tmp, path)


# ---- 11/04: EPR's final image, one switch for every figure and check ----
# EPR_FINAL="<suffix>:<log>,<log>,..." (logs relative to results/raw, without .txt; e.g. "w:q1104/q1104_s7a") makes
# every load_points / scheme_points call serve the points of eprz<suffix>, epr1<suffix> and epr12<suffix> from those
# logs as eprz, epr1 and epr12 (analysis/paper_numbers.py sets it).
EPR_FINAL = os.environ.get("EPR_FINAL", "")
EPR_KEYS = ("eprz", "epr1", "epr12", "eprzn")
# 11/06 (stage E-d, finer ablation): read with every EPR_FINAL image. eprz<suffix>n is EPR without the snapshot-query
# rule (served as "eprzn"; SmallBank 0.8 / 1.0 / 1.2), plus epr1<suffix> and epr12<suffix> on DEX 1 and 16.
FINAL_EXTRA = ["q1103/q1103_ed"]
_final_cache = {}


def epr_final():
    """(suffix, logs) of EPR_FINAL, or None."""
    if not EPR_FINAL:
        return None
    suffix, logs = EPR_FINAL.split(":", 1)
    return suffix, [x for x in logs.split(",") if x]


def _final_points():
    if "pts" not in _final_cache:
        suffix, logs = epr_final()
        raw = _parse_points(logs + [x for x in FINAL_EXTRA if x not in logs])
        names = {k + suffix: k for k in EPR_KEYS if k != "eprzn"}
        names["eprz" + suffix + "n"] = "eprzn"
        _final_cache["pts"] = {(m, wl, names[sc]): d for (m, wl, sc), d in raw.items() if sc in names}
    return _final_cache["pts"]


def load_points(phases):
    """(mode, workload, scheme) -> rate -> dict(goodput, p50_ms, commit) from the given phase logs (EPR from
    EPR_FINAL's logs when it is set)."""
    pts = _parse_points(phases)
    if epr_final():
        fin = _final_points()
        # EPR_FINAL_FILL=1: keep other EPR points where these logs have none (not used)
        fill = os.environ.get("EPR_FINAL_FILL") == "1"
        for k in [k for k in pts if k[2] in EPR_KEYS and (not fill or k in fin)]:
            del pts[k]
        for k, d in fin.items():
            pts[k] = dict(d)
    return pts


# ---- data: per-run points of the open-loop sweeps (same parsing as master_table.py) ----
def _parse_points(phases):
    """(mode, workload, scheme) -> rate -> dict(goodput, p50_ms, commit) from the given phase logs."""
    pts = defaultdict(dict)
    for ph in phases:
        path = os.path.join(RAW, ph + ".txt")
        if not os.path.exists(path):
            continue
        cur = None
        for line in open(path, encoding="utf-8", errors="replace"):
            if line.startswith("== "):
                cur = None
                m = re.match(r"== ([prld]k)_(\S+?)_([a-z0-9]+)_r(\d+) exit=", line)
                if m:  # lk/dk labels of earlier phases are the retry / no-retry modes (as in master_table.py)
                    mode = {"pk": "pk", "dk": "pk", "rk": "rk", "lk": "rk"}[m[1]]
                    cur = (mode, m[2], m[3]), int(m[4])
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
    return pts


# phases that hold each scheme's final implementation (as in scripts/analysis/master_table.py)
SOURCES = {
    "eprz": ["phase38", "phase40"],
    "htf2": ["phase32", "phase33", "phase28", "phase34", "phase40"],
    "rxo2": ["phase32", "phase28", "phase33", "phase34", "phase40"],
    "fs": ["phase25b", "phase25c", "phase28", "phase33", "phase40"],
    "fpp": ["phase31", "phase28", "phase33", "phase40"],
    "fifo": ["phase20", "phase20b", "phase21", "phase24", "phase28", "phase33", "phase34", "phase40"],
    "depg": ["phase23", "phase45"],
}
# 11/05: refinement points of the baseline sweeps (S14: baselines that bound a lead; S15a/b: the rest), same labels
REFINE = ["q1103/q1103_s14", "q1103/q1103_s15a", "q1103/q1103_s15b"]
for _s in ("htf2", "rxo2", "fs", "fpp", "fifo", "depg"):
    SOURCES[_s] = SOURCES[_s] + REFINE


def scheme_points(scheme, mode, workload):
    """rate -> point for one scheme (from its own source phases only)."""
    pts = load_points(SOURCES[scheme])
    return dict(sorted(pts.get((mode, workload, scheme), {}).items()))


def peak(points, mode):
    """Peak sustainable throughput: max goodput with p50 <= 2 s (and >= 95% committed with retries)."""
    best = None
    for r, p in points.items():
        if p["goodput"] is None or p["p50"] is None or p["p50"] > 2000:
            continue
        if mode == "rk" and (p["commit"] or 0) < 0.95:
            continue
        best = p["goodput"] if best is None else max(best, p["goodput"])
    return best


# ---- 11/03: bar style shared by every bar chart (Loom-style: hue + hatch + dark edge; EPR stays solid) ----
import matplotlib.colors as _mc  # noqa: E402

HATCH = {"eprz": "", "htf2": "////", "rxo2": "\\\\\\\\", "fs": "xxxx", "fpp": "....", "fifo": "----", "depg": "++++",
         "epr1": "////", "epr12": "\\\\\\\\"}


def dark(c, f=0.55):
    r, g, b = _mc.to_rgb(c)
    return (r * f, g * f, b * f)


def light(c, f=0.15):
    r, g, b = _mc.to_rgb(c)
    return (r + (1 - r) * f, g + (1 - g) * f, b + (1 - b) * f)


def bar_style(s, color=None):
    """facecolor / edgecolor / hatch for a scheme's bars; EPR solid, baselines lightened and hatched."""
    c = color or SCHEMES[s][1]
    return dict(facecolor=c if s == "eprz" else light(c), edgecolor=dark(c), hatch=HATCH.get(s, ""), linewidth=0.5)


def peak95(points):
    """Peak with the paper's criterion in both modes (Setup): p50 <= 2 s and >= 95% committed."""
    best = None
    for r, p in points.items():
        if p["goodput"] is None or p["p50"] is None or p["p50"] > 2000 or (p["commit"] or 0) < 0.95:
            continue
        best = p["goodput"] if best is None else max(best, p["goodput"])
    return best
