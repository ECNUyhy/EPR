#!/usr/bin/env bash
# Install and start rootless Docker for the current (non-root) user, with the pasta network driver
# (faster than slirp4netns) and the implicit port driver (pasta forwards published ports itself).
set -u
say() { echo "[rootless $(hostname) $(date +%F_%T)] $*"; }
export XDG_RUNTIME_DIR=/run/user/$(id -u)
[ -d "$XDG_RUNTIME_DIR" ] || { say "no $XDG_RUNTIME_DIR (linger?)"; exit 1; }
mkdir -p ~/.config/systemd/user/docker.service.d
cat > ~/.config/systemd/user/docker.service.d/override.conf <<'EOF'
[Service]
Environment="DOCKERD_ROOTLESS_ROOTLESSKIT_NET=pasta"
Environment="DOCKERD_ROOTLESS_ROOTLESSKIT_PORT_DRIVER=implicit"
EOF
if ! systemctl --user is-active -q docker; then
  dockerd-rootless-setuptool.sh install --skip-iptables > /tmp/rootless_install.log 2>&1 || { tail -15 /tmp/rootless_install.log; say INSTALL_FAILED; exit 1; }
fi
systemctl --user daemon-reload; systemctl --user restart docker; systemctl --user enable docker >/dev/null 2>&1
grep -q DOCKER_HOST ~/.bashrc || echo "export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock" >> ~/.bashrc
export DOCKER_HOST=unix://$XDG_RUNTIME_DIR/docker.sock
sleep 2
docker info --format 'rootless={{.SecurityOptions}} driver={{.Driver}} cgroup={{.CgroupVersion}} root={{.DockerRootDir}}' 2>&1 | tail -1
say "net=$(systemctl --user show docker -p Environment | grep -o 'NET=[a-z0-9]*')"
say ROOTLESS_DONE
