# Stage S14r (11/05): the repeats S14 should have made. S14 repeated a refined baseline peak when the LAST sustained
# refinement rate beat the strongest baseline, but the peak is the BEST sustained rate (HTFabric DEX 64 pairs: 906 tx/s
# gave 913, 937 gave 753, so 906 went unrepeated). Here every Fig. 8 workload whose strongest baseline changed in S14
# gets its new peak run measured twice (labels <scheme>rp1/rp2), unless S14 already did so at that rate.
XNAME=s14r
xbody() {
  local R=$D/scripts/srv_run_evm6w.sh
  local RXO2="rxo2 epr 1 1 oldqueue REEXEC=1 REEXEC_BIN=$D/bin/d16reexec_v2 FX_EPR_PROG=1 REEXEC_ALL=1"
  local sc wl r k x s img vc evm q ds gen
  # "<scheme> <workload> <rate>" for each refined sweep of S14 (retries) whose best sustained point beats the
  # strongest baseline's previous peak and has no repeats yet
  python3 - $D/logs/q1103_s14.txt > $D/logs/s14r_list.txt <<'PY'
import re, sys
prev = {"dex16": 499, "dex64": 878, "usdt": 1751, "dex1": 212, "usdc": 1479, "sb0.8": 5464, "sb1.0": 2528, "sb1.2": 1244}
pts, rep, cur = {}, set(), None
for line in open(sys.argv[1], errors="replace"):
    m = re.match(r"== rk_(\S+?)_(htf2|rxo2|fifo)(rp[12])?_r(\d+) exit=", line)
    if line.startswith("== "):
        cur = None
    if m:
        if m[3]:
            rep.add((m[2], m[1], int(m[4])))
        else:
            cur = (m[2], m[1], int(m[4]))
        continue
    if cur:
        g = re.search(r"whole_goodput=([0-9.]+) lat_ms_p50=([0-9.]+)", line)
        if g:
            pts[cur] = [float(g[1]), float(g[2]), None]
        c = re.search(r"GOODPUT .*processed=(\d+)/\d+ success=(\d+)", line)
        if c and cur in pts and int(c[1]) > 0:
            pts[cur][2] = int(c[2]) / int(c[1])
best = {}
for (s, wl, r), (g, p50, c) in pts.items():
    if wl in prev and p50 <= 2000 and (c or 0) >= 0.95 and g > prev[wl] and g > best.get((s, wl), (0, 0))[0]:
        best[(s, wl)] = (g, r)
for (s, wl), (g, r) in sorted(best.items()):
    if (s, wl, r) not in rep:
        print(s, wl, r)
PY
  echo "S14R repeats: $(tr '\n' ';' < $D/logs/s14r_list.txt)" | tee -a $out
  while read -r sc wl r; do
    case $sc in htf2) x="$HTF2";; rxo2) x="$RXO2";; fifo) x="$FIFO";; *) continue;; esac
    set -- $x; s=$1 img=$2 vc=$3 evm=$4 q=$5; shift 5
    read -r ds gen <<< "$(dsof $wl)"
    for k in 1 2; do
      run rk_${wl}_${s}rp${k}_r$r $img $vc $evm $q $ds $gen FX_RATE=$r "$@" $(xtra $wl) FX_RETRY=50 < /dev/null > /dev/null
    done
    save
  done < $D/logs/s14r_list.txt
  echo "S14R repeats done" | tee -a $out
}
