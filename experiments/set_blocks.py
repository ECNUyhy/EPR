#!/usr/bin/env python3
"""set_blocks.py <compose> <block-size> <block-timeout>: rewrite the test-node orderer block settings."""
import re, sys
p, bs, bt = sys.argv[1:4]
s = open(p).read()
s, n = re.subn(r'(?m)^( *)SC_ORDERER_BLOCK_SIZE:.*$', lambda m: f'{m[1]}SC_ORDERER_BLOCK_SIZE: "{bs}"', s)
assert n == 1, "SC_ORDERER_BLOCK_SIZE not found"
s, n = re.subn(r'(?m)^( *)#? *SC_ORDERER_BLOCK_TIMEOUT:.*$', lambda m: f'{m[1]}SC_ORDERER_BLOCK_TIMEOUT: "{bt}"', s)
assert n == 1, "SC_ORDERER_BLOCK_TIMEOUT not found"
open(p, "w").write(s)
