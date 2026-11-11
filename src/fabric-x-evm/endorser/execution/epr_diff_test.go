package execution

// Differential test of guarded-delta extraction on real USDC bytecode (Lemma 1 of the paper).
//
// For a transfer endorsed on base balances (x_from, x_to), the tracer extracts for each balance slot s a
// guarded delta (delta_s, lower_s, upper_s). The test then re-executes the same call on many other bases:
//   - every base satisfying all guards must succeed and write exactly base + delta_s to every slot s;
//   - bases just outside a guard bound must NOT reproduce the endorsed outcome (the EVM reverts or writes
//     something else), showing that the guard is tight rather than merely conservative.

import (
	"math/big"
	"math/rand"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/ethereum/go-ethereum/crypto"
	"github.com/ethereum/go-ethereum/params"
	"github.com/holiman/uint256"
)

type eprDiffEnv struct {
	sdb   *state.StateDB
	proxy common.Address
	ctx   vm.BlockContext
}

func (e *eprDiffEnv) slotOf(a common.Address) common.Hash {
	return crypto.Keccak256Hash(common.LeftPadBytes(a.Bytes(), 32), common.BigToHash(big.NewInt(9)).Bytes())
}

// run executes transfer(to, value) from `from` on the given balances; it returns success and the
// resulting balances. The state is restored afterwards.
func (e *eprDiffEnv) run(from, to common.Address, value, bFrom, bTo int64, tr *EPRTracer) (bool, int64, int64) {
	snap := e.sdb.Snapshot()
	defer e.sdb.RevertToSnapshot(snap)
	e.sdb.SetState(e.proxy, e.slotOf(from), common.BigToHash(big.NewInt(bFrom)))
	e.sdb.SetState(e.proxy, e.slotOf(to), common.BigToHash(big.NewInt(bTo)))
	selector := crypto.Keccak256([]byte("transfer(address,uint256)"))[:4]
	input := append(append(append([]byte{}, selector...), common.LeftPadBytes(to.Bytes(), 32)...),
		common.LeftPadBytes(big.NewInt(value).Bytes(), 32)...)
	cfg := vm.Config{}
	if tr != nil {
		cfg.Tracer = tr.Hooks()
	}
	evm := vm.NewEVM(e.ctx, e.sdb, params.MainnetChainConfig, cfg)
	evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
	_, _, err := evm.Call(from, e.proxy, input, vm.NewGasBudget(300_000), new(uint256.Int))
	if err != nil {
		return false, 0, 0
	}
	f := new(big.Int).SetBytes(e.sdb.GetState(e.proxy, e.slotOf(from)).Bytes())
	t := new(big.Int).SetBytes(e.sdb.GetState(e.proxy, e.slotOf(to)).Bytes())
	return true, f.Int64(), t.Int64()
}

func TestEPRDifferentialUSDC(t *testing.T) {
	b := &testing.B{}
	sdb, proxy, _ := eprBenchState(b, 0)
	env := &eprDiffEnv{sdb: sdb, proxy: proxy, ctx: vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}}
	rng := rand.New(rand.NewSource(16))
	from := common.BigToAddress(big.NewInt(0xA11CE))
	to := common.BigToAddress(big.NewInt(0xB0B))
	keyFrom := storeKey(proxy, env.slotOf(from))
	keyTo := storeKey(proxy, env.slotOf(to))
	const cases, rebases = 300, 40
	var checkedIn, checkedOut, tightLower int
	for c := 0; c < cases; c++ {
		value := 1 + rng.Int63n(1_000_000_000)
		bFrom := value + rng.Int63n(1_000_000_000_000)
		bTo := rng.Int63n(1_000_000_000_000)
		tr := NewEPRTracer()
		ok, _, _ := env.run(from, to, value, bFrom, bTo, tr)
		if !ok {
			t.Fatalf("case %d: endorsed transfer failed", c)
		}
		tr.GasLeft = 1 << 40
		written := map[string]bool{keyFrom: true, keyTo: true}
		dF, lF, uF, whyF := tr.slots[keyFrom].extract(written, true)
		dT, lT, uT, whyT := tr.slots[keyTo].extract(written, true)
		if whyF != "" || whyT != "" {
			t.Fatalf("case %d: not extracted: from=%q to=%q", c, whyF, whyT)
		}
		if dF != -value || dT != value {
			t.Fatalf("case %d: deltas %d/%d, want %d/%d", c, dF, dT, -value, value)
		}
		// Bases satisfying both guards: the EVM must succeed and write base + delta.
		for r := 0; r < rebases; r++ {
			xF := lF - dF + rng.Int63n(1_000_000_000_000)
			xT := lT - dT + rng.Int63n(1_000_000_000_000)
			if xF < 0 {
				xF = 0
			}
			if xT < 0 {
				xT = 0
			}
			if xF+dF < lF || xF+dF > uF || xT+dT < lT || xT+dT > uT {
				continue
			}
			ok, nf, nt := env.run(from, to, value, xF, xT, nil)
			if !ok || nf != xF+dF || nt != xT+dT {
				t.Fatalf("case %d rebase (%d,%d): ok=%v got (%d,%d) want (%d,%d)", c, xF, xT, ok, nf, nt, xF+dF, xT+dT)
			}
			checkedIn++
		}
		// Just below the sender's lower guard bound the endorsed outcome must not be reproduced.
		if xF := lF - dF - 1; xF >= 0 {
			ok, nf, nt := env.run(from, to, value, xF, bTo, nil)
			if ok && nf == xF+dF && nt == bTo+dT {
				t.Fatalf("case %d: base %d below the guard still reproduces the endorsed outcome", c, xF)
			}
			if !ok {
				tightLower++
			}
			checkedOut++
		}
	}
	t.Logf("EPR DIFFERENTIAL cases=%d in_guard_reexecutions=%d out_of_guard_checks=%d tight_lower_bound=%d",
		cases, checkedIn, checkedOut, tightLower)
}

// ---- transferFrom: three coupled slots (allowance, sender balance, recipient balance), two guards ----

func (e *eprDiffEnv) call(from common.Address, input []byte, tr *EPRTracer) bool {
	cfg := vm.Config{}
	if tr != nil {
		cfg.Tracer = tr.Hooks()
	}
	evm := vm.NewEVM(e.ctx, e.sdb, params.MainnetChainConfig, cfg)
	evm.SetTxContext(vm.TxContext{Origin: from, GasPrice: new(uint256.Int)})
	_, _, err := evm.Call(from, e.proxy, input, vm.NewGasBudget(300_000), new(uint256.Int))
	return err == nil
}

func eprABI(sig string, args ...[]byte) []byte {
	in := append([]byte{}, crypto.Keccak256([]byte(sig))[:4]...)
	for _, a := range args {
		in = append(in, common.LeftPadBytes(a, 32)...)
	}
	return in
}

// slotHashOf recovers the storage slot hash from a StateDB key "str:<addr>:<slot>".
func slotHashOf(key string) common.Hash {
	i := len(key) - 66
	return common.HexToHash(key[i:])
}

func TestEPRDifferentialTransferFrom(t *testing.T) {
	b := &testing.B{}
	sdb, proxy, _ := eprBenchState(b, 0)
	env := &eprDiffEnv{sdb: sdb, proxy: proxy, ctx: vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}}
	rng := rand.New(rand.NewSource(37))
	owner := common.BigToAddress(big.NewInt(0x0A11))
	spender := common.BigToAddress(big.NewInt(0x5EED))
	to := common.BigToAddress(big.NewInt(0xB0B))
	keyOwner, keyTo := storeKey(proxy, env.slotOf(owner)), storeKey(proxy, env.slotOf(to))
	var in, outAllow, outBal int
	for c := 0; c < 200; c++ {
		value := 1 + rng.Int63n(1_000_000)
		bOwner := value + rng.Int63n(1_000_000_000)
		bTo := rng.Int63n(1_000_000_000)
		allow := value + rng.Int63n(1_000_000_000)
		snap := sdb.Snapshot()
		sdb.SetState(proxy, env.slotOf(owner), common.BigToHash(big.NewInt(bOwner)))
		sdb.SetState(proxy, env.slotOf(to), common.BigToHash(big.NewInt(bTo)))
		if !env.call(owner, eprABI("approve(address,uint256)", spender.Bytes(), big.NewInt(allow).Bytes()), nil) {
			t.Fatal("approve failed")
		}
		input := eprABI("transferFrom(address,address,uint256)", owner.Bytes(), to.Bytes(), big.NewInt(value).Bytes())
		tr := NewEPRTracer()
		if !env.call(spender, input, tr) {
			t.Fatalf("case %d: endorsed transferFrom failed", c)
		}
		var keyAllow string
		written := map[string]bool{}
		for k, st := range tr.slots {
			if st.written {
				written[k] = true
				if k != keyOwner && k != keyTo {
					keyAllow = k
				}
			}
		}
		if len(written) != 3 || keyAllow == "" {
			t.Fatalf("case %d: expected 3 written slots, got %d", c, len(written))
		}
		type ext struct{ d, l, u int64 }
		ex := map[string]ext{}
		for _, k := range []string{keyOwner, keyTo, keyAllow} {
			d, l, u, why := tr.slots[k].extract(written, true)
			if why != "" {
				t.Fatalf("case %d: slot not extracted: %q", c, why)
			}
			ex[k] = ext{d, l, u}
		}
		if ex[keyOwner].d != -value || ex[keyTo].d != value || ex[keyAllow].d != -value {
			t.Fatalf("case %d: deltas %+v", c, ex)
		}
		allowSlot := slotHashOf(keyAllow)
		set := func(xO, xT, xA int64) {
			sdb.SetState(proxy, env.slotOf(owner), common.BigToHash(big.NewInt(xO)))
			sdb.SetState(proxy, env.slotOf(to), common.BigToHash(big.NewInt(xT)))
			sdb.SetState(proxy, allowSlot, common.BigToHash(big.NewInt(xA)))
		}
		get := func(slot common.Hash) int64 { return new(big.Int).SetBytes(sdb.GetState(proxy, slot).Bytes()).Int64() }
		for r := 0; r < 30; r++ {
			xO := ex[keyOwner].l - ex[keyOwner].d + rng.Int63n(1_000_000_000)
			xT := rng.Int63n(1_000_000_000)
			xA := ex[keyAllow].l - ex[keyAllow].d + rng.Int63n(1_000_000_000)
			s2 := sdb.Snapshot()
			set(xO, xT, xA)
			ok := env.call(spender, input, nil)
			if !ok || get(env.slotOf(owner)) != xO-value || get(env.slotOf(to)) != xT+value || get(allowSlot) != xA-value {
				t.Fatalf("case %d rebase: ok=%v", c, ok)
			}
			sdb.RevertToSnapshot(s2)
			in++
		}
		// just below the allowance guard, then just below the balance guard: both must revert
		s2 := sdb.Snapshot()
		set(bOwner, bTo, ex[keyAllow].l-ex[keyAllow].d-1)
		if env.call(spender, input, nil) {
			t.Fatalf("case %d: allowance below guard still succeeds", c)
		}
		sdb.RevertToSnapshot(s2)
		outAllow++
		s2 = sdb.Snapshot()
		set(ex[keyOwner].l-ex[keyOwner].d-1, bTo, allow)
		if env.call(spender, input, nil) {
			t.Fatalf("case %d: balance below guard still succeeds", c)
		}
		sdb.RevertToSnapshot(s2)
		outBal++
		sdb.RevertToSnapshot(snap)
	}
	t.Logf("EPR DIFFERENTIAL transferFrom cases=200 in_guard_reexecutions=%d allowance_guard_tight=%d balance_guard_tight=%d", in, outAllow, outBal)
}
