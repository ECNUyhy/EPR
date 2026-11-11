# Analysis

All scripts read only `../results/` and run in seconds to minutes without a server.

| Script | Output |
|---|---|
| `paper_numbers.py` | The numbers of the abstract, the introduction and Sections VIII-C to VIII-G, each in the phrase of the paper that states it, next to the data behind it; with `--figs`, every figure of the evaluation into `../results/figures/` |
| `repetitions.py` | The five runs of each peak of Fig. 7: coefficients of variation and EPR's lead in mean throughput (Section VIII-B) |
| `table2.py ../results/coverage/cov_mainnet.jsonl.gz` | Table III |
| `table2_split.py ../results/coverage/cov_mainnet.jsonl.gz` | Table III split into guarded deltas and programs, and the USDT and USDC rows of Section VIII-H |
| `rep_variance.py` | Helper of `repetitions.py` (log parser); run alone, it also lists every repetition |

A peak is the highest committed throughput over the rates a scheme sustains: median end-to-end latency within
2 s and at least 95% of the offered transactions committed (after resubmission with retries, at first try
without), as defined in Section VIII-B and implemented in `../plots/eprstyle.py`.
