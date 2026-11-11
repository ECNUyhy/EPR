#!/usr/bin/env bash
# One-time system preparation for rootless Docker on a borrowed lab server (run with sudo).
# Every action is idempotent and is printed with a tag, so the log is the record of what changed.
set -u
USER_NAME=${1:-d2025yhy}
PEERS="${NET_PREFIX:-10.0.0}.112 ${NET_PREFIX:-10.0.0}.115 ${NET_PREFIX:-10.0.0}.116 ${NET_PREFIX:-10.0.0}.118 ${NET_PREFIX:-10.0.0}.124"
say() { echo "[prep $(hostname) $(date +%F_%T)] $*"; }

# 1. packages for rootless Docker: newuidmap/newgidmap, pasta network driver, user D-Bus for cgroup delegation
need=""
for p in uidmap passt dbus-user-session; do dpkg -s $p >/dev/null 2>&1 || need="$need $p"; done
if [ -n "$need" ]; then
  say "apt install:$need"
  DEBIAN_FRONTEND=noninteractive apt-get install -y $need >/tmp/prep_apt.log 2>&1 || {
    say "apt install failed, trying apt-get update first"; apt-get update >/tmp/prep_apt.log 2>&1
    DEBIAN_FRONTEND=noninteractive apt-get install -y $need >>/tmp/prep_apt.log 2>&1 || { tail -5 /tmp/prep_apt.log; say "APT FAILED"; }; }
else say "packages already present"; fi

# 2. AppArmor: Ubuntu 26.04 already ships /etc/apparmor.d/rootlesskit (unconfined + userns), so nothing to add.
#    (An extra usr.bin.rootlesskit profile for the same binary conflicted with it and broke rootlesskit.)
[ -f /etc/apparmor.d/rootlesskit ] && say "system AppArmor profile rootlesskit present" || say "WARNING: no rootlesskit AppArmor profile"

# 3. netem kernel module (latency emulation inside the user's own container network only)
lsmod | grep -q sch_netem || { modprobe sch_netem && say "loaded sch_netem"; }
[ -f /etc/modules-load.d/netem.conf ] || { echo sch_netem > /etc/modules-load.d/netem.conf; say "created /etc/modules-load.d/netem.conf"; }

# 4. keep the user's systemd (and rootless dockerd) running after logout
loginctl enable-linger "$USER_NAME" && say "linger enabled for $USER_NAME"

# 5. firewall: experiment ports 20000-20999/tcp, only between the five experiment hosts
if systemctl is-active -q ufw; then
  for ip in $PEERS; do ufw allow from $ip to any port 20000:20999 proto tcp >/dev/null && say "ufw allow $ip -> 20000:20999/tcp"; done
fi

# 6. verification
say "VERIFY newuidmap=$(command -v newuidmap) pasta=$(command -v pasta) netem=$(lsmod | grep -c sch_netem) linger=$(loginctl show-user $USER_NAME -p Linger | cut -d= -f2) apparmor=$(aa-status 2>/dev/null | grep -c rootlesskit)"
say PREP_DONE
