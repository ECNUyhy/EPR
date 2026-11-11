"""Table III rows (same definitions as table2.py) split into guarded deltas vs programs, plus the
USDC and USDT hot direct writes and the hot-slot transactions. Read-only.
usage: python analysis/table2_split.py results/coverage/cov_mainnet.jsonl.gz"""
import gzip
import json
import sys
from collections import Counter

HOT_GAP = 8
txs = [json.loads(l) for l in (gzip.open(sys.argv[1], "rt", encoding="utf-8") if sys.argv[1].endswith(".gz") else open(sys.argv[1], encoding="utf-8")) if l.strip()]
txs = [t for t in txs if t.get("status") == "ok"]
txs.sort(key=lambda t: (t["block"], t["i"]))
last, hot = {}, set()
for t in txs:
    pos = (t["block"], t["i"])
    for w in t.get("writes") or []:
        k = w["k"]
        if k in last and t["block"] - last[k][0] <= HOT_GAP and last[k] != pos:
            hot.add((k, last[k]))
            hot.add((k, pos))
        last[k] = pos

USDC = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
USDT = "0xdac17f958d2ee523a2206206994597c13d831ec7"
names = ["Direct writes, all", "Direct writes, hot", "Indirect writes, all", "Indirect writes, hot",
         "USDC direct hot", "USDT direct hot"]
rows = {r: Counter() for r in names}
htx = Counter()


def cls(v):
    return v if v in ("delta", "program") else "plain"


for t in txs:
    pos = (t["block"], t["i"])
    to = (t.get("to") or "").lower()
    ws = t.get("writes") or []
    touches = False
    all_c = all_s = bool(ws)
    kinds_c, kinds_s = set(), set()
    for w in ws:
        k = w["k"]
        owner = k.split(":")[1].lower()
        direct = owner == to
        is_hot = (k, pos) in hot
        touches |= is_hot
        s, c = cls(w.get("s")), cls(w.get("c"))
        all_s &= s != "plain"
        all_c &= c != "plain"
        kinds_s.add(s)
        kinds_c.add(c)
        base = "Direct" if direct else "Indirect"
        keys = [base + " writes, all"] + ([base + " writes, hot"] if is_hot else [])
        if direct and is_hot and owner == USDC:
            keys.append("USDC direct hot")
        if direct and is_hot and owner == USDT:
            keys.append("USDT direct hot")
        for r in keys:
            x = rows[r]
            x["n"] += 1
            x["s_" + s] += 1
            x["c_" + c] += 1
    if touches:
        htx["n"] += 1
        if all_s:
            htx["s_all"] += 1
            htx["s_delta_only"] += kinds_s == {"delta"}
        if all_c:
            htx["c_all"] += 1
            htx["c_delta_only"] += kinds_c == {"delta"}

print("txs ok", len(txs))
hdr = "scope".ljust(22) + "      n | strict: delta   prog  total | headroom: delta   prog  total | delta share of headroom-repairable"
print(hdr)
for r in names:
    x = rows[r]
    n = max(1, x["n"])
    sd, sp = x["s_delta"] / n, x["s_program"] / n
    cd, cp = x["c_delta"] / n, x["c_program"] / n
    share = cd / (cd + cp) if cd + cp else 0.0
    print(f"{r:22} {x['n']:>7} | {sd:6.1%} {sp:6.1%} {sd + sp:6.1%} | {cd:6.1%} {cp:6.1%} {cd + cp:6.1%} | {share:6.1%}")
n = max(1, htx["n"])
print(f"hot-slot txs {htx['n']}: strict all {htx['s_all'] / n:.1%} (delta-only {htx['s_delta_only'] / n:.1%}); "
      f"headroom all {htx['c_all'] / n:.1%} (delta-only {htx['c_delta_only'] / n:.1%})")
