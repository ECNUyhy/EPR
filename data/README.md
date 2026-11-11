# Datasets

| File | Contents |
|---|---|
| `usdc-202001-dataset.tsv.gz` | 151,045 USDC transfers of January 2020 among 53,218 accounts, filtered from the Harvard Dataverse dump of Ethereum ERC-20 token transfers; fabric-x-evm's generator turns them into signed Ethereum transactions |
| `usdt-range-2000blk.csv.gz` | 88,879 USDT transfers of 2,000 consecutive Ethereum mainnet blocks (late 2025); `experiments/usdt_tsv.py` converts them to the generator's input. As in the paper, they are replayed in order on the USDC contract |
| `datasets.md5` | Checksums of every dataset and genesis file of the paper |

`../gen_datasets.sh WORKDIR` (after `../setup.sh WORKDIR`) regenerates every dataset: the two real traces, the
Zipf transfers (60k and 600k, every skew of the paper, and the 1M uniform set) over 10k accounts, SmallBank (six
procedures over 100k customers, 60k and 600k transactions, every skew, and the 1M uniform set) and the DEX (30k
swaps through the router against 1, 16 and 64 pairs, 5k users), each with its genesis state, and checks each
file against `datasets.md5`. The generators are deterministic (fixed seeds). The access pattern of the real
traces (senders, recipients, amounts and order) is the original one.
