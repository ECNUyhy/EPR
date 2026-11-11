# Experiments

The drivers of the runs in `../results/raw/`. The main README lists, for every experiment
of the paper, its drivers and logs, and explains the run labels. `srv_run_evm6w.sh` and its variants run one
experiment on one server; `multinode/` deploys the complete Fabric-X network with the four-party Arma BFT
orderer on five servers and adds round trips with netem; `twoorg/` and `twoorg_r/` add the second endorsing
organization; `prefund_tight/` builds the tight-funding genesis state; `config/` holds the compose files. The
scripts expect the work directory of `../setup.sh` in `$D` (set in `env.sh`) and the datasets of
`../gen_datasets.sh`; host names and paths of our servers appear as variables at the top of each script.
