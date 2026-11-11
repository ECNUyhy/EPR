#!/usr/bin/env python3
"""Real USDT transfer trace (2,000 consecutive Ethereum mainnet blocks, from the FlexTender range dump) in the
TSV columns fabric-x-evm's perf generator reads. The access pattern (senders, recipients, amounts, order) is the
real one; transfers are replayed on the harness's ERC-20 token contract, like the USDC trace.

usage: usdt_tsv.py <in.csv> <out.tsv.gz>
"""
import csv
import gzip
import sys

TOKEN = "0xa0b86991c6218b36c1d19d4a2e9eb0ce3606eb48"
cols = ["block_id", "transaction_hash", "time", "token_address", "sender", "recipient", "value",
        "token_name", "token_symbol", "token_decimals"]
n = 0
with open(sys.argv[1]) as f, gzip.open(sys.argv[2], "wt") as g:
    g.write("\t".join(cols) + "\n")
    for row in csv.DictReader(f):
        n += 1
        g.write("\t".join([row["block"], "0x%064x" % n, "2025-11-01 00:00:00", TOKEN, row["from"].lower(),
                           row["to"].lower(), row["amount"], "Tether USD", "USDT", "6"]) + "\n")
print(f"wrote {n} transfers")
