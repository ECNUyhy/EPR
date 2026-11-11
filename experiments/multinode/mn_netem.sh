#!/usr/bin/env bash
# Emulated WAN latency between the experiment hosts, for the experiment's own traffic only (run on 116).
#   mn_netem.sh set <rtt_ms>   every host delays its egress to the other experiment hosts by rtt/2 (RTT = rtt_ms)
#   mn_netem.sh clear          remove everything (restores the default root qdisc)
#   mn_netem.sh show
# Only TCP packets to another experiment host with source or destination port in 20000-20999 are delayed:
# nftables marks them (table inet d16mn), a prio qdisc sends marked packets to a netem band and all other
# traffic of the host to its normal band. Traffic between containers of the same host is not delayed.
set -u
OP=$1; RTT=${2:-0}
HOSTS="${NET_PREFIX:-10.0.0}.112 ${NET_PREFIX:-10.0.0}.115 ${NET_PREFIX:-10.0.0}.116 ${NET_PREFIX:-10.0.0}.118 ${NET_PREFIX:-10.0.0}.124"
C="ssh -F $HOME/.ssh/config_d16mn -o BatchMode=yes"
remote() {  # host-ip op one-way-ms
  local me=$1 op=$2 ow=$3 peers=""
  for h in $HOSTS; do [ "$h" != "$me" ] && peers="$peers${peers:+, }$h"; done
  cat <<EOF
set -u
IF=\$(ip route get ${peers%%,*} | grep -oP 'dev \K\S+')
sudo -n tc qdisc del dev \$IF root 2>/dev/null; sudo -n nft delete table inet d16mn 2>/dev/null
if [ "$op" = set ]; then
  sudo -n tc qdisc add dev \$IF root handle 1: prio bands 3 priomap 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1 1
  sudo -n tc qdisc add dev \$IF parent 1:3 handle 30: netem delay ${ow}ms limit 100000
  sudo -n tc filter add dev \$IF parent 1: protocol ip handle 0xd16 fw flowid 1:3
  sudo -n nft add table inet d16mn
  sudo -n nft add chain inet d16mn post '{ type filter hook postrouting priority mangle; }'
  sudo -n nft add rule inet d16mn post ip daddr { $peers } tcp dport 20000-20999 meta mark set 0xd16
  sudo -n nft add rule inet d16mn post ip daddr { $peers } tcp sport 20000-20999 meta mark set 0xd16
fi
echo "\$(hostname) \$IF: \$(tc qdisc show dev \$IF | grep -c netem) netem, \$(sudo -n nft list table inet d16mn 2>/dev/null | grep -c 'mark set') rules"
EOF
}
case $OP in
  set|clear)
    ow=$(python3 -c "print(${RTT}/2)")
    for h in $HOSTS; do
      case $h in ${NET_PREFIX:-10.0.0}.116) remote $h $OP $ow | bash ;; *) remote $h $OP $ow | $C n${h##*.} bash ;; esac &
    done; wait ;;
  show)
    for h in $HOSTS; do
      case $h in ${NET_PREFIX:-10.0.0}.116) bash -c "tc qdisc show | grep netem; sudo -n nft list table inet d16mn 2>/dev/null | grep -c mark" ;;
        *) $C n${h##*.} "tc qdisc show | grep netem; sudo -n nft list table inet d16mn 2>/dev/null | grep -c mark" ;; esac | sed "s/^/$h: /"
    done ;;
esac
