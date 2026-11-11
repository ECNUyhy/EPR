# Results

- `raw/`: the logs behind the figures and numbers of the paper, as written by the drivers in `../experiments/` (one block per
  run: its label, the configuration, the result line with throughput, latencies and commit share, and the checks
  after the run). `raw/q1103/` holds the final runs of EPR and the later baseline runs, `raw/p44/` and
  `raw/p44_w/` the five-server deployment. The main README maps each experiment of the paper to its logs.
- `coverage/cov_mainnet.jsonl.gz`: one line per transaction of the mainnet study of Section VIII-H (64,629
  transactions, 20 samples of 10 consecutive blocks one week apart in 2026): replay status, gas, and the class
  (guarded delta, program or plain) of every storage write with the users' gas limits and with headroom.
  `coverage/tracer_checks.txt`: the unit tests, the workload acceptance and Table III of the final tracer rules.
- `coverage/E1_SUMMARY.txt`: the mainnet differential test of Section VIII-I (written before the final numbering of
  the paper, it calls Table III "Table II").
- `figures/`: the figures of the evaluation as produced by `../analysis/paper_numbers.py --figs`.
