package execution

// Differential test of program extraction on a Uniswap-V2-style swap (cross-contract calls,
// packed reserve slot, constant-product guard). For a swap endorsed on one state, the program is evaluated
// on many other states (other reserves and balances) and compared with a real EVM re-execution:
//   - whenever the EVM re-execution follows the same path, the program's guards hold and its outputs
//     equal the EVM's writes;
//   - whenever the program's guards hold, the EVM succeeds with exactly those writes;
//   - whenever the EVM reverts (e.g. the K check fails), the program's guards fail.

import (
	"encoding/json"
	"math/big"
	"os"
	"math/rand"
	"sort"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/tracing"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"

	"github.com/hyperledger/fabric-x-evm/integration/contracts"
)

type eprUniEnv struct {
	t                  *testing.T
	sdb                *state.StateDB
	ctx                vm.BlockContext
	erc, pairABI       abi.ABI
	owner, user        common.Address
	t0, t1, pair       common.Address
}

func (e *eprUniEnv) evm(from common.Address, hooks *tracing.Hooks) *vm.EVM {
	cfg := vm.Config{Tracer: hooks}
	evm := vm.NewEVM(e.ctx, e.sdb, params.MainnetChainConfig, cfg)
	evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
	return evm
}

func (e *eprUniEnv) call(from, to common.Address, hooks *tracing.Hooks, a abi.ABI, method string, args ...interface{}) error {
	in, err := a.Pack(method, args...)
	if err != nil {
		e.t.Fatal(err)
	}
	_, _, err = e.evm(from, hooks).Call(from, to, in, vm.NewGasBudget(2_000_000), new(uint256.Int))
	return err
}

func newEPRUniEnv(t *testing.T) *eprUniEnv {
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	e := &eprUniEnv{t: t, sdb: sdb, ctx: vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}}
	e.erc, _ = abi.JSON(strings.NewReader(contracts.GenericERC20MetaData.ABI))
	e.pairABI, _ = abi.JSON(strings.NewReader(contracts.UniswapV2PairMetaData.ABI))
	e.owner = common.BigToAddress(big.NewInt(0x0E0E))
	e.user = common.BigToAddress(big.NewInt(0xA11CE))
	deploy := func(bin string, a abi.ABI, args ...interface{}) common.Address {
		in, _ := a.Pack("", args...)
		_, addr, _, err := e.evm(e.owner, nil).Create(e.owner, append(common.FromHex(bin), in...), vm.NewGasBudget(10_000_000), new(uint256.Int))
		if err != nil {
			t.Fatalf("deploy: %v", err)
		}
		return addr
	}
	supply := new(big.Int).Mul(big.NewInt(1_000_000_000), big.NewInt(1_000_000_000))
	a := deploy(contracts.GenericERC20MetaData.Bin, e.erc, "TokenA", "TKA", supply, uint8(6))
	b := deploy(contracts.GenericERC20MetaData.Bin, e.erc, "TokenB", "TKB", supply, uint8(6))
	e.t0, e.t1 = a, b
	if strings.ToLower(b.Hex()) < strings.ToLower(a.Hex()) {
		e.t0, e.t1 = b, a
	}
	e.pair = deploy(contracts.UniswapV2PairMetaData.Bin, e.pairABI)
	must := func(err error) {
		if err != nil {
			t.Fatal(err)
		}
	}
	must(e.call(e.owner, e.pair, nil, e.pairABI, "initialize", e.t0, e.t1))
	liq := big.NewInt(1_000_000_000_000)
	must(e.call(e.owner, e.t0, nil, e.erc, "transfer", e.pair, liq))
	must(e.call(e.owner, e.t1, nil, e.erc, "transfer", e.pair, liq))
	must(e.call(e.owner, e.pair, nil, e.pairABI, "mint", e.owner))
	return e
}

// slotsOf lists the storage keys (StateDB format) and slot hashes of the program's keys per contract.
func (e *eprUniEnv) setKey(key string, v *big.Int) {
	addr := common.HexToAddress(key[4:46])
	e.sdb.SetState(addr, slotHashOf(key), common.BigToHash(v))
}

func (e *eprUniEnv) getKey(key string) *big.Int {
	addr := common.HexToAddress(key[4:46])
	return new(big.Int).SetBytes(e.sdb.GetState(addr, slotHashOf(key)).Bytes())
}

func TestEPRProgramUniswapSwap(t *testing.T) {
	e := newEPRUniEnv(t)
	rng := rand.New(rand.NewSource(42))
	var agree, guardOK, guardFail, evmRevert, cases int
	for c := 0; c < 100; c++ {
		snap := e.sdb.Snapshot()
		amountIn := big.NewInt(1_000_000 + rng.Int63n(1_000_000_000))
		// amountOut a bit below the constant-product quote (slippage tolerance)
		amountOut := new(big.Int).Div(new(big.Int).Mul(amountIn, big.NewInt(int64(900+rng.Intn(90)))), big.NewInt(1000))
		if err := e.call(e.owner, e.t0, nil, e.erc, "transfer", e.pair, amountIn); err != nil {
			t.Fatal(err)
		}
		tr := NewEPRProgTracer()
		if err := e.call(e.user, e.pair, tr.Hooks(), e.pairABI, "swap", big.NewInt(0), amountOut, e.user, []byte{}); err != nil {
			t.Fatalf("case %d: endorsed swap failed: %v", c, err)
		}
		tr.GasLeft = 1 << 40
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		p := tr.Program(writes)
		if p == nil {
			t.Fatalf("case %d: program not extracted (lost=%v)", c, tr.lost)
		}
		q, ok := DecodeEPRProgram(p.Encode())
		if !ok {
			t.Fatalf("case %d: program does not round-trip", c)
		}
		// state right before the swap (after the transfer-in): base values of the program keys
		e.sdb.RevertToSnapshot(snap)
		snap2 := e.sdb.Snapshot()
		if err := e.call(e.owner, e.t0, nil, e.erc, "transfer", e.pair, amountIn); err != nil {
			t.Fatal(err)
		}
		base := map[string]*big.Int{}
		for _, k := range q.Keys {
			base[k] = e.getKey(k)
		}
		keys := append([]string{}, q.Keys...)
		sort.Strings(keys)
		in, _ := e.pairABI.Pack("swap", big.NewInt(0), amountOut, e.user, []byte{})
		for r := 0; r < 40; r++ {
			// perturb every input: scale each value by a random factor in [0.5, 1.5)
			s3 := e.sdb.Snapshot()
			vals := map[string][]byte{}
			scale := func(x *big.Int) *big.Int {
				x = new(big.Int).Mul(x, big.NewInt(int64(500+rng.Intn(1000))))
				return x.Div(x, big.NewInt(1000))
			}
			mask112 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 112), big.NewInt(1))
			for _, k := range keys {
				v := new(big.Int).Set(base[k])
				switch {
				case v.Sign() > 0 && v.BitLen() < 120: // token balances
					v = scale(v)
				case v.Bit(224) == 1 && v.BitLen() == 225: // packed reserve0 | reserve1<<112 | initialized<<224
					r0 := scale(new(big.Int).And(v, mask112))
					r1 := scale(new(big.Int).And(new(big.Int).Rsh(v, 112), mask112))
					v = new(big.Int).Lsh(big.NewInt(1), 224)
					v.Or(v, new(big.Int).Lsh(r1, 112))
					v.Or(v, r0)
				}
				e.setKey(k, v)
				vals[k] = v.Bytes()
			}
			out, pok := q.Eval(func(k string) []byte { return vals[k] })
			_, _, err := e.evm(e.user, nil).Call(e.user, e.pair, in, vm.NewGasBudget(2_000_000), new(uint256.Int))
			switch {
			case err != nil:
				evmRevert++
				if pok {
					t.Fatalf("case %d/%d: EVM reverted (%v) but the program's guards hold", c, r, err)
				}
			case pok:
				guardOK++
				for k, w := range out {
					if e.getKey(k).Cmp(new(big.Int).SetBytes(w)) != 0 {
						t.Fatalf("case %d/%d: key %s: program %x, EVM %x", c, r, k, w, e.getKey(k).Bytes())
					}
				}
				agree++
			default:
				guardFail++ // EVM succeeded on a different path: the program conservatively refuses
			}
			e.sdb.RevertToSnapshot(s3)
		}
		e.sdb.RevertToSnapshot(snap2)
		cases++
	}
	t.Logf("EPR PROGRAM uniswap-swap cases=%d reexecutions=%d guards_hold_and_outputs_equal=%d guards_fail_on_evm_revert=%d guards_fail_evm_ok=%d",
		cases, agree+guardFail+evmRevert, agree, evmRevert, guardFail)
}

// TestEPRProgramRouterProbe traces a D16Router swap (transferFrom + getReserves + quote + swap) and reports
// whether program extraction succeeds; set D16_ROUTER_JSON to the solc-js artifact.
func TestEPRProgramRouterProbe(t *testing.T) {
	path := os.Getenv("D16_ROUTER_JSON")
	if path == "" {
		t.Skip("D16_ROUTER_JSON not set")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var art struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatal(err)
	}
	rABI, _ := abi.JSON(strings.NewReader(string(art.ABI)))
	e := newEPRUniEnv(t)
	_, router, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.owner, e.t0, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.user, e.t0, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.owner, e.t1, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.user, e.t1, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
		t.Fatal(err)
	}
	for _, tokenIn := range []common.Address{e.t0, e.t1} {
		tr := NewEPRProgTracer()
		if err := e.call(e.user, router, tr.Hooks(), rABI, "swapExactIn", e.pair, tokenIn, big.NewInt(1_000_000), big.NewInt(1)); err != nil {
			t.Fatalf("router swap: %v", err)
		}
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		p := tr.Program(writes)
		t.Logf("EPR PROGRAM router tokenIn=token%d lost=%v lostAt=%q written=%d program=%v", map[bool]int{true: 0, false: 1}[tokenIn == e.t0], tr.lost, tr.lostAt, len(writes), p != nil)
		if p != nil {
			t.Logf("   keys=%d nodes=%d guards=%d outputs=%d bytes=%d", len(p.Keys), len(p.Nodes), len(p.Guards), len(p.Outputs), len(p.Encode()))
		}
	}
}

// TestEPRProgramRouterDifferential: the full router flow (transferFrom into the pair, quote from the
// reserves with the 0.3% fee, slippage check, swap) re-executed on perturbed reserves and balances.
func TestEPRProgramRouterDifferential(t *testing.T) {
	path := os.Getenv("D16_ROUTER_JSON")
	if path == "" {
		t.Skip("D16_ROUTER_JSON not set")
	}
	raw, _ := os.ReadFile(path)
	var art struct {
		ABI json.RawMessage `json:"abi"`
		Bin string          `json:"bin"`
	}
	if err := json.Unmarshal(raw, &art); err != nil {
		t.Fatal(err)
	}
	rABI, _ := abi.JSON(strings.NewReader(string(art.ABI)))
	e := newEPRUniEnv(t)
	_, router, _, err := e.evm(e.owner, nil).Create(e.owner, common.FromHex(art.Bin), vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.owner, e.t0, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.user, e.t0, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.owner, e.t1, nil, e.erc, "transfer", e.user, big.NewInt(1_000_000_000_000)); err != nil {
		t.Fatal(err)
	}
	if err := e.call(e.user, e.t1, nil, e.erc, "approve", router, new(big.Int).Lsh(big.NewInt(1), 200)); err != nil {
		t.Fatal(err)
	}
	rng := rand.New(rand.NewSource(99))
	mask112 := new(big.Int).Sub(new(big.Int).Lsh(big.NewInt(1), 112), big.NewInt(1))
	var agree, revertMatch, falseRefuse int
	for c := 0; c < 100; c++ {
		amountIn := big.NewInt(1_000_000 + rng.Int63n(1_000_000_000))
		tokenIn := e.t0
		if c%2 == 1 {
			tokenIn = e.t1
		}
		minOut := new(big.Int).Div(new(big.Int).Mul(amountIn, big.NewInt(980)), big.NewInt(1000)) // ~0.3% fee, 1.7% slack
		snap := e.sdb.Snapshot()
		tr := NewEPRProgTracer()
		if err := e.call(e.user, router, tr.Hooks(), rABI, "swapExactIn", e.pair, tokenIn, amountIn, minOut); err != nil {
			t.Fatalf("case %d: endorsed router swap failed: %v", c, err)
		}
		writes := map[string]bool{}
		for s, w := range tr.written {
			if w {
				writes[s] = true
			}
		}
		p := tr.Program(writes)
		if p == nil {
			t.Fatalf("case %d: no program (lost at %q)", c, tr.lostAt)
		}
		q, ok := DecodeEPRProgram(p.Encode())
		if !ok {
			t.Fatal("round trip")
		}
		e.sdb.RevertToSnapshot(snap)
		in, _ := rABI.Pack("swapExactIn", e.pair, tokenIn, amountIn, minOut)
		base := map[string]*big.Int{}
		for _, k := range q.Keys {
			base[k] = e.getKey(k)
		}
		for r := 0; r < 40; r++ {
			s3 := e.sdb.Snapshot()
			vals := map[string][]byte{}
			for _, k := range q.Keys {
				v := new(big.Int).Set(base[k])
				scale := func(x *big.Int) *big.Int {
					x = new(big.Int).Mul(x, big.NewInt(int64(970+rng.Intn(60)))) // +-3%: often within slippage
					return x.Div(x, big.NewInt(1000))
				}
				switch {
				case v.Sign() > 0 && v.BitLen() < 120:
					v = scale(v)
				case v.Bit(224) == 1 && v.BitLen() == 225:
					r0 := scale(new(big.Int).And(v, mask112))
					r1 := scale(new(big.Int).And(new(big.Int).Rsh(v, 112), mask112))
					v = new(big.Int).Lsh(big.NewInt(1), 224)
					v.Or(v, new(big.Int).Lsh(r1, 112))
					v.Or(v, r0)
				}
				e.setKey(k, v)
				vals[k] = v.Bytes()
			}
			out, pok := q.Eval(func(k string) []byte { return vals[k] })
			_, _, err := e.evm(e.user, nil).Call(e.user, router, in, vm.NewGasBudget(2_000_000), new(uint256.Int))
			switch {
			case err != nil && pok:
				t.Fatalf("case %d/%d: EVM reverted (%v) but program guards hold", c, r, err)
			case err != nil:
				revertMatch++
			case pok:
				for k, w := range out {
					if e.getKey(k).Cmp(new(big.Int).SetBytes(w)) != 0 {
						t.Fatalf("case %d/%d: key %s program %x EVM %x", c, r, k, w, e.getKey(k).Bytes())
					}
				}
				agree++
			default:
				falseRefuse++
			}
			e.sdb.RevertToSnapshot(s3)
		}
	}
	t.Logf("EPR PROGRAM router-differential reexecutions=%d guards_hold_and_outputs_equal=%d guards_fail_on_evm_revert=%d guards_fail_evm_ok=%d",
		agree+revertMatch+falseRefuse, agree, revertMatch, falseRefuse)
}
