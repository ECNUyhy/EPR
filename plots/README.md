# Plots

Figure scripts of the evaluation; they read `../results/raw/` and write to `../results/figures/` (run them
through `../analysis/paper_numbers.py --figs`, which sets the final EPR logs).

| Script | Figure |
|---|---|
| `fig_motivation.py` | Fig. 1 |
| `fig_overall.py` | Fig. 7 |
| `fig_layout1104.py sweep_lin skew modes mechanism ordering` | Figs. 8, 9, 10, 11 and 13 |
| `fig_blocks_ablation.py` | Fig. 12 |
| `fig_longrun.py`, `fig_geo.py` | data of Figs. 10b and 13b-d, used by `fig_layout1104.py` |
| `eprstyle.py` | shared style, log parser and the peak criterion |
