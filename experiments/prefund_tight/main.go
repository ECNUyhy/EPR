// Command d16prefund_tight (11/05, stage S16) writes the tight-funding variant of d16prefund's genesis: every
// account holds exactly the least balance that lets all its transfers succeed in ledger (dataset) order, so balances
// reach zero, instead of 10^18 plus everything it ever sends. Debits and credits are decoded from the calldata:
// transfer(to, v) debits the signer, transferFrom(from, to, v) debits from; any other call debits the signer by the
// dataset's Value (as d16prefund assumes).
//
// usage: go run ./d16prefund_tight <contract.json> <dataset.json.gz> <out.json>
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
	if len(os.Args) != 4 {
		fmt.Fprintln(os.Stderr, "usage: d16prefund_tight <contract.json> <dataset.json.gz> <out.json>")
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

	bal := map[common.Address]*big.Int{}  // running balance in ledger order, starting at 0
	need := map[common.Address]*big.Int{} // least funding: -min(running balance)
	get := func(m map[common.Address]*big.Int, a common.Address) *big.Int {
		if m[a] == nil {
			m[a] = new(big.Int)
		}
		return m[a]
	}
	var nTransfer, nFrom, nOther int
	for i, tr := range transfers {
		tx := new(types.Transaction)
		must(tx.UnmarshalBinary(tr.Transaction))
		signer, err := types.Sender(types.LatestSignerForChainID(tx.ChainId()), tx)
		if err != nil {
			panic(fmt.Sprintf("transfer %d: %v", i, err))
		}
		d := tx.Data()
		var from, to common.Address
		var v *big.Int
		hasTo := false
		switch {
		case len(d) >= 68 && d[0] == 0xa9 && d[1] == 0x05 && d[2] == 0x9c && d[3] == 0xbb:
			from, to, v, hasTo = signer, common.BytesToAddress(d[4:36]), new(big.Int).SetBytes(d[36:68]), true
			nTransfer++
		case len(d) >= 100 && d[0] == 0x23 && d[1] == 0xb8 && d[2] == 0x72 && d[3] == 0xdd:
			from, to, v, hasTo = common.BytesToAddress(d[4:36]), common.BytesToAddress(d[36:68]), new(big.Int).SetBytes(d[68:100]), true
			nFrom++
		default:
			var ok bool
			v, ok = new(big.Int).SetString(tr.Value, 10)
			if !ok {
				panic(fmt.Sprintf("transfer %d: bad value %q", i, tr.Value))
			}
			from = signer
			nOther++
		}
		b := get(bal, from)
		b.Sub(b, v)
		if b.Sign() < 0 && new(big.Int).Neg(b).Cmp(get(need, from)) > 0 {
			need[from] = new(big.Int).Neg(b)
		}
		if hasTo {
			c := get(bal, to)
			c.Add(c, v)
		}
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
	total, maxBal := new(big.Int), new(big.Int)
	funded := 0
	for a, n := range need {
		if n.Sign() == 0 {
			continue
		}
		if n.Cmp(new(big.Int).SetUint64(1<<63-1)) > 0 {
			panic(fmt.Sprintf("account %s needs %s, exceeds int64", a, n))
		}
		total.Add(total, n)
		if n.Cmp(maxBal) > 0 {
			maxBal.Set(n)
		}
		k := crypto.Keccak256Hash(common.LeftPadBytes(a.Bytes(), 32), slot.Bytes())
		storage[k.Hex()] = common.BigToHash(n).Hex()
		funded++
	}
	state[usdcKey]["storage"] = storage
	out, err := json.MarshalIndent(state, "", "  ")
	must(err)
	must(os.WriteFile(os.Args[3], out, 0o644))
	fmt.Printf("TIGHT transfers=%d transferFrom=%d other=%d funded=%d total=%s max=%s storage=%d\n",
		nTransfer, nFrom, nOther, funded, total, maxBal, len(storage))
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
