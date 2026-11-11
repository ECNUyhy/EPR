# EPR: Endorsement-Preserving Repair for Execute-Order-Validate Blockchains

This repository is the artifact of the paper *Endorsement-Preserving Repair: Turning Hotspot Aborts into
Commits in Execute-Order-Validate Blockchains* (ICDE 2027 submission). EPR lets endorsers sign *how* each
storage write depends on the values read, as a guarded delta or as a straight-line program with path guards,
both extracted automatically from unmodified EVM bytecode. Committers re-evaluate this signed content on the
latest state in ledger order instead of aborting transactions whose reads are stale. Credits that cannot fail
commute in the dependency graph, and an escrow classifier lets debits whose balance covers them bypass
ordering. EPR is implemented in the official Fabric-X committer (v1.0.3) and in IBM's fabric-x-evm, and all
baselines of the paper run in the same Fabric-X stack and replay harness.

The repository contains the source changes as patches against pinned upstream revisions, the original sources
of the ported baselines with the list of edits, the contracts, the real transfer traces and the dataset
generators, the experiment drivers, the raw logs behind the figures and numbers of the paper, and the scripts that
recompute every figure, table and number of the evaluation from these logs.

## Contents

| Path | Contents |
|---|---|
| `patches/` | The source changes, as patches against the pinned upstream revisions: `fabric-x-committer-v1.0.3.patch`, `fabric-x-evm-c42a92c.patch`, and `fabric-x-evm-two-org.patch` (harness support for two endorsing organizations, applied on top of the fabric-x-evm patch) |
| `src/` | Every file the patches add, at its upstream path, for browsing (identical to the content of the patches) |
| `baselines/` | Original FabricSharp and Fabric++ sources, the script that ports them with an explicit list of textual edits, and a description of every baseline |
| `contracts/` | SmallBank and the DEX router (Solidity sources, compiled artifacts used by the workload generators, compile scripts) |
| `data/` | The real USDC and USDT transfer traces and the checksums of every generated dataset |
| `gen_datasets.sh` | Regenerates every dataset and genesis file of the paper and checks them against `data/datasets.md5` |
| `experiments/` | The experiment drivers and deployment configuration of the paper |
| `results/` | `raw/`: the logs behind the paper; `coverage/`: the per-transaction data of the mainnet study and the tracer checks; `figures/`: the figures as produced by `plots/` |
| `analysis/` | Scripts that compute the numbers quoted in the text, Table III and the repetition statistics from `results/` |
| `plots/` | Scripts that draw every figure of the evaluation from `results/raw/` |
| `docs/` | `EPR-appendix.pdf`: the online appendix of the paper, with the model, the assumptions A1-A9 and the full proofs |
| `setup.sh` | Clones the pinned upstream revisions, applies `patches/`, builds, runs the unit tests and, optionally, builds the images |

## Requirements

- Linux x86-64 with git, curl, Python 3.8+ (with matplotlib for `plots/`) and Docker with Compose v2.
- Go 1.26.8 or later; `setup.sh` installs Go 1.27.1 under the work directory if none is found.
- Node.js with `solc` 0.8.20 only to recompile the contracts (the compiled artifacts are included).
- Hardware used in the paper: one server with 28 cores (56 hardware threads) and 192 GB of memory for all
  single-server experiments; five servers for the wide-area deployment (Section VIII-G).
- Disk: a few gigabytes for the sources, the Go module cache, the tools and the datasets; the Docker images need
  more.

## Getting started (about 30 minutes)

```bash
./setup.sh ~/epr-work            # clone, patch, build, unit tests (add --images for the Docker images)
./gen_datasets.sh ~/epr-work     # every dataset of the paper, checked against data/datasets.md5
```

`setup.sh` clones the Fabric-X committer at tag v1.0.3 and fabric-x-evm at commit `c42a92c`, applies the
patches, builds both, and runs the unit tests of the patched packages:

- `service/coordinator/dependencygraph/...`: escrow classification, the FabricSharp, Fabric++ and WARD
  integration tests, and the original unit tests of FabricSharp, which run unchanged on the ported packages;
- `service/vc`: decoding and evaluation of signed content, value prefetch, early gossip, stale reads decided per
  transaction (a batch and any split of it abort the same readers) and zero storage words stored as endorsed;
- `endorser/execution` and `endorser/storage`: the differential tests of Section VIII-I (extracted content
  compared with re-execution on perturbed states for USDC, USDT, Uniswap-V2-style swaps, the DEX router and
  SmallBank), the cross-check of the evaluator against go-ethereum's `uint256` arithmetic, the gas-headroom
  rule (including its counterexamples), the GAS-marker, CREATE init-code and call-input checks of the program tracer, the
  acceptance of every transaction of the evaluated workloads by these rules, and the encoding-determinism tests
  (repeated extractions sign identical bytes).

Every package ends with an `ok` line.

## Reproducing every figure, table and number from the included logs

The scripts read only `results/` and run in a few minutes without a server (`pip install matplotlib`).

| Paper item | Command | Output |
|---|---|---|
| Figs. 1, 7-13 (all evaluation figures) | `python analysis/paper_numbers.py --figs` | `results/figures/fig_{motivation,overall,sweep_lin,skew,modes,mech,ablation,order}.pdf` (Figs. 1, 7, 8, 9, 10, 11, 12, 13) |
| Numbers of the abstract and of Sections I and VIII-C to VIII-G | `python analysis/paper_numbers.py` | one line per number: the phrase of the paper that states it, with the value as printed, and the data behind it |
| Section VIII-B, five runs of each peak of Fig. 7 | `python analysis/repetitions.py` | coefficient of variation of EPR and of the strongest baseline, EPR's lead in mean throughput |
| Table III | `python analysis/table2.py results/coverage/cov_mainnet.jsonl.gz` | the five rows of Table III |
| Section VIII-H, deltas and programs among the repairable writes | `python analysis/table2_split.py results/coverage/cov_mainnet.jsonl.gz` | the share of guarded deltas per row and for USDT and USDC |
| Section VIII-I, correctness | `results/raw/*` lines `D16ORACLE` (serial-equivalence oracle), `results/raw/q1103/s10_mismatch.txt` (two organizations: endorsements that read the same versions) | |
| Section VIII-I, mainnet differential test (2,588 contracts, 73.9k perturbed states where the guards hold) | `experiments/e1/e1_run.sh` (extraction and perturbation on the mainnet sample), `experiments/e1/e1_summary.py` | `results/coverage/E1_SUMMARY.txt` |
| Tracer rules on the mainnet sample and the evaluated workloads | `results/coverage/tracer_checks.txt` | unit tests, workload acceptance and Table III of the final tracers |

The figures in `results/figures/` are the output of the first command and are identical to the figures of the
paper.

## Re-running the experiments

`experiments/` holds the drivers of the runs in `results/raw/`. They expect the work
directory layout of `setup.sh` and an `env.sh` defining `D` (the work directory); for the five-server deployment
in `experiments/multinode/`, set `NET_PREFIX` to the first three octets of the servers' addresses (the scripts
name each server by its last octet). One run starts the complete stack (database,
orderer or Arma, committer services, endorsers, gateway and the open-loop client), replays a dataset at a fixed
offered rate and prints one result line with throughput, latencies, commit share and the checks of the run.

| Experiment (paper item) | Drivers | Logs |
|---|---|---|
| EPR runs: rate sweeps with and without retries, skews, USDT, ablation, block cutting, 600k datasets (Figs. 7-10, 12, 13a) | `queue_1104.sh`, `queue_extra_1104/30_s7a.sh`, `40_s7b.sh`, `70_s7c.sh`, `38_s9.sh`, `92_s15e.sh`, library `queue_extra_1104/s7lib.inc` | `q1103/q1103_s7a.txt`, `q1103_s7b.txt`, `q1103_s7c.txt`, `q1103_s9.txt`, `q1103_s15e.txt` |
| Baselines: rate sweeps, ablation references, 600k datasets (Figs. 7-10, 12) | `phase38.sh`, `phase40.sh` (USDT), `phase45.sh`, `phase46b.sh`, `phase46c.sh` (skews), `phase20.sh`-`phase34.sh` (FabricSharp, Fabric++, Depgraph, Fabric-X) | `phase38.txt`, `phase40.txt`, `phase45.txt`, `phase46b.txt`, `phase46c.txt`, `phase20.txt`-`phase34.txt` |
| Block cutting, baselines (Fig. 13a) | `phase39.sh`, `phase39x.sh` | `phase39.txt`, `phase39x.txt` |
| Five servers with Arma BFT ordering and WAN round trips (Fig. 13b-d) | `multinode/phase44_mn.sh`, `phase44w.sh`, `multinode/mn_*.sh` | `phase44.txt`, `phase44_w.txt`, `p44/`, `p44_w/` |
| Five runs of each peak (Section VIII-B) | `queue_extra_1104/89b_rep.sh`, `55_s11.sh`, `57_s11u.sh` | `q1103/q1103_rep.txt`, `q1103_s11.txt`, `q1103_s11u.txt` |
| Endorsements per committed transaction (Fig. 11a) | `queue_extra_1104/37_s8.sh` | `q1103/q1103_s8.txt` |
| Commit-path cost (Fig. 11b): re-execution service time per transaction, and the extra prefetching tried for the baselines (Section VIII-B) | `queue_extra_1104/88b_ef.sh`, `89f_ef2.sh`, `88a_eg.sh` | `q1103/q1103_ef.txt`, `q1103_ef2.txt` (lines `D16REEXEC ... avg_us_per_tx`), `q1103_eg.txt` |
| Commit-path cost (Fig. 11b): EPR's evaluation of a delta, a transfer and a router program | `go test -run X -bench BenchmarkEPRDeltaEval ./service/vc` (committer), `go test -run X -bench BenchmarkEPRProgEval ./endorser/execution` (fabric-x-evm) | the values used by `plots/` are the constants `COST` in `plots/fig_layout1104.py` |
| Tight funding (Section VIII-D) | `queue_extra_1104/88c_s16b.sh`, `prefund_tight/` | `q1103/q1103_s16b.txt` |
| Two endorsing organizations, 2-of-2 (Section VIII-D) | `queue_extra_1104/50_s10.sh`, `65_s10b.sh`, `82_s10c.sh`, `twoorg/`, `twoorg_r/`, `s10_mismatch.py` | `q1103/q1103_s10*.txt`, `q1103/s10_mismatch.txt` |
| HTFabric with the snapshot rule for queries, SmallBank (Section VIII-C) | `queue_extra_1104/89a_ec.sh` | `q1103/q1103_ec.txt` (label `htf2ro`) |
| Ablation with the snapshot-query rule off (Section VIII-F) | `queue_extra_1104/89b_ed.sh` | `q1103/q1103_ed.txt` |
| Coordinator crash (Section VIII-I) | `queue_extra_1104/60_s12.sh`, `exp_restart4.sh` | printed by the driver: the final-state digests of the crashed run and of an uninterrupted run (lines `DIGEST`) |
| Uniform workloads (Section VIII-E) | EPR: `queue_extra_1104/30_s7a.sh`, `40_s7b.sh`; baselines: `phase46c.sh`, `phase38.sh` | labels `z0`, `sb0` (60k) and `z0M`, `sb0M` (1M, without retries) in `q1103/q1103_s7a.txt`, `q1103_s7b.txt`, `phase46c.txt`, `phase38.txt` |
| Unguarded counters (Section VIII-E) | `phase38.sh` and the SmallBank balance check `srv_sbcheck.sh` | runs labelled `ng` in `phase38.txt` and the related logs |
| Baseline re-measurements on the final harness and their repeats | `queue_extra_1104/80_s14.sh`, `83_s14r.sh`, `85_s15a.sh`, `90_s15b.sh` | `q1103/q1103_s14*.txt`, `q1103_s15a.txt`, `q1103_s15b.txt` |

Shared pieces: `srv_run_evm6w.sh` and its variants run one experiment, `sweep_lib.sh`, `refine_points.py` and
`peak_rates.py` implement the peak search of Section VIII-B (raise the rate until the criterion fails, search
downward if the first rate fails, then refine around the best sustained rate, for all schemes alike), and
`srv_statecheck*.sh`, `srv_sbcheck*.sh` check supply, balances and encoded content after each run. Each run
prints its label, which encodes the mode (`rk` with retries, `pk` without), the workload, the scheme and the
offered rate, e.g. `rk_usdc_eprzw_r6500`.

**Code versions.** Every transaction of the evaluated workloads is accepted by the tracer rules in `src/`
(`results/coverage/tracer_checks.txt`); Table III is computed with the code in `src/`.

## Baselines

All baselines run in the same Fabric-X stack and replay harness as EPR, on the same datasets and genesis
state. Each is selected by environment variables of the committer and options of the harness; the experiment
scripts set them, and the scheme part of a run label names it.

| # | Baseline | Implementation | Selected by | Label |
|---|---|---|---|---|
| 1 | Fabric-X (FIFO gateway, plain EOV) | Unmodified committer v1.0.3, built with the same toolchain; fabric-x-evm's FIFO gateway queue | harness option `-oldqueue` | `fifo` |
| 2 | Depgraph | Unmodified committer v1.0.3; fabric-x-evm's dependency-graph gateway queue | harness option `-depgraph` | `depg` |
| 3 | FabricSharp | The authors' scheduler (`sharpscheduler`, `common`), ported unchanged into the coordinator; endorsers carry the height of their snapshot | `FX_FABRICSHARP=1`, `TXN_SPAN_LIMIT` (max_span, default 10) | `fs` |
| 4 | Fabric++ | The Fabric++ scheduler shipped with FabricSharp (`fppscheduler`), ported unchanged into the coordinator | `FX_REORDER=1` | `fpp` |
| 5 | HTFabric | WARD reordering of every block in the coordinator; re-execution of every invalid transaction in TSP partitions (as many partitions as hardware threads) with fabric-x-evm's executor on the latest committed state; early gossip of the transactions that need no re-execution | `FX_WARD=1`, `FX_EARLY_GOSSIP=1`, re-execution with `-parallel=-1` | `htf2` |
| 6 | XOX | Re-execution of every invalid transaction in turn with fabric-x-evm's executor on the latest committed state | re-execution with `-parallel=0` | `rxo2` |
| 7 | Unguarded counters | EPR's deltas applied without their guards, as a CRDT-based design would merge them | `FX_EPR_NOGUARD=1` | `ng` |

EPR's runs are labelled `eprzw` (full EPR), `epr1w` (ordered repair) and `epr12w`
(ordered repair and commutative credits) for the ablation.

HTFabric and XOX re-execute next to the validator, inside the committer container, with the endorsers' own EVM
executor (`src/fabric-x-evm/d16reexec_v2`), prefetching each batch's read set in one query and caching contract
code; the VC side is `src/fabric-x-committer/service/vc/epr_reexec.go`. Endorsers attach each raw
transaction to its write set so that the committer can re-execute it, and the VC removes it before commit.
In these runs the VC also evaluates the endorsed content of the re-executed transactions and counts every
difference from the re-executed values, which provides the cross-check of Section VIII-I.

**Original sources, port edits and original unit tests.** `baselines/orig_src/` holds the original
FabricSharp (branch `sigmod20`) and Fabric++ (shipped with FabricSharp, branch `master`) sources verbatim.
`baselines/port_orig_baselines.py` derives the ported packages from them; every textual edit is listed in the
script and must match exactly once. Its output, formatted with `gofmt`, is
`src/fabric-x-committer/service/coordinator/dependencygraph/fabricsharp/`. The original unit tests
(`common/ds_test.go`, `common/mv_store_test.go`, `sharpscheduler/scheduler_test.go`) are ported unchanged and
pass on the ports (`go test ./service/coordinator/dependencygraph/fabricsharp/...`). See
`baselines/README.md` for details.

## Code map

Paths are relative to `src/fabric-x-committer/` (committer) and `src/fabric-x-evm/` (EVM layer). Files marked
(patch) are upstream files that the patches modify; their changes are in `patches/`.

| Component (paper section) | Files |
|---|---|
| Guarded-delta extraction (V-A) | `endorser/execution/epr_tracer.go` |
| Guarded-program extraction (V-B) | `endorser/execution/epr_prog_tracer.go`, `epr_lazy.go`; hook in `endorser/execution/executor.go` (patch) |
| Gas-headroom rule (V-A, V-B) | `endorser/execution/epr_gas.go` |
| Program encoding and evaluator, shared by endorsers and committers (V-B, VIII-A) | `endorser/execution/eprprog_core.go`; committer copy `service/vc/epr_prog_core.go` |
| Ordered repair, endorsed fast path, queries at their snapshot (VI-A, VI-D) | `service/vc/epr_pilot.go`, `service/vc/epr_formula.go`; `service/vc/validator.go` (patch) |
| Concurrent value fetch (VI-A) | `service/vc/epr_prefetch.go` |
| Repair stage (VI-A) | `service/vc/epr_pipeline.go`; `service/vc/validator.go` (patch) |
| Commutative credits (VI-B) | `service/coordinator/dependencygraph/transaction_node.go`, `dependency_detector.go` (patch); increments in `service/vc/epr_pilot.go`, `database.go`, `committer.go` (patch) |
| Escrow classifier (VI-C) | `service/coordinator/dependencygraph/epr_escrow.go`; `local_dependency_constructor.go`, `global_dependency_manager.go` (patch) |
| Replica application of signed content (IV) | `endorser/storage/lightkvs.go` (patch) |
| Harness: genesis funding, open-loop load, client retries, supply check, order log (VIII-A, VIII-B) | `integration/perf/replay_json_dataset_test.go` (patch), `integration/perf/epr_orderlog_test.go`, `endorser/testimpl/` (patch), `d16prefund/` |
| Harness fixes reported upstream (VIII-A) | `gateway/core/batch_submitter.go`, `gateway/core/depgraph_queue.go`, `endorser/execution/statedb.go` (patch) |
| Workload generators (VIII-B) | `d16smallbank/`, `d16dex/` |
| Serial-equivalence oracle (VIII-I) | `d16oracle/` |
| Mainnet coverage study (VIII-H) | `d16coverage/`, `endorser/execution/epr_diag.go` |
| Baselines (VIII-B) | `service/coordinator/dependencygraph/epr_fabricsharp*.go`, `epr_fpp_orig.go`, `epr_reorder.go`, `epr_ward.go`, `fabricsharp/`; `service/vc/epr_reexec.go`; `d16reexec_v2/` |
| Two endorsing organizations (VIII-D) | `patches/fabric-x-evm-two-org.patch` (`integration/test_helpers.go`, `integration/perf/`, `gateway/core/batch_submitter.go`) |
| Unit and differential tests | `*_test.go` next to the code they test |

## Datasets

The workloads of Section VIII-B: 151,045 real Ethereum USDC transfers from January 2020, 88,879 real USDT
transfers from 2,000 consecutive mainnet blocks (late 2025), Zipf transfers, SmallBank and a Uniswap-V2-style
DEX. `data/` holds the two real traces; `gen_datasets.sh` builds every dataset and genesis file with the
deterministic generators of `src/` (`d16smallbank`, `d16dex`, the Zipf generator of the harness, `d16prefund`)
and checks each against `data/datasets.md5`. See `data/README.md` for sources and filtering rules.

## Upstream versions and license

| Component | Repository | Revision |
|---|---|---|
| Fabric-X committer | `github.com/hyperledger/fabric-x-committer` | tag v1.0.3 (`26effd896244d6bc9fc76e5df7f8e887ba793cdb`) |
| fabric-x-evm | `github.com/hyperledger/fabric-x-evm` | `c42a92c6892d3eafb448cda78ef84e06b9056f93` |

This repository is licensed under the Apache License 2.0 (`LICENSE`), the license of the Fabric-X committer.
The Go code that `patches/fabric-x-evm-*.patch` adds to or changes in fabric-x-evm (also in
`src/fabric-x-evm*/`) is licensed, like fabric-x-evm's Go code, under LGPL-3.0-or-later (`LICENSE.LGPL3`). The
original FabricSharp and Fabric++ sources in `baselines/orig_src/` keep their license (Apache-2.0).
