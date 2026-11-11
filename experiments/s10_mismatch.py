"""Stage S10: classify the endorsement mismatches of the two-organization runs (read-only on the run logs).

The gateway rejects a transaction when the two organizations' proposal-response payloads differ ("ProposalResponse
Payloads do not match", status ENDORSEMENT_POLICY_FAILURE) and logs the pair. Both payloads are parsed as protobuf
wire format (nested messages recursively) and compared field by field:
  version_only        : only varint fields differ (read versions: the endorsers were at different block heights)
  plain_value         : a plain (not EPR-encoded) bytes leaf differs, and so do the read versions
  plain_same_snapshot : a plain bytes leaf differs at identical read versions
  epr_diff_snapshot   : an EPR-encoded value (EPRD/EPRP/EPRF prefix) differs, and so do the read versions
  epr_same_snapshot   : an EPR-encoded value differs at identical read versions
usage: python3 s10_mismatch.py [logdir]  ->  one line per two-organization run (S10 ..2o.., S10b ..2r..)
"""
import base64, glob, gzip, os, re, sys

logdir = sys.argv[1] if len(sys.argv) > 1 else os.path.expanduser("~/d16/logs")
pat = re.compile(r"do not match \(base64\): '([A-Za-z0-9+/=]+)'.*?'([A-Za-z0-9+/=]+)'")
EPR = (b"EPRD", b"EPRP", b"EPRF")


def varint(b, i):
    v = s = 0
    while True:
        if i >= len(b):
            raise ValueError
        c = b[i]; i += 1
        v |= (c & 0x7F) << s; s += 7
        if c < 0x80:
            return v, i


def parse(b, depth=0):
    """list of (field, kind, value); kind 'v' varint, 'm' nested message, 'b' bytes, 'f' fixed. Raises if invalid."""
    out, i = [], 0
    while i < len(b):
        key, i = varint(b, i)
        f, wt = key >> 3, key & 7
        if f == 0:
            raise ValueError
        if wt == 0:
            v, i = varint(b, i); out.append((f, "v", v))
        elif wt == 2:
            n, i = varint(b, i)
            if i + n > len(b):
                raise ValueError
            data = b[i:i + n]; i += n
            sub = None
            if depth < 12 and n and not data.startswith(EPR):
                try:
                    sub = parse(data, depth + 1)
                except ValueError:
                    sub = None
            out.append((f, "m", sub) if sub is not None else (f, "b", data))
        elif wt == 1:
            out.append((f, "f", b[i:i + 8])); i += 8
        elif wt == 5:
            out.append((f, "f", b[i:i + 4])); i += 4
        else:
            raise ValueError
    return out


def compare(x, y, acc):
    if len(x) != len(y):
        acc.add("structure"); return
    for (f1, k1, v1), (f2, k2, v2) in zip(x, y):
        if f1 != f2 or k1 != k2:
            acc.add("structure"); continue
        if k1 == "m":
            compare(v1, v2, acc)
        elif v1 != v2:
            if k1 == "v":
                acc.add("varint")
            elif k1 == "b" and (v1.startswith(EPR) or v2.startswith(EPR)):
                acc.add("epr")
            else:
                acc.add("bytes")


from collections import Counter


def leaves(t):
    for f, kind, v in t:
        if kind == "m":
            yield from leaves(v)
        else:
            yield kind, v


def classify(a, b):
    """version_only: the bytes leaves agree as a multiset (only read versions differ; proto3 omits a version 0, so
    the field count may differ too); epr_diff_snapshot / epr_same_snapshot: an EPR-encoded value differs, with the
    read versions differing / identical; plain_value: some other bytes leaf differs."""
    la, lb = list(leaves(parse(a))), list(leaves(parse(b)))
    ba, bb = Counter(v for k, v in la if k != "v"), Counter(v for k, v in lb if k != "v")
    if ba == bb:
        return "version_only"
    diff = (ba - bb) + (bb - ba)
    va, vb = Counter(v for k, v in la if k == "v"), Counter(v for k, v in lb if k == "v")
    if any(v.startswith(EPR) for v in diff):
        return "epr_same_snapshot" if va == vb else "epr_diff_snapshot"
    return "plain_same_snapshot" if va == vb else "plain_value"


for f in sorted(glob.glob(os.path.join(logdir, "evm_*2o_r*.log.gz")) + glob.glob(os.path.join(logdir, "evm_*2r_r*.log.gz"))):
    label = os.path.basename(f)[4:-7]
    c = {"failed": 0, "pairs": 0, "version_only": 0, "plain_value": 0, "plain_same_snapshot": 0,
         "epr_diff_snapshot": 0, "epr_same_snapshot": 0, "unparsed": 0}
    with gzip.open(f, "rt", errors="replace") as fh:
        for line in fh:
            if "status: ENDORSEMENT_POLICY_FAILURE" in line:
                c["failed"] += 1
            if "do not match" not in line:
                continue
            m = pat.search(line)
            if not m:
                continue
            c["pairs"] += 1
            try:
                c[classify(base64.b64decode(m[1]), base64.b64decode(m[2]))] += 1
            except ValueError:
                c["unparsed"] += 1
    print(label, " ".join(f"{k}={v}" for k, v in c.items()))
