// Command d16prefund writes a state-priming JSON in which every sender of a replay dataset holds a real
// USDC balance at genesis. Read-time balance priming fakes reads of zero balances, which
// hides the true base value from both MVCC and guarded deltas; funding senders in the genesis state
// gives baseline and EPR the same, real starting state instead.
//
// usage: go run ./d16prefund <contract.json> <dataset.json.gz> <out.json> <balance-decimal>
package main

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"math/big"
	"os"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/crypto"
)

const usdcBalanceSlot = 9 // FiatTokenV2_2 balanceAndBlacklistStates mapping position

func main() {
	if len(os.Args) != 5 {
		fmt.Fprintln(os.Stderr, "usage: d16prefund <contract.json> <dataset.json.gz> <out.json> <balance>")
		os.Exit(2)
	}
	var state map[string]map[string]any
	raw, err := os.ReadFile(os.Args[1])
	must(err)
	must(json.Unmarshal(raw, &state))

	f, err := os.Open(os.Args[2])
	must(err)
	zr, err := gzip.NewReader(f)
	must(err)
	var transfers []struct {
		Transaction []byte `json:"Transaction"`
		Value       string `json:"Value"`
	}
	must(json.NewDecoder(zr).Decode(&transfers))

	bal, ok := new(big.Int).SetString(os.Args[4], 10)
	if !ok || bal.Sign() <= 0 || bal.BitLen() > 255 {
		panic("bad balance")
	}
	// Each sender holds the base balance plus everything it ever sends, so no transfer can fail for lack
	// of funds in any order (the replay lacks the pre-dataset history).
	senders := map[common.Address]*big.Int{}
	for i, tr := range transfers {
		tx := new(types.Transaction)
		must(tx.UnmarshalBinary(tr.Transaction))
		from, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			panic(fmt.Sprintf("transfer %d: %v", i, err))
		}
		v, ok := new(big.Int).SetString(tr.Value, 10)
		if !ok {
			panic(fmt.Sprintf("transfer %d: bad value %q", i, tr.Value))
		}
		if senders[from] == nil {
			senders[from] = new(big.Int).Set(bal)
		}
		senders[from].Add(senders[from], v)
	}

	var usdcKey string
	for k := range state {
		if common.HexToAddress(k) == common.HexToAddress("0xA0b86991c6218b36c1d19d4a2e9eb0ce3606eb48") {
			usdcKey = k
		}
	}
	if usdcKey == "" {
		panic("USDC proxy not found in contract json")
	}
	storage, _ := state[usdcKey]["storage"].(map[string]any)
	if storage == nil {
		storage = map[string]any{}
	}
	slot := common.BigToHash(big.NewInt(usdcBalanceSlot))
	maxBal := new(big.Int)
	for a, b := range senders {
		if b.Cmp(new(big.Int).SetUint64(1<<63-1)) > 0 {
			panic(fmt.Sprintf("sender %s balance %s exceeds int64", a, b))
		}
		if b.Cmp(maxBal) > 0 {
			maxBal.Set(b)
		}
		k := crypto.Keccak256Hash(common.LeftPadBytes(a.Bytes(), 32), slot.Bytes())
		storage[k.Hex()] = common.BigToHash(b).Hex()
	}
	state[usdcKey]["storage"] = storage
	out, err := json.MarshalIndent(state, "", "  ")
	must(err)
	must(os.WriteFile(os.Args[3], out, 0o644))
	fmt.Printf("prefunded %d senders with %s (storage entries %d)\n", len(senders), bal, len(storage))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
