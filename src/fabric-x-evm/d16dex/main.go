// Command d16dex generates a DEX (Uniswap-V2-style) replay workload for fabric-x-evm's perf harness
// (evaluation workload): a genesis state (tokens, pairs with liquidity, the D16Router, funded and approved users)
// and signed swap transactions in the harness's dataset format.
//
// One base token B and P quote tokens Q_i form P pairs (B, Q_i). Each swap picks a pair by Zipf(theta)
// (P = 1 is a single hot pair), a random direction and a random input amount; its slippage bound is the
// quote obtained by replaying all swaps in order, minus slip_bps.
//
// usage: go run ./d16dex <router.json> <chain-id-source-dataset.json.gz> <out-genesis.json> <out-dataset.json.gz>
//
//	<users> <swaps> <pairs> <theta> <slip_bps> [seed]
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

	"github.com/hyperledger/fabric-x-evm/integration/contracts"
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
	if len(os.Args) < 10 {
		fmt.Fprintln(os.Stderr, "usage: d16dex <router.json> <chainsrc.json.gz> <genesis.json> <dataset.json.gz> <users> <swaps> <pairs> <theta> <slip_bps> [seed]")
		os.Exit(2)
	}
	nUsers, _ := strconv.Atoi(os.Args[5])
	nSwaps, _ := strconv.Atoi(os.Args[6])
	nPairs, _ := strconv.Atoi(os.Args[7])
	theta, _ := strconv.ParseFloat(os.Args[8], 64)
	slip, _ := strconv.Atoi(os.Args[9])
	seed := int64(16)
	if len(os.Args) > 10 {
		seed, _ = strconv.ParseInt(os.Args[10], 10, 64)
	}
	rng := rand.New(rand.NewSource(seed))

	// router artifact (solc-js output) and chain id from an existing harness dataset
	var routerArt struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	raw, err := os.ReadFile(os.Args[1])
	must(err)
	must(json.Unmarshal(raw, &routerArt))
	routerABI, err := abi.JSON(strings.NewReader(string(routerArt.ABI)))
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
	touched := map[common.Address]map[common.Hash]bool{}
	hooks := &tracing.Hooks{OnOpcode: func(pc uint64, op byte, gas, cost uint64, scope tracing.OpContext, rData []byte, depth int, err error) {
		if op != 0x55 {
			return
		}
		st := scope.StackData()
		a := scope.Address()
		if touched[a] == nil {
			touched[a] = map[common.Hash]bool{}
		}
		touched[a][common.Hash(st[len(st)-1].Bytes32())] = true
	}}
	ctx := vm.BlockContext{CanTransfer: core.CanTransfer, Transfer: core.Transfer, BlockNumber: big.NewInt(30_000_000),
		Time: 1_800_000_000, GasLimit: 1 << 40, Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{}}
	evmFor := func(from common.Address) *vm.EVM {
		e := vm.NewEVM(ctx, sdb, params.MainnetChainConfig, vm.Config{Tracer: hooks})
		e.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
		return e
	}
	owner := common.HexToAddress("0x00000000000000000000000000000000000d16d0")
	ercABI, _ := abi.JSON(strings.NewReader(contracts.GenericERC20MetaData.ABI))
	pairABI, _ := abi.JSON(strings.NewReader(contracts.UniswapV2PairMetaData.ABI))
	deploy := func(bin string, a abi.ABI, args ...interface{}) common.Address {
		in, err := a.Pack("", args...)
		must(err)
		_, addr, _, err := evmFor(owner).Create(owner, append(common.FromHex(bin), in...), vm.NewGasBudget(30_000_000), new(uint256.Int))
		must(err)
		if touched[addr] == nil { // contracts without storage writes (the router) still need their code
			touched[addr] = map[common.Hash]bool{}
		}
		return addr
	}
	call := func(from, to common.Address, a abi.ABI, m string, args ...interface{}) []byte {
		in, err := a.Pack(m, args...)
		must(err)
		ret, _, err := evmFor(from).Call(from, to, in, vm.NewGasBudget(30_000_000), new(uint256.Int))
		if err != nil {
			panic(fmt.Sprintf("%s: %v", m, err))
		}
		return ret
	}
	supply := new(big.Int).Exp(big.NewInt(10), big.NewInt(30), nil)
	base := deploy(contracts.GenericERC20MetaData.Bin, ercABI, "D16 Base", "D16B", supply, uint8(6))
	router := deploy(routerArt.Bin, routerABI)
	liq := new(big.Int).Exp(big.NewInt(10), big.NewInt(15), nil) // 1e9 tokens of 6 decimals per side
	type pairInfo struct {
		addr, quote, t0 common.Address
		r0, r1          *big.Int
	}
	pairs := make([]pairInfo, nPairs)
	for i := range pairs {
		q := deploy(contracts.GenericERC20MetaData.Bin, ercABI, fmt.Sprintf("D16 Quote %d", i), fmt.Sprintf("D16Q%d", i), supply, uint8(6))
		t0, t1 := base, q
		if strings.ToLower(t1.Hex()) < strings.ToLower(t0.Hex()) {
			t0, t1 = t1, t0
		}
		p := deploy(contracts.UniswapV2PairMetaData.Bin, pairABI)
		call(owner, p, pairABI, "initialize", t0, t1)
		call(owner, t0, ercABI, "transfer", p, liq)
		call(owner, t1, ercABI, "transfer", p, liq)
		call(owner, p, pairABI, "mint", owner)
		pairs[i] = pairInfo{addr: p, quote: q, t0: t0, r0: new(big.Int).Set(liq), r1: new(big.Int).Set(liq)}
	}

	// Zipf pair popularity
	cdf := make([]float64, nPairs)
	acc := 0.0
	for i := range cdf {
		acc += 1 / math.Pow(float64(i+1), theta)
		cdf[i] = acc
	}
	pickPair := func() int {
		u := rng.Float64() * acc
		return sort.SearchFloat64s(cdf, u)
	}

	// users: deterministic keys; swaps generated and quoted against the in-order reserves
	keys := make([]*ecdsaKey, nUsers)
	for i := range keys {
		k, err := crypto.ToECDSA(crypto.Keccak256([]byte(fmt.Sprintf("d16dex-user-%d", i))))
		must(err)
		keys[i] = &ecdsaKey{k: k, addr: crypto.PubkeyToAddress(k.PublicKey)}
	}
	type swap struct {
		user, pair int
		tokenIn    common.Address
		in, minOut *big.Int
	}
	swaps := make([]swap, nSwaps)
	spend := map[[2]common.Address]*big.Int{} // (user, token) -> total spent
	for i := range swaps {
		u, pi := rng.Intn(nUsers), pickPair()
		p := &pairs[pi]
		in := big.NewInt(1_000_000 + rng.Int63n(999_000_000)) // 1 .. 1000 tokens
		tokenIn := base
		if rng.Intn(2) == 0 {
			tokenIn = p.quote
		}
		zeroIn := tokenIn == p.t0
		rIn, rOut := p.r0, p.r1
		if !zeroIn {
			rIn, rOut = p.r1, p.r0
		}
		inFee := new(big.Int).Mul(in, big.NewInt(997))
		out := new(big.Int).Div(new(big.Int).Mul(inFee, rOut), new(big.Int).Add(new(big.Int).Mul(rIn, big.NewInt(1000)), inFee))
		rIn.Add(rIn, in)
		rOut.Sub(rOut, out)
		minOut := new(big.Int).Div(new(big.Int).Mul(out, big.NewInt(int64(10000-slip))), big.NewInt(10000))
		swaps[i] = swap{user: u, pair: pi, tokenIn: tokenIn, in: in, minOut: minOut}
		key := [2]common.Address{keys[u].addr, tokenIn}
		if spend[key] == nil {
			spend[key] = new(big.Int)
		}
		spend[key].Add(spend[key], in)
	}
	maxApprove := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 255), big.NewInt(1))
	for key, amt := range spend {
		call(owner, key[1], ercABI, "transfer", key[0], new(big.Int).Mul(amt, big.NewInt(2)))
		call(key[0], key[1], ercABI, "approve", router, maxApprove)
	}

	// genesis: code + touched storage of every contract
	alloc := map[string]allocEntry{}
	for a, slots := range touched {
		e := allocEntry{Code: "0x" + common.Bytes2Hex(sdb.GetCode(a)), Storage: map[string]string{}}
		for s := range slots {
			v := sdb.GetState(a, s)
			if v != (common.Hash{}) {
				e.Storage[s.Hex()] = v.Hex()
			}
		}
		alloc[a.Hex()] = e
	}
	g, err := json.Marshal(alloc)
	must(err)
	must(os.WriteFile(os.Args[3], g, 0o644))

	// signed transactions in the harness dataset format
	nonces := map[int]uint64{}
	recs := make([]record, 0, nSwaps)
	for i, s := range swaps {
		data, err := routerABI.Pack("swapExactIn", pairs[s.pair].addr, s.tokenIn, s.in, s.minOut)
		must(err)
		tx := types.NewTx(&types.LegacyTx{Nonce: nonces[s.user], GasPrice: big.NewInt(0), Gas: 800_000, To: &router, Data: data})
		nonces[s.user]++
		stx, err := types.SignTx(tx, signer, keys[s.user].k)
		must(err)
		b, err := stx.MarshalBinary()
		must(err)
		recs = append(recs, record{BlockID: 20_000_000 + i/100, TransactionHash: stx.Hash().Hex(),
			TokenAddress: s.tokenIn.Hex(), Sender: keys[s.user].addr.Hex(), Recipient: router.Hex(), Value: s.in.String(), Transaction: b})
	}
	out, err := os.Create(os.Args[4])
	must(err)
	zw := gzip.NewWriter(out)
	must(json.NewEncoder(zw).Encode(recs))
	must(zw.Close())
	must(out.Close())
	storageN := 0
	for _, e := range alloc {
		storageN += len(e.Storage)
	}
	fmt.Printf("chain=%s router=%s base=%s pairs=%d users=%d swaps=%d theta=%.2f slip=%dbps contracts=%d storage=%d\n",
		chainID, router.Hex(), base.Hex(), nPairs, nUsers, nSwaps, theta, slip, len(alloc), storageN)
}

type ecdsaKey struct {
	k    *ecdsa.PrivateKey
	addr common.Address
}
