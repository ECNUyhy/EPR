package execution

// Differential test of guarded-delta extraction on TetherToken (USDT, solc 0.8 port shipped
// with fabric-x-evm). Unlike USDC, a transfer charges a fee computed from storage (basisPointsRate,
// maximumFee) and credits it to the owner: three balance slots are written and the owner's delta depends
// on other (stable, MVCC-checked) storage slots. Checked arithmetic (solc 0.8) provides the guards.

import (
	"math/big"
	"math/rand"
	"strings"
	"testing"

	"github.com/ethereum/go-ethereum/accounts/abi"
	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"

	"github.com/hyperledger/fabric-x-evm/integration/contracts"
)

func TestEPRDifferentialUSDTFee(t *testing.T) {
	parsed, err := abi.JSON(strings.NewReader(contracts.TetherTokenMetaData.ABI))
	if err != nil {
		t.Fatal(err)
	}
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	ctx := vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}
	owner := common.BigToAddress(big.NewInt(0x0E0E))
	newEVM := func(from common.Address, tr *EPRTracer) *vm.EVM {
		cfg := vm.Config{}
		if tr != nil {
			cfg.Tracer = tr.Hooks()
		}
		evm := vm.NewEVM(ctx, sdb, params.MainnetChainConfig, cfg)
		evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
		return evm
	}
	ctorArgs, err := parsed.Pack("", big.NewInt(1_000_000_000_000_000), "Tether USD", "USDT", big.NewInt(6))
	if err != nil {
		t.Fatal(err)
	}
	code := append(common.FromHex(contracts.TetherTokenMetaData.Bin), ctorArgs...)
	_, token, _, err := newEVM(owner, nil).Create(owner, code, vm.NewGasBudget(10_000_000), new(uint256.Int))
	if err != nil {
		t.Fatalf("deploy: %v", err)
	}
	call := func(from common.Address, tr *EPRTracer, method string, args ...interface{}) bool {
		in, err := parsed.Pack(method, args...)
		if err != nil {
			t.Fatal(err)
		}
		_, _, err = newEVM(from, tr).Call(from, token, in, vm.NewGasBudget(500_000), new(uint256.Int))
		return err == nil
	}
	// 0.19% fee capped at 20 USDT (the contract requires < 20 basis points)
	if !call(owner, nil, "setParams", big.NewInt(19), big.NewInt(20)) {
		t.Fatal("setParams failed")
	}
	alice := common.BigToAddress(big.NewInt(0xA11CE))
	bob := common.BigToAddress(big.NewInt(0xB0B))
	if !call(owner, nil, "transfer", alice, big.NewInt(1_000_000_000_000)) {
		t.Fatal("funding transfer failed")
	}
	rng := rand.New(rand.NewSource(7))
	var in, tight, cases int
	for c := 0; c < 200; c++ {
		value := 1_000_000 + rng.Int63n(100_000_000_000) // 1 .. 100k USDT, fee sometimes capped
		snap := sdb.Snapshot()
		tr := NewEPRTracer()
		if !call(alice, tr, "transfer", bob, big.NewInt(value)) {
			t.Fatalf("case %d: endorsed transfer failed", c)
		}
		tr.GasLeft = 1 << 40
		written := map[string]bool{}
		for k, st := range tr.slots {
			if st.written {
				written[k] = true
			}
		}
		if len(written) != 3 {
			t.Fatalf("case %d: expected 3 written balance slots (sender, recipient, owner), got %d", c, len(written))
		}
		type ext struct {
			slot     common.Hash
			d, l, u  int64
			endorsed int64
		}
		var exts []ext
		for k := range written {
			d, l, u, why := tr.slots[k].extract(written, true)
			if why != "" {
				t.Fatalf("case %d: slot not extracted: %q", c, why)
			}
			exts = append(exts, ext{slot: slotHashOf(k), d: d, l: l, u: u, endorsed: tr.slots[k].orig.ToBig().Int64()})
		}
		sdb.RevertToSnapshot(snap)
		input, _ := parsed.Pack("transfer", bob, big.NewInt(value))
		run := func(bases []int64) (bool, []int64) {
			s2 := sdb.Snapshot()
			defer sdb.RevertToSnapshot(s2)
			for i, e := range exts {
				sdb.SetState(token, e.slot, common.BigToHash(big.NewInt(bases[i])))
			}
			_, _, err := newEVM(alice, nil).Call(alice, token, input, vm.NewGasBudget(500_000), new(uint256.Int))
			if err != nil {
				return false, nil
			}
			out := make([]int64, len(exts))
			for i, e := range exts {
				out[i] = new(big.Int).SetBytes(sdb.GetState(token, e.slot).Bytes()).Int64()
			}
			return true, out
		}
		for r := 0; r < 30; r++ {
			bases := make([]int64, len(exts))
			for i, e := range exts {
				bases[i] = e.l - e.d + rng.Int63n(1_000_000_000_000)
				if bases[i] < 0 {
					bases[i] = 0
				}
			}
			ok, out := run(bases)
			if !ok {
				t.Fatalf("case %d: rebase within guards reverted", c)
			}
			for i, e := range exts {
				if out[i] != bases[i]+e.d {
					t.Fatalf("case %d: slot %d wrote %d, want %d", c, i, out[i], bases[i]+e.d)
				}
			}
			in++
		}
		// the debited slot (negative delta) just below its guard must revert
		for i, e := range exts {
			if e.d >= 0 {
				continue
			}
			bases := make([]int64, len(exts))
			for j, f := range exts {
				bases[j] = f.endorsed
			}
			bases[i] = e.l - e.d - 1
			if ok, _ := run(bases); ok {
				t.Fatalf("case %d: base below the debit guard still succeeds", c)
			}
			tight++
		}
		cases++
	}
	t.Logf("EPR DIFFERENTIAL usdt-fee cases=%d in_guard_reexecutions=%d debit_guard_tight=%d", cases, in, tight)
}
