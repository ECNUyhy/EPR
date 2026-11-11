// Command d16smallbank generates a SmallBank workload for fabric-x-evm's perf harness (evaluation workload):
// a genesis state (the SmallBank contract with every account funded) and signed transactions in the
// harness's dataset format.
//
// Mix (H-Store SmallBank defaults): Amalgamate 15%, Balance 15%, DepositChecking 15%, SendPayment 25%,
// TransactSavings 15%, WriteCheck 15%. Accounts are drawn from Zipf(theta) over <accounts> customers
// (theta = 0 is uniform); two-account transactions draw two distinct accounts, except that Amalgamate's
// source account (which it empties) is uniform.
//
// usage: go run ./d16smallbank <smallbank.json> <chain-id-source-dataset.json.gz> <out-genesis.json>
//
//	<out-dataset.json.gz> <accounts> <txs> <theta> <users> [seed]
package main

import (
	"compress/gzip"
	"crypto/ecdsa"
	"encoding/json"
	"fmt"
	"math"
	"math/big"
	"math/rand"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/types"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

type allocEntry struct {
	Balance string            `json:"balance"`
	Code    string            `json:"code"`
	Nonce   string            `json:"nonce"`
	Storage map[string]string `json:"storage"`
}

type record struct {
	BlockID         int    `json:"BlockID"`
	TransactionHash string `json:"TransactionHash"`
	TokenAddress    string `json:"TokenAddress"`
	Sender          string `json:"Sender"`
	Recipient       string `json:"Recipient"`
	Value           string `json:"Value"`
	Transaction     []byte `json:"Transaction"`
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}

func main() {
	if len(os.Args) < 9 {
		fmt.Fprintln(os.Stderr, "usage: d16smallbank <smallbank.json> <chainsrc.json.gz> <genesis.json> <dataset.json.gz> <accounts> <txs> <theta> <users> [seed]")
		os.Exit(2)
	}
	nAcc, _ := strconv.Atoi(os.Args[5])
	nTx, _ := strconv.Atoi(os.Args[6])
	theta, _ := strconv.ParseFloat(os.Args[7], 64)
	nUsers, _ := strconv.Atoi(os.Args[8])
	seed := int64(16)
	if len(os.Args) > 9 {
		seed, _ = strconv.ParseInt(os.Args[9], 10, 64)
	}
	rng := rand.New(rand.NewSource(seed))

	var art struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	raw, err := os.ReadFile(os.Args[1])
	must(err)
	must(json.Unmarshal(raw, &art))
	sbABI, err := abi.JSON(strings.NewReader(string(art.ABI)))
	must(err)
	f, err := os.Open(os.Args[2])
	must(err)
	zr, err := gzip.NewReader(f)
	must(err)
	var src []record
	must(json.NewDecoder(zr).Decode(&src))
	first := new(types.Transaction)
	must(first.UnmarshalBinary(src[0].Transaction))
	chainID := first.ChainId()
	signer := types.LatestSignerForChainID(chainID)

	// in-memory deployment; every SSTORE is recorded so the genesis can carry the resulting storage
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	must(err)
	touched := map[common.Hash]bool{}
	hooks := &tracing.Hooks{OnOpcode: func(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
		if op == 0x55 {
			st := scope.StackData()
			touched[common.Hash(st[len(st)-1].Bytes32())] = true
		}
	}}
	ctx := vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, BlockNumber: big.NewInt(30_000_000),
		Time: 1_800_000_000, GasLimit: 1 << 40, Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{}}
	evmFor := func(from common.Address) *vm.EVM {
		e := vm.NewEVM(ctx, sdb, params.MainnetChainConfig, vm.Config{Tracer: hooks})
		e.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
		return e
	}
	owner := common.HexToAddress("0x00000000000000000000000000000000000d16d0")
	_, bank, _, err := evmFor(owner).Create(owner, common.FromHex(art.Bin), vm.NewGasBudget(30_000_000), new(uint256.Int))
	must(err)
	initBal := new(big.Int).Exp(big.NewInt(10), big.NewInt(12), nil)
	for from := 0; from < nAcc; from += 500 {
		n := 500
		if from+n > nAcc {
			n = nAcc - from
		}
		in, err := sbABI.Pack("init", big.NewInt(int64(from)), big.NewInt(int64(n)), initBal, initBal)
		must(err)
		_, _, err = evmFor(owner).Call(owner, bank, in, vm.NewGasBudget(1<<34), new(uint256.Int))
		must(err)
	}

	// Zipf account popularity (rank = account id)
	cdf := make([]float64, nAcc)
	acc := 0.0
	for i := range cdf {
		acc += 1 / math.Pow(float64(i+1), theta)
		cdf[i] = acc
	}
	pick := func() int64 { return int64(sort.SearchFloat64s(cdf, rng.Float64()*acc)) }
	pick2 := func() (int64, int64) {
		a := pick()
		for {
			if b := pick(); b != a {
				return a, b
			}
		}
	}

	keys := make([]*ecdsaKey, nUsers)
	for i := range keys {
		k, err := crypto.ToECDSA(crypto.Keccak256([]byte(fmt.Sprintf("d16smallbank-user-%d", i))))
		must(err)
		keys[i] = &ecdsaKey{k: k, addr: crypto.PubkeyToAddress(k.PublicKey)}
	}
	nonces := map[int]uint64{}
	recs := make([]record, 0, nTx)
	mix := map[string]int{}
	for i := 0; i < nTx; i++ {
		amt := big.NewInt(1 + rng.Int63n(1000))
		var data []byte
		var op string
		switch r := rng.Intn(100); {
		case r < 15:
			// Amalgamate empties its source account. A Zipf-drawn source would drain the hot accounts, after
			// which every debit on them fails in any serial order; the source is therefore uniform and the
			// destination Zipf, which keeps the contention on the hot accounts.
			a, b := int64(rng.Intn(nAcc)), pick()
			for a == b {
				a = int64(rng.Intn(nAcc))
			}
			op, data = "amalgamate", pack(sbABI, "amalgamate", big.NewInt(a), big.NewInt(b))
		case r < 30:
			op, data = "balance", pack(sbABI, "balance", big.NewInt(pick()))
		case r < 45:
			op, data = "depositChecking", pack(sbABI, "depositChecking", big.NewInt(pick()), amt)
		case r < 70:
			a, b := pick2()
			op, data = "sendPayment", pack(sbABI, "sendPayment", big.NewInt(a), big.NewInt(b), amt)
		case r < 85:
			v := new(big.Int).Set(amt)
			if rng.Intn(2) == 0 {
				v.Neg(v)
			}
			op, data = "transactSavings", pack(sbABI, "transactSavings", big.NewInt(pick()), v)
		default:
			op, data = "writeCheck", pack(sbABI, "writeCheck", big.NewInt(pick()), amt)
		}
		mix[op]++
		u := rng.Intn(nUsers)
		tx := types.NewTx(&types.LegacyTx{Nonce: nonces[u], GasPrice: big.NewInt(0), Gas: 300_000, To: &bank, Data: data})
		nonces[u]++
		stx, err := types.SignTx(tx, signer, keys[u].k)
		must(err)
		b, err := stx.MarshalBinary()
		must(err)
		recs = append(recs, record{BlockID: 20_000_000 + i/100, TransactionHash: stx.Hash().Hex(),
			TokenAddress: bank.Hex(), Sender: keys[u].addr.Hex(), Recipient: bank.Hex(), Value: "0", Transaction: b})
	}

	e := allocEntry{Code: "0x" + common.Bytes2Hex(sdb.GetCode(bank)), Storage: map[string]string{}}
	for s := range touched {
		if v := sdb.GetState(bank, s); v != (common.Hash{}) {
			e.Storage[s.Hex()] = v.Hex()
		}
	}
	g, err := json.Marshal(map[string]allocEntry{bank.Hex(): e})
	must(err)
	must(os.WriteFile(os.Args[3], g, 0o644))
	out, err := os.Create(os.Args[4])
	must(err)
	zw := gzip.NewWriter(out)
	must(json.NewEncoder(zw).Encode(recs))
	must(zw.Close())
	must(out.Close())
	fmt.Printf("chain=%s bank=%s accounts=%d txs=%d theta=%.2f users=%d storage=%d mix=%v\n",
		chainID, bank.Hex(), nAcc, nTx, theta, nUsers, len(e.Storage), mix)
}

func pack(a abi.ABI, m string, args ...interface{}) []byte {
	in, err := a.Pack(m, args...)
	must(err)
	return in
}

type ecdsaKey struct {
	k    *ecdsa.PrivateKey
	addr common.Address
}
