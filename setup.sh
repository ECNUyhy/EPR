#!/usr/bin/env bash
# Build the EPR artifact.
#   ./setup.sh [WORKDIR] [--images]
# Clones the Fabric-X committer and fabric-x-evm at the pinned revisions, applies patches/, builds both,
# runs the unit tests of the patched packages, and builds the command-line tools into WORKDIR/bin. A second
# copy of fabric-x-evm with patches/fabric-x-evm-two-org.patch serves the two-organization experiment. With
# --images, it also builds the release images of the EPR committer and of the unmodified committer.
set -euo pipefail
trap 'echo "[setup] FAILED at line $LINENO" >&2' ERR
ART=$(cd "$(dirname "$0")" && pwd)
W=$(realpath -m "${1:-$HOME/epr-work}")
IMAGES=${2:-}
COMMITTER_REPO=${COMMITTER_REPO:-https://github.com/hyperledger/fabric-x-committer.git}
COMMITTER_REV=v1.0.3   # tag, commit 26effd896244d6bc9fc76e5df7f8e887ba793cdb
EVM_REPO=${EVM_REPO:-https://github.com/hyperledger/fabric-x-evm.git}
EVM_REV=c42a92c6892d3eafb448cda78ef84e06b9056f93
GO_MIN=1.26.8
GO_VERSION=1.27.1
log() { echo "[setup $(date +%H:%M:%S)] $*"; }

mkdir -p "$W"/{src,logs,data,bin,opt}

# 1. Go toolchain (a recent enough system Go, otherwise one installed under WORKDIR/opt)
if [ ! -x "$W/opt/go/bin/go" ]; then
  sysgo=$(command -v go || true)
  if [ -n "$sysgo" ] && [ "$(printf '%s\n' "$GO_MIN" "$($sysgo env GOVERSION | sed 's/^go//')" | sort -V | head -1)" = "$GO_MIN" ]; then
    mkdir -p "$W/opt/go/bin" && ln -sf "$sysgo" "$W/opt/go/bin/go"
    log "using system Go: $($sysgo version)"
  else
    log "installing Go $GO_VERSION"
    for base in ${GO_DOWNLOAD_BASE:-https://go.dev/dl}; do
      curl -fsSL "$base/go$GO_VERSION.linux-amd64.tar.gz" -o "$W/opt/go.tgz" && break || rm -f "$W/opt/go.tgz"
    done
    tar -C "$W/opt" -xzf "$W/opt/go.tgz" && rm -f "$W/opt/go.tgz"
  fi
fi
cat > "$W/env.sh" <<EOF
export EPR_WORK=$W
export PATH=$W/opt/go/bin:$W/gopath/bin:\$PATH
export GOPATH=$W/gopath
export GOMODCACHE=$W/gopath/pkg/mod
export GOPROXY=${GOPROXY:-https://proxy.golang.org,direct}
export GOTOOLCHAIN=local
export GOFLAGS=${GOFLAGS:--tags=http2legacy}
EOF
. "$W/env.sh"

# 2. upstream sources at the pinned revisions
fetch() {  # repo rev dir
  local i
  for i in 1 2 3 4 5; do
    if [ -d "$3/.git" ] || git -c http.version=HTTP/1.1 clone -q "$1" "$3"; then break; fi
    rm -rf "$3"; log "clone of $1 failed (attempt $i), retrying"; sleep $((i * 20))
  done
  git -C "$3" checkout -q "$2"
}
log "cloning upstream repositories"
fetch "$COMMITTER_REPO" "$COMMITTER_REV" "$W/src/fabric-x-committer"
fetch "$COMMITTER_REPO" "$COMMITTER_REV" "$W/src/fabric-x-committer-base"
fetch "$EVM_REPO" "$EVM_REV" "$W/src/fabric-x-evm"
fetch "$EVM_REPO" "$EVM_REV" "$W/src/fabric-x-evm-two-org"

# 3. patches
apply_patch() {  # dir patch
  if git -C "$1" apply --check --whitespace=nowarn "$2" 2>/dev/null; then
    git -C "$1" apply --whitespace=nowarn "$2"; log "applied $(basename "$2") to $(basename "$1")"
  else
    git -C "$1" apply --reverse --check "$2" 2>/dev/null && log "$(basename "$2") already applied to $(basename "$1")" \
      || { echo "cannot apply $2 to $1" >&2; exit 1; }
  fi
}
apply_patch "$W/src/fabric-x-committer" "$ART/patches/fabric-x-committer-v1.0.3.patch"
apply_patch "$W/src/fabric-x-evm" "$ART/patches/fabric-x-evm-c42a92c.patch"
apply_patch "$W/src/fabric-x-evm-two-org" "$ART/patches/fabric-x-evm-c42a92c.patch"
apply_patch "$W/src/fabric-x-evm-two-org" "$ART/patches/fabric-x-evm-two-org.patch"
chmod +x "$W"/src/fabric-x-committer*/scripts/*.sh

# 4. build and unit tests
log "building and testing the committer"
( cd "$W/src/fabric-x-committer" && go build ./... \
  && go test -count=1 ./service/coordinator/dependencygraph/... \
  && go test -count=1 ./service/vc/ -run 'TestEPR|TestPilot' ) 2>&1 | tail -25 | tee "$W/logs/setup_committer.log"
( cd "$W/src/fabric-x-committer-base" && go build ./... ) 2>&1 | tail -5
log "building and testing fabric-x-evm"
( cd "$W/src/fabric-x-evm" && go build ./... \
  && D16_ROUTER_JSON="$ART/contracts/D16Router.json" D16_SMALLBANK_JSON="$ART/contracts/SmallBank.json" \
     go test -count=1 ./endorser/execution/ -run 'EPR|Diff|Prog' ) 2>&1 | tail -25 | tee "$W/logs/setup_evm.log"
( cd "$W/src/fabric-x-evm-two-org" && go build ./... && go vet -tags=perf,http2legacy ./integration/perf/ ) 2>&1 | tail -5
log "building the command-line tools"
for t in d16reexec_v2 d16oracle d16prefund d16smallbank d16dex d16coverage; do
  ( cd "$W/src/fabric-x-evm" && go build -o "$W/bin/$t" "./$t" )
done

# 5. release images (needed for the runs)
if [ "$IMAGES" = "--images" ]; then
  log "building release images"
  (cd "$W/src/fabric-x-committer" && make build-image-release version=epr image_namespace=epr docker_cmd=docker)
  (cd "$W/src/fabric-x-committer-base" && make build-image-release version=base image_namespace=epr docker_cmd=docker)
  log "full-stack prerequisites: fxconfig and the network's crypto material and genesis block"
  mkdir -p "$W/data/bin"
  (cd "$W/src/fabric-x-evm" && CGO_ENABLED=0 go build -o "$W/data/bin/fxconfig" github.com/hyperledger/fabric-x/tools/fxconfig)
  (cd "$W/src/fabric-x-evm" && make init-x)
fi
log "done"
echo SETUP_DONE
