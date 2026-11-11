#!/usr/bin/env bash
# Per-host helpers of the multi-node experiment (installed in ~/bin on 112/115/118/124, scripts/ on 116).
#   d16host.sh others_cpu   CPU% summed over the processes of users other than the experiment user and root,
#                           i.e. other people's jobs on the host
#   d16host.sh gov_perf     set the performance cpufreq governor on every cpu (needs passwordless sudo);
#                           prints the number of cpus NOT on it (0 = ok)
#   d16host.sh gov_show     current governor of cpu0
case $1 in
  others_cpu) me=$(id -un); ps -eo user:20,pcpu --no-headers | awk -v me="$me" '$1 != me && $1 != "root" {s += $2} END {printf "%d\n", s}' ;;
  gov_perf) for f in /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor; do echo performance | sudo -n tee $f >/dev/null; done
            grep -L performance /sys/devices/system/cpu/cpu*/cpufreq/scaling_governor | wc -l ;;
  gov_show) cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor ;;
  *) echo "usage: d16host.sh others_cpu|gov_perf|gov_show" >&2; exit 2 ;;
esac
