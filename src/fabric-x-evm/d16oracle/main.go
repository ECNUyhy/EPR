// Command d16oracle checks serial equivalence end to end (Theorem 1): it replays the transactions that
// the ledger marks valid, serially in ledger order, on the genesis state in an in-memory EVM, and compares
// every contract storage slot with the committer's database. Every valid transaction must also succeed
// in the replay (no revert).
//
// usage: d16oracle <genesis.json> <order.log> <db_dump.tsv> [chain-id]
//
//	order.log: "block txnum valid rawhex" per EVM transaction (FX_ORDER_LOG of the perf harness)
//	db_dump.tsv: "key<TAB>valuehex" for the storage keys of namespace basic (psql export)
package main

import (
	"bufio"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math/big"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"

	fxcommon "github.com/hyperledger/fabric-x-evm/common"
)

type allocEntry struct {
	Code    string            `json:"code"`
	Storage map[string]string `json:"storage"`
}

type entry struct {
	block, num uint64
	raw        []byte
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) < 4 {
		fmt.Fprintln(os.Stderr, "usage: d16oracle <genesis.json> <order.log> <db_dump.tsv> [chain-id]")
		os.Exit(2)
	}
	chain := int64(4011)
	if len(os.Args) > 4 {
		chain, _ = strconv.ParseInt(os.Args[4], 10, 64)
	}
	cfg := fxcommon.BuildChainConfig(chain)
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	must(err)
	raw, err := os.ReadFile(os.Args[1])
	must(err)
	var alloc map[string]allocEntry
	must(json.Unmarshal(raw, &alloc))
	for a, e := range alloc {
		addr := common.HexToAddress(a)
		sdb.SetCode(addr, common.FromHex(e.Code), 0)
		for k, v := range e.Storage {
			sdb.SetState(addr, common.HexToHash(k), common.HexToHash(v))
		}
	}

	f, err := os.Open(os.Args[2])
	must(err)
	var txs []entry
	seen := map[[2]uint64]bool{}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	invalid := 0
	for sc.Scan() {
		p := strings.Fields(sc.Text())
		if len(p) != 4 {
			continue
		}
		b, _ := strconv.ParseUint(p[0], 10, 64)
		n, _ := strconv.ParseUint(p[1], 10, 64)
		if p[2] != "1" {
			invalid++
			continue
		}
		if seen[[2]uint64{b, n}] {
			continue // the harness may see a block more than once
		}
		seen[[2]uint64{b, n}] = true
		r, err := hex.DecodeString(p[3])
		must(err)
		txs = append(txs, entry{b, n, r})
	}
	sort.Slice(txs, func(i, j int) bool {
		if txs[i].block != txs[j].block {
			return txs[i].block < txs[j].block
		}
		return txs[i].num < txs[j].num
	})

	ctx := vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, BlockNumber: big.NewInt(1),
		Time: 1_800_000_000, GasLimit: 1 << 40, Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
		GetHash: func(uint64) common.Hash { return common.Hash{} }}
	signer := types.LatestSignerForChainID(big.NewInt(chain))
	reverted := 0
	touched := map[common.Address]map[common.Hash]bool{}
	for _, e := range txs {
		tx := new(types.Transaction)
		must(tx.UnmarshalBinary(e.raw))
		if tx.To() == nil {
			continue // the harness's state-priming transaction; the genesis is loaded directly
		}
		from, err := types.Sender(signer, tx)
		must(err)
		evm := vm.NewEVM(ctx, sdb, cfg, vm.Config{})
		evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
		_, _, err = evm.Call(from, *tx.To(), tx.Data(), vm.NewGasBudget(tx.Gas()), uint256.MustFromBig(tx.Value()))
		if err != nil {
			reverted++
		}
		if touched[*tx.To()] == nil {
			touched[*tx.To()] = map[common.Hash]bool{}
		}
	}

	// compare every storage slot in the database with the replayed state
	d, err := os.Open(os.Args[3])
	must(err)
	sc = bufio.NewScanner(d)
	sc.Buffer(make([]byte, 1<<20), 1<<24)
	compared, mismatched, skipped := 0, 0, 0
	var examples []string
	// D16_ORACLE_SKIP: comma-separated small slot indices to ignore (Uniswap V2 pairs store block.timestamp
	// in slots 8-10, which depends on the endorsement time and cannot be reproduced by a replay)
	skip := map[common.Hash]bool{}
	for _, s := range strings.Split(os.Getenv("D16_ORACLE_SKIP"), ",") {
		if n, err := strconv.ParseUint(strings.TrimSpace(s), 10, 64); err == nil {
			skip[common.BigToHash(new(big.Int).SetUint64(n))] = true
		}
	}
	for sc.Scan() {
		p := strings.SplitN(sc.Text(), "\t", 2)
		if len(p) != 2 || !strings.HasPrefix(p[0], "str:") {
			continue
		}
		parts := strings.Split(p[0], ":") // str:<addr>:<slot>
		if len(parts) != 3 {
			continue
		}
		addr, slot := common.HexToAddress(parts[1]), common.HexToHash(parts[2])
		if skip[slot] {
			skipped++
			continue
		}
		val, err := hex.DecodeString(p[1])
		must(err)
		dbv := new(big.Int).SetBytes(val)
		ev := sdb.GetState(addr, slot).Big()
		compared++
		if dbv.Cmp(ev) != 0 {
			mismatched++
			if len(examples) < 5 {
				examples = append(examples, fmt.Sprintf("%s db=%s replay=%s", p[0], dbv, ev))
			}
		}
	}
	fmt.Printf("D16ORACLE valid_txs=%d invalid_txs=%d replay_reverted=%d slots_compared=%d mismatched=%d skipped=%d\n",
		len(txs), invalid, reverted, compared, mismatched, skipped)
	for _, x := range examples {
		fmt.Println("  mismatch:", x)
	}
	if mismatched > 0 || reverted > 0 {
		os.Exit(1)
	}
}
