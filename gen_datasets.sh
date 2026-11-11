#!/usr/bin/env bash
# Generate every dataset and genesis file used in the paper (deterministic generators and seeds).
#   ./gen_datasets.sh [WORKDIR]       (run ./setup.sh first)
# Real traces: the filtered USDC (Jan 2020) and USDT transfer lists are in data/. Contents are checked against data/datasets.md5.
set -euo pipefail
ART=$(cd "$(dirname "$0")" && pwd)
D=$(realpath -m "${1:-$HOME/epr-work}")
. "$D/env.sh"
S=$D/data/synth; X=$D/data/smallbank; Y=$D/data/dex; T=$D/data/usdt
mkdir -p "$S" "$X" "$Y" "$T"
for f in SmallBank.json D16Router.json; do [ -f "$D/data/$f" ] || cp "$ART/contracts/$f" "$D/data/"; done
cd "$D/src/fabric-x-evm"
TD=integration/perf/testdata
prefund() { go run ./d16prefund $TD/USDC_contract.json "$1" "$2" 1000000000000000000 | tail -1; }

echo "== USDC (real, 151k transfers of January 2020; filtered from the Harvard Dataverse token-transfer dump)"
if [ ! -f $TD/USDC_dataset.json.gz ]; then
  cp "$ART/data/usdc-202001-dataset.tsv.gz" $TD/dataset.tsv.gz
  go run -tags=perf ./integration/perf -mode generate -input $TD/dataset.tsv.gz > "$D/logs/gen_usdc.log" 2>&1
fi
[ -f "$D/data/USDC_genesis_151k.json" ] || prefund $TD/USDC_dataset.json.gz "$D/data/USDC_genesis_151k.json"

echo "== synthetic Zipf transfers"
gen_z() {  # theta n tag
  [ -f "$S/zipf_$3.json.gz" ] && return
  python3 "$ART/experiments/synth_tsv.py" "$S/zipf_$3.tsv.gz" 10000 "$2" "$1"
  go run -tags=perf ./integration/perf -mode generate -input "$S/zipf_$3.tsv.gz" -output "$S/zipf_$3.json.gz" > "$S/gen_$3.log" 2>&1
  prefund "$S/zipf_$3.json.gz" "$S/genesis_$3.json"
  rm -f "$S/zipf_$3.tsv.gz"
}
for th in 0.8 0.99; do gen_z $th 60000 $th; gen_z $th 600000 ${th}L; done
gen_z 0 1000000 0M

echo "== SmallBank"
gen_sb() {  # theta n tag
  [ -f "$X/sb_t$3.json.gz" ] || go run ./d16smallbank "$D/data/SmallBank.json" $TD/USDC_dataset_10k.json.gz \
    "$X/genesis_t$3.json" "$X/sb_t$3.json.gz" 100000 "$2" "$1" 2000 | tail -1
}
for th in 0.8 1.0 1.2; do gen_sb $th 60000 $th; gen_sb $th 600000 ${th}L; done
gen_sb 0 1000000 0M

echo "== DEX"
for P in 1 16 64; do
  [ -f "$Y/swaps_p$P.json.gz" ] || go run ./d16dex "$D/data/D16Router.json" $TD/USDC_dataset_10k.json.gz \
    "$Y/genesis_p$P.json" "$Y/swaps_p$P.json.gz" 5000 30000 $P 0 50 | tail -1
done

echo "== USDT (real, 88,879 transfers)"
if [ ! -f "$T/usdt.json.gz" ]; then
  gunzip -c "$ART/data/usdt-range-2000blk.csv.gz" > "$T/usdt.csv"
  python3 "$ART/experiments/usdt_tsv.py" "$T/usdt.csv" "$T/usdt.tsv.gz"
  go run -tags=perf ./integration/perf -mode generate -input "$T/usdt.tsv.gz" -output "$T/usdt.json.gz" > "$T/gen.log" 2>&1
  prefund "$T/usdt.json.gz" "$T/genesis.json"
fi
echo "== verifying contents against the datasets used in the paper"
cd "$D"; bad=0
while read -r sum f; do
  [ "$(zcat -f "$f" | md5sum | cut -d' ' -f1)" = "$sum" ] || { echo "MISMATCH $f"; bad=1; }
done < "$ART/data/datasets.md5"
[ $bad = 0 ] && echo "all $(wc -l < "$ART/data/datasets.md5") datasets identical to the paper's"
echo GEN_DATASETS_DONE
