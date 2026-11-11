#!/usr/bin/env python3
"""Generate the multi-host variant of the full Fabric-X stack (4-party Arma + org1 committer) from the
single-host tree (compose.d16full.yml, testdata/, integration/fabx-full.yaml).

Placement: Arma party p on PARTY_HOST[p]; the org1 committer stack (db, verifier, validator, coordinator,
sidecar, query service) on COMMITTER_HOST; the client/gateway (embedded org1 endorser) on CLIENT_HOST.
Every cross-host port is moved into 20000-20999 (the range opened in the firewalls):
  Arma party p, component k (router 0, batcher 1, consenter 2, assembler 3): 20000 + 100 p + 50 + k
  sidecar 4001 -> 20401, query service 7001 -> 20701.
Hosts are addressed by IP; every TLS certificate gets all experiment-host IPs as SANs.

usage: gen_multinode.py <fabric-x-evm tree> <out dir>
writes: <out>/testdata/{crypto-config.yaml,shared_config.yaml,configtx.yaml,config/*},
        <out>/compose.<host>.yml per host, <out>/fabx-mn.yaml (gateway config for the client),
        <out>/placement.txt
"""
import os
import re
import shutil

NET = os.environ.get("NET_PREFIX", "10.0.0")  # first three octets of the five servers' addresses
import sys

PARTY_HOST = {1: NET + ".112", 2: NET + ".115", 3: NET + ".116", 4: NET + ".118"}
COMMITTER_HOST = NET + ".112"
CLIENT_HOST = NET + ".124"
ALL_HOSTS = sorted(set(PARTY_HOST.values()) | {COMMITTER_HOST, CLIENT_HOST})
COMP = {"router": 0, "batcher": 1, "consenter": 2, "assembler": 3}


def old_port(p, k):
    return 7000 + (p - 1) * 100 + 50 + k


def new_port(p, k):
    return 20000 + p * 100 + 50 + k


PORTMAP = {old_port(p, k): new_port(p, k) for p in PARTY_HOST for k in range(4)}
PORTMAP.update({4001: 20401, 7001: 20701})


def rd(p):
    return open(p, encoding="utf-8").read()


def wr(p, s):
    os.makedirs(os.path.dirname(p), exist_ok=True)
    open(p, "w", encoding="utf-8", newline="\n").write(s)


def crypto_config(s):
    """Add every experiment-host IP to every SANS list (idempotent)."""
    out, lines, i = [], s.split("\n"), 0
    while i < len(lines):
        out.append(lines[i])
        if lines[i].strip() == "SANS:":
            j = i + 1
            ind = re.match(r"\s*", lines[j]).group(0) if j < len(lines) and lines[j].strip().startswith("- ") else \
                re.match(r"\s*", lines[i]).group(0) + "  "
            existing = set()
            while j < len(lines) and lines[j].strip().startswith("- "):
                existing.add(lines[j].strip()[2:].strip())
                j += 1
            for ip in ALL_HOSTS:
                if ip not in existing:
                    out.append(f"{ind}- {ip}")
        i += 1
    return "\n".join(out)


def shared_config(s):
    def host(m):
        p = int(m.group(1))
        return f"Host: {PARTY_HOST[p]}"
    s = re.sub(r"Host: orderer-party(\d)-(router|batcher|consenter|assembler)", host, s)
    return re.sub(r"Port: (\d+)", lambda m: f"Port: {PORTMAP.get(int(m.group(1)), int(m.group(1)))}", s)


def configtx(s):
    """OrdererEndpoints (host:port) and the consenter mapping (Host/Port) of the channel config."""
    def ep(m):
        p, comp, port = int(m.group(1)), m.group(2), int(m.group(3))
        return f"{PARTY_HOST[p]}:{PORTMAP.get(port, port)}"
    s = re.sub(r"orderer-party(\d)-(router|batcher|consenter|assembler):(\d+)", ep, s)
    return shared_config(s)


def node_config(s, p, comp):
    return re.sub(r"(General:\n  ListenAddress: 0\.0\.0\.0\n  ListenPort: )(\d+)", lambda m: m.group(1) + str(new_port(p, COMP[comp])), s)


def committer_cfg(s):
    s = s.replace("endpoint: :4001", "endpoint: :20401")
    return s.replace("endpoint: :7001", "endpoint: :20701")


def gateway_cfg(s):
    """host/port pairs: committer sidecar -> COMMITTER_HOST:20401; routers -> party host:new port."""
    lines = s.split("\n")
    for i, l in enumerate(lines):
        m = re.match(r"(\s*)port: (\d+)\s*$", l)
        if not m or i == 0 or "host:" not in lines[i - 1]:
            continue
        port = int(m.group(2))
        ind = re.match(r"\s*", lines[i - 1]).group(0)
        if port in (4001, 7001):
            lines[i - 1], lines[i] = f"{ind}host: {COMMITTER_HOST}", f"{m.group(1)}port: {PORTMAP[port]}"
        else:
            for p in PARTY_HOST:
                if port == old_port(p, 0):
                    lines[i - 1], lines[i] = f"{ind}host: {PARTY_HOST[p]}", f"{m.group(1)}port: {new_port(p, 0)}"
    return "\n".join(lines)


def split_services(compose):
    """Return (header, [(name, block)]) of a compose file with top-level 'services:'."""
    head, body = compose.split("\nservices:\n", 1)
    blocks, cur, name = [], [], None
    for l in body.split("\n"):
        m = re.match(r"^  ([a-z0-9][a-z0-9_-]*):\s*$", l)
        if m:
            if name:
                blocks.append((name, "\n".join(cur)))
            name, cur = m.group(1), [l]
        elif name:
            cur.append(l)
    if name:
        blocks.append((name, "\n".join(cur)))
    return head, blocks


def host_of(name):
    m = re.match(r"orderer-party(\d)-", name)
    if m:
        return PARTY_HOST[int(m.group(1))]
    if name.startswith("committer-"):
        return COMMITTER_HOST
    return None


def fix_block(name, block, local):
    # published ports -> new ports (container listens on the same new port)
    def port_line(m):
        a, b = int(m.group(2)), int(m.group(3))
        n = PORTMAP.get(b, PORTMAP.get(a, a))
        return f'{m.group(1)}- "{n}:{n}"'
    block = re.sub(r'(\s*)- "(\d+):(\d+)"', port_line, block)
    # drop dependencies on services placed on other hosts
    lines, out, skip = block.split("\n"), [], False
    for i, l in enumerate(lines):
        m = re.match(r"^      ([a-z0-9][a-z0-9_-]*):\s*$", l) or re.match(r"^      - ([a-z0-9][a-z0-9_-]*)\s*$", l)
        if m and in_depends(lines, i) and m.group(1) not in local:
            skip = l.strip().endswith(":")
            continue
        if skip and re.match(r"^        ", l):
            continue
        skip = False
        out.append(l)
    block = "\n".join(out)
    # an empty depends_on: is invalid
    return re.sub(r"\n    depends_on:\n(?=    [a-z])", "\n", block)


def in_depends(lines, i):
    for j in range(i - 1, -1, -1):
        if re.match(r"^    [a-z_]+:", lines[j]):
            return lines[j].strip() == "depends_on:"
    return False


def main():
    src, out = sys.argv[1], sys.argv[2]
    td = os.path.join(src, "testdata")
    if os.path.exists(out):
        shutil.rmtree(out)
    # testdata (configs only; crypto is regenerated)
    shutil.copytree(os.path.join(td, "config"), os.path.join(out, "testdata", "config"))
    wr(os.path.join(out, "testdata", "configtx.yaml"), configtx(rd(os.path.join(td, "configtx.yaml"))))
    wr(os.path.join(out, "testdata", "crypto-config.yaml"), crypto_config(rd(os.path.join(td, "crypto-config.yaml"))))
    wr(os.path.join(out, "testdata", "shared_config.yaml"), shared_config(rd(os.path.join(td, "shared_config.yaml"))))
    for p in PARTY_HOST:
        for comp in COMP:
            f = os.path.join(out, "testdata", "config", f"party{p}-{comp}.yaml")
            wr(f, node_config(rd(f), p, comp))
    for f in ("committer-sidecar.yaml", "committer-query-service.yaml"):
        g = os.path.join(out, "testdata", "config", f)
        wr(g, committer_cfg(rd(g)))
    wr(os.path.join(out, "fabx-mn.yaml"), gateway_cfg(rd(os.path.join(src, "integration", "fabx-full.yaml"))))
    # per-host compose files
    head, blocks = split_services(rd(os.path.join(src, "compose.d16full.yml")))
    placement = []
    for h in ALL_HOSTS:
        mine = [(n, b) for n, b in blocks if host_of(n) == h]
        if not mine:
            continue
        local = {n for n, _ in mine}
        body = "\n".join(fix_block(n, b, local) for n, b in mine)
        wr(os.path.join(out, f"compose.{h.split('.')[-1]}.yml"), head + "\nservices:\n" + body.rstrip("\n") + "\n")
        placement.append(f"{h}: " + " ".join(sorted(local)))
    placement.append(f"{CLIENT_HOST}: client + gateway (embedded org1 endorser), config fabx-mn.yaml")
    placement.append("ports: " + " ".join(f"{a}->{b}" for a, b in sorted(PORTMAP.items())))
    wr(os.path.join(out, "placement.txt"), "\n".join(placement) + "\n")
    print("\n".join(placement))


if __name__ == "__main__":
    main()
