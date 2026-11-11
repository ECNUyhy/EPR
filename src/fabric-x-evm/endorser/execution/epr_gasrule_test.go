package execution

// Two gas counterexamples, checked against the
// per-opcode gas rule of epr_gas.go.
//   repeated-writes: a program writes one slot five times, twice back to its original value, so every
//     write is priced as a clean write again; on a zero base the three clean writes cost 3 x 17,100 more.
//     The old check (20k left at the end per written key) accepts the endorsed run; on the other base the
//     same gas limit runs out. The rule must reject it.
//   fixed-allowance callee: a caller calls a child with a fixed 10,000 gas; the child writes SLOAD(0)+1, a
//     delta. On a zero base the SSTORE costs 20,000 and the child runs out of gas, so the write is lost
//     although the caller ends with plenty of gas. The rule must reject it.
// Controls: the same transactions with enough headroom in every frame are accepted.

import (
	"math/big"
	"testing"

	"github.com/ethereum/go-ethereum/common"
	"github.com/ethereum/go-ethereum/core"
	"github.com/ethereum/go-ethereum/core/state"
	"github.com/ethereum/go-ethereum/core/vm"
	"github.com/holiman/uint256"
)

func eprGasEnv(t *testing.T) *eprUniEnv {
	sdb, err := state.New(common.Hash{}, state.NewDatabaseForTesting())
	if err != nil {
		t.Fatal(err)
	}
	return &eprUniEnv{t: t, sdb: sdb, ctx: vm.BlockContext{
		CanTransfer: core.CanTransfer, Transfer: core.Transfer,
		BlockNumber: big.NewInt(30_000_000), Time: 1_800_000_000, GasLimit: 30_000_000,
		Difficulty: big.NewInt(0), BaseFee: big.NewInt(0), Random: &common.Hash{},
	}}
}

func eprGasDeploy(t *testing.T, e *eprUniEnv, owner common.Address, runtime []byte) common.Address {
	if len(runtime) > 255 {
		t.Fatal("runtime too long for the one-byte init code")
	}
	initCode := append([]byte{0x60, byte(len(runtime)), 0x80, 0x60, 0x0b, 0x60, 0x00, 0x39, 0x60, 0x00, 0xf3}, runtime...)
	_, c, _, err := e.evm(owner, nil).Create(owner, initCode, vm.NewGasBudget(1_000_000), new(uint256.Int))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestEPRGasRuleRepeatedWrites(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	runtime := []byte{
		0x60, 0x00, 0x54, // x = SLOAD(0)
		0x60, 0x02, 0x60, 0x00, 0x55, // SSTORE(0, 2)
		0x80, 0x60, 0x00, 0x55, // SSTORE(0, x)
		0x60, 0x02, 0x60, 0x00, 0x55, // SSTORE(0, 2)
		0x80, 0x60, 0x00, 0x55, // SSTORE(0, x)
		0x60, 0x01, 0x01, 0x60, 0x00, 0x55, // SSTORE(0, x + 1)
		0x00,
	}
	c := eprGasDeploy(t, e, owner, runtime)
	slot0 := storeKey(c, common.Hash{})
	run := func(x int64, budget uint64, tr *EPRProgTracer) (uint64, *big.Int, error) {
		snap := e.sdb.Snapshot()
		defer e.sdb.RevertToSnapshot(snap)
		e.setKey(slot0, big.NewInt(x))
		evm := e.evm(user, nil)
		if tr != nil {
			evm = e.evm(user, tr.Hooks())
		}
		b := vm.NewGasBudget(budget)
		_, left, err := evm.Call(user, c, nil, b, new(uint256.Int))
		return left.Used(b), e.getKey(slot0), err
	}
	used1, _, err := run(1, 1_000_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	used0, _, err := run(0, 1_000_000, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("gas used: base 1 %d, base 0 %d (difference %d)", used1, used0, used0-used1)
	budget := used1 + 25_000 // the old check needs 20,000 per written key: one key

	tr := NewEPRProgTracer()
	if _, v, err := run(1, budget, tr); err != nil || v.Int64() != 2 {
		t.Fatalf("endorsed run: %v %v", v, err)
	}
	writes := map[string]bool{}
	for s, w := range tr.written {
		if w {
			writes[s] = true
		}
	}
	if p := tr.Program(writes); p == nil {
		t.Fatalf("no program (lostAt=%q): the counterexample needs one", tr.lostAt)
	}
	if left := budget - used1; left < eprGasPerSlot*uint64(len(writes)) {
		t.Fatalf("the old check would reject it already (left %d)", left)
	}
	if _, _, err := run(0, budget, nil); err == nil {
		t.Fatal("base 0 does not run out of gas with the endorsed gas limit")
	}
	if !tr.GasShort() {
		t.Fatal("gas rule accepts a program whose re-execution runs out of gas on base 0")
	}
	t.Logf("rejected: rule broken at pc %d (depth %d), need %d", tr.gasRule.at, tr.gasRule.depth, tr.gasRule.need)

	ok := NewEPRProgTracer() // control: 100k of headroom covers five SSTOREs
	if _, _, err := run(1, used1+110_000, ok); err != nil {
		t.Fatal(err)
	}
	if ok.GasShort() {
		t.Fatalf("control rejected: pc %d need %d", ok.gasRule.at, ok.gasRule.need)
	}
	if _, v, err := run(0, used1+110_000, nil); err != nil || v.Int64() != 1 {
		t.Fatalf("control on base 0: %v %v", v, err)
	}
}

func TestEPRGasRuleFixedAllowanceCallee(t *testing.T) {
	e := eprGasEnv(t)
	owner, user := common.BigToAddress(big.NewInt(0x0E0E)), common.BigToAddress(big.NewInt(0xA11CE))
	child := eprGasDeploy(t, e, owner, []byte{
		0x60, 0x01, 0x60, 0x00, 0x54, 0x01, 0x60, 0x00, 0x55, 0x00, // SSTORE(0, SLOAD(0) + 1); STOP
	})
	callWith := func(gas uint16) []byte {
		code := []byte{0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x60, 0x00, 0x73} // ret, args, value 0
		code = append(code, child.Bytes()...)
		code = append(code, 0x61, byte(gas>>8), byte(gas), 0xf1, 0x50, 0x00) // PUSH2 gas; CALL; POP; STOP
		return code
	}
	parent := eprGasDeploy(t, e, owner, callWith(10_000))
	roomy := eprGasDeploy(t, e, owner, callWith(60_000))
	slot0 := storeKey(child, common.Hash{})
	run := func(to common.Address, x int64, tr *EPRTracer) (*big.Int, error) {
		snap := e.sdb.Snapshot()
		defer e.sdb.RevertToSnapshot(snap)
		e.setKey(slot0, big.NewInt(x))
		evm := e.evm(user, nil)
		if tr != nil {
			evm = e.evm(user, tr.Hooks())
		}
		_, _, err := evm.Call(user, to, nil, vm.NewGasBudget(300_000), new(uint256.Int))
		return e.getKey(slot0), err
	}
	tr := NewEPRTracer()
	if v, err := run(parent, 1, tr); err != nil || v.Int64() != 2 {
		t.Fatalf("endorsed run: %v %v", v, err)
	}
	written := map[string]bool{slot0: true}
	st := tr.slots[slot0]
	if st == nil {
		t.Fatal("child slot not traced")
	}
	if d, _, _, why := st.extract(written, true); why != "" || d != 1 {
		t.Fatalf("the counterexample needs a delta +1: d=%d why=%q", d, why)
	}
	if v, err := run(parent, 0, nil); err != nil || v.Int64() != 0 {
		t.Fatalf("base 0 should lose the child's write (child out of gas): %v %v", v, err)
	}
	if !tr.GasShort() {
		t.Fatal("gas rule accepts a delta whose callee runs out of gas on base 0")
	}
	t.Logf("rejected: rule broken at pc %d (depth %d), need %d", tr.gas.at, tr.gas.depth, tr.gas.need)

	ok := NewEPRTracer() // control: a 60,000 allowance covers the child's one SSTORE
	if _, err := run(roomy, 1, ok); err != nil {
		t.Fatal(err)
	}
	if ok.GasShort() {
		t.Fatalf("control rejected: pc %d need %d", ok.gas.at, ok.gas.need)
	}
	if v, err := run(roomy, 0, nil); err != nil || v.Int64() != 1 {
		t.Fatalf("control on base 0: %v %v", v, err)
	}
}
