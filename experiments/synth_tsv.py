#!/usr/bin/env python3
"""Synthetic USDC transfer TSV with tunable contention.

Sender and recipient are drawn independently from a Zipf(theta) distribution over N accounts (theta = 0 is
uniform); self-transfers are redrawn. The output has the columns of the Harvard Dataverse ERC-20 dump that
fabric-x-evm's perf generator reads, so signing, replay and genesis funding are exactly as for real data.

usage: synth_tsv.py <out.tsv.gz> <n_accounts> <n_transfers> <theta> [seed]
"""
import gzip
import random
import sys

USDC = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"


def zipf_sampler(n, theta, rng):
    weights = [1.0 / ((i + 1) ** theta) for i in range(n)]
    total = sum(weights)
    cdf, acc = [], 0.0
    for w in weights:
        acc += w / total
        cdf.append(acc)
    perm = list(range(n))
    rng.shuffle(perm)  # hot ranks land on random addresses

    def draw():
        u = rng.random()
        lo, hi = 0, n - 1
        while lo < hi:
            mid = (lo + hi) // 2
            if cdf[mid] < u:
                lo = mid + 1
            else:
                hi = mid
        return perm[lo]
    return draw


def main():
    out, n, m, theta = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), float(sys.argv[4])
    seed = int(sys.argv[5]) if len(sys.argv) > 5 else 2026
    rng = random.Random(seed)
    draw = zipf_sampler(n, theta, rng)
    addr = ["0x%040x" % (0x1000000 + i * 7919) for i in range(n)]
    cols = ["block_id", "transaction_hash", "time", "token_address", "sender", "recipient", "value",
            "token_name", "token_symbol", "token_decimals"]
    with gzip.open(out, "wt") as f:
        f.write("\t".join(cols) + "\n")
        for i in range(m):
            s = draw()
            r = draw()
            while r == s:
                r = draw()
            value = rng.randint(1, 10**9)  # up to 1000 USDC
            f.write("\t".join([str(9000000 + i // 100), "0x%064x" % (i + 1), "2020-01-01 00:00:00", USDC,
                               addr[s], addr[r], str(value), "USD Coin", "USDC", "6"]) + "\n")
    print(f"wrote {m} transfers over {n} accounts, theta={theta}")


if __name__ == "__main__":
    main()
