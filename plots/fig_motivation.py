"""Fig. 1 (motivation; 11/03: 1.2 -> 1.6 in tall and Loom-style hatched bars, after comparing with the
group's ICDE/VLDB papers whose Fig. 1 is 1.5-2.5 in tall), real USDC transfers on Fabric-X EVM: (a) without retries, the share of transfers Fabric-X
commits vs. aborts as the offered rate grows; (b) peak sustainable throughput with retries, per scheme."""
import eprstyle as S


def main():
    S.setup()
    S.plt.rcParams["hatch.linewidth"] = 0.5
    fig, (a, b) = S.plt.subplots(1, 2, figsize=(S.COL_W, 1.68), gridspec_kw={"width_ratios": [1, 1.25]})
    # (a) Fabric-X commit ratio without retries
    pts = {r: p for r, p in S.scheme_points("fifo", "pk", "usdc").items() if p["commit"] is not None}
    rs = sorted(pts)
    ok = [pts[r]["commit"] * 100 for r in rs]
    ab = [100 - v for v in ok]
    xs = range(len(rs))
    # 11/06: bars 0.62 -> 0.74 wide and labels without "%" (the axis says "Transfers (%)"), so they fit in the bars
    # 11/10: one outlined bar per rate, split into committed (sage green, G2) and aborted (hatched, original gray); the
    # two parts have no borders of their own, so the split shows no step
    a.bar(xs, ok, 0.74, color="#bcd9a2", linewidth=0, zorder=2)
    a.bar(xs, ab, 0.74, bottom=ok, color="white", edgecolor=S.SCHEMES["fifo"][1], hatch="//////", linewidth=0, zorder=2)
    a.bar(xs, [100] * len(xs), 0.74, fill=False, edgecolor=S.SCHEMES["fifo"][1], linewidth=0.6, zorder=3)
    a.plot([[x - 0.74 / 2 for x in xs], [x + 0.74 / 2 for x in xs]], [ok, ok], color=S.SCHEMES["fifo"][1], linewidth=0.6,
           zorder=3)
    for x, v, w in zip(xs, ok, ab):
        a.text(x, v / 2, f"{v:.0f}", ha="center", va="center", fontsize=6.5, color=S.INK, fontweight="bold")
        a.text(x, v + w / 2, f"{w:.0f}", ha="center", va="center", fontsize=6.5, color=S.INK, fontweight="bold",
               bbox=dict(boxstyle="square,pad=0.12", fc="white", ec="none"), zorder=4)
    a.set_xticks(list(xs))
    a.set_xticklabels([f"{r // 1000}k" for r in rs])
    a.set_xlabel("Offered rate (tx/s)")
    a.set_ylabel("Transfers (%)")
    a.set_ylim(0, 100)
    a.grid(axis="x", visible=False)
    a.annotate("(a) Fabric-X, no retries", xy=(0.5, 0), xycoords="axes fraction", xytext=(0, -27), textcoords="offset points", ha="center", va="top", fontsize=7.5, color=S.INK)  # 11/10: sub-caption below, as in Figs. 9-14
    # legend in the caption: green = committed, hatched = aborted
    # (b) peak throughput with retries
    rows = []
    for s in ["fifo", "depg", "fpp", "fs", "htf2", "eprz"]:  # 11/06: Fabric++ added (intro: "none of them exceeds")
        mode = "pk" if s == "depg" else "rk"  # depgraph never aborts: both modes coincide
        v = S.peak(S.scheme_points(s, mode, "usdc"), mode)
        if v:
            rows.append((S.SCHEMES[s][0], v, s))
    rows.sort(key=lambda r: r[1])  # 11/06: ordered by value, largest at the top
    ys = range(len(rows))
    for y, (_, v, s) in zip(ys, rows):
        b.barh(y, v, 0.62, zorder=2, **S.bar_style(s))
    for y, (_, v, _) in zip(ys, rows):
        b.text(v * 1.15, y, f"{v:,.0f}", va="center", fontsize=6.5, color=S.INK)
    b.set_yticks(list(ys))
    b.set_yticklabels([l for l, _, _ in rows])
    b.set_xscale("log")
    b.set_xlim(20, 60000)
    b.set_xlabel("Peak throughput (tx/s)")
    b.grid(axis="y", visible=False)
    b.tick_params(axis="y", length=0)
    b.annotate("(b) Peak, with retries", xy=(0.5, 0), xycoords="axes fraction", xytext=(0, -27), textcoords="offset points", ha="center", va="top", fontsize=7.5, color=S.INK)
    fig.tight_layout(w_pad=0.6)
    print(S.save(fig, "fig_motivation"))


if __name__ == "__main__":
    main()
