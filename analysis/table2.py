#!/usr/bin/env python3
"""The rows of Table III (repairable storage writes on mainnet) from a d16coverage jsonl.
A write is hot if another transaction writes the same slot within HOT_GAP blocks (coverage_by_contract.py).
Direct: the written contract is the transaction's `to`. usage: python analysis/table2.py results/coverage/cov_mainnet.jsonl.gz"""
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

rows = {r: Counter() for r in ("Direct writes, all", "Direct writes, hot", "Indirect writes, all", "Indirect writes, hot")}
hot_txs = Counter()
for t in txs:
    pos = (t["block"], t["i"])
    to = (t.get("to") or "").lower()
    ws = t.get("writes") or []
    touches_hot = False
    all_s = all_c = bool(ws)
    for w in ws:
        k = w["k"]
        direct = k.split(":")[1].lower() == to
        is_hot = (k, pos) in hot
        touches_hot |= is_hot
        rep_s = (w.get("s") or "plain") != "plain"
        rep_c = (w.get("c") or "plain") != "plain"
        all_s &= rep_s
        all_c &= rep_c
        for r in ("Direct" if direct else "Indirect",):
            for scope in ("all",) + (("hot",) if is_hot else ()):
                c = rows[f"{r} writes, {scope}"]
                c["n"] += 1
                c["s"] += rep_s
                c["c"] += rep_c
    if touches_hot:
        hot_txs["n"] += 1
        hot_txs["s"] += all_s
        hot_txs["c"] += all_c

print(f"transactions ok: {len(txs)}")
print(f"{'Scope':38} {'Count':>8} {'Strict':>8} {'Headroom':>9}")
for r, c in rows.items():
    print(f"{r:38} {c['n'] / 1000:>7.1f}k {c['s'] / max(1, c['n']):>8.1%} {c['c'] / max(1, c['n']):>9.1%}")
print(f"{'Hot-slot txs, all writes repairable':38} {hot_txs['n'] / 1000:>7.1f}k {hot_txs['s'] / max(1, hot_txs['n']):>8.1%} "
      f"{hot_txs['c'] / max(1, hot_txs['n']):>9.1%}")
