#!/usr/bin/env python3
"""E1 (11/09, revised) summary: consistency with cov_1109b, the differential check, determinism, reverse control.
usage: e1_summary.py WORKDIR COV_1109B"""
import collections
import glob
import json
import os
import sys

W, REF = sys.argv[1], sys.argv[2]
ROUTERS = {"0x7a250d5630b4cf539739df2c5dacb4c659f2488d": "Uniswap V2 Router02",
           "0x66a9893cc07d91d95644aedd05d03f95e1dba8af": "Uniswap UniversalRouter (V4)",
           "0x3fc91a3afd70395cd496c647d5a6cc9d4b2b7fad": "Uniswap UniversalRouter",
           "0x68b3465833fb72a70ecdf485e0e4c7bd8665fc45": "Uniswap SwapRouter02",
           "0x111111125421ca6dc452d289314280a0f8842a65": "1inch v6",
           "0xdac17f958d2ee523a2206206994597c13d831ec7": "USDT",
           "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48": "USDC"}


def load(paths):
    out = []
    for p in paths:
        for line in open(p):
            out.append(json.loads(line))
    return out


def classes(rows):
    return {r["hash"]: (r["status"], tuple(sorted((w["k"], w["s"], w["c"]) for w in (r.get("writes") or []))))
            for r in rows}


new = classes(load(sorted(glob.glob(os.path.join(W, "cov_p*.jsonl")))))
ref = classes(load([REF]))
both = [h for h in new if h in ref]
print(f"== 0. coverage lines vs cov_1109b (fix_1109c tracer, Table III's write set): {len(new)} transactions, "
      f"{len(both)} in both, {sum(1 for h in both if new[h] != ref[h])} differ (expected 0)")

P = load(sorted(glob.glob(os.path.join(W, "pert_p*.jsonl"))))
print(f"\n== 1. differential check on {len(P)} transactions with repairable content (endorser's write set)")
print("   by kind:", dict(collections.Counter(r["kind"] for r in P)))
nw = sum(r["nw"] for r in P)
print(f"   content writes {nw}, of which value-preserving {sum(r['nsame'] for r in P)}")
print(f"   determinism: transactions with a differing repeated extraction: {sum(1 for r in P if r['det'])} (expected 0)")
tab = collections.Counter()
why = collections.Counter()
bad = []
for r in P:
    for s in r.get("s", []):
        k = (r["kind"], s["k"])
        tab[k + ("samples",)] += 1
        if s["g"]:
            tab[k + ("hold",)] += 1
            if s.get("m"):
                tab[k + ("match",)] += 1
            elif s.get("why", "").startswith("evm_oog"):
                tab[k + ("oog (A7)",)] += 1
            else:
                tab[k + ("MISMATCH",)] += 1
                why[s.get("why", "?").split(":")[0]] += 1
                if len(bad) < 25:
                    bad.append((r["hash"], r["to"], r["kind"], s["k"], s.get("why")))
        else:
            tab[k + ("fail",)] += 1
            tab[k + ("fail/" + (s.get("f") or "ok") + ("/same" if s.get("same") else ""),)] += 1
            tab[k + ("forced " + s.get("forced", "?"),)] += 1
cols = ["hold", "match", "MISMATCH", "oog (A7)", "fail", "fail/revert", "fail/oog", "fail/ok/same", "fail/ok",
        "forced wrong", "forced right"]
for kind in ("delta", "mixed", "program"):
    for sk in ("self", "small", "large", "lower_at", "lower_below", "upper_at", "upper_above"):
        n = tab[(kind, sk, "samples")]
        if n:
            print(f"   {kind:8s} {sk:12s} n={n:7d} | " + " | ".join(f"{c} {tab[(kind, sk, c)]}" for c in cols))
pert = [s for r in P for s in r.get("s", []) if s["k"] != "self"]
hold = [s for s in pert if s["g"]]
print(f"   perturbed samples {len(pert)}; guards hold {len(hold)}, of which match {sum(1 for s in hold if s.get('m'))}; "
      f"mismatches (all samples) {sum(1 for r in P for s in r.get('s', []) if s['g'] and not s.get('m') and not s.get('why','').startswith('evm_oog'))} (expected 0)")
fails = [s for s in pert if not s["g"]]
print(f"   guards fail {len(fails)}: re-execution reverts {sum(1 for s in fails if s.get('f'))}; evaluated without guards, "
      f"the content would be wrong in {sum(1 for s in fails if s.get('forced') == 'wrong')} and right in "
      f"{sum(1 for s in fails if s.get('forced') == 'right')}")
print("   mismatch reasons:", dict(why))
for b in bad:
    print("    ", b)
print("\n   by contract (transactions, perturbed samples, guards hold, mismatches):")
for addr, name in ROUTERS.items():
    rs = [r for r in P if r["to"] == addr]
    ss = [s for r in rs for s in r.get("s", []) if s["k"] != "self"]
    print(f"   {name:30s} {len(rs):6d} {len(ss):7d} {sum(1 for s in ss if s['g']):7d} "
          f"{sum(1 for s in ss if s['g'] and not s.get('m') and not s.get('why','').startswith('evm_oog')):4d}")
top = collections.Counter(r["to"] for r in P)
print("   distinct target contracts:", len(top), "; programs over distinct targets:",
      len({r["to"] for r in P if r["kind"] == "program"}))
