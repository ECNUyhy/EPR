"""Next rates of a unified sweep (11/05; the rule of S14, S10c and S15, see usweep_lib.sh).

usage: usweep_next.py [--best] <label prefix, e.g. rk_sb1.0_htf2ro> <mode rk|pk> <log>...
With --best it prints the peak as "<goodput>@<rate>" (or "none"). Otherwise it prints "up r1 r2 ..." (run ascending until the first failing run), "down r1 r2 ..." (run descending until the
first sustained run whose goodput is at least 95% of its rate), or nothing (the sweep is complete).
  - criterion: median latency at most 2 s and, with retries (rk), at least 95% of the processed transactions
    committed (as sustained() in sweep_lib.sh);
  - below a saturated peak (goodput < 90% of its rate) with a coarse gap (> 7%) to the next tested rate below:
    the quarter points of that gap, descending;
  - above the peak: the quarter points of [peak rate, next tested rate above it] if that gap exceeds 7%;
  - a sweep that sustained its highest tested rate: one 10% step further (repeated by the caller until a run fails).
"""
import re
import sys

best_only = sys.argv[1] == "--best"
if best_only:
    del sys.argv[1]
lab, mode, logs = sys.argv[1], sys.argv[2], sys.argv[3:]
pat = re.compile(r"== " + re.escape(lab) + r"_r(\d+) exit=")
pts = {}
for f in logs:
    try:
        lines = open(f, errors="replace")
    except OSError:
        continue
    cur = None
    for line in lines:
        if line.startswith("== "):
            m = pat.match(line)
            cur = int(m[1]) if m else None
            if cur is not None:
                pts[cur] = [None, None, None]
            continue
        if cur is None:
            continue
        g = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
        if g:
            pts[cur][0], pts[cur][1] = float(g[1]), float(g[2])
        c = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
        if c and int(c[1]) > 0:
            pts[cur][2] = int(c[2]) / int(c[1])


def ok(p):  # as sustained(): the integer part of the median latency at most 2000 ms
    return p[0] is not None and p[1] is not None and int(p[1]) <= 2000 and (mode == "pk" or (p[2] or 0) >= 0.95)


sus = {r: p[0] for r, p in pts.items() if ok(p)}
if best_only:
    print(f"{max(sus.values()):.0f}@{max(sus, key=sus.get)}" if sus else "none")
    sys.exit(0)
if not sus:
    sys.exit(0)
best = max(sus, key=sus.get)
above = sorted(r for r in pts if r > best)
below = sorted(r for r in pts if r < best)
if sus[best] < 0.9 * best and below and best / below[-1] > 1.07:
    down = [r for r in (below[-1] + (best - below[-1]) * k // 4 for k in (3, 2, 1)) if r not in pts]
    if down:
        print("down", *down)
        sys.exit(0)
if sus[best] < 0.9 * best and not below:  # 11/05: saturated at the lowest tested rate, nothing below to refine against
    g = int(sus[best])
    down = [r for r in ((best + g) // 2, g) if r not in pts]
    if down:
        print("down", *down)
        sys.exit(0)
if not above:
    print("up", int(best * 1.1))
elif above[0] / best > 1.07:
    up = [r for r in (best + (above[0] - best) * k // 4 for k in (1, 2, 3)) if r not in pts and r > best]
    if up:
        print("up", *up)
