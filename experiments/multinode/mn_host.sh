#!/usr/bin/env bash
# Per-host control of the multi-node Fabric-X stack.
#   mn_host.sh <dir> <host-suffix> up   [service...]   pre-create bind-mount dirs as the user, then up -d
#   mn_host.sh <dir> <host-suffix> down                 down -v and wipe ./data (ledgers, db)
#   mn_host.sh <dir> <host-suffix> ps
set -u
DIR=$1; H=$2; OP=$3; shift 3
cd "$DIR" || exit 1
export UID; export GID=$(id -g)
F=compose.$H.yml
case $OP in
  up)
    # a .tmpfs marker in the compose dir keeps this host's ledgers in RAM (/dev/shm/d16mn): diagnostic for hosts
    # whose disk throttles the ordering service (112's /data syncs 10x slower than the other hosts)
    if [ -f .tmpfs ]; then mkdir -p /dev/shm/d16mn/$H && rm -rf ./data && ln -s /dev/shm/d16mn/$H ./data; fi
    # every ./data/... host path of a bind mount must exist and be owned by the container user (UID:GID)
    for d in $(grep -oE '\./data/[^:]+' "$F" | sort -u); do mkdir -p "$d"; done
    docker compose -f "$F" up -d "$@" 2>&1 | grep -vE "^ *(Container|Network) .* (Created|Starting|Started|Running|Waiting|Healthy)$" | tail -5
    ;;
  down)
    docker compose -f "$F" down -v --remove-orphans 2>&1 | tail -1
    # ledgers may have been created by the daemon (root) before; remove with sudo if needed
    [ -L ./data ] && rm -rf /dev/shm/d16mn/$H
    rm -rf ./data 2>/dev/null || sudo -n rm -rf ./data
    ;;
  ps)
    docker compose -f "$F" ps --format '{{.Name}} {{.Status}}'
    ;;
esac
